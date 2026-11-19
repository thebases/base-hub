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

// runControlTestDB opens one connection on a throwaway schema holding a minimal run table, and a
// context whose env runs the run-control SQL on that connection. Skips without Postgres.
func runControlTestDB(t *testing.T) (context.Context, bun.Conn, *orm.ModelMetadata) {
	t.Helper()
	bunDB, err := orm.NewBunDB(testutil.EnvPostgresConfig())
	if err != nil {
		t.Skipf("open postgres fixture: %v", err)
	}
	t.Cleanup(func() { _ = bunDB.Close() })
	ctx := context.Background()
	schema := fmt.Sprintf("wf_run_control_test_%d", time.Now().UnixNano())
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
			trigger_type TEXT, next_retry_at BIGINT NOT NULL DEFAULT 0, last_error TEXT,
			input_payload JSONB, output_payload JSONB, lease_owner TEXT, lease_until BIGINT,
			cancel_requested BOOLEAN NOT NULL DEFAULT false, updated_at TIMESTAMPTZ,
			resume_token_hash TEXT, approval_expires_at BIGINT, admission_pending BOOLEAN,
			current_step TEXT, step_runs JSONB, attempt_count INT NOT NULL DEFAULT 0)`,
	} {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	env := orm.NewEnvironmentWithDBName(conn, "run_control_test", 0, nil, true)
	return orm.ContextWithEnvironment(ctx, env), conn, &orm.ModelMetadata{Name: WorkflowRunModelName, StorageName: "wf_run"}
}

func insertControlRun(t *testing.T, ctx context.Context, conn bun.Conn, status, owner string, leaseUntil int64) int64 {
	t.Helper()
	var id int64
	err := conn.NewRaw(`INSERT INTO wf_run (workflow_id, status, trigger_type, next_retry_at, input_payload, output_payload, lease_owner, lease_until)
		VALUES (7, ?, 'kafka', ?, '{"in":1}', '{"out":2}', ?, ?) RETURNING id`,
		status, map[bool]int64{true: 99, false: 0}[status == WorkflowRunStatusRetryWaiting], owner, leaseUntil).Scan(ctx, &id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestCancelWorkflowRunTransitionsAgainstPostgres(t *testing.T) {
	ctx, conn, meta := runControlTestDB(t)
	live := time.Now().Unix() + 120
	waiting := insertControlRun(t, ctx, conn, WorkflowRunStatusRetryWaiting, "", 0)
	pending := insertControlRun(t, ctx, conn, WorkflowRunStatusPending, "creator", live)
	running := insertControlRun(t, ctx, conn, WorkflowRunStatusRunning, "worker", live)
	done := insertControlRun(t, ctx, conn, WorkflowRunStatusSucceeded, "", 0)

	for _, tc := range []struct {
		id     int64
		status string
	}{{waiting, WorkflowRunStatusCancelled}, {pending, WorkflowRunStatusCancelled}, {running, WorkflowRunStatusRunning}} {
		got, err := cancelWorkflowRunLocked(ctx, conn, meta.StorageName, tc.id)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Found || !got.Cancelled || got.Status != tc.status || got.TriggerType != "kafka" {
			t.Fatalf("cancel %d = %+v, want status %s", tc.id, got, tc.status)
		}
	}
	// waiting_retry -> cancelled: no retry left for the cron, lease cleared, output replaced by the
	// (policy-stored) input.
	var row struct {
		NextRetryAt int64          `bun:"next_retry_at"`
		Output      map[string]any `bun:"output_payload,type:jsonb"`
		LeaseOwner  string         `bun:"lease_owner"`
	}
	if err := conn.NewRaw(`SELECT next_retry_at, output_payload, COALESCE(lease_owner,'') AS lease_owner FROM wf_run WHERE id = ?`, waiting).Scan(ctx, &row); err != nil {
		t.Fatal(err)
	}
	if row.NextRetryAt != 0 || row.Output["in"] == nil || row.LeaseOwner != "" {
		t.Fatalf("cancelled waiting run row = %+v", row)
	}
	// The cancelled pending run's creator lost its lease; the running worker sees the flag on renewal.
	if held, err := renewWorkflowRunLease(ctx, meta, nil, nil, map[string]any{"id": pending, "lease_owner": "creator"}); err != nil || held != false {
		t.Fatalf("renew cancelled pending run = %v, %v; want false", held, err)
	}
	if got, err := renewWorkflowRunLease(ctx, meta, nil, nil, map[string]any{"id": running, "lease_owner": "worker"}); err != nil || got != WorkflowRunLeaseCancelRequested {
		t.Fatalf("renew flagged run = %v, %v; want %q", got, err, WorkflowRunLeaseCancelRequested)
	}
	// Final and missing runs are refused.
	if got, err := cancelWorkflowRunLocked(ctx, conn, meta.StorageName, done); err != nil || !got.Found || got.Cancelled || got.Status != WorkflowRunStatusSucceeded {
		t.Fatalf("cancel succeeded run = %+v, %v", got, err)
	}
	if got, err := cancelWorkflowRunLocked(ctx, conn, meta.StorageName, 999999); err != nil || got.Found {
		t.Fatalf("cancel missing run = %+v, %v", got, err)
	}
	// The resume claim never sees a cancelled run (it only takes waiting_retry rows).
	var claimable int
	if err := conn.NewRaw(`SELECT count(*) FROM wf_run WHERE status = ?`, WorkflowRunStatusRetryWaiting).Scan(ctx, &claimable); err != nil || claimable != 0 {
		t.Fatalf("waiting_retry rows after cancel = %d, %v", claimable, err)
	}
}

func TestAdmitWorkflowRunAgainstPostgres(t *testing.T) {
	ctx, conn, meta := runControlTestDB(t)
	live := time.Now().Unix() + 120
	admit := func(id int64, owner string) string {
		t.Helper()
		got, err := admitWorkflowRun(ctx, meta, nil, nil, map[string]any{
			"id": id, "workflow_id": int64(7), "limit": 1, "lease_owner": owner,
			"park_until": time.Now().Unix() + 5, "reason": "limit",
		})
		if err != nil {
			t.Fatal(err)
		}
		return got.(string)
	}
	// A running run whose lease expired (crashed worker) does not hold a slot.
	insertControlRun(t, ctx, conn, WorkflowRunStatusRunning, "dead", time.Now().Unix()-10)
	first := insertControlRun(t, ctx, conn, WorkflowRunStatusPending, "a", live)
	if got := admit(first, "a"); got != WorkflowRunAdmitted {
		t.Fatalf("first run = %s, want admitted", got)
	}
	second := insertControlRun(t, ctx, conn, WorkflowRunStatusPending, "b", live)
	if got := admit(second, "b"); got != WorkflowRunRejected {
		t.Fatalf("second fresh run = %s, want rejected", got)
	}
	// A resumed (claimed, running) run over the limit is parked back without an attempt.
	resumed := insertControlRun(t, ctx, conn, WorkflowRunStatusRunning, "batch", live)
	if got := admit(resumed, "batch"); got != WorkflowRunParked {
		t.Fatalf("resumed run = %s, want parked", got)
	}
	var row struct {
		Status      string `bun:"status"`
		NextRetryAt int64  `bun:"next_retry_at"`
		LeaseOwner  string `bun:"lease_owner"`
	}
	if err := conn.NewRaw(`SELECT status, next_retry_at, COALESCE(lease_owner,'') AS lease_owner FROM wf_run WHERE id = ?`, resumed).Scan(ctx, &row); err != nil {
		t.Fatal(err)
	}
	if row.Status != WorkflowRunStatusRetryWaiting || row.NextRetryAt <= 0 || row.LeaseOwner != "" {
		t.Fatalf("parked run = %+v", row)
	}
	// Someone else's lease: lost.
	if got := admit(second, "intruder"); got != WorkflowRunLost {
		t.Fatalf("foreign lease = %s, want lost", got)
	}
	// Once the first run leaves running, the slot is free again.
	if _, err := conn.ExecContext(ctx, `UPDATE wf_run SET status = ? WHERE id = ?`, WorkflowRunStatusSucceeded, first); err != nil {
		t.Fatal(err)
	}
	if got := admit(second, "b"); got != WorkflowRunAdmitted {
		t.Fatalf("second run after slot freed = %s, want admitted", got)
	}
}

// TestAdmitIgnoresClaimedRunsAwaitingAdmissionAgainstPostgres: claim_due_retries flips a batch of
// due runs to running with live leases before ResumeRun admits each one. Those runs must not hold
// slots, or with limit 1 every claimed run parks every tick and fresh callers get spurious 409s.
func TestAdmitIgnoresClaimedRunsAwaitingAdmissionAgainstPostgres(t *testing.T) {
	ctx, conn, meta := runControlTestDB(t)
	live := time.Now().Unix() + 120
	admit := func(id int64, owner string) string {
		t.Helper()
		got, err := admitWorkflowRun(ctx, meta, nil, nil, map[string]any{
			"id": id, "workflow_id": int64(7), "limit": 1, "lease_owner": owner,
			"park_until": time.Now().Unix() + 5, "reason": "limit",
		})
		if err != nil {
			t.Fatal(err)
		}
		return got.(string)
	}
	pendingFlag := func(id int64) bool {
		t.Helper()
		var flag bool
		if err := conn.NewRaw(`SELECT COALESCE(admission_pending, false) FROM wf_run WHERE id = ?`, id).Scan(ctx, &flag); err != nil {
			t.Fatal(err)
		}
		return flag
	}
	// Three due runs claimed in one batch, all running with the cron's lease.
	var claimed []int64
	for range 3 {
		claimed = append(claimed, insertControlRun(t, ctx, conn, WorkflowRunStatusRetryWaiting, "", 0))
	}
	if _, err := claimDueWorkflowRunRetries(ctx, meta, nil, nil, map[string]any{"limit": 10, "lease_owner": "cron", "lease_seconds": 120}); err != nil {
		t.Fatal(err)
	}
	for _, id := range claimed {
		if !pendingFlag(id) {
			t.Fatalf("claimed run %d must await admission", id)
		}
	}
	// The first claimed run is admitted although its siblings are running with live leases.
	if got := admit(claimed[0], "cron"); got != WorkflowRunAdmitted || pendingFlag(claimed[0]) {
		t.Fatalf("first claimed run = %s (pending %v), want admitted", got, pendingFlag(claimed[0]))
	}
	// The next one now sees one admitted run and parks, dropping the flag.
	if got := admit(claimed[1], "cron"); got != WorkflowRunParked || pendingFlag(claimed[1]) {
		t.Fatalf("second claimed run = %s (pending %v), want parked", got, pendingFlag(claimed[1]))
	}
	// A fresh run is refused only because of the admitted run, not the still-unadmitted third one.
	if _, err := conn.ExecContext(ctx, `UPDATE wf_run SET status = ? WHERE id = ?`, WorkflowRunStatusSucceeded, claimed[0]); err != nil {
		t.Fatal(err)
	}
	fresh := insertControlRun(t, ctx, conn, WorkflowRunStatusPending, "caller", live)
	if got := admit(fresh, "caller"); got != WorkflowRunAdmitted {
		t.Fatalf("fresh run beside an unadmitted claimed run = %s, want admitted", got)
	}
	// take_run_lease (direct ResumeRun) flags the run the same way.
	if _, err := takeWorkflowRunLease(ctx, meta, nil, nil, map[string]any{"id": claimed[1], "lease_owner": "direct", "lease_seconds": 120}); err != nil {
		t.Fatal(err)
	}
	if !pendingFlag(claimed[1]) {
		t.Fatal("take_run_lease must flag the run as awaiting admission")
	}
}
