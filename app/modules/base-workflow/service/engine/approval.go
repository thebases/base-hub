package engine

// File overview:
// - Responsibility: wait_approval@v1 in the linear engine (plan T103): park a run at the step,
//   continue it on approve/reject, and expire waits from the resume cron.
// - Invariants:
//   - Parking is one markRun to waiting_approval (lease released, token stored as its sha256 only).
//     The parked execution returns approvalParked, which also matches ErrRunRetryScheduled: every
//     trigger treats the run as accepted (kafka commits, model triggers and trigger health count
//     no failure) and /wh answers 202.
//   - A decision first wins the conditional claim_approval UPDATE (status, token hash, expiry), which
//     leases the run to this execution; everything after goes through markRun. A run started by a
//     system trigger continues as sudo (as before it parked); a caller-bound run (/wh, test-run)
//     continues under the identity of whoever started it and retries in place, never on the cron.
//   - The token may be left out only by base.group_system or by a member of the run's
//     approval_group_id (copied from the step's approver_group_id at park time).
//   - Expiry is one SKIP LOCKED UPDATE per resume_due_runs tick, before the retry claim.
//   - A tcp (synchronous) run and a sub-workflow child cannot wait: their caller is blocked on
//     them, so the step fails without retries instead.
// - Key symbols: `Service.parkForApproval`, `Service.DecideApproval`, `Service.expireApprovalWaits`.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/modelruntime"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
	"git.thebase.vn/base_hub/modules/base-workflow/service/nodes"
)

// approvalParked is what an execution that parked its run returns.
type approvalParked struct {
	pending *workflowmodels.WorkflowApprovalPending
}

func (e approvalParked) Error() string { return e.pending.Error() }

func (e approvalParked) Unwrap() []error { return []error{e.pending, ErrRunRetryScheduled} }

// isApprovalStep reports whether the gated step is wait_approval@v1 and must park instead of
// running a handler. A dry run never parks: the node's handler passes it through.
func isApprovalStep(ctx context.Context, gate stepGate) bool {
	return gate.handlerKey == workflowmodels.WaitApprovalActionCode && !workflowmodels.WorkflowDryRunFromContext(ctx)
}

// approvalUnsupported is why this run cannot wait, or nil.
func approvalUnsupported(ctx context.Context) error {
	switch {
	case synchronousRun(ctx):
		return fmt.Errorf("wait_approval: a synchronous (tcp) run cannot wait for approval: %w", workflowmodels.ErrWorkflowStepNonRetryable)
	case childRunContext(ctx):
		return fmt.Errorf("wait_approval: a sub-workflow run cannot wait for approval (its caller is blocked): %w", workflowmodels.ErrWorkflowStepNonRetryable)
	}
	return nil
}

// newResumeToken returns 32 random bytes, base64url encoded.
func newResumeToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// parkForApproval writes the run as waiting_approval at stepKey and returns approvalParked.
func (s *Service) parkForApproval(ctx context.Context, runtime flowRuntime, a stepAttempt, stepRuns []workflowmodels.WorkflowStepRun, totalAttempts int) error {
	token, err := newResumeToken()
	if err != nil {
		return fmt.Errorf("workflow: resume token: %w", err)
	}
	now := s.now()
	expiresAt := int64(0)
	if seconds := nodes.ApprovalWaitSeconds(a.step.Params); seconds > 0 {
		expiresAt = now.Unix() + seconds
	}
	row := newStepRun(stepRunSpec{
		name: a.name, key: a.key, step: a.step,
		implementationKey: a.gate.handlerKey, implementationVersion: a.gate.handlerVersion,
		attempt: 1, status: workflowmodels.WorkflowRunStatusWaitingApproval,
		startedAt: now, finishedAt: now, input: a.payload, output: a.payload,
	})
	// The park counts as the step's attempt, like any recorded row (resume derives interrupted
	// attempts from attempt_count minus recorded rows).
	update := s.runUpdate(workflowmodels.WorkflowRunStatusWaitingApproval, a.key, a.payload, joinStepRuns(stepRuns, []workflowmodels.WorkflowStepRun{row}), totalAttempts+1, nil, 0)
	update["resume_token_hash"] = workflowmodels.HashWorkflowResumeToken(token)
	update["approval_expires_at"] = expiresAt
	// Always written (NULL when unset) so a later wait never inherits an earlier wait's group.
	update["approval_group_id"] = nil
	if group := nodes.ApprovalGroupID(a.step.Params); group > 0 {
		update["approval_group_id"] = group
	}
	if err := s.markRun(ctx, runtime.flow.Topic, runtime.runID, update); err != nil {
		return err
	}
	return approvalParked{pending: &workflowmodels.WorkflowApprovalPending{RunID: runtime.runID, StepKey: a.key, Token: token, ExpiresAt: expiresAt}}
}

