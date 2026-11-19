package models

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	testutil "git.thebase.vn/base_hub/internal/test"
)

// TestDeleteExpiredBatchAgainstPostgres runs the purge SQL and the index DDL on a throwaway schema.
func TestDeleteExpiredBatchAgainstPostgres(t *testing.T) {
	bunDB, err := orm.NewBunDB(testutil.EnvPostgresConfig())
	if err != nil {
		t.Skipf("open postgres fixture: %v", err)
	}
	t.Cleanup(func() { _ = bunDB.Close() })
	ctx := context.Background()
	schema := fmt.Sprintf("wf_retention_test_%d", time.Now().UnixNano())
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
		`CREATE TABLE wf_run (id BIGSERIAL PRIMARY KEY, workflow_id BIGINT, status TEXT NOT NULL,
			next_retry_at BIGINT NOT NULL DEFAULT 0, approval_expires_at BIGINT NOT NULL DEFAULT 0,
			updated_at TIMESTAMPTZ NOT NULL)`,
	} {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	old := time.Now().UTC().AddDate(0, 0, -60)
	fresh := time.Now().UTC()
	for _, row := range []struct {
		status string
		at     time.Time
		n      int
	}{
		{WorkflowRunStatusSucceeded, old, 5}, {WorkflowRunStatusFailed, old, 2},
		{WorkflowRunStatusSucceeded, fresh, 2}, {WorkflowRunStatusRunning, old, 2},
		{WorkflowRunStatusRetryWaiting, old, 1}, {WorkflowRunStatusPending, old, 1},
	} {
		for i := 0; i < row.n; i++ {
			if _, err := conn.ExecContext(ctx, "INSERT INTO wf_run (status, updated_at) VALUES (?, ?)", row.status, row.at); err != nil {
				t.Fatal(err)
			}
		}
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -30)
	total, err := purgeInBatches(ctx, 3, 100, func(ctx context.Context, limit int) (int64, error) {
		return deleteExpiredBatch(ctx, conn, "wf_run", workflowRunTerminalStatuses, cutoff, limit)
	})
	if err != nil || total != 7 {
		t.Fatalf("deleted %d err=%v, want 7", total, err)
	}
	var left int
	if err := conn.NewRaw("SELECT COUNT(*) FROM wf_run").Scan(ctx, &left); err != nil || left != 6 {
		t.Fatalf("left %d err=%v, want 6 (2 fresh + 4 resumable)", left, err)
	}
	for _, stmt := range workflowRunIndexStatements("wf_run") {
		createIndexBestEffort(ctx, conn, stmt)
		createIndexBestEffort(ctx, conn, stmt) // idempotent
	}
	var indexes int
	if err := conn.NewRaw("SELECT COUNT(*) FROM pg_indexes WHERE schemaname = ? AND tablename = 'wf_run' AND indexname IN (?, ?, ?)",
		schema, workflowRunRetryIndexName, workflowRunListIndexName, workflowRunApprovalExpiryIndexName).Scan(ctx, &indexes); err != nil || indexes != 3 {
		t.Fatalf("indexes=%d err=%v", indexes, err)
	}
	// The approval expiry index is partial on the waits expire_approvals can close (plan T103 review).
	var def string
	if err := conn.NewRaw("SELECT indexdef FROM pg_indexes WHERE schemaname = ? AND indexname = ?", schema, workflowRunApprovalExpiryIndexName).Scan(ctx, &def); err != nil ||
		!strings.Contains(def, "(approval_expires_at)") || !strings.Contains(def, "waiting_approval") || !strings.Contains(def, "approval_expires_at > 0") {
		t.Fatalf("approval expiry index = %q err=%v", def, err)
	}
}
