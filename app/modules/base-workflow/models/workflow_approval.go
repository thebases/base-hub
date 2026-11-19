package models

// File overview:
// - Responsibility: the run side of wait_approval@v1 (plan T103): the `waiting_approval` status,
//   the resume token (stored only as its sha256), approve/reject (operator RPC) and the engine-only
//   claim_approval / expire_approvals transitions.
// - Invariants:
//   - A waiting run holds no lease and is never claimed by resume_due_runs (it only claims
//     waiting_retry) nor reaped (only running): it moves on only through approve/reject, cancel_run
//     or expiry.
//   - Every transition out of waiting_approval is ONE conditional UPDATE guarded by the status (and
//     the token hash and expiry for a decision), so two deciders, a decider and the expiry sweep, or
//     a decider and cancel_run can never both win. The decision's winner owns the run with a fresh
//     lease and continues it through the engine's markRun.
//   - The plaintext token is handed out once, to whoever ran the execution that parked the run
//     (the /wh caller in its 202 answer, test-run, replay_run); the database only holds its hash.
// - Key symbols: `WorkflowRunStatusWaitingApproval`, `WorkflowApprovalPending`, `HashWorkflowResumeToken`.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

// WorkflowRunStatusWaitingApproval: the run is parked at a wait_approval@v1 step (non-terminal).
const WorkflowRunStatusWaitingApproval = "waiting_approval"

// WaitApprovalActionCode is the action code the linear engine parks on instead of calling a handler.
const WaitApprovalActionCode = "wait_approval"

// WaitApprovalGroupParam is the wait_approval@v1 param (extra_params key) naming the tbl.groups
// row whose operators may decide the wait without its resume token.
const WaitApprovalGroupParam = "approver_group_id"

// WorkflowApprovalExpiredMessage is the last_error of a run whose approval wait expired.
const WorkflowApprovalExpiredMessage = "workflow: approval wait expired"

// ErrWorkflowRunAwaitingApproval: the execution parked the run at a wait_approval step. The run
// was accepted and continues after approve/reject; callers must not treat it as a failure.
var ErrWorkflowRunAwaitingApproval = errors.New("workflow: run is waiting for approval")

// WorkflowApprovalPending is the error the engine returns when it parks a run. Token is the
// plaintext resume token, available only here.
type WorkflowApprovalPending struct {
	RunID     int64
	StepKey   string
	Token     string
	ExpiresAt int64
}

func (e *WorkflowApprovalPending) Error() string {
	return fmt.Sprintf("workflow: run %d is waiting for approval at step %s", e.RunID, e.StepKey)
}

func (e *WorkflowApprovalPending) Unwrap() error { return ErrWorkflowRunAwaitingApproval }

// HashWorkflowResumeToken is the stored form of a resume token (hex sha256 of the trimmed token).
func HashWorkflowResumeToken(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
}

// WorkflowApprovalDecision is one approve/reject call handed to the engine.
type WorkflowApprovalDecision struct {
	Approved bool
	// TokenHash is the hash of the token the caller presented. Empty is allowed only for an author
	// (base.group_system) or a member of the run's approval_group_id; the engine checks that
	// against the run it claims (approvalTokenWaived).
	TokenHash string
	// Author: the decider is base.group_system (may decide without token, approve their own run
	// and approve a test run).
	Author  bool
	By      int64
	Comment string
}

// approveWorkflowRun / rejectWorkflowRun are base.workflow.run.approve/<id> and reject/<id>.
func approveWorkflowRun(ctx context.Context, _ *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	return decideWorkflowApproval(ctx, "approve", ids, args, kwargs, true)
}

func rejectWorkflowRun(ctx context.Context, _ *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	return decideWorkflowApproval(ctx, "reject", ids, args, kwargs, false)
}

