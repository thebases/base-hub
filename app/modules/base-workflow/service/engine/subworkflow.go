package engine

// File overview:
// - Responsibility: sub-workflow calls (plan T103, call_workflow@v1 and for_each@v1): run another
//   workflow's PUBLISHED snapshot inline, in the same tenant DB, as a child run of the calling run.
// - Invariants:
//   - The call chain (workflow ids from the root run down to the caller) travels on ctx: at most
//     maxWorkflowCallDepth nested calls, and a workflow already in the chain is refused, so calls can
//     never recurse. Both refusals are non-retryable step errors.
//   - The child is linked by parent_run_id and is caller-bound: it retries in place and is never
//     resumed by the cron (the caller waits for it; a crash closes it failed and the calling step's
//     own retry starts a new child). Its final status on exhaustion is failed, never dead_letter.
//   - The child inherits the caller's identity, dry-run flag, audit mode and trigger type, and stops
//     when the calling step stops: cancel_run of the caller cancels it, the caller's max_run_seconds
//     or step timeout fails it. Its own max_run_seconds / max_concurrent_runs apply as for any run
//     (a concurrency refusal is a retryable step error).
//   - The child never writes the caller's tcp reply sink; its final payload is handed back through
//     its own holder.
// - Key symbols: `Service.CallWorkflow`, `maxWorkflowCallDepth`.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
	"github.com/google/uuid"
)

// maxWorkflowCallDepth is how many sub-workflow levels may nest under a root run.
const maxWorkflowCallDepth = 5

// ErrWorkflowCallRefused: the call would exceed the depth limit or recurse. Not retryable.
var ErrWorkflowCallRefused = errors.New("workflow: sub-workflow call refused")

type workflowCallContextKey struct{}

// workflowCallFrame is bound on a child run's ctx.
type workflowCallFrame struct {
	// chain holds the workflow ids from the root run down to the child's caller.
	chain []int64
	// output receives the child's final payload (publishWorkflowOutput).
	output *workflowmodels.WorkflowOutput
}

func workflowCallFrameFromContext(ctx context.Context) (workflowCallFrame, bool) {
	if ctx == nil {
		return workflowCallFrame{}, false
	}
	frame, ok := ctx.Value(workflowCallContextKey{}).(workflowCallFrame)
	return frame, ok
}

type childIdempotencyBaseContextKey struct{}

// childIdempotencyBase is the prefix of a child run's step idempotency keys ("" outside a child
// started from a step with a key).
func childIdempotencyBase(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	base, _ := ctx.Value(childIdempotencyBaseContextKey{}).(string)
	return base
}

// childRunContext reports whether ctx executes a sub-workflow child run.
func childRunContext(ctx context.Context) bool {
	_, ok := workflowCallFrameFromContext(ctx)
	return ok
}

// CallWorkflow runs workflow workflowID with payload as a child of the run executing on ctx (a step
// handler's ctx) and returns the child's final payload. label distinguishes sibling children of
// one step in their run names.
func (s *Service) CallWorkflow(ctx context.Context, workflowID int64, payload []byte, label string) ([]byte, error) {
	s = s.withDefaults()
	if workflowID <= 0 {
		return nil, fmt.Errorf("%w: target workflow is required: %w", ErrWorkflowCallRefused, workflowmodels.ErrWorkflowStepNonRetryable)
	}
	chain, err := childCallChain(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	flow, err := s.loadFlowByID(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	// A caller-bound run must not reach a workflow its caller could not run directly.
	if callerBoundContext(ctx) && !replayCallerAllowed(ctx, flow) {
		return nil, fmt.Errorf("%w: workflow %d: permission denied: workflow access denied: %w", ErrWorkflowCallRefused, workflowID, workflowmodels.ErrWorkflowStepNonRetryable)
	}
	runtime, err := s.publishedRuntime(ctx, flow)
	if err != nil {
		return nil, err
	}

	output := &workflowmodels.WorkflowOutput{}
	childCtx, stop := childCallContext(ctx, workflowCallFrame{chain: chain, output: output}, label)
	defer stop()
	runID, err := s.executeFlowFrom(childCtx, runtime, payload, 0)
	if err != nil {
		return nil, fmt.Errorf("workflow: sub-workflow %q (run %d) failed: %w", strings.TrimSpace(flow.Name), runID, err)
	}
	result, ok := output.Get()
	if !ok {
		return nil, fmt.Errorf("workflow: sub-workflow %q (run %d) returned no output", strings.TrimSpace(flow.Name), runID)
	}
	return result, nil
}

// childCallChain returns the chain a child of workflowID runs under, or a non-retryable refusal.
func childCallChain(ctx context.Context, workflowID int64) ([]int64, error) {
	frame, _ := workflowCallFrameFromContext(ctx)
	chain := slices.Clone(frame.chain)
	if scope, ok := workflowmodels.WorkflowScopeFromContext(ctx); ok && scope.WorkflowID > 0 {
		chain = append(chain, scope.WorkflowID)
	}
	if len(chain) > maxWorkflowCallDepth {
		return nil, fmt.Errorf("%w: more than %d nested sub-workflow calls: %w", ErrWorkflowCallRefused, maxWorkflowCallDepth, workflowmodels.ErrWorkflowStepNonRetryable)
	}
	if slices.Contains(chain, workflowID) {
		return nil, fmt.Errorf("%w: workflow %d is already in the call chain %v (recursion): %w", ErrWorkflowCallRefused, workflowID, chain, workflowmodels.ErrWorkflowStepNonRetryable)
	}
	return chain, nil
}

// childCallContext derives the child's execution ctx from the calling step's ctx. Values (tenant
// env, identity, dry run, audit mode, trigger type) are inherited; cancellation is re-raised with a
// run-control cause so the child's markRun still writes its final status (cancelled when the caller
// was cancelled, failed for any other stop such as the caller's deadline or step timeout).
func childCallContext(ctx context.Context, frame workflowCallFrame, label string) (context.Context, func()) {
	childCtx, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
	stopAfter := context.AfterFunc(ctx, func() {
		cause := context.Cause(ctx)
		if !errors.Is(cause, ErrRunCancelled) {
			cause = fmt.Errorf("%w: the calling step stopped: %v", ErrRunTimedOut, cause)
		}
		cancel(cause)
	})
	childCtx = context.WithValue(childCtx, workflowCallContextKey{}, frame)
	childCtx = workflowmodels.ContextWithoutWorkflowOutputSink(childCtx)
	if lease, ok := runLeaseFromContext(ctx); ok && lease.runID > 0 {
		childCtx = contextWithReplayParent(childCtx, lease.runID)
	}
	// A fresh, unique run name: the caller's name would collide on the unique run name, and a
	// retry of the calling step starts a new child. The child's step idempotency keys use the
	// stable `<parent step key>:<label>` instead (stepIdempotencyKey), so systems that deduplicate
	// see a retried child step as the same request.
	parentKey := workflowmodels.WorkflowStepIdempotencyKeyFromContext(ctx)
	if label = strings.TrimSpace(label); label == "" {
		label = "call"
	}
	if parentKey != "" {
		childCtx = context.WithValue(childCtx, childIdempotencyBaseContextKey{}, parentKey+":"+label)
	} else {
		// No calling step key (no run name): the unique run name is the only safe base.
		parentKey = "wfcall"
		childCtx = context.WithValue(childCtx, childIdempotencyBaseContextKey{}, "")
	}
	childCtx = workflowmodels.ContextWithWorkflowRequestID(childCtx, parentKey+":"+label+":"+uuid.NewString()[:8])
	return childCtx, func() {
		stopAfter()
		cancel(nil)
	}
}
