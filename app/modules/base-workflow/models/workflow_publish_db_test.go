package models

import (
	"context"
	"fmt"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	testutil "git.thebase.vn/base_hub/internal/test"
	"github.com/uptrace/bun"
)

// publishTestDB opens one connection on a throwaway schema holding minimal workflow, action, edge
// and version tables under their real storage names. Skips without Postgres.
func publishTestDB(t *testing.T) (context.Context, bun.Conn) {
	t.Helper()
	bunDB, err := orm.NewBunDB(testutil.EnvPostgresConfig())
	if err != nil {
		t.Skipf("open postgres fixture: %v", err)
	}
	t.Cleanup(func() { _ = bunDB.Close() })
	ctx := context.Background()
	schema := fmt.Sprintf("wf_publish_test_%d", time.Now().UnixNano())
	conn, err := bunDB.DB.Conn(ctx)
	if err != nil {
		t.Skipf("conn: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE")
		_ = conn.Close()
	})
	for _, stmt := range []string{
		"CREATE SCHEMA " + schema,
		"SET search_path TO " + schema,
		`CREATE TABLE base_workflow (id BIGSERIAL PRIMARY KEY, active BOOLEAN DEFAULT true, created_at TIMESTAMP DEFAULT now(),
			updated_at TIMESTAMP, name TEXT, topic TEXT, version INT, company_id BIGINT, revision BIGINT DEFAULT 0,
			execution_mode TEXT DEFAULT 'linear', max_parallel_steps INT DEFAULT 4, dag_failure_policy TEXT DEFAULT 'fail_branch',
			audit_redact_keys TEXT, max_run_seconds INT DEFAULT 0, max_concurrent_runs INT DEFAULT 0,
			state TEXT DEFAULT 'published', published_version INT DEFAULT 0, published_at TIMESTAMP, published_snapshot JSONB)`,
		`CREATE TABLE base_workflow_action (id BIGSERIAL PRIMARY KEY, active BOOLEAN DEFAULT true, created_at TIMESTAMP DEFAULT now(),
			updated_at TIMESTAMP, workflow_id BIGINT, company_id BIGINT, sequence INT, name TEXT, action_code TEXT, action_version INT,
			is_active BOOLEAN DEFAULT true, step_key TEXT, timeout_seconds INT DEFAULT 30, retry_policy JSONB, extra_params JSONB)`,
		`CREATE TABLE base_workflow_edge (id BIGSERIAL PRIMARY KEY, active BOOLEAN DEFAULT true, workflow_id BIGINT,
			from_step_key TEXT, to_step_key TEXT, edge_type TEXT, condition_expr TEXT DEFAULT '')`,
		`CREATE TABLE base_workflow_version (id BIGSERIAL PRIMARY KEY, active BOOLEAN DEFAULT true, created_at TIMESTAMP,
			updated_at TIMESTAMP, workflow_id BIGINT, company_id BIGINT, version INT, revision BIGINT DEFAULT 0, snapshot JSONB)`,
		// 1: pre-T97 workflow (published by the column default, no snapshot), version 3, two steps
		// (one inactive) and one edge. 2: workflow created after T97, still a draft.
		`INSERT INTO base_workflow (id, name, topic, version, company_id, revision, execution_mode, max_run_seconds)
			VALUES (1, 'legacy', 'legacy.in', 3, 5, 9, 'dag', 45)`,
		`INSERT INTO base_workflow (id, name, topic, version, state) VALUES (2, 'new', 'new.in', 1, 'draft')`,
		`INSERT INTO base_workflow_action (workflow_id, sequence, name, action_code, action_version, step_key, retry_policy)
			VALUES (1, 20, 'B', 'parse_payload', 1, 'b', '{"max_attempts": 7}'), (1, 10, 'A', 'parse_payload', 1, 'a', NULL),
			(2, 10, 'X', 'parse_payload', 1, 'x', NULL)`,
		`INSERT INTO base_workflow_action (workflow_id, sequence, name, action_code, action_version, step_key, is_active)
			VALUES (1, 30, 'Off', 'parse_payload', 1, 'off', false)`,
		`INSERT INTO base_workflow_edge (workflow_id, from_step_key, to_step_key, edge_type) VALUES (1, 'a', 'b', 'success')`,
	} {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	return ctx, conn
}