// DecideApproval is base.workflow.run approve/reject behind the models hook. The decision is
// computed first (definition loaded, nothing written), then written by the one conditional
// claim_approval UPDATE, so it is durable the moment the claim is won:
//   - a final decision (rejected without on_error, approved at the last step, or a definition
//     that can no longer run) ends the run in the claim;
//   - a sudo-started (system trigger) run is handed to resume_due_runs (waiting_retry, due now)
//     and continues through its normal admission; the answer is at once {status: waiting_retry};
//   - a caller-bound or test run (it cannot be resumed by the system) continues in this request
//     under the identity that started it, after max_concurrent_runs admits it; a refused
//     admission puts the wait back (same token and expiry) and answers 409.
//
// It answers {id, status, last_error} (+ resume_token/expires_at when the run parked again at a
// later wait step).
func (s *Service) DecideApproval(ctx context.Context, runID int64, decision workflowmodels.WorkflowApprovalDecision) (orm.RecordMap, error) {
	s = s.withDefaults()
	run, err := s.readRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, orm.NotFoundf("workflow: run %d not found", runID)
	}
	if run.Status != workflowmodels.WorkflowRunStatusWaitingApproval {
		return nil, orm.Conflictf("workflow: run %d is %s, not waiting for approval", runID, run.Status)
	}
	if err := s.approvalTokenWaived(ctx, run, decision); err != nil {
		return nil, err
	}
	if err := approvalDeciderAllowed(run, decision); err != nil {
		return nil, err
	}
	execCtx, err := s.approvalExecutionContext(ctx, run)
	if err != nil {
		return nil, err
	}
	execCtx, plan := s.planApproval(execCtx, run, decision)
	inRequest := plan.continues && (run.CallerBound || run.FlowVersion <= 0)
	switch {
	case inRequest:
		plan.values["status"] = workflowmodels.WorkflowRunStatusPending
	case plan.continues:
		plan.values["status"] = workflowmodels.WorkflowRunStatusRetryWaiting
		plan.values["next_retry_at"] = s.now().Unix()
	}
	values := auditRunValues(execCtx, plan.values)
	values["expect_current_step"] = run.CurrentStep
	values["expect_attempt_count"] = run.AttemptCount

	owner := newRunLeaseOwner()
	won, err := s.claimApproval(ctx, runID, owner, decision.TokenHash, values)
	if err != nil {
		return nil, err
	}
	if !won {
		return nil, s.lostApprovalClaim(ctx, run)
	}
	// The claim bypassed markRun, so the transition is counted and published here (plan T102).
	status, _ := values["status"].(string)
	stepKey, _ := values["current_step"].(string)
	countRunTransition(execCtx, status)
	publishWorkflowRunStepEvent(execCtx, run.Topic, runID, stepKey, status)
	if plan.cause != nil {
		log.Warnf("%v", plan.cause)
	}
	lastError, _ := values["last_error"].(string)
	if !inRequest {
		return orm.RecordMap{"id": runID, "status": status, "last_error": lastError}, nil
	}

	execCtx = contextWithRunLease(execCtx, runID, owner)
	plan.runtime.runID = runID
	admission, err := s.admitRun(execCtx, plan.runtime)
	if err != nil {
		return nil, err
	}
	if admission != workflowmodels.WorkflowRunAdmitted {
		return nil, s.restoreApprovalWait(ctx, run, owner)
	}
	execErr := s.executeRun(execCtx, plan.runtime, plan.state)
	return s.approvalAnswer(execCtx, runID, execErr)
}

