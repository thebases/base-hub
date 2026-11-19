package models

// Retention for run and outbound delivery rows (plan T96) plus the claim indexes.
//
// base.workflow.run and base.workflow.outbound grow without bound: every run stores its input,
// output and step_runs, every notify keeps its request/response body. A daily go_handler
// (`workflow.purge_retention`, seeded as automation.action/cron) deletes terminal rows older than a
// per-database retention (sys_config_param `workflow.run_retention_days` and
// `workflow.outbound_retention_days`, default 30, 0 disables).
//
// Safety:
//   - only terminal statuses are deleted; pending/running/waiting_retry/waiting_approval (runs) and
//     queued/sending/retryable_failed (deliveries) are still owned by the engine or the sweeper;
//   - nothing references a run row (step runs are jsonb inside it, outbound rows carry workflow_id
//     but no run id), so a delete cannot violate an FK or orphan an engine-owned row;
//   - deletes are bounded batches that skip locked rows, so the purge never blocks on or races a
//     worker that is still writing a row.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/uptrace/bun"

	automationservice "git.thebase.vn/base_hub/modules/base-automation/service"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

const (
	// WorkflowPurgeHandlerKey is the automation go_handler key seeded in data/19-20.
	WorkflowPurgeHandlerKey = "workflow.purge_retention"

	WorkflowRunRetentionParam      = "workflow.run_retention_days"
	WorkflowOutboundRetentionParam = "workflow.outbound_retention_days"

	defaultWorkflowRetentionDays = 30
	workflowPurgeBatchSize       = 500
	// workflowPurgeMaxPerRun bounds one pass per table so a first purge of a huge backlog spreads
	// over several daily runs instead of holding the tenant DB busy.
	workflowPurgeMaxPerRun = 50000
	workflowPurgeTimeout   = 5 * time.Minute

	workflowRunRetryIndexName = "base_workflow_run_retry_claim_idx"
	workflowRunListIndexName  = "base_workflow_run_workflow_id_idx_desc"
	// workflowRunApprovalExpiryIndexName serves expire_approvals (plan T103 review): it holds only
	// waits that can expire.
	workflowRunApprovalExpiryIndexName = "base_workflow_run_approval_expiry_idx"
)

// workflowRunTerminalStatuses are the run statuses nothing resumes from.
var workflowRunTerminalStatuses = []string{
	WorkflowRunStatusSucceeded,
	WorkflowRunStatusFailed,
	WorkflowRunStatusDeadLetter,
	WorkflowRunStatusCancelled,
	WorkflowRunStatusSkipped,
}

// outboundTerminalStatuses are the delivery statuses the sweeper never touches again.
var outboundTerminalStatuses = []string{
	OutboundDeliveryStatusDelivered,
	OutboundDeliveryStatusPermanentFailed,
}

func init() {
	orm.RegisterLegacyDataMigration("base-workflow: run claim indexes", ensureWorkflowRunIndexes)
}

// InitRetentionRuntime registers the purge go_handler and returns its unregister func.
func InitRetentionRuntime() func() {
	if err := automationservice.RegisterGoHandler(WorkflowPurgeHandlerKey, func(ctx context.Context, _ map[string]any) error {
		_, _, err := PurgeWorkflowRetention(ctx)
		return err
	}); err != nil {
		log.Fatalf("workflow: register go handler: %v", err)
	}
	return func() { automationservice.UnregisterGoHandler(WorkflowPurgeHandlerKey) }
}

// batchDeleteFunc deletes up to limit rows and reports how many it removed.
type batchDeleteFunc func(ctx context.Context, limit int) (int64, error)

// purgeInBatches calls del until a batch comes back short, maxTotal rows were removed or ctx ends.
// Rows deleted before an error or the deadline stay deleted and are counted.
func purgeInBatches(ctx context.Context, batch int, maxTotal int64, del batchDeleteFunc) (int64, error) {
	var total int64
	for total < maxTotal {
		if ctx.Err() != nil {
			return total, nil
		}
		n, err := del(ctx, batch)
		total += n
		if err != nil {
			if ctx.Err() != nil {
				return total, nil
			}
			return total, err
		}
		if n < int64(batch) {
			break
		}
	}
	return total, nil
}

