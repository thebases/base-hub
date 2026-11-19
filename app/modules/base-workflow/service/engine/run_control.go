package engine

// File overview:
// - Responsibility: run control (plan T100) — per-execution timeout (max_run_seconds), cancel
//   (in-process registry plus the cross-instance cancel_requested flag read on lease renewal) and
//   the max_concurrent_runs admission gate at run start.
// - Invariant: a stopped run (cancelled or timed out) still ends through markRun, which writes the
//   final status with a context detached from the cancelled one, so history and lease release are
//   never lost to the cancellation itself.
// - Key symbols: `ErrRunCancelled`, `ErrRunTimedOut`, `withRunControl`, `runStopCause`, `admitRun`.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"git.thebase.vn/base_hub/internal/modelruntime"
	"git.thebase.vn/base_hub/internal/orm"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

var (
	// ErrRunCancelled: cancel_run stopped the run; it ends cancelled.
	ErrRunCancelled = errors.New("workflow: run cancelled")
	// ErrRunTimedOut: the execution exceeded the workflow's max_run_seconds; the run ends failed and
	// is not retried (neither in place nor by the cron).
	ErrRunTimedOut = errors.New("workflow: run exceeded max_run_seconds")
)

// runStopWriteTimeout bounds the final write of a stopped run (its own ctx is already done).
const runStopWriteTimeout = 10 * time.Second

// concurrencyParkDelay is how long a sudo-started run over max_concurrent_runs waits before the
// resume cron tries it again.
const concurrencyParkDelay = 5 * time.Second

// runCancelEntry is one execution's cancel func; compared by pointer so a finishing execution
// never removes the entry of a later execution of the same run on this node.
type runCancelEntry struct {
	cancel context.CancelCauseFunc
}

var runCancelRegistry = struct {
	sync.Mutex
	entries map[string]*runCancelEntry
}{entries: map[string]*runCancelEntry{}}

// runControlKey is `<logical db>|<run id>`: run ids are only unique within one tenant database.
func runControlKey(ctx context.Context, runID int64) string {
	return orm.ResolveLogicalDBName(orm.EnvironmentFromContext(ctx).DBName()) + "|" + strconv.FormatInt(runID, 10)
}

// withRunControl binds one execution of a run to a cancellable context registered under its key,
// and to the workflow's max_run_seconds deadline. Each execution (first start or resume) gets its
// own budget; time spent parked in waiting_retry does not count. release must be called when the
// execution returns.
func withRunControl(ctx context.Context, runtime flowRuntime) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(ctx)
	stopTimeout := context.CancelFunc(func() {})
	if seconds := runtime.flow.MaxRunSeconds; seconds > 0 {
		ctx, stopTimeout = context.WithTimeoutCause(ctx, time.Duration(seconds)*time.Second,
			fmt.Errorf("%w (%d s)", ErrRunTimedOut, seconds))
	}
	if runtime.runID <= 0 {
		return ctx, func() { stopTimeout(); cancel(nil) }
	}
	key := runControlKey(ctx, runtime.runID)
	entry := &runCancelEntry{cancel: cancel}
	runCancelRegistry.Lock()
	runCancelRegistry.entries[key] = entry
	runCancelRegistry.Unlock()
	return ctx, func() {
		runCancelRegistry.Lock()
		if runCancelRegistry.entries[key] == entry {
			delete(runCancelRegistry.entries, key)
		}
		runCancelRegistry.Unlock()
		stopTimeout()
		cancel(nil)
	}
}

// cancelLocalRun cancels the execution of runID running on this node, if any.
func cancelLocalRun(ctx context.Context, runID int64) bool {
	key := runControlKey(ctx, runID)
	runCancelRegistry.Lock()
	entry := runCancelRegistry.entries[key]
	runCancelRegistry.Unlock()
	if entry == nil {
		return false
	}
	entry.cancel(fmt.Errorf("%w: run %d", ErrRunCancelled, runID))
	return true
}

// onRunCancelled is the RunCancelled hook behind cancel_run: a run cancelled directly by the UPDATE
// (pending/waiting_retry) is counted here since no markRun will see it; a flagged running run is
// stopped at once when it executes on this node (other nodes see the flag on lease renewal).
func onRunCancelled(ctx context.Context, runID int64, status, triggerType string) {
	if status == workflowmodels.WorkflowRunStatusCancelled {
		countRunTransition(workflowmodels.ContextWithWorkflowTriggerType(ctx, triggerType), status)
		return
	}
	cancelLocalRun(ctx, runID)
}

// runStopCause returns the cancel or timeout cause when ctx is done because of run control, else nil
// (another cancellation, e.g. shutdown, keeps its previous behavior).
func runStopCause(ctx context.Context) error {
	if ctx == nil || ctx.Err() == nil {
		return nil
	}
	cause := context.Cause(ctx)
	if errors.Is(cause, ErrRunCancelled) || errors.Is(cause, ErrRunTimedOut) {
		return cause
	}
	return nil
}