// approvalTokenWaived refuses a decision without the resume token unless the decider is an author
// (base.group_system) or belongs to the run's approval_group_id. The group comes from the parked
// run (written at park time), and the claim is fenced on that same run's current_step and
// attempt_count, so a run that moved on and parked again elsewhere is never claimed under this
// check. The operator role itself was checked by the RPC before the engine was called.
func (s *Service) approvalTokenWaived(ctx context.Context, run *workflowmodels.WorkflowRunRecord, decision workflowmodels.WorkflowApprovalDecision) error {
	if decision.TokenHash != "" || decision.Author {
		return nil
	}
	if run.ApprovalGroupID == nil || *run.ApprovalGroupID <= 0 {
		return orm.Forbiddenf("workflow: deciding run %d requires its resume token", run.ID)
	}
	member, err := s.inApprovalGroup(ctx, *run.ApprovalGroupID)
	if err != nil {
		return fmt.Errorf("workflow: check the approver group of run %d: %w", run.ID, err)
	}
	if !member {
		return orm.Forbiddenf("workflow: deciding run %d requires its resume token or membership of its approver group", run.ID)
	}
	return nil
}

// approvalDeciderAllowed applies the segregation-of-duties defaults (plan T103 review): the user
// who started a run cannot approve it, and only base.group_system approves a test run (it ran
// the unpublished draft). Rejecting stays open to both, and base.group_system may do anything.
func approvalDeciderAllowed(run *workflowmodels.WorkflowRunRecord, decision workflowmodels.WorkflowApprovalDecision) error {
	if !decision.Approved || decision.Author {
		return nil
	}
	if run.FlowVersion <= 0 {
		return orm.Forbiddenf("workflow: run %d is a test run; only base.group_system can approve it", run.ID)
	}
	if run.StartedByUID > 0 && run.StartedByUID == decision.By {
		return orm.Forbiddenf("workflow: run %d was started by you; another user must approve it", run.ID)
	}
	return nil
}

// restoreApprovalWait puts a claimed, never admitted decision back to waiting_approval with the
// run's former token hash, expiry and checkpoint, and answers the 409 of the refused admission.
func (s *Service) restoreApprovalWait(ctx context.Context, run *workflowmodels.WorkflowRunRecord, owner string) error {
	restored, err := s.releaseApproval(ctx, run.ID, owner, orm.RecordMap{
		"token_hash": run.ResumeTokenHash, "approval_expires_at": run.ApprovalExpiresAt,
		"current_step": run.CurrentStep, "output_payload": run.OutputPayload, "step_runs": run.StepRuns,
		"attempt_count": run.AttemptCount, "last_error": run.LastError,
	})
	if err != nil {
		return fmt.Errorf("workflow: restore the approval wait of run %d: %w", run.ID, err)
	}
	if !restored {
		return orm.Conflictf("workflow: run %d changed while its approval was refused admission", run.ID)
	}
	return orm.Conflictf("workflow concurrency limit reached")
}

