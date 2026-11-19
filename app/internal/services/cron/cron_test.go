package cron

// File overview:
// - Responsibility: verifies the scheduler's claim, failure, and execution semantics against a real
//   PostgreSQL fixture (SRS-F-015 acceptance criteria).
// - Key symbols: focused Test cases plus DB fixture helpers.
// - Pattern: Locks down the SKIP LOCKED no-double-run guarantee, the exponential backoff and
//   deactivation path, and handler dispatch so refactors preserve scheduler safety.

import (
	"context"
	"errors"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	testutil "git.thebase.vn/base_hub/internal/test"
	"github.com/uptrace/bun"
)

// setupCronTestDB opens the shared PostgreSQL fixture, registers the sys.cron
// model, migrates it, and starts from an empty job table. Tests skip when no
// fixture database is reachable, matching the repo's DB-backed test style.
func setupCronTestDB(t *testing.T) *bun.DB {
	t.Helper()

	bunDB, err := orm.NewBunDB(testutil.EnvPostgresConfig())
	if err != nil {
		t.Skipf("open postgres fixture: %v", err)
	}
	t.Cleanup(func() { _ = bunDB.Close() })
	testutil.SwapBunDB(t, bunDB)

	// Raw DDL instead of migrating models.SysCronModel: importing internal/models
	// here creates an import cycle in tests (internal/models -> module models ->
	// a module package importing this cron package). Only the columns the
	// scheduler touches are needed; an existing migrated table is kept as is.
	db := bunDB.DB
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE IF NOT EXISTS `+cronFallbackTable+` (
		id bigserial PRIMARY KEY, name varchar, handler varchar, interval_seconds bigint,
		next_call timestamptz, fail_count bigint DEFAULT 0, max_failures bigint DEFAULT 5,
		payload jsonb, active boolean DEFAULT TRUE)`); err != nil {
		t.Fatalf("create sys_cron: %v", err)
	}
	// Each test owns the whole table so claims and counters are deterministic.
	if _, err := db.NewRaw("DELETE FROM " + cronFallbackTable).Exec(t.Context()); err != nil {
		t.Fatalf("clean sys_cron: %v", err)
	}
	return db
}

// insertCronJob seeds one job row that is already due so the next claim picks it up.
func insertCronJob(t *testing.T, db *bun.DB, name, handler string, failCount, maxFailures int64, payload string) int64 {
	t.Helper()
	var id int64
	if err := db.NewRaw(
		"INSERT INTO "+cronFallbackTable+
			" (name, handler, interval_seconds, next_call, fail_count, max_failures, payload, active)"+
			" VALUES (?, ?, 60, now() - interval '1 second', ?, ?, ?::jsonb, TRUE) RETURNING id",
		name, handler, failCount, maxFailures, payload,
	).Scan(t.Context(), &id); err != nil {
		t.Fatalf("insert cron job: %v", err)
	}
	return id
}

type cronRowState struct {
	FailCount int64     `bun:"fail_count"`
	Active    bool      `bun:"active"`
	NextCall  time.Time `bun:"next_call"`
}

// readCronRowState reads the bookkeeping columns for assertions.
func readCronRowState(t *testing.T, db *bun.DB, id int64) cronRowState {
	t.Helper()
	var state cronRowState
	if err := db.NewRaw(
		"SELECT fail_count, active, next_call FROM "+cronFallbackTable+" WHERE id = ?", id,
	).Scan(t.Context(), &state); err != nil {
		t.Fatalf("read cron row %d: %v", id, err)
	}
	return state
}

func TestClaimDueJobsSkipLockedPreventsDoubleClaim(t *testing.T) {
	db := setupCronTestDB(t)
	ctx := t.Context()
	insertCronJob(t, db, "contended", "test.contended", 0, 5, "{}")

	// First claimer holds the row lock in an open transaction, simulating a
	// scheduler process that claimed the job and is still mid-statement.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	first, err := claimDueJobs(ctx, tx, cronFallbackTable, 10)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("first claim = %d rows, want 1", len(first))
	}

	// A concurrent claimer must skip the locked row instead of blocking on it
	// or claiming it twice — this is the no-double-run guarantee.
	second, err := claimDueJobs(ctx, db, cronFallbackTable, 10)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("second claim = %d rows, want 0 (row is locked)", len(second))
	}

	// The first claimer dies before commit: its claim rolls back and the job
	// becomes claimable again, so a crash never loses a job.
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	third, err := claimDueJobs(ctx, db, cronFallbackTable, 10)
	if err != nil {
		t.Fatalf("third claim: %v", err)
	}
	if len(third) != 1 {
		t.Fatalf("third claim = %d rows, want 1 (rollback released the job)", len(third))
	}
}

func TestClaimDueJobsReschedulesInsideClaim(t *testing.T) {
	db := setupCronTestDB(t)
	ctx := t.Context()
	id := insertCronJob(t, db, "reschedule", "test.reschedule", 0, 5, "{}")

	claimed, err := claimDueJobs(ctx, db, cronFallbackTable, 10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != id {
		t.Fatalf("claim = %+v, want the seeded job", claimed)
	}

	// next_call moved forward inside the claim, so a repeat claim finds nothing:
	// a crash after this point delays the job by one interval, never duplicates it.
	state := readCronRowState(t, db, id)
	if !state.NextCall.After(time.Now().Add(30 * time.Second)) {
		t.Fatalf("next_call = %v, want pushed forward by the interval", state.NextCall)
	}
	again, err := claimDueJobs(ctx, db, cronFallbackTable, 10)
	if err != nil {
		t.Fatalf("repeat claim: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("repeat claim = %d rows, want 0", len(again))
	}
}

func TestMarkJobFailureAppliesBackoffThenDeactivates(t *testing.T) {
	db := setupCronTestDB(t)
	ctx := t.Context()
	id := insertCronJob(t, db, "failing", "test.failing", 0, 2, "{}")

	claimed, err := claimDueJobs(ctx, db, cronFallbackTable, 10)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = (%v, %v), want one row", claimed, err)
	}
	row := claimed[0]

	// First failure: fail_count 1 of 2 — backoff, still active.
	deactivated, err := markJobFailure(ctx, db, cronFallbackTable, row)
	if err != nil {
		t.Fatalf("first failure: %v", err)
	}
	if deactivated {
		t.Fatalf("first failure must not deactivate (1 of 2)")
	}
	state := readCronRowState(t, db, id)
	if state.FailCount != 1 || !state.Active {
		t.Fatalf("state after first failure = %+v, want fail_count 1 and active", state)
	}
	// Backoff is interval * 2^fail_count = 60s * 2 = 120s from now.
	if !state.NextCall.After(time.Now().Add(90*time.Second)) || state.NextCall.After(time.Now().Add(200*time.Second)) {
		t.Fatalf("next_call = %v, want exponential backoff (~120s) applied, not the 5 min lease", state.NextCall)
	}

	// Second failure crosses max_failures: the job deactivates itself.
	row.FailCount = 1
	deactivated, err = markJobFailure(ctx, db, cronFallbackTable, row)
	if err != nil {
		t.Fatalf("second failure: %v", err)
	}
	if !deactivated {
		t.Fatalf("second failure must deactivate (2 of 2)")
	}
	state = readCronRowState(t, db, id)
	if state.FailCount != 2 || state.Active {
		t.Fatalf("state after second failure = %+v, want fail_count 2 and inactive", state)
	}
}

func TestRecordJobFailurePublishesDeactivationAlert(t *testing.T) {
	db := setupCronTestDB(t)
	id := insertCronJob(t, db, "alerting", "test.alerting", 4, 5, "{}")

	// Swap the bus publisher so the test asserts the alert without requiring
	// the base.service.bus model in this fixture process.
	alerted := false
	original := publishCronDeactivationAlert
	publishCronDeactivationAlert = func(_ context.Context, job claimedJob, _ error) {
		alerted = true
		if job.row.ID != id {
			t.Errorf("alert for job %d, want %d", job.row.ID, id)
		}
	}
	t.Cleanup(func() { publishCronDeactivationAlert = original })

	s := &Service{baseCtx: t.Context()}
	s.recordJobFailure(claimedJob{row: cronJobRow{
		ID: id, Name: "alerting", Handler: "test.alerting",
		IntervalSeconds: 60, FailCount: 4, MaxFailures: 5,
	}}, errors.New("boom"))

	if !alerted {
		t.Fatalf("expected the deactivation alert to be published")
	}
	state := readCronRowState(t, db, id)
	if state.Active || state.FailCount != 5 {
		t.Fatalf("state = %+v, want inactive with fail_count 5", state)
	}
}

func TestExecuteJobRunsHandlerAndResetsFailCount(t *testing.T) {
	db := setupCronTestDB(t)
	id := insertCronJob(t, db, "healthy", "test.healthy", 3, 5, `{"limit": 7}`)

	var gotPayload map[string]any
	if err := RegisterHandler("test.healthy", func(_ context.Context, payload map[string]any) error {
		gotPayload = payload
		return nil
	}); err != nil {
		t.Fatalf("register handler: %v", err)
	}
	t.Cleanup(func() { UnregisterHandler("test.healthy") })

	claimed, err := claimDueJobs(t.Context(), db, cronFallbackTable, 10)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = (%v, %v), want one row", claimed, err)
	}

	s := &Service{baseCtx: t.Context()}
	if _, err := s.executeJob(t.Context(), claimedJob{row: claimed[0]}); err != nil {
		t.Fatalf("executeJob: %v", err)
	}

	if gotPayload == nil {
		t.Fatalf("expected the stub handler to run")
	}
	if limit, ok := gotPayload["limit"].(float64); !ok || limit != 7 {
		t.Fatalf("payload limit = %#v, want 7", gotPayload["limit"])
	}
	// A clean run resets the consecutive failure streak.
	state := readCronRowState(t, db, id)
	if state.FailCount != 0 {
		t.Fatalf("fail_count = %d after success, want 0", state.FailCount)
	}
}

func TestExecuteJobRecoversPanicsAsFailures(t *testing.T) {
	db := setupCronTestDB(t)
	id := insertCronJob(t, db, "panicky", "test.panicky", 0, 5, "{}")

	if err := RegisterHandler("test.panicky", func(context.Context, map[string]any) error {
		panic("kaboom")
	}); err != nil {
		t.Fatalf("register handler: %v", err)
	}
	t.Cleanup(func() { UnregisterHandler("test.panicky") })

	claimed, err := claimDueJobs(t.Context(), db, cronFallbackTable, 10)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = (%v, %v), want one row", claimed, err)
	}

	s := &Service{baseCtx: t.Context()}
	// A panicking handler must be converted into ordinary failure bookkeeping,
	// never allowed to crash the scheduler process.
	if _, err := s.executeJob(t.Context(), claimedJob{row: claimed[0]}); err != nil {
		t.Fatalf("executeJob: %v", err)
	}
	state := readCronRowState(t, db, id)
	if state.FailCount != 1 {
		t.Fatalf("fail_count = %d after panic, want 1", state.FailCount)
	}
}

func TestExecuteJobSkipsUnknownHandlerWithoutFailureBookkeeping(t *testing.T) {
	db := setupCronTestDB(t)
	id := insertCronJob(t, db, "orphan", "test.not_registered", 0, 5, "{}")

	claimed, err := claimDueJobs(t.Context(), db, cronFallbackTable, 10)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = (%v, %v), want one row", claimed, err)
	}

	s := &Service{baseCtx: t.Context()}
	if _, err := s.executeJob(t.Context(), claimedJob{row: claimed[0]}); err != nil {
		t.Fatalf("executeJob: %v", err)
	}
	// Unknown handlers are a deployment-topology situation (module compiled out
	// or registered later), so the job must keep its budget and stay active.
	state := readCronRowState(t, db, id)
	if state.FailCount != 0 || !state.Active {
		t.Fatalf("state = %+v, want untouched fail_count and active", state)
	}
}

func TestRegisterHandlerRejectsDuplicates(t *testing.T) {
	if err := RegisterHandler("test.duplicate", func(context.Context, map[string]any) error { return nil }); err != nil {
		t.Fatalf("first register: %v", err)
	}
	t.Cleanup(func() { UnregisterHandler("test.duplicate") })
	if err := RegisterHandler("test.duplicate", func(context.Context, map[string]any) error { return nil }); err == nil {
		t.Fatalf("expected duplicate registration to be rejected")
	}
}

// TestClaimLeasesJobForTimeoutThenSuccessReschedules covers BUG-K-4: the claim
// must hold the job for at least jobTimeout (a 60 s job running 5 min must not
// be claimed again mid-run), and a successful run restores the regular
// schedule (claim time + interval).
func TestClaimLeasesJobForTimeoutThenSuccessReschedules(t *testing.T) {
	db := setupCronTestDB(t)
	ctx := t.Context()
	id := insertCronJob(t, db, "lease", "test.lease", 0, 5, "{}")

	claimed, err := claimDueJobs(ctx, db, cronFallbackTable, 10)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = (%v, %v), want one row", claimed, err)
	}
	if lease := readCronRowState(t, db, id).NextCall; lease.Before(time.Now().Add(jobTimeout - 10*time.Second)) {
		t.Fatalf("lease next_call = %v, want at least jobTimeout ahead", lease)
	}

	if err := markJobSuccess(ctx, db, cronFallbackTable, claimed[0]); err != nil {
		t.Fatalf("mark success: %v", err)
	}
	next := readCronRowState(t, db, id).NextCall
	want := claimed[0].ClaimedAt.Add(60 * time.Second)
	if d := next.Sub(want); d < -time.Second || d > time.Second {
		t.Fatalf("next_call after success = %v, want claim time + interval %v", next, want)
	}
}

// TestMarkJobSuccessKeepsOperatorEdit: a next_call changed during the run is
// not overwritten by the success bookkeeping.
func TestMarkJobSuccessKeepsOperatorEdit(t *testing.T) {
	db := setupCronTestDB(t)
	ctx := t.Context()
	id := insertCronJob(t, db, "edited", "test.edited", 0, 5, "{}")
	claimed, err := claimDueJobs(ctx, db, cronFallbackTable, 10)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = (%v, %v), want one row", claimed, err)
	}
	if _, err := db.NewRaw("UPDATE "+cronFallbackTable+" SET next_call = now() + interval '3 hours' WHERE id = ?", id).Exec(ctx); err != nil {
		t.Fatalf("operator edit: %v", err)
	}
	if err := markJobSuccess(ctx, db, cronFallbackTable, claimed[0]); err != nil {
		t.Fatalf("mark success: %v", err)
	}
	if next := readCronRowState(t, db, id).NextCall; next.Before(time.Now().Add(2 * time.Hour)) {
		t.Fatalf("next_call = %v, want the operator's edit kept", next)
	}
}

// TestHasSQLStateMatchesUndefinedTableOnly covers K-12: only undefined_table
// (42P01) means "no jobs"; other errors still surface.
func TestHasSQLStateMatchesUndefinedTableOnly(t *testing.T) {
	db := setupCronTestDB(t)
	_, err := claimDueJobs(t.Context(), db, "sys_cron_missing_table", 1)
	if !hasSQLState(err, "42P01") {
		t.Fatalf("missing table error = %v, want SQLSTATE 42P01", err)
	}
	_, err = db.NewRaw("SELECT no_such_column FROM " + cronFallbackTable).Exec(t.Context())
	if err == nil || hasSQLState(err, "42P01") {
		t.Fatalf("missing column error = %v, must not match 42P01", err)
	}
}

func TestMarkJobFailureKeepsNextCallChangedDuringRun(t *testing.T) {
	db := setupCronTestDB(t)
	ctx := t.Context()
	id := insertCronJob(t, db, "failing-edited", "test.failing", 0, 5, "{}")

	claimed, err := claimDueJobs(ctx, db, cronFallbackTable, 10)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = (%v, %v), want one row", claimed, err)
	}
	// An operator reschedules the job while it runs.
	edited := time.Now().Add(3 * time.Hour).UTC().Truncate(time.Second)
	if _, err := db.NewRaw("UPDATE "+cronFallbackTable+" SET next_call = ? WHERE id = ?", edited, id).Exec(ctx); err != nil {
		t.Fatalf("operator edit: %v", err)
	}
	if _, err := markJobFailure(ctx, db, cronFallbackTable, claimed[0]); err != nil {
		t.Fatalf("failure: %v", err)
	}
	state := readCronRowState(t, db, id)
	if !state.NextCall.Equal(edited) || state.FailCount != 1 {
		t.Fatalf("state = %+v, want operator next_call %v kept and fail_count 1", state, edited)
	}
}
