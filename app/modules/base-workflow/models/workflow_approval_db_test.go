package models

import (
	"context"
	"testing"
	"time"

	"github.com/uptrace/bun"
)

// insertWaitingRun parks a run at waiting_approval with token "tok-<id>"-style hash and expiry.
func insertWaitingRun(t *testing.T, ctx context.Context, conn bun.Conn, token string, expiresAt int64) int64 {
	t.Helper()
	var id int64
	err := conn.NewRaw(`INSERT INTO wf_run (workflow_id, status, trigger_type, input_payload, output_payload, lease_owner, lease_until, resume_token_hash, approval_expires_at,
		current_step, step_runs, attempt_count)
		VALUES (7, ?, 'webhook', '{"in":1}', '{"checkpoint":2}', '', 0, ?, ?, 'wait', '[{"step_key":"wait","status":"waiting_approval"}]', 2) RETURNING id`,
		WorkflowRunStatusWaitingApproval, HashWorkflowResumeToken(token), expiresAt).Scan(ctx, &id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

type approvalRow struct {
	Status      string         `bun:"status"`
	CurrentStep string         `bun:"current_step"`
	Attempts    int            `bun:"attempt_count"`
	NextRetryAt int64          `bun:"next_retry_at"`
	StepRuns    []any          `bun:"step_runs,type:jsonb"`
	LeaseOwner  string         `bun:"lease_owner"`
	TokenHash   string         `bun:"resume_token_hash"`
	ExpiresAt   int64          `bun:"approval_expires_at"`
	LastError   string         `bun:"last_error"`
	Output      map[string]any `bun:"output_payload,type:jsonb"`
}

func readApprovalRow(t *testing.T, ctx context.Context, conn bun.Conn, id int64) approvalRow {
	t.Helper()
	var row approvalRow
	if err := conn.NewRaw(`SELECT status, COALESCE(current_step,'') AS current_step, attempt_count, next_retry_at, step_runs, COALESCE(lease_owner,'') AS lease_owner, COALESCE(resume_token_hash,'') AS resume_token_hash,
		COALESCE(approval_expires_at,0) AS approval_expires_at, COALESCE(last_error,'') AS last_error, output_payload FROM wf_run WHERE id = ?`, id).Scan(ctx, &row); err != nil {
		t.Fatal(err)
	}
	return row
}

// approvalDecisionKwargs is the decision the engine computes for a run parked at "wait" with 2 attempts.
func approvalDecisionKwargs(status string) map[string]any {
	return map[string]any{
		"status": status, "current_step": "after", "attempt_count": 3, "next_retry_at": int64(123),
		"output_payload":      map[string]any{"checkpoint": 2, "_approval": map[string]any{"approved": true}},
		"step_runs":           []map[string]any{{"step_key": "wait", "status": "waiting_approval"}, {"step_key": "wait", "status": "succeeded"}},
		"expect_current_step": "wait", "expect_attempt_count": 2,
	}
}

func TestClaimWorkflowApprovalAgainstPostgres(t *testing.T) {
	ctx, conn, meta := runControlTestDB(t)
	claim := func(id int64, kwargs map[string]any) bool {
		t.Helper()
		decision := approvalDecisionKwargs(WorkflowRunStatusRetryWaiting)
		for k, v := range kwargs {
			decision[k] = v
		}
		decision["id"], decision["lease_owner"] = id, "decider"
		got, err := claimWorkflowApproval(ctx, meta, nil, nil, decision)
		if err != nil {
			t.Fatal(err)
		}
		return got.(bool)
	}
	future := time.Now().Unix() + 600
	run := insertWaitingRun(t, ctx, conn, "good", future)
	if claim(run, map[string]any{"token_hash": HashWorkflowResumeToken("bad")}) {
		t.Fatal("a wrong token claimed the run")
	}
	// A decision computed for another wait (the run moved on and parked again) never matches.
	if claim(run, map[string]any{"token_hash": HashWorkflowResumeToken("good"), "expect_attempt_count": 1}) {
		t.Fatal("a stale decision claimed the run")
	}
	if !claim(run, map[string]any{"token_hash": HashWorkflowResumeToken("good")}) {
		t.Fatal("the right token did not claim the run")
	}
	// The decision is durable in the claim itself (plan T103 review): a sudo run is due for the cron.
	row := readApprovalRow(t, ctx, conn, run)
	approval, _ := row.Output["_approval"].(map[string]any)
	if row.Status != WorkflowRunStatusRetryWaiting || row.LeaseOwner != "" || row.TokenHash != "" || row.ExpiresAt != 0 ||
		row.CurrentStep != "after" || row.Attempts != 3 || row.NextRetryAt != 123 || len(row.StepRuns) != 2 || approval["approved"] != true {
		t.Fatalf("claimed row = %+v", row)
	}
	// A second decision (same token) loses: the run is no longer waiting.
	if claim(run, map[string]any{"token_hash": HashWorkflowResumeToken("good")}) {
		t.Fatal("the run was decided twice")
	}
	// An expired wait cannot be decided, even by an author without token.
	expired := insertWaitingRun(t, ctx, conn, "x", time.Now().Unix()-1)
	if claim(expired, map[string]any{"without_token": true}) {
		t.Fatal("an expired wait was claimed")
	}
	noExpiry := insertWaitingRun(t, ctx, conn, "y", 0)
	if !claim(noExpiry, map[string]any{"without_token": true, "status": WorkflowRunStatusPending}) {
		t.Fatal("an author could not decide a wait without expiry")
	}
	if row := readApprovalRow(t, ctx, conn, noExpiry); row.Status != WorkflowRunStatusPending || row.LeaseOwner != "decider" {
		t.Fatalf("in-request claim = %+v", row)
	}
	if _, err := claimWorkflowApproval(ctx, meta, nil, nil, map[string]any{"id": noExpiry, "lease_owner": "d", "status": WorkflowRunStatusPending}); err == nil {
		t.Fatal("a claim without token_hash or without_token must be refused")
	}
	if _, err := claimWorkflowApproval(ctx, meta, nil, nil, map[string]any{"id": noExpiry, "lease_owner": "d", "without_token": true, "status": WorkflowRunStatusRunning}); err == nil {
		t.Fatal("a claim writing an unknown decision status must be refused")
	}
}

// A caller-bound decision refused by max_concurrent_runs is put back to its wait (plan T103 review).
func TestReleaseWorkflowApprovalClaimAgainstPostgres(t *testing.T) {
	ctx, conn, meta := runControlTestDB(t)
	expiry := time.Now().Unix() + 600
	run := insertWaitingRun(t, ctx, conn, "tok", expiry)
	decision := approvalDecisionKwargs(WorkflowRunStatusPending)
	decision["id"], decision["lease_owner"], decision["token_hash"] = run, "decider", HashWorkflowResumeToken("tok")
	if won, err := claimWorkflowApproval(ctx, meta, nil, nil, decision); err != nil || won != true {
		t.Fatalf("claim = %v, %v", won, err)
	}
	restore := map[string]any{
		"id": run, "token_hash": HashWorkflowResumeToken("tok"), "approval_expires_at": expiry,
		"current_step": "wait", "output_payload": map[string]any{"checkpoint": 2},
		"step_runs": []map[string]any{{"step_key": "wait", "status": "waiting_approval"}}, "attempt_count": 2,
	}
	restore["lease_owner"] = "intruder"
	if got, err := releaseWorkflowApprovalClaim(ctx, meta, nil, nil, restore); err != nil || got != false {
		t.Fatalf("release by another owner = %v, %v", got, err)
	}
	restore["lease_owner"] = "decider"
	if got, err := releaseWorkflowApprovalClaim(ctx, meta, nil, nil, restore); err != nil || got != true {
		t.Fatalf("release = %v, %v", got, err)
	}
	row := readApprovalRow(t, ctx, conn, run)
	if row.Status != WorkflowRunStatusWaitingApproval || row.TokenHash != HashWorkflowResumeToken("tok") || row.ExpiresAt != expiry ||
		row.CurrentStep != "wait" || row.Attempts != 2 || len(row.StepRuns) != 1 || row.LeaseOwner != "" || row.Output["_approval"] != nil {
		t.Fatalf("restored row = %+v", row)
	}
	// The same token decides it again.
	decision["lease_owner"] = "decider2"
	if won, _ := claimWorkflowApproval(ctx, meta, nil, nil, decision); won != true {
		t.Fatal("the restored wait could not be decided with its token")
	}
}

func TestExpireWorkflowApprovalsAndCancelAgainstPostgres(t *testing.T) {
	ctx, conn, meta := runControlTestDB(t)
	expired := insertWaitingRun(t, ctx, conn, "a", time.Now().Unix()-5)
	live := insertWaitingRun(t, ctx, conn, "b", time.Now().Unix()+600)
	forever := insertWaitingRun(t, ctx, conn, "c", 0)
	got, err := expireWorkflowApprovals(ctx, meta, nil, nil, map[string]any{"limit": 10})
	if err != nil {
		t.Fatal(err)
	}
	rows := got.([]WorkflowExpiredApproval)
	if len(rows) != 1 || rows[0].ID != expired || rows[0].TriggerType != "webhook" {
		t.Fatalf("expired = %+v", rows)
	}
	row := readApprovalRow(t, ctx, conn, expired)
	if row.Status != WorkflowRunStatusFailed || row.LastError != WorkflowApprovalExpiredMessage || row.TokenHash != "" || row.Output["in"] == nil {
		t.Fatalf("expired row = %+v", row)
	}
	for _, id := range []int64{live, forever} {
		if readApprovalRow(t, ctx, conn, id).Status != WorkflowRunStatusWaitingApproval {
			t.Fatalf("run %d left waiting_approval", id)
		}
	}
	// cancel_run ends a waiting run at once and clears its token, so a late approval finds nothing.
	result, err := cancelWorkflowRunLocked(ctx, conn, meta.StorageName, live)
	if err != nil || !result.Cancelled || result.Status != WorkflowRunStatusCancelled {
		t.Fatalf("cancel waiting run = %+v, %v", result, err)
	}
	if row := readApprovalRow(t, ctx, conn, live); row.TokenHash != "" || row.Output["in"] == nil {
		t.Fatalf("cancelled waiting row = %+v", row)
	}
	if ok, _ := claimWorkflowApproval(ctx, meta, nil, nil, map[string]any{"id": live, "lease_owner": "d", "token_hash": HashWorkflowResumeToken("b"), "status": WorkflowRunStatusRetryWaiting, "expect_current_step": "wait", "expect_attempt_count": 2}); ok == true {
		t.Fatal("a cancelled run was approved")
	}
}
