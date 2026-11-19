package service

// File overview:
// - Responsibility: the base-automation go_handler that deletes base.calendar.recurrence rows whose
//   master record no longer exists. Dispatched by base-automation's own sys.cron bridge (via the
//   seeded automation.action/automation.cron rows in data/), not by a base-calendar-owned sys.cron
//   job, mirroring modules/base-credential/service/refresh_cron.go's claim-then-execute shape so one
//   broken row never trips the outer bridge job's own backoff.
// - Key symbols: `cleanupOrphanedRecurrencesHandlerKey`, `cleanupOrphanedRecurrences`, `claimBaseCalendarRecurrenceCandidates`.

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strconv"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/uptrace/bun"
)

const (
	cleanupOrphanedRecurrencesHandlerKey = "base_calendar.cleanup_orphaned_recurrences"
	// baseCalendarRecurrenceTable is the physical table backing base.calendar.recurrence.
	baseCalendarRecurrenceTable = "base_calendar_recurrence"
	// cleanupBatchLimit caps how many recurrence rules one tick inspects,
	// mirroring base-automation's/credential's own claim batch limit.
	cleanupBatchLimit = 100
	// cleanupMaxPages and cleanupTimeBudget bound one tick across all pages.
	cleanupMaxPages   = 100
	cleanupTimeBudget = 30 * time.Second
)

// baseCalendarRecurrenceCandidateRow is one row considered for orphan cleanup.
type baseCalendarRecurrenceCandidateRow struct {
	ID       int64  `bun:"id"`
	SysModel string `bun:"sys_model"`
	RecordID int64  `bun:"record_id"`
}

// cleanupOrphanedRecurrences is the go_handler registered with base-automation
// (see runtime.go). It never returns an error for a single row's check
// failing — only for infrastructure failures (no DB bound) — so one broken
// target model never trips the outer automation.cron bridge job's own backoff.
func cleanupOrphanedRecurrences(ctx context.Context, _ map[string]any) error {
	db := orm.DBFromContext(ctx)
	if db == nil {
		return errors.New("base-calendar: cleanup requires a database-bound context")
	}

	// Page through the table with an id cursor: a fixed "first 100" window would
	// never reach orphans past row 100. The loop ends on a short page, or when
	// the page/time budget runs out (the next tick resumes from the start, and
	// already-deleted orphans are gone, so progress is monotonic).
	deadline := time.Now().Add(cleanupTimeBudget)
	var afterID int64
	for page := 0; page < cleanupMaxPages && time.Now().Before(deadline); page++ {
		candidates, err := claimBaseCalendarRecurrenceCandidates(ctx, db, afterID, cleanupBatchLimit)
		if err != nil {
			if isMissingBaseCalendarRecurrenceRelation(err) {
				// A fresh or partially migrated DB without this module's table yet
				// is a normal condition, not a job failure.
				return nil
			}
			return err
		}
		deleteOrphanedBaseCalendarRecurrences(ctx, candidates)
		if len(candidates) < cleanupBatchLimit {
			return nil
		}
		afterID = candidates[len(candidates)-1].ID
	}
	return nil
}

// deleteOrphanedBaseCalendarRecurrences unlinks the orphaned rules of one page.
func deleteOrphanedBaseCalendarRecurrences(ctx context.Context, candidates []baseCalendarRecurrenceCandidateRow) {
	orphanedIDs := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if isOrphanedBaseCalendarRecurrence(ctx, candidate) {
			orphanedIDs = append(orphanedIDs, strconv.FormatInt(candidate.ID, 10))
		}
	}
	if len(orphanedIDs) == 0 {
		return
	}
	if _, err := orm.RuntimeRepositoryUnlink(ctx, orm.RuntimeModelRef("base.calendar.recurrence", nil), orphanedIDs); err != nil {
		log.Errorf("base-calendar: failed to delete orphaned recurrence rules %v: %v", orphanedIDs, err)
	}
}

// claimBaseCalendarRecurrenceCandidates reads a bounded batch of recurrence
// rows with id > afterID to check, in id order (the paging cursor). Unlike
// automation.cron's claim query, this needs no FOR UPDATE SKIP LOCKED
// ownership semantics: re-checking the same row on an overlapping tick is
// harmless (idempotent existence check), so a plain read keeps this simple.
func claimBaseCalendarRecurrenceCandidates(ctx context.Context, db bun.IDB, afterID int64, limit int) ([]baseCalendarRecurrenceCandidateRow, error) {
	if limit <= 0 {
		limit = cleanupBatchLimit
	}
	rows := make([]baseCalendarRecurrenceCandidateRow, 0, limit)
	query := fmt.Sprintf(`
		SELECT id, sys_model, record_id FROM %s
		WHERE id > ?
		ORDER BY id ASC
		LIMIT ?
	`, baseCalendarRecurrenceTable)
	if err := db.NewRaw(query, afterID, limit).Scan(ctx, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// isOrphanedBaseCalendarRecurrence reports whether a rule's master record is
// gone or its target model is no longer registered, with panic recovery
// mirroring internal/services/cron's runHandler.
func isOrphanedBaseCalendarRecurrence(ctx context.Context, candidate baseCalendarRecurrenceCandidateRow) (orphaned bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("base-calendar: cleanup check for recurrence %d panicked: %v\n%s", candidate.ID, r, debug.Stack())
			orphaned = false
		}
	}()
	if _, ok := orm.LookupModel(candidate.SysModel); !ok {
		return true
	}
	result, err := orm.ExecuteRuntimeMethod(ctx, candidate.SysModel, "read", []string{strconv.FormatInt(candidate.RecordID, 10)}, nil, map[string]any{"fields": []string{"id"}})
	if err != nil {
		// A transient read failure (ACL/RBAC on a sudo-less context, etc.) is
		// not evidence of an orphan — only an empty result set is.
		return false
	}
	return len(recordMapsFromCleanupResult(result)) == 0
}

// recordMapsFromCleanupResult normalizes ExecuteRuntimeMethod's heterogeneous
// return shapes into a length we can check, mirroring the equivalent helper
// in models/base_calendar_recurrence.go (kept local since packages must not
// cross-import each other's unexported helpers).
func recordMapsFromCleanupResult(value any) []map[string]any {
	switch typed := value.(type) {
	case []orm.RecordMap:
		out := make([]map[string]any, 0, len(typed))
		for _, row := range typed {
			out = append(out, row)
		}
		return out
	case orm.RecordMap:
		return []map[string]any{typed}
	case []map[string]any:
		return typed
	case map[string]any:
		return []map[string]any{typed}
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if row, ok := item.(map[string]any); ok {
				out = append(out, row)
			} else if row, ok := item.(orm.RecordMap); ok {
				out = append(out, row)
			}
		}
		return out
	default:
		return nil
	}
}

func isMissingBaseCalendarRecurrenceRelation(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42P01" // undefined_table
}