// decideWorkflowApproval checks the caller (operator role, read access to the run) and hands the
// decision to the engine, which checks the token (or the author / approver-group waiver) against
// the run it reads and claims, then continues the run.
func decideWorkflowApproval(ctx context.Context, method string, ids []string, args []any, kwargs map[string]any, approved bool) (any, error) {
	id, err := workflowRunPathID(method, ids)
	if err != nil {
		return nil, err
	}
	if err := EnsureWorkflowRole(ctx, WorkflowRoleOperator); err != nil {
		return nil, err
	}
	if err := workflowApprovalReadAccess(ctx, WorkflowRunModelName, ids); err != nil {
		return nil, err
	}
	decision := WorkflowApprovalDecision{
		Approved: approved,
		Comment:  strings.TrimSpace(stringValue(firstNonNil(mapValue(kwargs, "comment"), argValue(args, 1)))),
	}
	if len(decision.Comment) > 2000 {
		return nil, orm.InvalidParamsf("base.workflow.run %s comment is longer than 2000 characters", method)
	}
	// Any failure of the role lookup counts as "not an author" (the restrictive answer).
	decision.Author = EnsureWorkflowRole(ctx, WorkflowRoleAuthor) == nil
	// A missing token is not refused here: whether the caller may go without it depends on the
	// approval_group_id of the parked run, which the engine reads in the same record it fences
	// the claim on (current_step + attempt_count), so a re-parked run is never decided under the
	// group of an earlier wait.
	if token := strings.TrimSpace(stringValue(firstNonNil(mapValue(kwargs, "token"), argValue(args, 0)))); token != "" {
		decision.TokenHash = HashWorkflowResumeToken(token)
	}
	if env := orm.EnvironmentFromContext(ctx); env != nil {
		decision.By = env.UserID()
	}
	if workflowRunControlHooks.DecideApproval == nil {
		return nil, orm.NewError(orm.ErrUnavailable, 0, "workflow engine is not running")
	}
	return workflowRunControlHooks.DecideApproval(ctx, id, decision)
}

// WorkflowCallerInGroup reports whether the caller in ctx belongs to tbl.groups row groupID:
// directly (the session's group ids, primary group included) or through implied groups. Implied
// membership is matched by the group's BID against orm.CallerGroupBIDs (the kernel's expansion), so
// a group without a BID only matches its direct members. Sudo or a missing environment is not a
// member: base.group_system decides through the author path instead.
func WorkflowCallerInGroup(ctx context.Context, groupID int64) (bool, error) {
	env := orm.EnvironmentFromContext(ctx)
	if env == nil || env.IsSudo() || groupID <= 0 {
		return false, nil
	}
	role, _ := env.ContextValue(orm.RBACContextRoleIDKey)
	groups, _ := env.ContextValue(orm.RBACContextGroupIDsKey)
	if int64Value(role) == groupID || slices.Contains(int64SliceValue(groups), groupID) {
		return true, nil
	}
	bids, err := workflowCallerGroupBIDs(ctx)
	if err != nil || len(bids) == 0 {
		return false, err
	}
	bid, err := workflowGroupBID(ctx, groupID)
	if err != nil || bid == "" {
		return false, err
	}
	return slices.Contains(bids, bid), nil
}

// workflowGroupBID is a seam over the sudo read of one tbl.groups BID ("" when the row is gone).
var workflowGroupBID = func(ctx context.Context, groupID int64) (string, error) {
	rows, err := orm.RuntimeRepositoryRead(WorkflowEngineContext(ctx), orm.RuntimeModelRef(workflowGroupModelName, nil), []string{strconv.FormatInt(groupID, 10)}, []string{"bid"})
	if err != nil || len(rows) == 0 {
		return "", err
	}
	return strings.TrimSpace(stringValue(rows[0]["bid"])), nil
}

// int64SliceValue reads the session group ids ([]int64 in process, []any after a JSON round trip).
func int64SliceValue(raw any) []int64 {
	switch typed := raw.(type) {
	case []int64:
		return typed
	case []any:
		out := make([]int64, 0, len(typed))
		for _, v := range typed {
			if id, ok := int64FromAny(v); ok {
				out = append(out, id)
			}
		}
		return out
	}
	return nil
}

// workflowApprovalReadAccess is a test seam over the record read check of approve/reject.
var workflowApprovalReadAccess = orm.EnsureRecordReadAccess

