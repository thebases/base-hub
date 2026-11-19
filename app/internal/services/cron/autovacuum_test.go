package cron

// File overview:
// - Responsibility: verifies the SRS §5.3 autovacuum retention semantics against a real PostgreSQL
//   fixture — aged terminal runs (and their deliveries) disappear, everything else survives.
// - Key symbols: focused Test cases plus scratch-table fixtures.
// - Pattern: Redirects the vacuum table bindings to test-owned scratch tables so the shared fixture
//   database's real audit rows are never deleted by a test run.

import (
	"context"
	"fmt"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
	testutil "git.thebase.vn/base_hub/internal/test"
	"github.com/uptrace/bun"
)

// setupAutovacuumTables opens the fixture DB and rebinds the vacuum targets to
// scratch tables owned by this test, restoring the real bindings on cleanup.
func setupAutovacuumTables(t *testing.T) *bun.DB {
	t.Helper()

	bunDB, err := orm.NewBunDB(testutil.EnvPostgresConfig())
	if err != nil {
		t.Skipf("open postgres fixture: %v", err)
	}
	t.Cleanup(func() { _ = bunDB.Close() })
	db := bunDB.DB

	prevRun, prevOutbound, prevLog, prevConfig := vacuumWorkflowRunTable, vacuumOutboundDeliveryTable, vacuumModelLogTable, vacuumConfigParamTable
	vacuumWorkflowRunTable = "test_av_workflow_run"
	vacuumOutboundDeliveryTable = "test_av_outbound_delivery"
	vacuumModelLogTable = "test_av_model_log"
	vacuumConfigParamTable = "test_av_config_param"
	t.Cleanup(func() {
		vacuumWorkflowRunTable, vacuumOutboundDeliveryTable, vacuumModelLogTable, vacuumConfigParamTable = prevRun, prevOutbound, prevLog, prevConfig
	})

	ctx := t.Context()
	for _, ddl := range []string{
		"CREATE TABLE IF NOT EXISTS " + vacuumWorkflowRunTable + " (id bigserial PRIMARY KEY, name text, status text, created_at timestamptz, updated_at timestamptz)",
		"CREATE TABLE IF NOT EXISTS " + vacuumOutboundDeliveryTable + " (id bigserial PRIMARY KEY, name text)",
		"CREATE TABLE IF NOT EXISTS " + vacuumModelLogTable + " (id bigserial PRIMARY KEY, created_at timestamptz)",
		"CREATE TABLE IF NOT EXISTS " + vacuumConfigParamTable + " (id bigserial PRIMARY KEY, name text UNIQUE, value jsonb)",
		"DELETE FROM " + vacuumWorkflowRunTable,
		"DELETE FROM " + vacuumOutboundDeliveryTable,
		"DELETE FROM " + vacuumModelLogTable,
		"DELETE FROM " + vacuumConfigParamTable,
	} {
		if _, err := db.NewRaw(ddl).Exec(ctx); err != nil {
			t.Fatalf("prepare scratch table: %v", err)
		}
	}
	return db
}

// insertRun seeds one workflow-run row with the given age in days.
func insertRun(t *testing.T, db *bun.DB, name, status string, ageDays int) {
	t.Helper()
	if _, err := db.NewRaw(
		fmt.Sprintf("INSERT INTO %s (name, status, created_at, updated_at) VALUES (?, ?, now() - make_interval(days => ?), now() - make_interval(days => ?))", vacuumWorkflowRunTable),
		name, status, ageDays, ageDays,
	).Exec(t.Context()); err != nil {
		t.Fatalf("insert run %s: %v", name, err)
	}
}

