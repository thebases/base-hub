package engine

// File overview:
// - Responsibility: run ownership (plan T49) and run identity rules (T48 review).
//   - Lease: every execution holds a lease token on its run. markRun renews it before writing and
//     stops with ErrRunLeaseLost when another worker took the run; a heartbeat renews it while a
//     step runs and cancels the step when it is lost. The reaper reclaims a running run only once
//     its lease expired, so a long step is no longer reaped while in flight.
//   - Caller-bound runs (started under a non-sudo env: /wh, test-run) retry in place under that
//     identity with status running; the system cron (uid 0, sudo) never resumes them, because it
//     would replay the caller's steps with more rights than the caller had.
// - Key symbols: `ErrRunLeaseLost`, `contextWithRunLease`, `startRunLeaseHeartbeat`, `callerBoundContext`.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"git.thebase.vn/base_hub/internal/modelruntime"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
	"github.com/google/uuid"
)

// ErrRunLeaseLost: another worker owns the run now (it was reaped and claimed again). The
// execution holding the stale lease must stop without writing.
var ErrRunLeaseLost = errors.New("workflow: run lease lost")

// runLeaseSeconds is the lease length; the heartbeat renews it every third of that.
const runLeaseSeconds = workflowmodels.DefaultWorkflowRunLeaseSeconds

// runLeaseHeartbeat is how often a running step renews its lease; a var for tests.
var runLeaseHeartbeat = time.Duration(runLeaseSeconds) * time.Second / 3

type runLease struct {
	runID int64 // 0 while the token is only held for a claim batch (ResumeDueRuns)
	owner string
}

type runLeaseContextKey struct{}

func contextWithRunLease(ctx context.Context, runID int64, owner string) context.Context {
	return context.WithValue(ctx, runLeaseContextKey{}, runLease{runID: runID, owner: owner})
}

func runLeaseFromContext(ctx context.Context) (runLease, bool) {
	if ctx == nil {
		return runLease{}, false
	}
	lease, ok := ctx.Value(runLeaseContextKey{}).(runLease)
	return lease, ok && lease.owner != ""
}

// newRunLeaseOwner returns a token unique to one execution (host and pid help an operator see
// which instance holds a run).
func newRunLeaseOwner() string {
	host, _ := os.Hostname()
	return fmt.Sprintf("%s:%d:%s", host, os.Getpid(), uuid.NewString())
}

// Defaults for the Service lease hooks; package vars so the test binary can replace them.
var (
	defaultRenewRunLease = func(ctx context.Context, runID int64, owner string) (bool, error) {
		return callRunLeaseMethod(ctx, "renew_run_lease", runID, owner)
	}
	defaultTakeRunLease = func(ctx context.Context, runID int64, owner string) (bool, error) {
		return callRunLeaseMethod(ctx, "take_run_lease", runID, owner)
	}
)

func callRunLeaseMethod(ctx context.Context, method string, runID int64, owner string) (bool, error) {
	result, err := modelruntime.NewService().Execute(workflowmodels.WorkflowEngineContext(ctx), modelruntime.Request{
		Model:  workflowmodels.WorkflowRunModelName,
		Method: method,
		Kwargs: map[string]any{"id": runID, "lease_owner": owner, "lease_seconds": runLeaseSeconds},
	})
	if err != nil {
		return false, err
	}
	if flag, ok := result.(string); ok && flag == workflowmodels.WorkflowRunLeaseCancelRequested {
		// Held, but cancel_run flagged the run (plan T100).
		return true, errRunCancelRequested
	}
	held, _ := result.(bool)
	return held, nil
}

// errRunCancelRequested is what a renewLease hook returns (with held=true) for a flagged run.
var errRunCancelRequested = errors.New("workflow: run cancel requested")

