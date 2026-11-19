package engine

// File overview:
// - Responsibility: replay_run (plan T95, WF-X-6): start a NEW run from a finished run's stored
//   input (or from the stored output of the step before from_step_key), linked by parent_run_id.
// - The parent is never resumed or changed: ResumeRun still refuses dead_letter, and replay never
//   reads step snapshots except the one exception (the payload going into from_step_key), and only
//   when it was stored in full.
// - Identity: like /wf/workflows/:id/test-run, the replay executes under the caller's env (non-sudo
//   for normal users, so caller-bound: retries in place, never resumed by the cron).
// - Side effects: the replay gets a fresh run name, hence fresh step idempotency keys
//   (`<run name>:<step key>`). Downstream systems that deduplicate on that key WILL see the
//   replayed steps as new requests: a replay of a succeeded run posts, notifies and writes again.
//   That is the point of a replay, and the reason it is an explicit operator action.
// - Key symbols: `Service.ReplayRun`, `replayableRun`, `replayInput`.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
	"github.com/google/uuid"
)

type replayParentContextKey struct{}

// ensureWorkflowAuthor checks the caller is base.group_system (a seam for tests).
var ensureWorkflowAuthor = func(ctx context.Context) error {
	return workflowmodels.EnsureWorkflowRole(ctx, workflowmodels.WorkflowRoleAuthor)
}

func contextWithReplayParent(ctx context.Context, parentID int64) context.Context {
	return context.WithValue(ctx, replayParentContextKey{}, parentID)
}

// replayParentFromContext is the parent_run_id createWorkflowRun stores (nil outside replay_run).
func replayParentFromContext(ctx context.Context) *int64 {
	if ctx == nil {
		return nil
	}
	if id, ok := ctx.Value(replayParentContextKey{}).(int64); ok && id > 0 {
		return &id
	}
	return nil
}

// ReplayRun replays run parentID (plan T95) and answers the new run's {run_id, parent_run_id,
// name, status, last_error}. The new run executes synchronously through the normal engine path
// (executeFlowFrom: audit policy, lease, admission, retries, metrics), on the definition version
// the parent ran (plan T97; a test-run parent, flow_version 0, replays on the current draft).
func (s *Service) ReplayRun(ctx context.Context, parentID int64, fromStepKey string) (orm.RecordMap, error) {
	s = s.withDefaults()
	ctx = workflowContextOrBackground(ctx)
	parent, err := s.readRun(ctx, parentID)
	if err != nil {
		return nil, err
	}
	if parent == nil {
		return nil, orm.NotFoundf("workflow: run %d not found", parentID)
	}
	if err := replayableRun(parent); err != nil {
		return nil, err
	}
	flow, err := s.loadRunFlow(ctx, parent, func() (*workflowmodels.FlowRecord, error) {
		return s.loadActiveFlow(ctx, parent.Topic)
	})
	if err != nil {
		if errors.Is(err, ErrFlowNotFound) {
			return nil, orm.NotFoundf("workflow: run %d cannot be replayed: %v", parent.ID, err)
		}
		return nil, err
	}
	if !replayCallerAllowed(ctx, flow) {
		return nil, orm.Forbiddenf("permission denied: workflow access denied")
	}
	// A replay of a run that waited for approval would let an operator start a fresh wait and
	// decide it (plan T103 review): only base.group_system replays such runs.
	if runWaitedForApproval(parent) {
		if err := ensureWorkflowAuthor(ctx); err != nil {
			return nil, orm.Forbiddenf("workflow: run %d waited for approval; only base.group_system can replay it", parent.ID)
		}
	}
	runtime, err := s.versionRuntime(ctx, flow, parent.FlowVersion)
	if err != nil {
		if errors.Is(err, ErrFlowNotFound) {
			return nil, orm.NotFoundf("workflow: run %d cannot be replayed: %v", parent.ID, err)
		}
		return nil, err
	}
	input, startIndex, err := replayInput(parent, &runtime.flow, runtime.steps, fromStepKey)
	if err != nil {
		return nil, err
	}

	name := fmt.Sprintf("%s:replay:%s", parent.Name, uuid.NewString()[:8])
	ctx = workflowmodels.ContextWithWorkflowTriggerType(ctx, parent.TriggerType)
	// The parent was stored in full (replayableRun); the replay stores the same way.
	ctx = workflowmodels.ContextWithWorkflowAuditMode(ctx, workflowmodels.WorkflowAuditPayloadFull)
	ctx = workflowmodels.ContextWithWorkflowRequestID(ctx, name)
	ctx = contextWithModelTriggerChain(ctx, parent.TriggerChain)
	ctx = contextWithReplayParent(ctx, parent.ID)

	runID, execErr := s.executeFlowFrom(ctx, runtime, input, startIndex)
	if runID <= 0 || errors.Is(execErr, workflowmodels.ErrWorkflowRunConcurrencyLimit) {
		// No run, or a refused one (409 through the models error classifier).
		return nil, execErr
	}
	// Like test-run, a failed step is an audited outcome of the replay, not an RPC failure.
	result := orm.RecordMap{"run_id": runID, "parent_run_id": parent.ID, "name": name, "status": "", "last_error": ""}
	// A replay that parked at a wait_approval step (plan T103) never hands out its resume token:
	// the replayer could approve their own request. base.group_system decides it without token.
	run, err := s.readRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run != nil {
		result["name"] = run.Name
		result["status"] = run.Status
		result["last_error"] = run.LastError
	}
	return result, nil
}

