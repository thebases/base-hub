package cron

// File overview:
// - Responsibility: implements the `platform.autovacuum` scheduler handler enforcing the SRS §5.3
//   retention policy — batch deletion of aged terminal workflow runs (with their outbound delivery
//   audit rows) and aged sys.model.log entries.
// - Key symbols: `runAutovacuum`, `vacuumWorkflowRuns`, `vacuumModelLogs`, `retentionDays`.
// - Pattern: Uses bounded batch deletes (LIMIT loops) so retention on a large backlog never holds
//   long locks or bloats one transaction.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope,
//   and transaction state can follow the work end to end.

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

const (
	autovacuumHandlerKey = "platform.autovacuum"
	// Retention windows are per-database overridable via sys.config_param
	// (`retention.<key>_days`, SRS §5.3); the defaults below apply when the
	// param is absent or unparsable.
	retentionWorkflowRunDaysKey     = "retention.workflow_run_days"
	retentionModelLogDaysKey        = "retention.model_log_days"
	defaultWorkflowRunRetentionDays = 180
	defaultModelLogRetentionDays    = 365
	// vacuumBatchSize bounds each delete statement so retention on a large
	// backlog proceeds in small lock windows instead of one huge transaction.
	vacuumBatchSize = 1000
)

// Legacy workflow table names are intentional here (coding-rules exception):
// the logical models stay dotted but these physical names are bound explicitly.
// They are vars, not consts, so the DB-backed tests can redirect deletes to
// scratch tables instead of touching real audit rows on a shared fixture DB.
var (
	vacuumWorkflowRunTable      = "base_workflow_run"
	vacuumOutboundDeliveryTable = "base_outbound_delivery"
	vacuumModelLogTable         = "sys_model_log"
	vacuumConfigParamTable      = "sys_config_param"
)

// terminalWorkflowRunStatuses mirrors modules/workflow/models run statuses.
// Listed literally because internal/ must not import modules/*: pending,
// running, and waiting_retry rows are never vacuumed regardless of age.
var terminalWorkflowRunStatuses = []string{"succeeded", "failed", "dead_letter", "cancelled", "skipped"}

func init() {
	// The autovacuum handler is kernel-owned, so it registers at package init;
	// the seeded `platform.autovacuum` job starts firing once the scheduler runs.
	if err := RegisterHandler(autovacuumHandlerKey, runAutovacuum); err != nil {
		log.Errorf("cron: register autovacuum handler: %v", err)
	}
}

// runAutovacuum enforces SRS §5.3 retention on the job's database.
// Dispute-hold exclusion (runs referenced by an open reconciliation) is
// deferred until a reconciliation model exists — vacuum is age-only for now,
// exactly as the SRS constraint's "mechanism TBD" notes.
func runAutovacuum(ctx context.Context, _ map[string]any) error {
	db := orm.DBFromContext(ctx)
	if db == nil {
		log.Errorf("cron: autovacuum requires a database-bound context")
		return fmt.Errorf("cron: autovacuum requires a database-bound context")
	}
	dbName := vacuumDBName(ctx)

	runDays := retentionDays(ctx, db, retentionWorkflowRunDaysKey, defaultWorkflowRunRetentionDays)
	logDays := retentionDays(ctx, db, retentionModelLogDaysKey, defaultModelLogRetentionDays)

	runsDeleted, deliveriesDeleted, err := vacuumWorkflowRuns(ctx, db, runDays)
	if err != nil {
		return fmt.Errorf("cron: autovacuum workflow runs on %q: %w", dbName, err)
	}
	logsDeleted, err := vacuumModelLogs(ctx, db, logDays)
	if err != nil {
		return fmt.Errorf("cron: autovacuum model logs on %q: %w", dbName, err)
	}
	// Counts are the ops-visible signal that retention is actually running
	// (SRS §5.3 DoD) — keep this line even when everything is zero.
	log.Infof("cron: autovacuum on %q: workflow_runs=%d outbound_deliveries=%d model_logs=%d (windows %dd/%dd)",
		dbName, runsDeleted, deliveriesDeleted, logsDeleted, runDays, logDays)
	return nil
}