func countRows(t *testing.T, db *bun.DB, table, where string, args ...any) int {
	t.Helper()
	var count int
	query := "SELECT count(*) FROM " + table
	if where != "" {
		query += " WHERE " + where
	}
	if err := db.NewRaw(query, args...).Scan(t.Context(), &count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func TestVacuumWorkflowRunsDeletesAgedTerminalRunsAndDeliveries(t *testing.T) {
	db := setupAutovacuumTables(t)
	ctx := t.Context()

	// Aged terminal run with two delivery audit rows — must disappear together.
	insertRun(t, db, "req-old-done", "succeeded", 200)
	for i := 0; i < 2; i++ {
		if _, err := db.NewRaw(
			fmt.Sprintf("INSERT INTO %s (name) VALUES (?)", vacuumOutboundDeliveryTable), "req-old-done",
		).Exec(ctx); err != nil {
			t.Fatalf("insert delivery: %v", err)
		}
	}
	// Aged but non-terminal: retention must never touch it regardless of age.
	insertRun(t, db, "req-old-running", "running", 200)
	// Terminal but young: inside the window, survives.
	insertRun(t, db, "req-young-done", "failed", 10)

	runs, deliveries, err := vacuumWorkflowRuns(ctx, db, 180)
	if err != nil {
		t.Fatalf("vacuumWorkflowRuns: %v", err)
	}
	if runs != 1 || deliveries != 2 {
		t.Fatalf("deleted (runs=%d, deliveries=%d), want (1, 2)", runs, deliveries)
	}
	if n := countRows(t, db, vacuumWorkflowRunTable, "name = ?", "req-old-done"); n != 0 {
		t.Fatalf("aged terminal run survived (%d rows)", n)
	}
	if n := countRows(t, db, vacuumOutboundDeliveryTable, "", nil...); n != 0 {
		t.Fatalf("deliveries of vacuumed run survived (%d rows)", n)
	}
	if n := countRows(t, db, vacuumWorkflowRunTable, "name = ?", "req-old-running"); n != 1 {
		t.Fatalf("non-terminal run must survive vacuum, found %d", n)
	}
	if n := countRows(t, db, vacuumWorkflowRunTable, "name = ?", "req-young-done"); n != 1 {
		t.Fatalf("young terminal run must survive vacuum, found %d", n)
	}
}

func TestVacuumModelLogsDeletesOnlyAgedRows(t *testing.T) {
	db := setupAutovacuumTables(t)
	ctx := t.Context()

	for _, age := range []int{400, 10} {
		if _, err := db.NewRaw(
			fmt.Sprintf("INSERT INTO %s (created_at) VALUES (now() - make_interval(days => ?))", vacuumModelLogTable), age,
		).Exec(ctx); err != nil {
			t.Fatalf("insert model log: %v", err)
		}
	}

	deleted, err := vacuumModelLogs(ctx, db, 365)
	if err != nil {
		t.Fatalf("vacuumModelLogs: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	if n := countRows(t, db, vacuumModelLogTable, "", nil...); n != 1 {
		t.Fatalf("young log row must survive, found %d rows", n)
	}
}

func TestRetentionDaysReadsConfigParamWithFallback(t *testing.T) {
	db := setupAutovacuumTables(t)
	ctx := t.Context()

	// Absent param falls back to the SRS default.
	if got := retentionDays(ctx, db, retentionWorkflowRunDaysKey, 180); got != 180 {
		t.Fatalf("fallback = %d, want 180", got)
	}
	// jsonb number and quoted-string forms both parse.
	for _, seed := range []string{"90", `"45"`} {
		if _, err := db.NewRaw(
			fmt.Sprintf("INSERT INTO %s (name, value) VALUES (?, ?::jsonb) ON CONFLICT (name) DO UPDATE SET value = EXCLUDED.value", vacuumConfigParamTable),
			retentionWorkflowRunDaysKey, seed,
		).Exec(ctx); err != nil {
			t.Fatalf("seed config param: %v", err)
		}
		want := int64(90)
		if seed != "90" {
			want = 45
		}
		if got := retentionDays(ctx, db, retentionWorkflowRunDaysKey, 180); got != want {
			t.Fatalf("retentionDays with %s = %d, want %d", seed, got, want)
		}
	}
	// Non-positive values are rejected in favor of the default so a bad param
	// can never turn retention into delete-everything.
	if _, err := db.NewRaw(
		fmt.Sprintf("UPDATE %s SET value = '0'::jsonb WHERE name = ?", vacuumConfigParamTable), retentionWorkflowRunDaysKey,
	).Exec(ctx); err != nil {
		t.Fatalf("update config param: %v", err)
	}
	if got := retentionDays(ctx, db, retentionWorkflowRunDaysKey, 180); got != 180 {
		t.Fatalf("non-positive value must fall back, got %d", got)
	}
}

func TestRunAutovacuumEndToEndUsesEnvironmentDB(t *testing.T) {
	db := setupAutovacuumTables(t)

	insertRun(t, db, "req-e2e-old", "cancelled", 200)
	insertRun(t, db, "req-e2e-young", "succeeded", 5)

	// The handler resolves its DB from the environment exactly like a real
	// scheduler-claimed job context.
	ctx := orm.ContextWithEnvironment(t.Context(), orm.NewEnvironment(db, 0, nil, true))
	if err := runAutovacuum(ctx, map[string]any{}); err != nil {
		t.Fatalf("runAutovacuum: %v", err)
	}
	if n := countRows(t, db, vacuumWorkflowRunTable, "", nil...); n != 1 {
		t.Fatalf("expected only the young run to survive, found %d rows", n)
	}
}

func TestVacuumToleratesMissingTables(t *testing.T) {
	db := setupAutovacuumTables(t)
	ctx := context.Background()

	prev := vacuumWorkflowRunTable
	vacuumWorkflowRunTable = "test_av_missing_table"
	t.Cleanup(func() { vacuumWorkflowRunTable = prev })

	// Databases without the workflow tables have nothing to vacuum — that is a
	// no-op, never a job failure that would burn the cron fail budget.
	runs, deliveries, err := vacuumWorkflowRuns(ctx, db, 180)
	if err != nil || runs != 0 || deliveries != 0 {
		t.Fatalf("missing table = (%d, %d, %v), want (0, 0, nil)", runs, deliveries, err)
	}
}