// runWaitedForApproval reports whether the run's history has a wait_approval step row.
func runWaitedForApproval(run *workflowmodels.WorkflowRunRecord) bool {
	return slices.ContainsFunc(run.StepRuns, func(row workflowmodels.WorkflowStepRun) bool {
		return row.Action == workflowmodels.WaitApprovalActionCode || row.ImplementationKey == workflowmodels.WaitApprovalActionCode
	})
}

// replayableStatuses are the final statuses a run can be replayed from.
var replayableStatuses = []string{
	workflowmodels.WorkflowRunStatusDeadLetter, workflowmodels.WorkflowRunStatusFailed,
	workflowmodels.WorkflowRunStatusSucceeded, workflowmodels.WorkflowRunStatusCancelled,
}

// replayableRun checks the parent run can be replayed at all: final status, not tcp (its caller
// and reply sink are gone), payloads stored in full (a hash or none row holds no input to replay).
func replayableRun(parent *workflowmodels.WorkflowRunRecord) error {
	status := strings.TrimSpace(parent.Status)
	if !slices.Contains(replayableStatuses, status) {
		return orm.Conflictf("workflow: run %d is %s; only dead_letter, failed, succeeded or cancelled runs can be replayed", parent.ID, status)
	}
	if synchronousTriggerType(parent.TriggerType) {
		return orm.InvalidParamsf("workflow: run %d is a %s run and cannot be replayed", parent.ID, parent.TriggerType)
	}
	// A dry run only recorded intents (plan T101); replaying it would perform the real side
	// effects, so the operator starts a new test run (dry or not) instead.
	if parent.DryRun {
		return orm.InvalidParamsf("workflow: run %d is a dry run and cannot be replayed; start a new test run", parent.ID)
	}
	mode := strings.TrimSpace(parent.AuditMode)
	if !workflowmodels.IsWorkflowAuditPayloadMode(mode) {
		mode = workflowmodels.DefaultWorkflowAuditMode(parent.TriggerType)
	}
	if mode != workflowmodels.WorkflowAuditPayloadFull {
		return orm.InvalidParamsf("workflow: run %d stored its payloads as %s, so there is no input to replay", parent.ID, mode)
	}
	return nil
}