func TestBackfillWorkflowPublishedSnapshotsAgainstPostgres(t *testing.T) {
	ctx, conn := publishTestDB(t)
	run := func() {
		t.Helper()
		if err := ensureWorkflowVersionIndex(ctx, conn); err != nil {
			t.Fatalf("index: %v", err)
		}
		if err := backfillWorkflowPublishedSnapshots(ctx, conn); err != nil {
			t.Fatalf("backfill: %v", err)
		}
	}
	type flowRow struct {
		State            string         `bun:"state"`
		PublishedVersion int            `bun:"published_version"`
		PublishedAt      time.Time      `bun:"published_at,nullzero"`
		Snapshot         map[string]any `bun:"published_snapshot,type:jsonb"`
	}
	readFlow := func(id int64) flowRow {
		t.Helper()
		var row flowRow
		if err := conn.NewRaw("SELECT state, published_version, published_at, published_snapshot FROM base_workflow WHERE id = ?", id).Scan(ctx, &row); err != nil {
			t.Fatal(err)
		}
		return row
	}
	versionRows := func() int {
		t.Helper()
		var count int
		if err := conn.NewRaw("SELECT count(*) FROM base_workflow_version").Scan(ctx, &count); err != nil {
			t.Fatal(err)
		}
		return count
	}

	run()
	legacy := readFlow(1)
	if legacy.State != WorkflowStatePublished || legacy.PublishedVersion != 3 || legacy.PublishedAt.IsZero() {
		t.Fatalf("legacy = %+v, want published as version 3", legacy)
	}
	snapshot, err := DecodeWorkflowSnapshot(legacy.Snapshot)
	if err != nil || snapshot == nil {
		t.Fatalf("snapshot = %v (%v)", legacy.Snapshot, err)
	}
	if snapshot.Version != 3 || snapshot.Revision != 9 || snapshot.Flow.ExecutionMode != "dag" || snapshot.Flow.MaxRunSeconds != 45 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if len(snapshot.Steps) != 2 || snapshot.Steps[0].Key != "a" || snapshot.Steps[1].Key != "b" || snapshot.Steps[1].RetryPolicy.MaxAttempts != 7 {
		t.Fatalf("steps = %+v, want active steps a, b in sequence order", snapshot.Steps)
	}
	if len(snapshot.Edges) != 1 || snapshot.Edges[0].FromStepKey != "a" {
		t.Fatalf("edges = %+v", snapshot.Edges)
	}
	if draft := readFlow(2); draft.State != WorkflowStateDraft || draft.Snapshot != nil || draft.PublishedVersion != 0 {
		t.Fatalf("draft = %+v, want untouched", draft)
	}
	var stored struct {
		Version   int   `bun:"version"`
		CompanyID int64 `bun:"company_id"`
	}
	if err := conn.NewRaw("SELECT version, company_id FROM base_workflow_version WHERE workflow_id = 1").Scan(ctx, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Version != 3 || stored.CompanyID != 5 || versionRows() != 1 {
		t.Fatalf("version row = %+v (rows %d), want v3 of company 5", stored, versionRows())
	}

	// Idempotent: a second boot changes nothing.
	run()
	again := readFlow(1)
	if !again.PublishedAt.Equal(legacy.PublishedAt) || again.PublishedVersion != 3 || versionRows() != 1 {
		t.Fatalf("rerun changed the workflow: %+v (rows %d)", again, versionRows())
	}

	// The unique index refuses a second row for the same version.
	if _, err := conn.ExecContext(ctx, "INSERT INTO base_workflow_version (workflow_id, version) VALUES (1, 3)"); err == nil {
		t.Fatal("duplicate (workflow_id, version) accepted")
	}
}