// PurgeWorkflowRetention deletes expired terminal run and outbound rows of the context's database.
func PurgeWorkflowRetention(ctx context.Context) (runs, deliveries int64, err error) {
	db := orm.DBFromContext(ctx)
	if db == nil {
		return 0, 0, errors.New("workflow retention: no database in context")
	}
	ctx, cancel := context.WithTimeout(ctx, workflowPurgeTimeout)
	defer cancel()

	runDays := readRetentionDays(ctx, db, WorkflowRunRetentionParam)
	outboundDays := readRetentionDays(ctx, db, WorkflowOutboundRetentionParam)
	var errs []error
	if runDays > 0 {
		table := WorkflowRunModelSpec.ResolveStorageName()
		cutoff := time.Now().UTC().AddDate(0, 0, -runDays)
		runs, err = purgeInBatches(ctx, workflowPurgeBatchSize, workflowPurgeMaxPerRun, func(ctx context.Context, limit int) (int64, error) {
			return deleteExpiredBatch(ctx, db, table, workflowRunTerminalStatuses, cutoff, limit)
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("purge %s: %w", table, err))
		}
	}
	if outboundDays > 0 {
		table := OutboundDeliveryModelSpec.ResolveStorageName()
		cutoff := time.Now().UTC().AddDate(0, 0, -outboundDays)
		deliveries, err = purgeInBatches(ctx, workflowPurgeBatchSize, workflowPurgeMaxPerRun, func(ctx context.Context, limit int) (int64, error) {
			return deleteExpiredBatch(ctx, db, table, outboundTerminalStatuses, cutoff, limit)
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("purge %s: %w", table, err))
		}
	}
	if runs > 0 || deliveries > 0 {
		log.Infof("workflow retention: deleted %d run(s) and %d outbound delivery row(s)", runs, deliveries)
	}
	return runs, deliveries, errors.Join(errs...)
}

// deleteExpiredBatch removes one batch of rows in a terminal status last updated before cutoff.
// SKIP LOCKED leaves rows another transaction holds for the next pass.
func deleteExpiredBatch(ctx context.Context, db bun.IDB, table string, statuses []string, cutoff time.Time, limit int) (int64, error) {
	res, err := db.ExecContext(ctx, fmt.Sprintf(
		`DELETE FROM %[1]s WHERE id IN (
			SELECT id FROM %[1]s WHERE status IN (?) AND updated_at < ?
			ORDER BY id LIMIT ? FOR UPDATE SKIP LOCKED)`, table),
		bun.In(statuses), cutoff, limit)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// readRetentionDays reads a per-database retention in days: missing or invalid falls back to the
// default, 0 or negative disables. A read error also falls back so a transient failure never
// widens deletion beyond the default.
func readRetentionDays(ctx context.Context, db bun.IDB, name string) int {
	var raw string
	err := db.NewRaw(`SELECT value::text FROM sys_config_param WHERE name = ? LIMIT 1`, name).Scan(ctx, &raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		log.Warnf("workflow retention: read %s: %v (using default %d)", name, err, defaultWorkflowRetentionDays)
		return defaultWorkflowRetentionDays
	}
	return parseRetentionDays(raw)
}

// parseRetentionDays turns a stored jsonb::text value (quoted string or number) into days.
func parseRetentionDays(raw string) int {
	raw = strings.Trim(strings.TrimSpace(raw), `"`)
	if raw == "" {
		return defaultWorkflowRetentionDays
	}
	days, err := strconv.Atoi(raw)
	if err != nil {
		return defaultWorkflowRetentionDays
	}
	if days < 0 {
		return 0
	}
	return days
}

// ensureWorkflowRunIndexes creates the run claim/list/approval-expiry indexes. The retry index is partial on the
// claim's status so it only holds waiting runs: it matches claim_due_retries
// (status = 'waiting_retry' AND next_retry_at > 0 AND next_retry_at <= ? ORDER BY next_retry_at, id).
// Failures are logged and swallowed: the indexes are an optimization and must not stop boot. Not
// CONCURRENTLY: data migrations run inside a transaction.
func ensureWorkflowRunIndexes(ctx context.Context, db bun.IDB) error {
	table := WorkflowRunModelSpec.ResolveStorageName()
	var regclass sql.NullString
	if err := db.NewRaw(`SELECT to_regclass(?)::text`, table).Scan(ctx, &regclass); err != nil {
		log.Warnf("base-workflow: run indexes skipped: %v", err)
		return nil
	}
	if !regclass.Valid || regclass.String == "" {
		return nil
	}
	for _, stmt := range workflowRunIndexStatements(table) {
		createIndexBestEffort(ctx, db, stmt)
	}
	return nil
}

func workflowRunIndexStatements(table string) []string {
	return []string{
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s (next_retry_at, id) WHERE status = '%s' AND next_retry_at > 0`,
			workflowRunRetryIndexName, table, WorkflowRunStatusRetryWaiting),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s (workflow_id, id DESC)`, workflowRunListIndexName, table),
		fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s (approval_expires_at) WHERE status = '%s' AND approval_expires_at > 0`,
			workflowRunApprovalExpiryIndexName, table, WorkflowRunStatusWaitingApproval),
	}
}

// createIndexBestEffort runs one CREATE INDEX; a failure is logged, never returned. A savepoint
// keeps a failed statement from aborting the surrounding migration transaction; outside a
// transaction the savepoint itself fails and the statement just runs on its own.
func createIndexBestEffort(ctx context.Context, db bun.IDB, stmt string) {
	_, spErr := db.ExecContext(ctx, "SAVEPOINT wf_run_index")
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		log.Warnf("base-workflow: create index failed (boot continues): %v: %s", err, stmt)
		if spErr == nil {
			_, _ = db.ExecContext(ctx, "ROLLBACK TO SAVEPOINT wf_run_index")
		}
	}
	if spErr == nil {
		_, _ = db.ExecContext(ctx, "RELEASE SAVEPOINT wf_run_index")
	}
}