// stoppedRunValues turns a run write into the final write of a stopped run: cancelled, or failed on
// timeout, never left to retry. A write that already reports success keeps it: the work finished.
func stoppedRunValues(values orm.RecordMap, stop error) (orm.RecordMap, bool) {
	if status, _ := values["status"].(string); status == workflowmodels.WorkflowRunStatusSucceeded {
		return values, false
	}
	out := make(orm.RecordMap, len(values)+3)
	for key, value := range values {
		out[key] = value
	}
	out["status"] = workflowmodels.WorkflowRunStatusFailed
	if errors.Is(stop, ErrRunCancelled) {
		out["status"] = workflowmodels.WorkflowRunStatusCancelled
	}
	out["next_retry_at"] = int64(0)
	out["last_error"] = stop.Error()
	return out, true
}

// runAdmission is one admit_run call.
type runAdmission struct {
	runID, workflowID int64
	limit             int
	owner             string
	parkUntil         int64
	reason            string
}

// defaultAdmitRun calls the engine-only admit_run method in the run's own database.
func defaultAdmitRun(ctx context.Context, a runAdmission) (string, error) {
	result, err := modelruntime.NewService().Execute(workflowmodels.WorkflowEngineContext(ctx), modelruntime.Request{
		Model:  workflowmodels.WorkflowRunModelName,
		Method: "admit_run",
		Kwargs: map[string]any{
			"id": a.runID, "workflow_id": a.workflowID, "limit": a.limit,
			"lease_owner": a.owner, "park_until": a.parkUntil, "reason": a.reason,
		},
	})
	if err != nil {
		return "", err
	}
	outcome, _ := result.(string)
	return outcome, nil
}

// admitRun applies max_concurrent_runs to the run in runtime (leased by ctx). It returns the
// admit_run outcome; a lost lease is ErrRunLeaseLost. Unlimited workflows and runs without a
// workflow row (ExecuteSteps) are always admitted without a query.
func (s *Service) admitRun(ctx context.Context, runtime flowRuntime) (string, error) {
	limit := runtime.flow.MaxConcurrentRuns
	if limit <= 0 || runtime.flow.ID <= 0 {
		return workflowmodels.WorkflowRunAdmitted, nil
	}
	lease, _ := runLeaseFromContext(ctx)
	outcome, err := s.admit(ctx, runAdmission{
		runID: runtime.runID, workflowID: runtime.flow.ID, limit: limit, owner: lease.owner,
		parkUntil: s.now().Add(concurrencyParkDelay).Unix(),
		reason:    concurrencyLimitError(runtime).Error(),
	})
	if err != nil {
		return "", fmt.Errorf("workflow: admit run %d: %w", runtime.runID, err)
	}
	if outcome == workflowmodels.WorkflowRunLost || outcome == "" {
		return "", fmt.Errorf("%w: run %d", ErrRunLeaseLost, runtime.runID)
	}
	return outcome, nil
}

func concurrencyLimitError(runtime flowRuntime) error {
	return fmt.Errorf("%w (max_concurrent_runs=%d, run %d)", workflowmodels.ErrWorkflowRunConcurrencyLimit, runtime.flow.MaxConcurrentRuns, runtime.runID)
}

// rejectNewRun closes or parks a fresh run refused by admitRun. update is the run's initial write
// (input and output payloads), so the row keeps the event either way.
//   - tcp and caller-bound runs (/wh, test-run, replay) cannot wait: the run ends failed and the
//     caller gets ErrWorkflowRunConcurrencyLimit (409). The row gives its request id back
//     (`<name>:rejected:<id>`) so the caller's retry with the same Idempotency-Key runs instead of
//     being answered as a duplicate of a run that never executed.
//   - sudo-started runs (kafka/mqtt/model) park as waiting_retry at the start step with no attempt
//     charged (attempt_count 0, no step_runs row); resume_due_runs admits them again later (T48).
func (s *Service) rejectNewRun(ctx context.Context, runtime flowRuntime, startIndex int, update orm.RecordMap) error {
	limitErr := concurrencyLimitError(runtime)
	update["last_error"] = limitErr.Error()
	if synchronousRun(ctx) || runFlagsFromContext(ctx).retryInPlace {
		update["status"] = workflowmodels.WorkflowRunStatusFailed
		if name := workflowmodels.WorkflowRequestIDFromContext(ctx); name != "" {
			update["name"] = fmt.Sprintf("%s:rejected:%d", name, runtime.runID)
		}
		if err := s.markRun(ctx, runtime.flow.Topic, runtime.runID, update); err != nil {
			return err
		}
		return limitErr
	}
	update["status"] = workflowmodels.WorkflowRunStatusRetryWaiting
	update["next_retry_at"] = s.now().Add(concurrencyParkDelay).Unix()
	if startIndex >= 0 && startIndex < len(runtime.steps) {
		// Linear resume starts at current_step; DAG resume re-derives its frontier from step_runs.
		update["current_step"] = runtime.steps[startIndex].ResolvedKey(startIndex)
	}
	if err := s.markRun(ctx, runtime.flow.Topic, runtime.runID, update); err != nil {
		return err
	}
	return fmt.Errorf("%w: %v", ErrRunRetryScheduled, limitErr)
}
