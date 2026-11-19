package service

// File overview:
// - Responsibility: the single bridge handler between the kernel `sys.cron` scheduler and user-defined
//   `automation.cron` rows. One seeded sys.cron row (`automation.dispatch_due_crons`, data/
//   01_sys_cron_automation_dispatch.json) ticks this handler; it claims due automation.cron rows
//   itself and executes their linked action. A failing inner row must never fail the outer sys.cron
//   job or trip its own backoff/deactivation — that is why claiming and failure bookkeeping happen
//   here, at this layer, instead of overloading sys.cron's own retry state.
// - Key symbols: `dispatchHandlerKey`, `dispatchDueAutomationCrons`, `claimDueAutomationCrons`.

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/uptrace/bun"
)

const (
	dispatchHandlerKey = "automation.dispatch_due_crons"
	// automationCronTable is the physical table backing automation.cron.
	automationCronTable = "automation_cron"
	// automationCronMinIntervalSeconds mirrors the model-level floor
	// (models.AutomationCronMinIntervalSeconds) so a legacy row that predates
	// validation still cannot hot-loop the claim query.
	automationCronMinIntervalSeconds = 30
	// dispatchBatchLimit caps how many automation.cron rows one tick claims,
	// mirroring internal/services/cron's own claimBatchLimit so a large
	// backlog cannot flood one dispatch call.
	dispatchBatchLimit = 16
	// automationCronRowTimeout bounds one claimed row's action so a single hung
	// action cannot consume the whole outer sys.cron jobTimeout (5 min) and
	// starve the rest of the batch.
	automationCronRowTimeout = 60 * time.Second
	// automationCronBookkeepingTimeout bounds the fail_count/status update,
	// which runs detached from the row's (possibly expired) context.
	automationCronBookkeepingTimeout = 5 * time.Second
)

// automationCronRow is the claimed-row shape returned by claimDueAutomationCrons.
type automationCronRow struct {
	ID              int64 `bun:"id"`
	ActionID        int64 `bun:"action_id"`
	IntervalSeconds int64 `bun:"interval_seconds"`
	FailCount       int64 `bun:"fail_count"`
	MaxFailures     int64 `bun:"max_failures"`
}

// dispatchDueAutomationCrons is the sys.cron-registered handler. It never
// returns an error for a single row's failure — only for infrastructure
// failures (no DB bound) — so one broken automation never trips the outer
// sys.cron job's own backoff/deactivation.
func dispatchDueAutomationCrons(ctx context.Context, _ map[string]any) error {
	db := orm.DBFromContext(ctx)
	if db == nil {
		return fmt.Errorf("automation: dispatch requires a database-bound context")
	}

	rows, err := claimDueAutomationCrons(ctx, db, dispatchBatchLimit)
	if err != nil {
		if isMissingAutomationCronRelation(err) {
			// A fresh or partially migrated DB without the module's tables yet
			// is a normal condition, not a job failure.
			return nil
		}
		return err
	}

	for i, row := range rows {
		// Once the outer job context is gone (shutdown or jobTimeout), the
		// remaining claimed rows would only fail on a dead context and be
		// charged a failure they did not cause; leave them to their next
		// next_call (already pushed one interval by the claim).
		if ctx.Err() != nil {
			log.Warnf("automation: dispatch stopped (%v); %d claimed cron row(s) deferred to their next interval", ctx.Err(), len(rows)-i)
			break
		}
		runAutomationCronRow(ctx, db, row)
	}
	return nil
}