// checkRunLease renews the lease ctx holds on runID before a write. No lease in ctx (or a lease
// for another run) means the caller does not fence this write.
func (s *Service) checkRunLease(ctx context.Context, runID int64) error {
	lease, ok := runLeaseFromContext(ctx)
	if !ok || lease.runID != runID {
		return nil
	}
	renew := s.renewLease
	if renew == nil { // a service used without withDefaults (internal entry points in tests)
		renew = defaultRenewRunLease
	}
	held, err := renew(ctx, runID, lease.owner)
	if errors.Is(err, errRunCancelRequested) {
		return fmt.Errorf("%w: run %d (cancel requested)", ErrRunCancelled, runID)
	}
	if err != nil {
		return fmt.Errorf("workflow: renew lease of run %d: %w", runID, err)
	}
	if !held {
		return fmt.Errorf("%w: run %d", ErrRunLeaseLost, runID)
	}
	return nil
}

// startRunLeaseHeartbeat renews the run lease while a step runs and cancels the step (cause
// ErrRunLeaseLost) when the lease cannot be renewed. The returned stop waits for the goroutine.
func (s *Service) startRunLeaseHeartbeat(ctx context.Context, cancel context.CancelCauseFunc) func() {
	lease, ok := runLeaseFromContext(ctx)
	if !ok || lease.runID <= 0 {
		return func() {}
	}
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		ticker := time.NewTicker(runLeaseHeartbeat)
		defer ticker.Stop()
		lastRenewed := time.Now()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				err := s.checkRunLease(ctx, lease.runID)
				if err == nil {
					lastRenewed = time.Now()
					continue
				}
				if errors.Is(err, ErrRunCancelled) {
					// cancel_run flagged the run (possibly on another instance): stop the step now;
					// the next markRun writes the cancelled status.
					cancel(err)
					return
				}
				log.Errorf("workflow: run %d heartbeat: %v", lease.runID, err)
				// Cancel only when the lease is really gone (another owner), or when renewals kept
				// failing until the lease ran out; a transient DB error retries on the next tick.
				if errors.Is(err, ErrRunLeaseLost) || time.Since(lastRenewed) >= time.Duration(runLeaseSeconds)*time.Second {
					cancel(fmt.Errorf("%w: %v", ErrRunLeaseLost, err))
					return
				}
			}
		}
	}()
	return func() {
		close(done)
		<-finished
	}
}

// sleepHoldingLease waits d between attempts while keeping the run lease alive, so a long cooldown
// of a run retrying in place is not mistaken for a dead worker.
func (s *Service) sleepHoldingLease(ctx context.Context, d time.Duration) error {
	sleepCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stop := s.startRunLeaseHeartbeat(sleepCtx, cancel)
	defer stop()
	if err := s.sleep(sleepCtx, d); err != nil {
		if cause := context.Cause(sleepCtx); cause != nil && !errors.Is(cause, context.Canceled) {
			return cause
		}
		return err
	}
	return nil
}

// maxInPlaceRetryBackoff caps each cooldown of a run retrying in place: a caller-bound run holds
// its HTTP request (and a server goroutine) while it waits, and clients give up long before a
// day-long cooldown.
const maxInPlaceRetryBackoff = 30 * time.Second

// callerBoundContext reports whether ctx executes under a caller's own (non-sudo) identity.
func callerBoundContext(ctx context.Context) bool {
	env := orm.EnvironmentFromContext(ctx)
	return env != nil && !env.IsSudo()
}

type runFlagsContextKey struct{}

// runFlags are decided once when a run starts (or resumes) and read by the attempt loops.
type runFlags struct {
	// retryInPlace: retries stay in this execution (status running) instead of being handed to
	// the cron: caller-bound runs, and runs without a flow version (cannot be resumed).
	retryInPlace bool
}

func contextWithRunFlags(ctx context.Context, flags runFlags) context.Context {
	return context.WithValue(ctx, runFlagsContextKey{}, flags)
}

func runFlagsFromContext(ctx context.Context) runFlags {
	if ctx == nil {
		return runFlags{}
	}
	flags, _ := ctx.Value(runFlagsContextKey{}).(runFlags)
	return flags
}

// isRunRetryScheduled reports a step failure whose retry was handed to resume_due_runs: the run is
// stored and will continue, so callers report it as accepted rather than failed.
func isRunRetryScheduled(err error) bool {
	return errors.Is(err, ErrRunRetryScheduled)
}