// replayInput returns the payload and start index of the replay. Without fromStepKey it is the
// parent's input_payload. With it, the step must exist in the current (linear) flow and the
// payload going into it is the stored output of the parent's last row of the step before it,
// accepted only when that row passed its payload on (succeeded, skipped, or failed with
// continue_on_error) and was stored complete: not truncated, no audit marker, no redacted value.
func replayInput(parent *workflowmodels.WorkflowRunRecord, flow *workflowmodels.FlowRecord, steps []workflowmodels.Step, fromStepKey string) ([]byte, int, error) {
	fromStepKey = strings.TrimSpace(fromStepKey)
	if fromStepKey == "" {
		input, err := replayPayload(parent.InputPayload, fmt.Sprintf("run %d input_payload", parent.ID))
		return input, 0, err
	}
	if flow.ExecutionMode == workflowmodels.WorkflowExecutionModeDAG {
		return nil, 0, orm.InvalidParamsf("workflow: from_step_key is only supported for linear workflows")
	}
	index := -1
	for i, step := range steps {
		if step.ResolvedKey(i) == fromStepKey {
			index = i
			break
		}
	}
	if index < 0 {
		return nil, 0, orm.InvalidParamsf("workflow: step %q does not exist in the active workflow", fromStepKey)
	}
	if index == 0 {
		input, err := replayPayload(parent.InputPayload, fmt.Sprintf("run %d input_payload", parent.ID))
		return input, 0, err
	}
	previousKey := steps[index-1].ResolvedKey(index - 1)
	var row *workflowmodels.WorkflowStepRun
	for i := len(parent.StepRuns) - 1; i >= 0; i-- {
		if strings.TrimSpace(parent.StepRuns[i].StepKey) == previousKey {
			row = &parent.StepRuns[i]
			break
		}
	}
	if row == nil {
		return nil, 0, orm.InvalidParamsf("workflow: run %d has no recorded output of step %s", parent.ID, previousKey)
	}
	passedOn := row.Status == workflowmodels.WorkflowRunStatusSucceeded || row.Status == workflowmodels.WorkflowRunStatusSkipped ||
		(row.Status == workflowmodels.WorkflowRunStatusFailed && row.ContinueOnError)
	if !passedOn {
		return nil, 0, orm.InvalidParamsf("workflow: step %s of run %d ended %s, so it produced no input for step %s", previousKey, parent.ID, row.Status, fromStepKey)
	}
	input, err := replayPayload(row.OutputSnapshot, fmt.Sprintf("run %d output of step %s", parent.ID, previousKey))
	return input, index, err
}

// replayPayload turns a stored payload back into bytes, refusing any form that lost data.
func replayPayload(stored map[string]any, what string) ([]byte, error) {
	if isTruncatedSnapshot(stored) {
		return nil, orm.InvalidParamsf("workflow: %s was truncated in the run history and cannot be replayed", what)
	}
	if isAuditMarker(stored) || containsRedactedValue(stored) {
		return nil, orm.InvalidParamsf("workflow: %s is hashed or redacted in the run history and cannot be replayed", what)
	}
	return payloadFromAudit(stored)
}

// containsRedactedValue reports whether the full-mode policy replaced any value in payload.
func containsRedactedValue(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for _, child := range typed {
			if containsRedactedValue(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsRedactedValue(child) {
				return true
			}
		}
	case string:
		return typed == auditRedactedValue
	}
	return false
}

// replayCallerAllowed applies the workflow's group gate like the /wf routes (workflowRouteAllows):
// no group = everyone, else sudo or membership. Calls without an RBAC identity (internal) pass.
func replayCallerAllowed(ctx context.Context, flow *workflowmodels.FlowRecord) bool {
	if flow.GroupID == nil || *flow.GroupID <= 0 {
		return true
	}
	env := orm.EnvironmentFromContext(ctx)
	if env == nil || env.IsSudo() {
		return true
	}
	if _, ok := env.ContextValue(orm.RBACContextAuthModeKey); !ok {
		return true
	}
	raw, _ := env.ContextValue(orm.RBACContextGroupIDsKey)
	groups, _ := raw.([]int64)
	return slices.Contains(groups, *flow.GroupID)
}