// claimDueAutomationCrons claims due rows with FOR UPDATE SKIP LOCKED and
// pushes next_call forward inside the same statement, mirroring
// internal/services/cron's claimDueJobs so a crash mid-execution only delays
// a schedule by one interval instead of losing or duplicating it.
func claimDueAutomationCrons(ctx context.Context, db bun.IDB, limit int) ([]automationCronRow, error) {
	if limit <= 0 {
		limit = dispatchBatchLimit
	}
	rows := make([]automationCronRow, 0, limit)
	query := fmt.Sprintf(`
		UPDATE %s AS c
		SET next_call = now() + make_interval(secs => GREATEST(c.interval_seconds, %d))
		WHERE c.id IN (
			SELECT t.id FROM %s AS t
			WHERE t.active = TRUE AND t.next_call <= now()
				-- A paused (inactive) action is skipped, not failed: ExecuteAction
				-- would refuse it, and charging the cron for that would deactivate
				-- the schedule after max_failures ticks of a deliberate pause.
				AND EXISTS (SELECT 1 FROM automation_action AS a WHERE a.id = t.action_id AND a.active = TRUE)
			ORDER BY t.next_call ASC
			LIMIT ?
			FOR UPDATE SKIP LOCKED
		)
		RETURNING c.id, c.action_id, c.interval_seconds, c.fail_count, c.max_failures
	`, automationCronTable, automationCronMinIntervalSeconds, automationCronTable)
	if err := db.NewRaw(query, limit).Scan(ctx, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// runAutomationCronRow executes one claimed row's action with panic recovery
// (mirroring internal/services/cron's runHandler) and records success/failure
// bookkeeping directly on the automation_cron row.
//
// The action runs under its own automationCronRowTimeout; bookkeeping runs on
// context.WithoutCancel (same tenant DB binding, no inherited deadline) so a
// row that timed out still gets its failure recorded instead of the UPDATE
// failing on the dead context and the failure being silently lost.
func runAutomationCronRow(ctx context.Context, db bun.IDB, row automationCronRow) {
	rowCtx, cancel := context.WithTimeout(ctx, automationCronRowTimeout)
	err := runAutomationActionWithRecover(rowCtx, row.ActionID)
	cancel()

	bookCtx, bookCancel := context.WithTimeout(context.WithoutCancel(ctx), automationCronBookkeepingTimeout)
	defer bookCancel()
	if err != nil {
		log.Warnf("automation: cron %d action %d failed: %v", row.ID, row.ActionID, err)
		recordAutomationCronFailure(bookCtx, db, row)
		return
	}
	recordAutomationCronSuccess(bookCtx, db, row.ID)
}

func runAutomationActionWithRecover(ctx context.Context, actionID int64) (err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("automation: cron action %d panicked: %v\n%s", actionID, r, debug.Stack())
			err = fmt.Errorf("automation action %d panicked: %v", actionID, r)
		}
	}()
	_, err = ExecuteAction(ctx, actionID, map[string]any{})
	return err
}

func recordAutomationCronSuccess(ctx context.Context, db bun.IDB, cronID int64) {
	if _, err := db.NewUpdate().
		TableExpr("automation_cron").
		Set("fail_count = 0").
		Set("last_run_status = 'ok'").
		Where("id = ?", cronID).
		Exec(ctx); err != nil {
		log.Errorf("automation: record cron %d success: %v", cronID, err)
	}
}

// recordAutomationCronFailure increments fail_count and deactivates the row
// once it exceeds max_failures, the same policy the kernel sys.cron scheduler
// applies to its own jobs (SRS-F-015) — reimplemented here because these
// rows never touch the outer sys.cron job's own backoff state.
func recordAutomationCronFailure(ctx context.Context, db bun.IDB, row automationCronRow) {
	failCount := row.FailCount + 1
	query := db.NewUpdate().
		TableExpr("automation_cron").
		Set("fail_count = ?", failCount).
		Set("last_run_status = 'error'").
		Where("id = ?", row.ID)
	if row.MaxFailures > 0 && failCount >= row.MaxFailures {
		query = query.Set("active = FALSE")
	}
	if _, err := query.Exec(ctx); err != nil {
		log.Errorf("automation: record cron %d failure: %v", row.ID, err)
	}
}

// isMissingAutomationCronRelation matches SQLSTATE 42P01 (undefined_table):
// the claim touches only automation_cron/automation_action, both owned by this
// module, so either missing means a not-yet-migrated DB.
func isMissingAutomationCronRelation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42P01"
}