// approvalClaimStatuses are the statuses a decision may write: pending (the decider's request
// continues the run, leased to it, after admission), waiting_retry (resume_due_runs continues it),
// or a final status (the decision ended the run).
var approvalClaimStatuses = []string{
	WorkflowRunStatusPending, WorkflowRunStatusRetryWaiting, WorkflowRunStatusSucceeded, WorkflowRunStatusFailed,
}

// claimWorkflowApproval (engine-only claim_approval) decides run `id` in one conditional UPDATE:
// it matches only while the run still waits at `expect_current_step` with `expect_attempt_count`
// (a run decided and parked again at a later wait has moved on), the token hash matches (or
// `without_token`) and the wait has not expired. It writes the decision the engine computed
// (status, current_step, output_payload, step_runs, attempt_count, last_error, next_retry_at), so
// the decision is durable the moment the claim is won. A pending status is leased to
// `lease_owner`; any other status holds no lease. It answers whether this caller won.
func claimWorkflowApproval(ctx context.Context, meta *orm.ModelMetadata, _ []string, _ []any, kwargs map[string]any) (any, error) {
	id := int64Value(mapValue(kwargs, "id"))
	owner, seconds := runLeaseArgs(kwargs)
	tokenHash := strings.TrimSpace(stringValue(mapValue(kwargs, "token_hash")))
	withoutToken := boolValue(mapValue(kwargs, "without_token"))
	status := strings.TrimSpace(stringValue(mapValue(kwargs, "status")))
	if id <= 0 || owner == "" || (tokenHash == "" && !withoutToken) || !slices.Contains(approvalClaimStatuses, status) {
		return nil, orm.InvalidParamsf("base.workflow.run claim_approval requires id, lease_owner, token_hash and a decision status")
	}
	output, steps, err := approvalJSONArgs(kwargs)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	leaseOwner, leaseUntil := "", int64(0)
	if status == WorkflowRunStatusPending {
		leaseOwner, leaseUntil = owner, now.Unix()+seconds
	}
	return updateWorkflowRunLease(ctx, meta, fmt.Sprintf(
		`UPDATE %s SET status = ?, lease_owner = ?, lease_until = ?, resume_token_hash = '',
		   approval_expires_at = 0, current_step = ?, output_payload = ?::jsonb, step_runs = ?::jsonb,
		   attempt_count = ?, last_error = ?, next_retry_at = ?, admission_pending = false, updated_at = ?
		 WHERE id = ? AND status = ?
		   AND COALESCE(current_step, '') = ? AND COALESCE(attempt_count, 0) = ?
		   AND (? OR resume_token_hash = ?)
		   AND (COALESCE(approval_expires_at, 0) = 0 OR approval_expires_at > ?)
		 RETURNING id`, meta.StorageName),
		status, leaseOwner, leaseUntil,
		strings.TrimSpace(stringValue(mapValue(kwargs, "current_step"))), output, steps,
		int64Value(mapValue(kwargs, "attempt_count")), stringValue(mapValue(kwargs, "last_error")),
		int64Value(mapValue(kwargs, "next_retry_at")), now,
		id, WorkflowRunStatusWaitingApproval,
		strings.TrimSpace(stringValue(mapValue(kwargs, "expect_current_step"))), int64Value(mapValue(kwargs, "expect_attempt_count")),
		withoutToken, tokenHash, now.Unix())
}

