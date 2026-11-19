package service

// File overview:
// - Responsibility: verifies the sys.cron bridge handler's claim, dispatch, and failure bookkeeping
//   against a real PostgreSQL fixture, mirroring internal/services/cron/cron_test.go's DB-backed style.
// - Key symbols: setupAutomationCronTestDB, insertAutomationAction, insertAutomationCron.

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	testutil "git.thebase.vn/base_hub/internal/test"
	automationmodels "git.thebase.vn/base_hub/modules/base-automation/models"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/uptrace/bun"
)

// setupAutomationCronTestDB opens the shared PostgreSQL fixture, registers
// automation.action/automation.cron, migrates them, and starts from empty
// tables. Tests skip when no fixture database is reachable, matching the
// repo's DB-backed test style (internal/services/cron/cron_test.go).
func setupAutomationCronTestDB(t *testing.T) *bun.DB {
	t.Helper()

	bunDB, err := orm.NewBunDB(testutil.EnvPostgresConfig())
	if err != nil {
		t.Skipf("open postgres fixture: %v", err)
	}
	t.Cleanup(func() { _ = bunDB.Close() })
	testutil.SwapBunDB(t, bunDB)

	automationmodels.AutomationActionModel.InitDefault()
	t.Cleanup(automationmodels.AutomationActionModel.Shutdown)
	automationmodels.AutomationCronModel.InitDefault()
	t.Cleanup(automationmodels.AutomationCronModel.Shutdown)

	db := bunDB.DB
	if err := orm.AutoMigrateRegisteredModelsToDB(t.Context(), db, orm.DefaultModelRegistry()); err != nil {
		t.Fatalf("auto-migrate registered models: %v", err)
	}
	// Each test owns both tables so claims and counters are deterministic.
	if _, err := db.NewRaw("DELETE FROM " + automationCronTable).Exec(t.Context()); err != nil {
		t.Fatalf("clean automation_cron: %v", err)
	}
	if _, err := db.NewRaw("DELETE FROM automation_action").Exec(t.Context()); err != nil {
		t.Fatalf("clean automation_action: %v", err)
	}
	return db
}

// insertAutomationAction seeds one automation.action row and returns its id.
func insertAutomationAction(t *testing.T, db *bun.DB, luaCode string) int64 {
	t.Helper()
	var id int64
	if err := db.NewRaw(
		"INSERT INTO automation_action (name, action_type, lua_code, active)"+
			" VALUES (?, 'lua', ?, TRUE) RETURNING id",
		"test action", luaCode,
	).Scan(t.Context(), &id); err != nil {
		t.Fatalf("insert automation_action: %v", err)
	}
	return id
}

// insertAutomationCron seeds one already-due automation.cron row.
func insertAutomationCron(t *testing.T, db *bun.DB, actionID int64, failCount, maxFailures int64) int64 {
	t.Helper()
	var id int64
	if err := db.NewRaw(
		"INSERT INTO "+automationCronTable+
			" (name, action_id, interval_seconds, next_call, fail_count, max_failures, active)"+
			" VALUES (?, ?, 30, now() - interval '1 second', ?, ?, TRUE) RETURNING id",
		"test cron", actionID, failCount, maxFailures,
	).Scan(t.Context(), &id); err != nil {
		t.Fatalf("insert automation_cron: %v", err)
	}
	return id
}

type automationCronRowState struct {
	FailCount     int64     `bun:"fail_count"`
	Active        bool      `bun:"active"`
	NextCall      time.Time `bun:"next_call"`
	LastRunStatus string    `bun:"last_run_status"`
}

func readAutomationCronRowState(t *testing.T, db *bun.DB, id int64) automationCronRowState {
	t.Helper()
	var state automationCronRowState
	if err := db.NewRaw(
		"SELECT fail_count, active, next_call, last_run_status FROM "+automationCronTable+" WHERE id = ?", id,
	).Scan(t.Context(), &state); err != nil {
		t.Fatalf("read automation_cron row %d: %v", id, err)
	}
	return state
}