// lostApprovalClaim explains a claim that matched nothing: the run moved on (decided, cancelled,
// expired), or the token is wrong.
func (s *Service) lostApprovalClaim(ctx context.Context, run *workflowmodels.WorkflowRunRecord) error {
	if run.ApprovalExpiresAt > 0 && run.ApprovalExpiresAt <= s.now().Unix() {
		return orm.Conflictf("workflow: the approval wait of run %d has expired", run.ID)
	}
	current, err := s.readRun(ctx, run.ID)
	if err == nil && current != nil && current.Status == workflowmodels.WorkflowRunStatusWaitingApproval &&
		current.CurrentStep == run.CurrentStep && current.AttemptCount == run.AttemptCount {
		return orm.Forbiddenf("workflow: invalid resume token for run %d", run.ID)
	}
	return orm.Conflictf("workflow: run %d is no longer waiting for approval", run.ID)
}

// approvalExecutionContext binds the run the way ResumeRun does, on the identity the run had: a
// run started by a system trigger continues as sudo; a caller-bound run continues under the user
// who started it (anonymous for an anonymous /wh run), never under the decider, and retries in
// place. The lease is bound after the claim.
func (s *Service) approvalExecutionContext(ctx context.Context, run *workflowmodels.WorkflowRunRecord) (context.Context, error) {
	if run.CallerBound {
		creatorCtx, err := s.creatorContext(ctx, run.StartedByUID)
		if err != nil {
			return nil, err
		}
		ctx = creatorCtx
	} else {
		ctx = workflowmodels.WorkflowEngineContext(ctx)
	}
	ctx = workflowmodels.ContextWithWorkflowRequestID(ctx, run.Name)
	ctx = workflowmodels.ContextWithWorkflowTriggerType(ctx, run.TriggerType)
	ctx = contextWithModelTriggerChain(ctx, run.TriggerChain)
	ctx = contextWithRunFlags(ctx, runFlags{retryInPlace: run.CallerBound || run.FlowVersion <= 0})
	ctx, _ = bindRunAuditPolicy(ctx, workflowmodels.FlowRecord{}, run.AuditMode, run.TriggerType)
	return ctx, nil
}

// defaultRunCreatorContext rebuilds the identity of user uid in the database of ctx, the way the
// /wh and test-run routes build it (live groups and companies); uid 0 is the anonymous caller of
// an anonymous /wh run. A user who is gone or archived cannot carry the run on.
func defaultRunCreatorContext(ctx context.Context, uid int64) (context.Context, error) {
	env := orm.EnvironmentFromContext(ctx)
	if env == nil {
		return nil, fmt.Errorf("workflow: no database to continue the run in")
	}
	dbName := strings.TrimSpace(env.DBName())
	if uid <= 0 {
		return orm.ContextWithEnvironment(ctx, orm.NewEnvironmentWithDBName(env.DB(), dbName, 0, map[string]any{
			orm.ContextKeyDatabaseName: dbName,
		}, false)), nil
	}
	identity, err := orm.LookupRBACIdentityByUserID(ctx, uid)
	if err != nil {
		return nil, fmt.Errorf("workflow: load the user who started the run: %w", err)
	}
	if identity == nil {
		return nil, orm.Conflictf("workflow: user %d who started the run is no longer active; the run can only be cancelled", uid)
	}
	identity.DBName = dbName
	return orm.ContextWithEnvironment(ctx, orm.EnvironmentForIdentity(identity).WithDB(env.DB())), nil
}

// approvalPlan is a decision computed before anything is written.
type approvalPlan struct {
	runtime flowRuntime
	// values is the run write of the decision (runUpdate shape); DecideApproval sets the status of
	// a continuing run.
	values orm.RecordMap
	// continues: the run goes on at state.startStepIndex.
	continues bool
	state     executionState
	// cause is why the run is closed without applying the decision (definition unusable).
	cause error
}