// vacuumWorkflowRuns deletes terminal runs older than the retention window in
// batches, removing each batch's outbound delivery audit rows first so the
// dependent side never outlives its run (SRS §5.3 cascade consistency).
func vacuumWorkflowRuns(ctx context.Context, db bun.IDB, retentionDays int64) (int64, int64, error) {
	var totalRuns, totalDeliveries int64
	for {
		// Age is measured from the last mutation (terminal rows stop updating),
		// falling back to created_at for legacy rows with a NULL updated_at.
		var doomed []struct {
			ID   int64  `bun:"id"`
			Name string `bun:"name"`
		}
		err := db.NewRaw(fmt.Sprintf(`
			SELECT id, name FROM %s
			WHERE status IN (?)
			  AND COALESCE(updated_at, created_at) < now() - make_interval(days => ?)
			ORDER BY id ASC
			LIMIT ?
		`, vacuumWorkflowRunTable), bun.In(terminalWorkflowRunStatuses), retentionDays, vacuumBatchSize).Scan(ctx, &doomed)
		if err != nil {
			// A database without the workflow tables simply has nothing to vacuum.
			if isMissingRelationError(err) {
				return totalRuns, totalDeliveries, nil
			}
			return totalRuns, totalDeliveries, err
		}
		if len(doomed) == 0 {
			return totalRuns, totalDeliveries, nil
		}

		ids := make([]int64, 0, len(doomed))
		names := make([]string, 0, len(doomed))
		for _, row := range doomed {
			ids = append(ids, row.ID)
			// Outbound rows link to runs through the shared request ID persisted
			// in both tables' `name` columns; skip blank names so the delivery
			// delete can never match unrelated rows with empty identifiers.
			if trimmed := strings.TrimSpace(row.Name); trimmed != "" {
				names = append(names, trimmed)
			}
		}

		// Delete the dependent audit rows first: if the process dies between the
		// two statements, orphaned runs remain claimable by the next vacuum pass,
		// whereas the reverse order would leave deliveries that outlive their run.
		if len(names) > 0 {
			res, err := db.NewRaw(fmt.Sprintf("DELETE FROM %s WHERE name IN (?)", vacuumOutboundDeliveryTable), bun.In(names)).Exec(ctx)
			if err != nil {
				// Missing delivery table is tolerable; the run delete still proceeds.
				if !isMissingRelationError(err) {
					return totalRuns, totalDeliveries, err
				}
			} else if affected, countErr := res.RowsAffected(); countErr == nil {
				totalDeliveries += affected
			}
		}

		res, err := db.NewRaw(fmt.Sprintf("DELETE FROM %s WHERE id IN (?)", vacuumWorkflowRunTable), bun.In(ids)).Exec(ctx)
		if err != nil {
			return totalRuns, totalDeliveries, err
		}
		if affected, countErr := res.RowsAffected(); countErr == nil {
			totalRuns += affected
		}
		// A short batch means the backlog is drained; stop instead of re-scanning.
		if len(doomed) < vacuumBatchSize {
			return totalRuns, totalDeliveries, nil
		}
	}
}

// vacuumModelLogs deletes sys.model.log rows older than the retention window
// in bounded batches.
func vacuumModelLogs(ctx context.Context, db bun.IDB, retentionDays int64) (int64, error) {
	var total int64
	for {
		res, err := db.NewRaw(fmt.Sprintf(`
			DELETE FROM %s WHERE id IN (
				SELECT id FROM %s
				WHERE created_at < now() - make_interval(days => ?)
				ORDER BY id ASC
				LIMIT ?
			)
		`, vacuumModelLogTable, vacuumModelLogTable), retentionDays, vacuumBatchSize).Exec(ctx)
		if err != nil {
			// A database without the log table simply has nothing to vacuum.
			if isMissingRelationError(err) {
				return total, nil
			}
			return total, err
		}
		affected, countErr := res.RowsAffected()
		if countErr != nil {
			return total, countErr
		}
		total += affected
		// A short batch means the backlog is drained; stop instead of re-scanning.
		if affected < vacuumBatchSize {
			return total, nil
		}
	}
}

// retentionDays reads one retention window from sys.config_param, tolerating
// jsonb numbers and quoted strings, and falls back to the SRS default when the
// param is absent, unparsable, or non-positive.
func retentionDays(ctx context.Context, db bun.IDB, key string, fallback int64) int64 {
	var raw string
	err := db.NewRaw(fmt.Sprintf("SELECT value::text FROM %s WHERE name = ? LIMIT 1", vacuumConfigParamTable), key).Scan(ctx, &raw)
	if err != nil {
		// Missing row or missing table both mean "use the default"; only log
		// unexpected failures so a fresh DB does not spam warnings.
		if !isMissingRelationError(err) && !strings.Contains(err.Error(), "no rows") {
			log.Warnf("cron: read %s: %v", key, err)
		}
		return fallback
	}
	// jsonb text form quotes string values ("180"); strip before parsing.
	trimmed := strings.Trim(strings.TrimSpace(raw), `"`)
	parsed, parseErr := strconv.ParseInt(trimmed, 10, 64)
	if parseErr != nil || parsed <= 0 {
		log.Warnf("cron: %s value %q is not a positive integer, using default %d", key, raw, fallback)
		return fallback
	}
	return parsed
}

// vacuumDBName resolves the logical database name for log lines.
func vacuumDBName(ctx context.Context) string {
	if env := orm.EnvironmentFromContext(ctx); env != nil {
		if name := strings.TrimSpace(env.DBName()); name != "" {
			return name
		}
	}
	return "default"
}

// isMissingRelationError reports whether the error means the target table does
// not exist on this database — a normal condition for partially migrated or
// fixture databases, never a job failure.
func isMissingRelationError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "does not exist")
}