func TestClaimDueAutomationCronsReschedulesInsideClaim(t *testing.T) {
	db := setupAutomationCronTestDB(t)
	ctx := t.Context()
	actionID := insertAutomationAction(t, db, `return {}`)
	id := insertAutomationCron(t, db, actionID, 0, 5)

	claimed, err := claimDueAutomationCrons(ctx, db, 10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != id {
		t.Fatalf("claim = %+v, want the seeded row", claimed)
	}

	// next_call moved forward inside the claim, so a repeat claim finds nothing.
	state := readAutomationCronRowState(t, db, id)
	if !state.NextCall.After(time.Now().Add(20 * time.Second)) {
		t.Fatalf("next_call = %v, want pushed forward by the interval", state.NextCall)
	}
	again, err := claimDueAutomationCrons(ctx, db, 10)
	if err != nil {
		t.Fatalf("repeat claim: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("repeat claim = %d rows, want 0", len(again))
	}
}

func TestDispatchDueAutomationCronsRecordsSuccess(t *testing.T) {
	db := setupAutomationCronTestDB(t)
	ctx := context.Background()
	actionID := insertAutomationAction(t, db, `return {ok = true}`)
	id := insertAutomationCron(t, db, actionID, 0, 5)

	if err := dispatchDueAutomationCrons(orm.ContextWithEnvironment(ctx, orm.NewEnvironmentWithDBName(db, "test", 0, nil, true)), nil); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	state := readAutomationCronRowState(t, db, id)
	if state.LastRunStatus != "ok" {
		t.Fatalf("last_run_status = %q, want ok", state.LastRunStatus)
	}
	if state.FailCount != 0 {
		t.Fatalf("fail_count = %d, want 0", state.FailCount)
	}
	if !state.Active {
		t.Fatalf("active = false, want true after a successful run")
	}
}

func TestDispatchDueAutomationCronsDeactivatesAfterMaxFailures(t *testing.T) {
	db := setupAutomationCronTestDB(t)
	ctx := context.Background()
	// A Lua runtime error drives ExecuteAction's error path.
	actionID := insertAutomationAction(t, db, `error("boom")`)
	id := insertAutomationCron(t, db, actionID, 0, 1)

	if err := dispatchDueAutomationCrons(orm.ContextWithEnvironment(ctx, orm.NewEnvironmentWithDBName(db, "test", 0, nil, true)), nil); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	state := readAutomationCronRowState(t, db, id)
	if state.LastRunStatus != "error" {
		t.Fatalf("last_run_status = %q, want error", state.LastRunStatus)
	}
	if state.FailCount != 1 {
		t.Fatalf("fail_count = %d, want 1", state.FailCount)
	}
	if state.Active {
		t.Fatalf("active = true, want false once fail_count reaches max_failures")
	}
}

// insertInactiveAutomationAction seeds one paused (active=false) lua action.
func insertInactiveAutomationAction(t *testing.T, db *bun.DB) int64 {
	t.Helper()
	var id int64
	if err := db.NewRaw(
		"INSERT INTO automation_action (name, action_type, lua_code, active) VALUES ('paused', 'lua', 'return {}', FALSE) RETURNING id",
	).Scan(t.Context(), &id); err != nil {
		t.Fatalf("insert automation_action: %v", err)
	}
	return id
}

// A paused action is skipped by the claim, not charged as a cron failure.
func TestClaimDueAutomationCronsSkipsInactiveAction(t *testing.T) {
	db := setupAutomationCronTestDB(t)
	id := insertAutomationCron(t, db, insertInactiveAutomationAction(t, db), 0, 1)

	claimed, err := claimDueAutomationCrons(t.Context(), db, 10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claim = %+v, want none for a paused action", claimed)
	}
	if state := readAutomationCronRowState(t, db, id); !state.Active || state.FailCount != 0 {
		t.Fatalf("paused action's cron changed: %+v", state)
	}
}

// Bookkeeping must land even when the dispatch context is already cancelled.
func TestRunAutomationCronRowRecordsFailureOnCancelledContext(t *testing.T) {
	db := setupAutomationCronTestDB(t)
	actionID := insertAutomationAction(t, db, `return {}`)
	id := insertAutomationCron(t, db, actionID, 0, 5)

	ctx, cancel := context.WithCancel(orm.ContextWithEnvironment(context.Background(), orm.NewEnvironmentWithDBName(db, "test", 0, nil, true)))
	cancel()
	runAutomationCronRow(ctx, db, automationCronRow{ID: id, ActionID: actionID, IntervalSeconds: 30, MaxFailures: 5})

	state := readAutomationCronRowState(t, db, id)
	if state.LastRunStatus == "" {
		t.Fatalf("no bookkeeping recorded on a cancelled context: %+v", state)
	}
}

func TestIsMissingAutomationCronRelationMatchesSQLState(t *testing.T) {
	if !isMissingAutomationCronRelation(fmt.Errorf("claim: %w", &pgconn.PgError{Code: "42P01"})) {
		t.Fatal("wrapped 42P01 must match")
	}
	if isMissingAutomationCronRelation(errors.New(`relation "automation_cron" does not exist`)) || isMissingAutomationCronRelation(nil) {
		t.Fatal("text/nil must not match")
	}
}