// planApproval applies the decision to the run as read: the wait step gets a decision row, the
// payload `_approval`, and the run goes on after the step (approved), to its on_error target
// (rejected), or ends. The returned ctx carries the workflow's audit policy.
func (s *Service) planApproval(ctx context.Context, run *workflowmodels.WorkflowRunRecord, decision workflowmodels.WorkflowApprovalDecision) (context.Context, approvalPlan) {
	flow, err := s.loadRunFlow(ctx, run, func() (*workflowmodels.FlowRecord, error) {
		return s.loadActiveFlow(ctx, run.Topic)
	})
	if err != nil {
		return ctx, s.closedApprovalPlan(run, fmt.Errorf("workflow: continue run %d: %w", run.ID, err))
	}
	// The run continues on the definition it was started with (plan T97); a test-run (version 0)
	// on the current draft.
	runtime, err := s.versionRuntime(ctx, flow, run.FlowVersion)
	if err != nil {
		return ctx, s.closedApprovalPlan(run, fmt.Errorf("workflow: continue run %d: %w", run.ID, err))
	}
	ctx, _ = bindRunAuditPolicy(ctx, runtime.flow, run.AuditMode, run.TriggerType)
	index := -1
	for i, step := range runtime.steps {
		if step.ResolvedKey(i) == strings.TrimSpace(run.CurrentStep) {
			index = i
			break
		}
	}
	if index < 0 {
		return ctx, s.closedApprovalPlan(run, fmt.Errorf("workflow: run %d waits at step %q, which is not in its definition", run.ID, run.CurrentStep))
	}
	step := runtime.steps[index]
	stepKey := step.ResolvedKey(index)

	parked, err := payloadFromAudit(run.OutputPayload)
	if err != nil {
		return ctx, s.closedApprovalPlan(run, fmt.Errorf("workflow: run %d output_payload: %w", run.ID, err))
	}
	object := payloadForAudit(parked)
	object["_approval"] = map[string]any{"approved": decision.Approved, "by": decision.By, "comment": decision.Comment, "decided_at": s.now().Unix()}
	payload, err := json.Marshal(object)
	if err != nil {
		return ctx, s.closedApprovalPlan(run, err)
	}

	var decisionErr error
	status := workflowmodels.WorkflowRunStatusSucceeded
	if !decision.Approved {
		decisionErr = fmt.Errorf("workflow: approval rejected by user %d: %s", decision.By, decision.Comment)
		status = workflowmodels.WorkflowRunStatusFailed
	}
	now := s.now()
	row := newStepRun(stepRunSpec{
		name: step.ResolvedName(index), key: stepKey, step: step,
		implementationKey: workflowmodels.WaitApprovalActionCode, implementationVersion: 1,
		attempt: 1, status: status, startedAt: now, finishedAt: now,
		input: parked, output: payload, err: decisionErr,
	})
	stepRuns := joinStepRuns(run.StepRuns, []workflowmodels.WorkflowStepRun{row})
	totalAttempts := run.AttemptCount + 1
	plan := approvalPlan{runtime: runtime}

	next := index + 1
	if decisionErr != nil {
		target, handlerPayload, ok := onErrorTarget(ctx, runtime.steps, index, step, stepKey, payload, decisionErr)
		if !ok {
			plan.values = s.runUpdate(workflowmodels.WorkflowRunStatusFailed, stepKey, payload, stepRuns, totalAttempts, decisionErr, 0)
			return ctx, plan
		}
		next, payload = target, handlerPayload
	}
	if next >= len(runtime.steps) {
		plan.values = s.runUpdate(workflowmodels.WorkflowRunStatusSucceeded, "", payload, stepRuns, totalAttempts, nil, 0)
		return ctx, plan
	}
	// current_step is the step the run goes on with, so a resume (or the cron) starts there and
	// never asks for the decision again.
	plan.values = s.runUpdate("", runtime.steps[next].ResolvedKey(next), payload, stepRuns, totalAttempts, decisionErr, 0)
	plan.continues = true
	plan.state = executionState{
		currentPayload: payload,
		stepRuns:       stepRuns,
		totalAttempts:  totalAttempts,
		startStepIndex: next,
		startAttempt:   1,
	}
	return ctx, plan
}