// releaseWorkflowApprovalClaim (engine-only release_approval_claim) puts a claimed decision back:
// run `id`, still pending under `lease_owner` (claimed, never admitted), waits again at
// waiting_approval with its former token hash, expiry and checkpoint (current_step,
// output_payload, step_runs, attempt_count, last_error). Used when max_concurrent_runs refuses a
// caller-bound decision: the decider retries later with the same token.
func releaseWorkflowApprovalClaim(ctx context.Context, meta *orm.ModelMetadata, _ []string, _ []any, kwargs map[string]any) (any, error) {
	id := int64Value(mapValue(kwargs, "id"))
	owner := strings.TrimSpace(stringValue(mapValue(kwargs, "lease_owner")))
	if id <= 0 || owner == "" {
		return nil, orm.InvalidParamsf("base.workflow.run release_approval_claim requires id and lease_owner")
	}
	output, steps, err := approvalJSONArgs(kwargs)
	if err != nil {
		return nil, err
	}
	return updateWorkflowRunLease(ctx, meta, fmt.Sprintf(
		`UPDATE %s SET status = ?, lease_owner = '', lease_until = 0, resume_token_hash = ?,
		   approval_expires_at = ?, current_step = ?, output_payload = ?::jsonb, step_runs = ?::jsonb,
		   attempt_count = ?, last_error = ?, next_retry_at = 0, updated_at = ?
		 WHERE id = ? AND lease_owner = ? AND status = ?
		 RETURNING id`, meta.StorageName),
		WorkflowRunStatusWaitingApproval, strings.TrimSpace(stringValue(mapValue(kwargs, "token_hash"))),
		int64Value(mapValue(kwargs, "approval_expires_at")), strings.TrimSpace(stringValue(mapValue(kwargs, "current_step"))),
		output, steps, int64Value(mapValue(kwargs, "attempt_count")), stringValue(mapValue(kwargs, "last_error")), time.Now().UTC(),
		id, owner, WorkflowRunStatusPending)
}

// approvalJSONArgs encodes the output_payload and step_runs kwargs for a jsonb column.
func approvalJSONArgs(kwargs map[string]any) (string, string, error) {
	output, err := json.Marshal(mapValue(kwargs, "output_payload"))
	if err != nil {
		return "", "", orm.InvalidParamsf("base.workflow.run output_payload: %v", err)
	}
	steps, err := json.Marshal(mapValue(kwargs, "step_runs"))
	if err != nil {
		return "", "", orm.InvalidParamsf("base.workflow.run step_runs: %v", err)
	}
	return string(output), string(steps), nil
}

// WorkflowExpiredApproval is one run closed by expire_approvals.
type WorkflowExpiredApproval struct {
	ID          int64  `bun:"id" json:"id"`
	TriggerType string `bun:"trigger_type" json:"trigger_type"`
}

// expireWorkflowApprovals (engine-only expire_approvals, run by the resume cron) closes up to
// `limit` waits whose approval_expires_at has passed as failed. output_payload becomes input_payload
// like cancel_run: the parked checkpoint was never passed through the audit policy, the input was.
// SKIP LOCKED keeps it off rows a decider is updating right now; the status guard in both UPDATEs
// decides the race.
func expireWorkflowApprovals(ctx context.Context, meta *orm.ModelMetadata, _ []string, _ []any, kwargs map[string]any) (any, error) {
	limit := int64Value(mapValue(kwargs, "limit"))
	if limit <= 0 {
		limit = 100
	}
	now := time.Now().UTC()
	var rows []WorkflowExpiredApproval
	err := workflowRunWithRequestTx(ctx, func(ctx context.Context, db bun.IDB) error {
		return db.NewRaw(fmt.Sprintf(
			`UPDATE %[1]s SET status = ?, last_error = ?, resume_token_hash = '', approval_expires_at = 0,
			   next_retry_at = 0, output_payload = input_payload, updated_at = ?
			 WHERE id IN (
			   SELECT id FROM %[1]s
			   WHERE status = ? AND approval_expires_at > 0 AND approval_expires_at <= ?
			   ORDER BY approval_expires_at
			   LIMIT ?
			   FOR UPDATE SKIP LOCKED)
			   AND status = ?
			 RETURNING id, COALESCE(trigger_type, '') AS trigger_type`, meta.StorageName),
			WorkflowRunStatusFailed, WorkflowApprovalExpiredMessage, now,
			WorkflowRunStatusWaitingApproval, now.Unix(), limit, WorkflowRunStatusWaitingApproval).Scan(ctx, &rows)
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		log.Errorf("base.workflow.run expire_approvals: %v", err)
		return nil, fmt.Errorf("base.workflow.run expire_approvals: %w", err)
	}
	return rows, nil
}