// closedApprovalPlan ends a run whose definition can no longer carry the decision: failed, its
// checkpoint kept, cause as last_error.
func (s *Service) closedApprovalPlan(run *workflowmodels.WorkflowRunRecord, cause error) approvalPlan {
	payload, _ := payloadFromAudit(run.OutputPayload)
	return approvalPlan{
		values: s.runUpdate(workflowmodels.WorkflowRunStatusFailed, run.CurrentStep, payload, run.StepRuns, run.AttemptCount, cause, 0),
		cause:  cause,
	}
}

// approvalAnswer is the RPC result of a decision continued in the request. A step failure after
// the decision is an audited outcome of the run (like replay_run), not an RPC error.
func (s *Service) approvalAnswer(ctx context.Context, runID int64, execErr error) (orm.RecordMap, error) {
	result := orm.RecordMap{"id": runID, "status": "", "last_error": ""}
	var parked approvalParked
	if errors.As(execErr, &parked) {
		result["resume_token"] = parked.pending.Token
		result["expires_at"] = parked.pending.ExpiresAt
	}
	run, err := s.readRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run != nil {
		result["status"] = run.Status
		result["last_error"] = run.LastError
	}
	return result, nil
}

// defaultClaimApproval calls the engine-only claim_approval in the run's database; decision holds
// the run write and the expect_current_step / expect_attempt_count fence.
func defaultClaimApproval(ctx context.Context, runID int64, owner, tokenHash string, decision orm.RecordMap) (bool, error) {
	kwargs := map[string]any{
		"id": runID, "lease_owner": owner, "lease_seconds": int64(runLeaseSeconds),
		"token_hash": tokenHash, "without_token": tokenHash == "",
	}
	for key, value := range decision {
		kwargs[key] = value
	}
	return callApprovalMethod(ctx, "claim_approval", kwargs)
}

// defaultReleaseApproval calls the engine-only release_approval_claim in the run's database.
func defaultReleaseApproval(ctx context.Context, runID int64, owner string, restore orm.RecordMap) (bool, error) {
	kwargs := map[string]any{"id": runID, "lease_owner": owner}
	for key, value := range restore {
		kwargs[key] = value
	}
	return callApprovalMethod(ctx, "release_approval_claim", kwargs)
}

func callApprovalMethod(ctx context.Context, method string, kwargs map[string]any) (bool, error) {
	result, err := modelruntime.NewService().Execute(workflowmodels.WorkflowEngineContext(ctx), modelruntime.Request{
		Model: workflowmodels.WorkflowRunModelName, Method: method, Kwargs: kwargs,
	})
	if err != nil {
		return false, err
	}
	won, _ := result.(bool)
	return won, nil
}

// defaultExpireApprovals calls the engine-only expire_approvals in the run's database.
func defaultExpireApprovals(ctx context.Context, limit int64) ([]workflowmodels.WorkflowExpiredApproval, error) {
	result, err := modelruntime.NewService().Execute(workflowmodels.WorkflowEngineContext(ctx), modelruntime.Request{
		Model:  workflowmodels.WorkflowRunModelName,
		Method: "expire_approvals",
		Kwargs: map[string]any{"limit": limit},
	})
	if err != nil {
		return nil, err
	}
	rows, _ := result.([]workflowmodels.WorkflowExpiredApproval)
	return rows, nil
}

// expireApprovalWaits closes expired waits (resume cron, before the retry claim). A failure is
// logged and never blocks the retry claim.
func (s *Service) expireApprovalWaits(ctx context.Context, limit int64) {
	rows, err := s.expireApprovals(ctx, limit)
	if err != nil {
		log.Errorf("workflow: expire approval waits: %v", err)
		return
	}
	for _, row := range rows {
		// The UPDATE bypassed markRun, so the transition is counted here (plan T102).
		countRunTransition(workflowmodels.ContextWithWorkflowTriggerType(ctx, row.TriggerType), workflowmodels.WorkflowRunStatusFailed)
		log.Infof("workflow: run %d approval wait expired", row.ID)
	}
}
