package engine

// File overview:
// - Responsibility: owns `workflow` module orchestration for service, where long-running workflow logic and operational state transitions are coordinated.
// - Key symbols: `ErrFlowNotFound`, `ErrActionDefinitionNotFound`, `ErrActionNotSupported`, `ActionHandler`.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/modelruntime"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
	"git.thebase.vn/base_hub/modules/base-workflow/service/libs"
	"git.thebase.vn/base_hub/modules/base-workflow/service/nodes"
	"golang.org/x/sync/errgroup"
)

var (
	ErrFlowNotFound             = errors.New("workflow: active flow not found")
	ErrActionDefinitionNotFound = errors.New("workflow: action definition not found")
	// ErrRunRetryScheduled: a linear step failed with attempts left; the run is waiting_retry and
	// resume_due_runs makes the next attempt (plan T48). The run itself has not failed yet.
	ErrRunRetryScheduled  = errors.New("workflow: step failed, retry scheduled")
	ErrActionNotSupported = errors.New("workflow: action not supported")
	// ErrStepPanicked: a step handler panicked; the panic is turned into this step error (plan T52)
	// so one bad handler fails its step instead of crashing the worker.
	ErrStepPanicked = errors.New("workflow: step handler panicked")
)

type ActionHandler func(ctx context.Context, req ActionRequest) ([]byte, error)

type ActionRequest struct {
	Topic   string
	Step    workflowmodels.Step
	Payload []byte
	// CredentialRefs maps each param resolved from a @cred:/@syscred: reference to that
	// reference, so a node can persist the reference instead of the secret (plan T40).
	CredentialRefs map[string]string
	// IdempotencyKey is `<run name>:<step key>`, the same for every attempt of the step in one
	// run (plan T56); empty when the run has no name. Also bound on the handler ctx
	// (workflowmodels.WorkflowStepIdempotencyKeyFromContext) for nodes that only see ctx.
	IdempotencyKey string
}

type flowRuntime struct {
	flow  workflowmodels.FlowRecord
	steps []workflowmodels.Step
	// edges and pinned: a run of a published snapshot (plan T97) takes its DAG edges from the
	// snapshot; an unpinned run (draft test-run, legacy rows) reads the edge rows at start.
	edges  []workflowmodels.WorkflowGraphEdge
	pinned bool
	runID  int64
}

type executionState struct {
	currentPayload []byte
	stepRuns       []workflowmodels.WorkflowStepRun
	totalAttempts  int
	startStepIndex int
	startAttempt   int
	// dagInterrupted counts, per DAG step key, attempts that were persisted (attempt_count) but
	// never recorded in step_runs because the execution died mid-layer (plan T52).
	dagInterrupted map[string]int
}

type Service struct {
	loadActiveFlow  func(context.Context, string) (*workflowmodels.FlowRecord, error)
	loadFlowVersion func(context.Context, string, int) (*workflowmodels.FlowRecord, error)
	loadFlowByID    func(context.Context, int64) (*workflowmodels.FlowRecord, error)
	loadFlowActions func(context.Context, int64) ([]workflowmodels.WorkflowActionRecord, error)
	loadFlowEdges   func(context.Context, int64) ([]workflowmodels.WorkflowGraphEdge, error)
	loadSnapshot    func(context.Context, int64, int) (*workflowmodels.WorkflowSnapshot, error)
	readRun         func(context.Context, int64) (*workflowmodels.WorkflowRunRecord, error)
	claimDueRuns    func(context.Context, int64, int64) ([]int64, error)
	reapStaleRuns   func(context.Context, int64, int64) ([]int64, error)
	createRun       func(context.Context, workflowmodels.WorkflowRunRecord) (*orm.InsertResult, error)
	updateRun       func(context.Context, orm.RecordMap, orm.RecordMap) (*orm.UpdateResult, error)
	lookupAction    func(string, int) (ActionHandler, bool)
	renewLease      func(context.Context, int64, string) (bool, error)
	takeLease       func(context.Context, int64, string) (bool, error)
	admit           func(context.Context, runAdmission) (string, error)
	claimApproval   func(ctx context.Context, runID int64, owner, tokenHash string, decision orm.RecordMap) (bool, error)
	releaseApproval func(ctx context.Context, runID int64, owner string, restore orm.RecordMap) (bool, error)
	expireApprovals func(ctx context.Context, limit int64) ([]workflowmodels.WorkflowExpiredApproval, error)
	// inApprovalGroup reports whether the decider in ctx belongs to a run's approval_group_id.
	inApprovalGroup func(ctx context.Context, groupID int64) (bool, error)
	// creatorContext rebinds ctx to the identity that started a caller-bound run (uid 0 = anonymous).
	creatorContext func(ctx context.Context, uid int64) (context.Context, error)
	now            func() time.Time
	sleep          func(context.Context, time.Duration) error
	// complete marks a service built by NewService, whose collaborators are all
	// set, so withDefaults can return it as-is instead of cloning per call.
	// Struct-literal services (test doubles) leave it false and get filled.
	complete bool
}

// NewService constructs and returns the service handled by this package.
func NewService() *Service {
	return &Service{
		loadActiveFlow:  loadLatestActiveFlow,
		loadFlowVersion: loadFlowByVersion,
		loadFlowByID:    loadActiveFlowByID,
		loadFlowActions: loadWorkflowActions,
		loadFlowEdges:   loadWorkflowEdges,
		loadSnapshot:    workflowmodels.LoadWorkflowVersionSnapshot,
		readRun:         loadWorkflowRun,
		claimDueRuns:    claimDueWorkflowRuns,
		reapStaleRuns:   reapStaleWorkflowRuns,
		createRun:       createWorkflowRunRecord,
		updateRun:       updateWorkflowRunRecord,
		lookupAction:    resolveWorkflowModuleAction,
		renewLease:      defaultRenewRunLease,
		takeLease:       defaultTakeRunLease,
		admit:           defaultAdmitRun,
		claimApproval:   defaultClaimApproval,
		releaseApproval: defaultReleaseApproval,
		expireApprovals: defaultExpireApprovals,
		inApprovalGroup: workflowmodels.WorkflowCallerInGroup,
		creatorContext:  defaultRunCreatorContext,
		now:             workflowNowUTC,
		sleep:           sleepWithContext,
		complete:        true,
	}
}

// withDefaults clones the service and fills any nil collaborators with the package defaults.
// It keeps partially injected test doubles intact while guaranteeing the execution path always has the hooks it needs.
func (s *Service) withDefaults() *Service {
	if s == nil {
		return NewService()
	}
	if s.complete {
		return s
	}

	defaults := NewService()
	cloned := *s
	cloned.complete = true
	if cloned.loadActiveFlow == nil {
		cloned.loadActiveFlow = defaults.loadActiveFlow
	}
	if cloned.loadFlowVersion == nil {
		cloned.loadFlowVersion = defaults.loadFlowVersion
	}
	if cloned.loadFlowByID == nil {
		cloned.loadFlowByID = defaults.loadFlowByID
	}
	if cloned.loadFlowActions == nil {
		cloned.loadFlowActions = defaults.loadFlowActions
	}
	if cloned.loadFlowEdges == nil {
		cloned.loadFlowEdges = defaults.loadFlowEdges
	}
	if cloned.loadSnapshot == nil {
		cloned.loadSnapshot = defaults.loadSnapshot
	}
	if cloned.readRun == nil {
		cloned.readRun = defaults.readRun
	}
	if cloned.claimDueRuns == nil {
		cloned.claimDueRuns = defaults.claimDueRuns
	}
	if cloned.reapStaleRuns == nil {
		cloned.reapStaleRuns = defaults.reapStaleRuns
	}
	if cloned.createRun == nil {
		cloned.createRun = defaults.createRun
	}
	if cloned.updateRun == nil {
		cloned.updateRun = defaults.updateRun
	}
	if cloned.lookupAction == nil {
		cloned.lookupAction = defaults.lookupAction
	}
	if cloned.renewLease == nil {
		cloned.renewLease = defaults.renewLease
	}
	if cloned.takeLease == nil {
		cloned.takeLease = defaults.takeLease
	}
	if cloned.admit == nil {
		cloned.admit = defaults.admit
	}
	if cloned.claimApproval == nil {
		cloned.claimApproval = defaults.claimApproval
	}
	if cloned.releaseApproval == nil {
		cloned.releaseApproval = defaults.releaseApproval
	}
	if cloned.creatorContext == nil {
		cloned.creatorContext = defaults.creatorContext
	}
	if cloned.expireApprovals == nil {
		cloned.expireApprovals = defaults.expireApprovals
	}
	if cloned.inApprovalGroup == nil {
		cloned.inApprovalGroup = defaults.inApprovalGroup
	}
	if cloned.now == nil {
		cloned.now = defaults.now
	}
	if cloned.sleep == nil {
		cloned.sleep = defaults.sleep
	}
	return &cloned
}

// ExecuteTopic executes the topic handled by this package.
func (s *Service) ExecuteTopic(ctx context.Context, topic string, input []byte) error {
	s = s.withDefaults()
	ctx = workflowContextOrBackground(ctx)
	ctx = workflowmodels.EnsureWorkflowTriggerType(ctx, workflowmodels.WorkflowTriggerTypeWebhook)
	// Validate the invariants for this stage before the function commits to the next step.
	ctx, _ = workflowmodels.EnsureWorkflowRequestID(ctx)

	flow, err := s.loadActiveFlow(ctx, topic)
	if err != nil {
		return err
	}
	// The test-run lets authors try the draft before publishing; every other caller (/wh) runs the
	// published snapshot (plan T97).
	load := s.publishedRuntime
	if workflowmodels.WorkflowTriggerTypeFromContext(ctx) == workflowmodels.WorkflowTriggerTypeManualTest {
		load = s.draftRuntime
	}
	runtime, err := load(ctx, flow)
	if err != nil {
		return err
	}
	return s.executeFlow(ctx, runtime, input)
}

// ExecuteSteps executes the steps handled by this package.
func (s *Service) ExecuteSteps(ctx context.Context, topic string, steps []workflowmodels.Step, input []byte) error {
	s = s.withDefaults()
	ctx = workflowContextOrBackground(ctx)
	return s.executeFlow(ctx, flowRuntime{
		flow: workflowmodels.FlowRecord{
			Topic:    strings.TrimSpace(topic),
			Version:  0,
			IsActive: true,
		},
		steps: append([]workflowmodels.Step(nil), steps...),
	}, input)
}

// ResumeRun resumes the run handled by this package.
func (s *Service) ResumeRun(ctx context.Context, runID int64) error {
	s = s.withDefaults()
	ctx = workflowContextOrBackground(ctx)
	if runID <= 0 {
		log.Errorf("workflow: resume workflow run requires a positive id")
		return fmt.Errorf("workflow: resume workflow run requires a positive id")
	}

	run, err := s.readRun(ctx, runID)
	if err != nil {
		return err
	}
	if run == nil {
		log.Errorf("workflow: workflow run %d not found", runID)
		return orm.NotFoundf("workflow: workflow run %d not found", runID)
	}
	status := strings.TrimSpace(run.Status)
	if status != workflowmodels.WorkflowRunStatusRunning && status != workflowmodels.WorkflowRunStatusRetryWaiting {
		log.Errorf("workflow: workflow run %d is not resumable from status %s", runID, status)
		return orm.Conflictf("workflow: workflow run %d is not resumable from status %s", runID, status)
	}

	// Own the run before touching it (plan T49): the claim already gave this batch's token the
	// lease; a direct call takes it only when no other execution holds a live one.
	owner := ""
	if lease, ok := runLeaseFromContext(ctx); ok && lease.runID == 0 {
		owner = lease.owner
	} else {
		owner = newRunLeaseOwner()
		held, err := s.takeLease(ctx, runID, owner)
		if err != nil {
			return fmt.Errorf("workflow: take lease of run %d: %w", runID, err)
		}
		if !held {
			return orm.Conflictf("workflow: workflow run %d is being executed elsewhere", runID)
		}
	}
	ctx = contextWithRunLease(ctx, runID, owner)
	// A claimed run can wait behind others of its batch; if its lease expired meanwhile and the
	// run was reclaimed elsewhere, leave it to that owner.
	if err := s.checkRunLease(ctx, runID); err != nil {
		if errors.Is(err, ErrRunLeaseLost) {
			log.Infof("workflow: run %d is now owned by another worker, skipped", runID)
			return nil
		}
		if errors.Is(err, ErrRunCancelled) {
			// cancel_run flagged a running run whose execution died (reaped, then claimed): close it
			// as cancelled without running a step (plan T100).
			ctx = workflowmodels.ContextWithWorkflowTriggerType(ctx, run.TriggerType)
			ctx, _ = bindRunAuditPolicy(ctx, workflowmodels.FlowRecord{}, run.AuditMode, run.TriggerType)
			return s.closeRun(ctx, run, workflowmodels.WorkflowRunStatusCancelled, err)
		}
		return err
	}
	ctx = workflowmodels.ContextWithWorkflowRequestID(ctx, run.Name)
	ctx = workflowmodels.ContextWithWorkflowTriggerType(ctx, run.TriggerType)
	// The model-trigger chain that started the run still stops recursion after a resume (T47).
	ctx = contextWithModelTriggerChain(ctx, run.TriggerChain)
	// Payload policy before the flow is known (used only by the close paths below): stored mode,
	// built-in keys. Rebound with the workflow's own keys once the flow is loaded.
	ctx, _ = bindRunAuditPolicy(ctx, workflowmodels.FlowRecord{}, run.AuditMode, run.TriggerType)

	// Runs that must not execute again are closed, never left running (they would cycle through
	// the reaper forever).
	if synchronousTriggerType(run.TriggerType) {
		// The tcp caller got its answer (or timed out) long ago and no reply sink exists any more
		// (plan T50). Reaches here from a run parked before T50 or reaped after a crash.
		return s.closeRun(ctx, run, workflowmodels.WorkflowRunStatusFailed,
			fmt.Errorf("workflow: synchronous %s run %d is not resumed after its reply deadline", run.TriggerType, runID))
	}
	if run.CallerBound {
		// Reaped after a crash: replaying it from the cron would run the caller's steps as sudo.
		return s.closeRun(ctx, run, workflowmodels.WorkflowRunStatusFailed,
			fmt.Errorf("workflow: run %d was started under its caller's identity and is not resumed by the system", runID))
	}
	if run.FlowVersion <= 0 {
		return s.closeRun(ctx, run, workflowmodels.WorkflowRunStatusDeadLetter,
			fmt.Errorf("workflow: workflow run %d cannot resume without a persisted flow version", runID))
	}

	liveFlow, err := s.loadRunFlow(ctx, run, func() (*workflowmodels.FlowRecord, error) {
		return s.loadFlowVersion(ctx, run.Topic, run.FlowVersion)
	})
	if err != nil {
		return s.closeRun(ctx, run, workflowmodels.WorkflowRunStatusDeadLetter, fmt.Errorf("workflow: resume run %d: %w", runID, err))
	}
	// The run continues on the snapshot it was started with (plan T97), never on rows saved since;
	// a version that is no longer stored closes it.
	runtime, err := s.versionRuntime(ctx, liveFlow, run.FlowVersion)
	if err != nil {
		return s.closeRun(ctx, run, workflowmodels.WorkflowRunStatusDeadLetter, fmt.Errorf("workflow: resume run %d: %w", runID, err))
	}
	runtime.runID = runID
	flow, steps := &runtime.flow, runtime.steps
	// Keep the mode the run was created with; runs from before T45 get their transport default.
	ctx, _ = bindRunAuditPolicy(ctx, *flow, run.AuditMode, run.TriggerType)

	// max_concurrent_runs also gates resumed runs (plan T100); an over-limit run was parked back to
	// waiting_retry by admit_run itself (no attempt charged), which callers treat as accepted.
	admission, err := s.admitRun(ctx, runtime)
	if err != nil {
		if errors.Is(err, ErrRunLeaseLost) {
			log.Infof("workflow: run %d is now owned by another worker, skipped", runID)
			return nil
		}
		return err
	}
	if admission != workflowmodels.WorkflowRunAdmitted {
		return fmt.Errorf("%w: %v", ErrRunRetryScheduled, concurrencyLimitError(runtime))
	}
	// DAG-mode runs have no single current_step to resume from — their
	// resume point is re-derived from step_runs[] by executeRunDAG's
	// dagResumeState, not from an index into a linear steps[] slice. Linear
	// runs keep the existing current_step-indexed resume behavior unchanged.
	var state executionState
	if flow.ExecutionMode == workflowmodels.WorkflowExecutionModeDAG {
		state, err = s.resumeStateForDAGRun(run)
	} else {
		state, err = s.resumeStateForRun(run, flow, steps)
	}
	if err != nil {
		return s.closeRun(ctx, run, workflowmodels.WorkflowRunStatusDeadLetter, err)
	}
	return s.executeRun(ctx, runtime, state)
}

// closeRun ends a run that cannot execute again with status and cause, keeping its history. The
// error returned is the cause (or the write error), so the resume report still shows it.
func (s *Service) closeRun(ctx context.Context, run *workflowmodels.WorkflowRunRecord, status string, cause error) error {
	log.Warnf("%v", cause)
	payload, _ := payloadFromAudit(run.OutputPayload)
	if err := s.markRun(ctx, run.Topic, run.ID, s.runUpdate(status, run.CurrentStep, payload, run.StepRuns, run.AttemptCount, cause, 0)); err != nil {
		return err
	}
	return cause
}

// ResumeDueRuns resumes the due runs handled by this package.
// resumeDueRunsConcurrency bounds how many claimed runs resume at once.
const resumeDueRunsConcurrency = 4

func (s *Service) ResumeDueRuns(ctx context.Context, limit int64) (int, error) {
	s = s.withDefaults()
	ctx = workflowContextOrBackground(ctx)
	if limit <= 0 {
		limit = 100
	}

	// Expired approval waits (plan T103) are closed first; the claim below only takes waiting_retry
	// rows, so a run waiting for approval is never resumed by the cron.
	s.expireApprovalWaits(ctx, limit)
	// The claim gives this batch's token the lease of every claimed run (plan T49).
	ctx = contextWithRunLease(ctx, 0, newRunLeaseOwner())
	runIDs, err := s.claimDueRuns(ctx, limit, s.now().Unix())
	if err != nil {
		return 0, err
	}
	workflowDueRetryRuns.Set(float64(len(runIDs)))

	// Claimed runs are independent, so resume a few at a time. Every resume
	// still goes through ResumeRun; errors are kept per slot so the report
	// order matches the claim order regardless of completion order.
	resumeErrs := make([]string, 0)
	failures := make([]string, len(runIDs))
	var group errgroup.Group
	group.SetLimit(resumeDueRunsConcurrency)
	for i, runID := range runIDs {
		group.Go(func() error {
			// One run that panics outside its step handlers (handlers are already recovered in
			// runStep) is reported as that run's failure; it must not take down the whole batch.
			defer func() {
				if recovered := recover(); recovered != nil {
					log.Errorf("workflow: resume run %d panicked: %v\n%s", runID, recovered, debug.Stack())
					failures[i] = fmt.Sprintf("%d: panic: %v", runID, recovered)
				}
			}()
			// A resumed attempt that failed again with attempts left is a normal outcome.
			if err := s.ResumeRun(ctx, runID); err != nil && !isRunRetryScheduled(err) {
				failures[i] = fmt.Sprintf("%d: %v", runID, err)
			}
			return nil
		})
	}
	_ = group.Wait()
	resumed := 0
	for _, failure := range failures {
		if failure == "" {
			resumed++
			continue
		}
		resumeErrs = append(resumeErrs, failure)
	}
	if len(resumeErrs) > 0 {
		log.Errorf("workflow: resume due runs: %s", strings.Join(resumeErrs, "; "))
		return resumed, fmt.Errorf("workflow: resume due runs: %s", strings.Join(resumeErrs, "; "))
	}
	return resumed, nil
}

// ReapStaleRuns reclaims runs orphaned at status=running (worker crash or a
// hung step handler with no attempt ever completing) by flipping them to
// waiting_retry with next_retry_at set to now. It does not resume them
// directly; the existing resume_due_runs cron picks them up on its next
// tick, the same path already used for ordinary retries.
func (s *Service) ReapStaleRuns(ctx context.Context, limit int64, staleAfterSeconds int64) (int, error) {
	s = s.withDefaults()
	ctx = workflowContextOrBackground(ctx)
	if limit <= 0 {
		limit = 100
	}
	if staleAfterSeconds <= 0 {
		staleAfterSeconds = 300
	}

	runIDs, err := s.reapStaleRuns(ctx, limit, staleAfterSeconds)
	if err != nil {
		return 0, err
	}
	return len(runIDs), nil
}

// workflowContextOrBackground keeps workflow entrypoints tolerant of callers
// that omit a context while still preserving any request-scoped values already present.
func workflowContextOrBackground(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	return context.Background()
}

// Run rows are engine-owned: written in the run's own database (the trigger's or caller's env,
// plan T46) with access checks bypassed, never on the default database a bare context falls back to.
func createWorkflowRunRecord(ctx context.Context, record workflowmodels.WorkflowRunRecord) (*orm.InsertResult, error) {
	created, err := modelruntime.NewService().Create(workflowmodels.WorkflowEngineContext(ctx), workflowmodels.WorkflowRunModelName, recordToMap(record))
	if err != nil {
		return nil, err
	}
	return &orm.InsertResult{InsertedID: created["id"], Acknowledged: true}, nil
}

// recordToMap is a local copy of the libs package's JSON-roundtrip record
// converter (see nodes' stringValue comment for why small helpers like this
// are duplicated across the engine/nodes/libs split rather than exported).
func recordToMap(record any) orm.RecordMap {
	values := orm.RecordMap{}
	raw, err := json.Marshal(record)
	if err != nil {
		return values
	}
	_ = json.Unmarshal(raw, &values)
	return values
}

func updateWorkflowRunRecord(ctx context.Context, filter orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
	id, ok := flowInsertedIDAsString(filter["id"])
	if !ok {
		log.Errorf("workflow: update requires id filter")
		return nil, fmt.Errorf("workflow: update requires id filter")
	}
	updated, err := modelruntime.NewService().Write(workflowmodels.WorkflowEngineContext(ctx), workflowmodels.WorkflowRunModelName, []string{id}, values)
	if err != nil {
		return nil, err
	}
	modified := int64(0)
	if updated {
		modified = 1
	}
	return &orm.UpdateResult{Acknowledged: true, MatchedCount: modified, ModifiedCount: modified}, nil
}

func workflowNowUTC() time.Time {
	return time.Now().UTC()
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// executeFlow executes the flow handled by this package.
func (s *Service) executeFlow(ctx context.Context, runtime flowRuntime, input []byte) error {
	_, err := s.executeFlowFrom(ctx, runtime, input, 0)
	return err
}

// executeFlowFrom creates a run of runtime and executes it from step startIndex (0 everywhere but
// replay_run from a step, plan T95). It returns the run id once the row exists (0 before).
func (s *Service) executeFlowFrom(ctx context.Context, runtime flowRuntime, input []byte, startIndex int) (int64, error) {
	// Payload storage for this run: trigger audit_payload mode + workflow audit_redact_keys (T45).
	ctx, _ = bindRunAuditPolicy(ctx, runtime.flow, "", workflowmodels.WorkflowTriggerTypeFromContext(ctx))
	// Caller-bound runs (non-sudo env), unversioned runs (ExecuteSteps; cannot be resumed) and
	// sub-workflow children (their caller waits, plan T103) retry in place; everything else hands
	// retries to resume_due_runs (T48).
	ctx = contextWithRunFlags(ctx, runFlags{retryInPlace: callerBoundContext(ctx) || runtime.flow.Version <= 0 || childRunContext(ctx)})
	ctx = contextWithRunLease(ctx, 0, newRunLeaseOwner())
	if startIndex == 0 {
		// A fresh run never trusts engine-owned keys from its trigger input (plan T103 review): a
		// caller could otherwise claim a verified signature or a decided approval. A replay from a
		// later step starts from a stored step output, which legitimately carries them.
		input = stripReservedPayloadKeys(input)
	}
	runID, err := s.createWorkflowRun(ctx, runtime.flow, input)
	if err != nil {
		return 0, err
	}
	runtime.runID = runID
	lease, _ := runLeaseFromContext(ctx)
	ctx = contextWithRunLease(ctx, runID, lease.owner)

	// Persist the raw input before any step mutates it so operators can compare
	// the original event with the evolving output snapshot after each handler.
	initialUpdate := s.runUpdate(workflowmodels.WorkflowRunStatusRunning, "", input, []workflowmodels.WorkflowStepRun{}, 0, nil, 0)
	initialUpdate["input_payload"] = payloadForAudit(input)
	// max_concurrent_runs gate (plan T100), before the run becomes running.
	admission, err := s.admitRun(ctx, runtime)
	if err != nil {
		return runID, err
	}
	if admission != workflowmodels.WorkflowRunAdmitted {
		return runID, s.rejectNewRun(ctx, runtime, startIndex, initialUpdate)
	}
	if err := s.markRun(ctx, runtime.flow.Topic, runtime.runID, initialUpdate); err != nil {
		return runID, err
	}
	return runID, s.executeRun(ctx, runtime, executionState{
		currentPayload: append([]byte(nil), input...),
		stepRuns:       make([]workflowmodels.WorkflowStepRun, 0, len(runtime.steps)),
		totalAttempts:  0,
		startStepIndex: startIndex,
		startAttempt:   1,
	})
}

// reservedPayloadKeys are the payload keys only the engine and its nodes write.
var reservedPayloadKeys = []string{"_hmac_verified", "_error", "_approval", "_for_each", nodes.DryRunMarkerKey, nodes.DryRunIntendedKey}

// stripReservedPayloadKeys removes reservedPayloadKeys from a JSON object input. Any other input
// (not an object, or holding none of the keys) is returned unchanged; values are kept as raw JSON,
// so numbers keep their precision.
func stripReservedPayloadKeys(input []byte) []byte {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(input, &object); err != nil || object == nil {
		return input
	}
	stripped := false
	for _, key := range reservedPayloadKeys {
		if _, ok := object[key]; ok {
			delete(object, key)
			stripped = true
		}
	}
	if !stripped {
		return input
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return input
	}
	return encoded
}

// executeRun is the hybrid linear/DAG entrypoint used by every execution and
// resume path. execution_mode=="dag" with at least one persisted edge row
// routes to executeRunDAG (graph_schedule.go); everything else — including
// every workflow that predates this feature, since it never sets
// execution_mode or never adds an edge row — takes the unchanged linear
// path. This keeps linear-mode workflows byte-for-byte unaffected.
func (s *Service) executeRun(ctx context.Context, runtime flowRuntime, state executionState) error {
	// One execution = one cancellable, optionally time-boxed context (plan T100).
	ctx, release := withRunControl(ctx, runtime)
	defer release()
	if runtime.flow.ExecutionMode == workflowmodels.WorkflowExecutionModeDAG {
		edges := runtime.edges
		if !runtime.pinned {
			loaded, err := s.loadFlowEdges(ctx, runtime.flow.ID)
			if err != nil {
				return err
			}
			edges = loaded
		}
		if len(edges) > 0 {
			return s.executeRunDAG(ctx, runtime, state, edges)
		}
	}
	return s.executeRunLinear(ctx, runtime, state)
}

// executeRunLinear executes the run handled by this package.
func (s *Service) executeRunLinear(ctx context.Context, runtime flowRuntime, state executionState) error {
	currentPayload := append([]byte(nil), state.currentPayload...)
	stepRuns := append([]workflowmodels.WorkflowStepRun(nil), state.stepRuns...)
	totalAttempts := state.totalAttempts

	for index := state.startStepIndex; index < len(runtime.steps); index++ {
		step := runtime.steps[index]
		stepKey := step.ResolvedKey(index)
		stepName := step.ResolvedName(index)
		startAttempt := 1
		if index == state.startStepIndex && state.startAttempt > 1 {
			// Resume begins at the next unfinished attempt for the persisted step
			// instead of replaying already recorded failures from scratch. Interrupted
			// attempts count (plan T49), so a step that crashes the worker every time ends
			// dead-lettered instead of being retried forever.
			startAttempt = state.startAttempt
		}

		if err := s.markRun(ctx, runtime.flow.Topic, runtime.runID, s.runUpdate(workflowmodels.WorkflowRunStatusRunning, stepKey, currentPayload, nil, totalAttempts, nil, 0)); err != nil {
			return err
		}

		gate := s.gateStep(stepName, stepKey, step, currentPayload)
		if !gate.run {
			stepRuns = append(stepRuns, gate.row)
			if gate.err == nil {
				// Skipped by its condition or branch label: the payload passes through unchanged.
				if err := s.markRun(ctx, runtime.flow.Topic, runtime.runID, s.runUpdate(workflowmodels.WorkflowRunStatusRunning, stepKey, currentPayload, stepRuns, totalAttempts, nil, 0)); err != nil {
					return err
				}
				continue
			}
			// A gate failure (condition error, unknown handler) counts as the step's attempt.
			totalAttempts++
			if step.ContinueOnError {
				if err := s.markRun(ctx, runtime.flow.Topic, runtime.runID, s.runUpdate(workflowmodels.WorkflowRunStatusRunning, stepKey, currentPayload, stepRuns, totalAttempts, gate.err, 0)); err != nil {
					return err
				}
				continue
			}
			if target, handlerPayload, ok := onErrorTarget(ctx, runtime.steps, index, step, stepKey, currentPayload, gate.err); ok {
				if err := s.markRun(ctx, runtime.flow.Topic, runtime.runID, s.runUpdate(workflowmodels.WorkflowRunStatusRunning, step.OnErrorStepKey, handlerPayload, stepRuns, totalAttempts, gate.err, 0)); err != nil {
					return err
				}
				currentPayload, index = handlerPayload, target-1
				continue
			}
			_ = s.markRun(ctx, runtime.flow.Topic, runtime.runID, s.runUpdate(workflowmodels.WorkflowRunStatusFailed, stepKey, currentPayload, stepRuns, totalAttempts, gate.err, 0))
			return gate.err
		}

		attempt := stepAttempt{
			name: stepName, key: stepKey, step: step, gate: gate,
			payload: currentPayload, startAttempt: startAttempt,
			// Persist the attempt before running it (plan T49): a crash mid-attempt still counts.
			beforeAttempt: func(rows []workflowmodels.WorkflowStepRun) error {
				totalAttempts++
				return s.markRun(ctx, runtime.flow.Topic, runtime.runID, s.runUpdate(workflowmodels.WorkflowRunStatusRunning, stepKey, currentPayload, joinStepRuns(stepRuns, rows), totalAttempts, nil, 0))
			},
			// A run retrying in place stays running (and leased): the cron never claims it, so
			// nothing can race the next attempt.
			beforeInPlaceRetry: func(rows []workflowmodels.WorkflowStepRun, attemptErr error, nextRetryAt int64) error {
				return s.markRun(ctx, runtime.flow.Topic, runtime.runID, s.runUpdate(workflowmodels.WorkflowRunStatusRunning, stepKey, currentPayload, joinStepRuns(stepRuns, rows), totalAttempts, attemptErr, nextRetryAt))
			},
		}
		var result stepAttemptResult
		var err error
		if isApprovalStep(ctx, gate) {
			// wait_approval@v1 (plan T103) parks the run instead of calling a handler; a run whose
			// caller is blocked on it fails the step (non-retryable) and follows the normal path.
			unsupported := approvalUnsupported(ctx)
			if unsupported == nil {
				return s.parkForApproval(ctx, runtime, attempt, stepRuns, totalAttempts)
			}
			totalAttempts++
			result = stepAttemptResult{err: unsupported, rows: []workflowmodels.WorkflowStepRun{
				s.failedStepRun(stepName, stepKey, step, gate.handlerKey, gate.handlerVersion, 1, unsupported, 0, currentPayload),
			}}
		} else {
			result, err = s.attemptStep(ctx, runtime.flow, attempt)
		}
		stepRuns = joinStepRuns(stepRuns, result.rows)
		if err != nil {
			return err
		}

		switch {
		case result.err == nil:
			// Each step receives the previous payload and replaces it with its
			// own output. The workflow therefore behaves like a pipeline where
			// later handlers always see the latest successful transformation.
			currentPayload = result.output
			if err := s.markRun(ctx, runtime.flow.Topic, runtime.runID, s.runUpdate(workflowmodels.WorkflowRunStatusRunning, stepKey, currentPayload, stepRuns, totalAttempts, nil, 0)); err != nil {
				return err
			}
		case result.nextRetryAt > 0:
			// Plan T48: never retry in-process. The run is persisted as waiting_retry and the
			// cron claim (claim_due_retries) owns the next attempt; sleeping and retrying here
			// raced that claim and ran the step twice (duplicate HTTP post or CRUD write).
			if err := s.markRun(ctx, runtime.flow.Topic, runtime.runID, s.runUpdate(workflowmodels.WorkflowRunStatusRetryWaiting, stepKey, currentPayload, stepRuns, totalAttempts, result.err, result.nextRetryAt)); err != nil {
				return err
			}
			return fmt.Errorf("%w (run %d, step %s, next attempt at %d): %v", ErrRunRetryScheduled, runtime.runID, stepKey, result.nextRetryAt, result.err)
		case step.ContinueOnError:
			// Continue-on-error keeps the failed attempt in history but leaves
			// the payload unchanged, so downstream steps can decide whether the
			// last successful snapshot is still good enough to continue.
			if err := s.markRun(ctx, runtime.flow.Topic, runtime.runID, s.runUpdate(workflowmodels.WorkflowRunStatusRunning, stepKey, currentPayload, stepRuns, totalAttempts, result.err, 0)); err != nil {
				return err
			}
		default:
			if target, handlerPayload, ok := onErrorTarget(ctx, runtime.steps, index, step, stepKey, currentPayload, result.err); ok {
				// The failed step keeps its failed row; the run goes on at the error handler step
				// (plan T103) and ends succeeded if that path succeeds. current_step is already the
				// target, so a resume after a crash here starts at the handler, never re-runs the
				// failed step.
				if err := s.markRun(ctx, runtime.flow.Topic, runtime.runID, s.runUpdate(workflowmodels.WorkflowRunStatusRunning, step.OnErrorStepKey, handlerPayload, stepRuns, totalAttempts, result.err, 0)); err != nil {
					return err
				}
				currentPayload, index = handlerPayload, target-1
				continue
			}
			if err := s.markRun(ctx, runtime.flow.Topic, runtime.runID, s.runUpdate(exhaustedRunStatus(ctx), stepKey, currentPayload, stepRuns, totalAttempts, result.err, 0)); err != nil {
				return err
			}
			return result.err
		}
	}
	if err := s.markRun(ctx, runtime.flow.Topic, runtime.runID, s.runUpdate(workflowmodels.WorkflowRunStatusSucceeded, "", currentPayload, stepRuns, totalAttempts, nil, 0)); err != nil {
		return err
	}
	publishWorkflowOutput(ctx, currentPayload)
	return nil
}

// onErrorTarget resolves a terminally failed linear step's on_error_step_key (plan T103): the index
// of the named step and the payload it runs with (the failed step's input plus
// `_error {step_key, message}`). Only a LATER step qualifies, so a run can never loop; validate and
// publish refuse other targets, and one that slips through (an unpublished draft) just fails the
// run as before. A stopped run (cancel, max_run_seconds) never jumps.
func onErrorTarget(ctx context.Context, steps []workflowmodels.Step, index int, step workflowmodels.Step, stepKey string, payload []byte, cause error) (int, []byte, bool) {
	targetKey := strings.TrimSpace(step.OnErrorStepKey)
	if targetKey == "" || cause == nil || ctx.Err() != nil {
		return 0, nil, false
	}
	for target := index + 1; target < len(steps); target++ {
		if steps[target].ResolvedKey(target) != targetKey {
			continue
		}
		object := payloadForAudit(payload)
		object["_error"] = map[string]any{"step_key": stepKey, "message": cause.Error()}
		encoded, err := json.Marshal(object)
		if err != nil {
			return 0, nil, false
		}
		return target, encoded, true
	}
	log.Warnf("workflow: step %s on_error_step_key %q is not a later step; the run fails", stepKey, targetKey)
	return 0, nil, false
}

// synchronousRun reports whether a caller is waiting on this run's output (a reply sink is bound:
// the tcp trigger). Such a run is answered once, before its reply deadline (plan T50).
func synchronousRun(ctx context.Context) bool {
	return workflowmodels.WorkflowOutputSinkFromContext(ctx) != nil
}

// stepMaxAttempts is the step's attempt budget; a synchronous run gets exactly one attempt so it is
// never parked as waiting_retry and resumed after the caller has given up (a card terminal
// reverses on timeout; a late approval would then be applied anyway).
func stepMaxAttempts(ctx context.Context, step workflowmodels.Step) int {
	// A dry run also gets one attempt (plan T101): it is a preview, never retried or resumed.
	if synchronousRun(ctx) || workflowmodels.WorkflowDryRunFromContext(ctx) {
		return 1
	}
	if attempts := step.ResolvedMaxAttempts(); attempts > 0 {
		return attempts
	}
	return 1
}

// exhaustedRunStatus is the final status of a run whose step used up its attempts: dead_letter
// (an operator may replay it), or failed for a synchronous run, which must never be replayed.
func exhaustedRunStatus(ctx context.Context) string {
	// A sub-workflow child (plan T103) is retried by its calling step, never replayed on its own.
	if synchronousRun(ctx) || childRunContext(ctx) {
		return workflowmodels.WorkflowRunStatusFailed
	}
	return workflowmodels.WorkflowRunStatusDeadLetter
}

// synchronousTriggerType reports run trigger types whose caller waited for a reply.
func synchronousTriggerType(triggerType string) bool {
	return strings.EqualFold(strings.TrimSpace(triggerType), workflowmodels.WorkflowTriggerTypeTCP)
}

// publishWorkflowOutput copies the final payload of a successful run into the output sink bound on
// ctx (synchronous-reply triggers such as tcp). Without a sink it does nothing.
func publishWorkflowOutput(ctx context.Context, payload []byte) {
	if sink := workflowmodels.WorkflowOutputSinkFromContext(ctx); sink != nil {
		sink.Set(payload)
	}
	// A sub-workflow child hands its final payload back to the calling step (plan T103).
	if frame, ok := workflowCallFrameFromContext(ctx); ok {
		frame.output.Set(payload)
	}
}

// createWorkflowRun creates the workflow run handled by this package.
func (s *Service) createWorkflowRun(ctx context.Context, flow workflowmodels.FlowRecord, input []byte) (int64, error) {
	workflowRequestID := workflowmodels.WorkflowRequestIDFromContext(ctx)
	// A pending run is never resumed, so both payloads are stored through the audit policy;
	// the first markRun(running) writes the complete resume checkpoint.
	audit := auditPolicyFromContext(ctx)
	// A dry run (plan T101) is caller-bound even when started as sudo, so ResumeRun never runs it.
	dryRun := workflowmodels.WorkflowDryRunFromContext(ctx)
	inserted, err := s.createRun(ctx, workflowmodels.WorkflowRunRecord{
		// BaseModel.Active must be set explicitly: the struct is JSON-marshalled
		// without `omitempty` before insert, so an unset (zero-value) Active would
		// serialize as an explicit "active": false and permanently hide every run
		// from the default active-only list/search filters.
		BaseModel:     orm.BaseModel{Active: true},
		Name:          workflowRequestID,
		WorkflowID:    &flow.ID,
		TriggerType:   workflowmodels.WorkflowTriggerTypeFromContext(ctx),
		Topic:         strings.TrimSpace(flow.Topic),
		FlowVersion:   flow.Version,
		Status:        workflowmodels.WorkflowRunStatusPending,
		CurrentStep:   "",
		InputPayload:  audit.apply(payloadForAudit(input)),
		OutputPayload: audit.apply(payloadForAudit(input)),
		StepRuns:      []workflowmodels.WorkflowStepRun{},
		AttemptCount:  0,
		LastError:     "",
		NextRetryAt:   0,
		AuditMode:     audit.mode,
		ParentRunID:   replayParentFromContext(ctx),
		// The creating execution holds the lease from the start (plan T49).
		LeaseOwner: createLeaseOwner(ctx),
		LeaseUntil: s.now().Unix() + runLeaseSeconds,
		// A sub-workflow child (plan T103) is caller-bound too: its caller waits for it.
		CallerBound:  callerBoundContext(ctx) || dryRun || childRunContext(ctx),
		TriggerChain: modelTriggerChainFromContext(ctx),
		DryRun:       dryRun,
		// Who started it (plan T103 review): an approval continues a caller-bound run under this
		// user, and this user cannot approve it.
		StartedByUID: runStarterUID(ctx),
	})
	if err != nil {
		log.Errorf("workflow: create workflow run: %v", err)
		return 0, fmt.Errorf("workflow: create workflow run: %w", err)
	}

	runID, err := insertedIDAsInt64(inserted)
	if err != nil {
		log.Errorf("workflow: create workflow run: %v", err)
		return 0, fmt.Errorf("workflow: create workflow run: %w", err)
	}
	return runID, nil
}

// runStarterUID is the user of ctx's environment (0 without one, or for a system trigger).
func runStarterUID(ctx context.Context) int64 {
	if env := orm.EnvironmentFromContext(ctx); env != nil {
		return env.UserID()
	}
	return 0
}

func createLeaseOwner(ctx context.Context) string {
	lease, _ := runLeaseFromContext(ctx)
	return lease.owner
}

// markRun updates the run handled by this package.
func (s *Service) markRun(ctx context.Context, topic string, runID int64, values orm.RecordMap) error {
	// A run stopped by cancel or max_run_seconds (plan T100) still writes its final status: on a
	// context detached from the cancelled one (values such as the lease and audit policy are kept).
	stop := runStopCause(ctx)
	if stop != nil {
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), runStopWriteTimeout)
		defer cancel()
		ctx = writeCtx
	}
	// Fencing (plan T49): only the lease holder writes; a run leaving running releases the lease.
	// The renewal also reports a cancel_run flag set from any instance.
	if err := s.checkRunLease(ctx, runID); err != nil {
		if !errors.Is(err, ErrRunCancelled) {
			log.Errorf("%v", err)
			return err
		}
		if stop == nil {
			stop = err
		}
	}
	stopped := false
	if stop != nil {
		values, stopped = stoppedRunValues(values, stop)
	}
	// Every run write is stored through the run's audit policy (T45); the in-memory payloads the
	// engine keeps executing with are not touched. Applied after the stop override, so a stopped
	// run's output_payload goes through the policy like any final status.
	values = auditRunValues(ctx, values)
	if status, _ := values["status"].(string); status != workflowmodels.WorkflowRunStatusRunning && status != workflowmodels.WorkflowRunStatusPending {
		values["lease_owner"] = ""
		values["lease_until"] = int64(0)
	}
	if _, err := s.updateRun(ctx, orm.RecordMap{"id": runID}, values); err != nil {
		log.Errorf("workflow: update workflow run %d: %v", runID, err)
		return fmt.Errorf("workflow: update workflow run %d: %w", runID, err)
	}
	stepKey, _ := values["current_step"].(string)
	status, _ := values["status"].(string)
	// Counted after the write succeeded: a transition that was fenced or failed did not happen.
	countRunTransition(ctx, status)
	publishWorkflowRunStepEvent(ctx, topic, runID, stepKey, status)
	if stopped {
		// The run is final now: the caller must stop instead of writing more.
		return fmt.Errorf("run %d: %w", runID, stop)
	}
	return nil
}

// runUpdate runs the update handled by this package.
func (s *Service) runUpdate(status string, currentStep string, payload []byte, stepRuns []workflowmodels.WorkflowStepRun, attempts int, runErr error, nextRetryAt int64) orm.RecordMap {
	// Keep every workflow status write on one code path so retries and resume use the same payload shape.
	update := orm.RecordMap{
		"status":         status,
		"current_step":   currentStep,
		"output_payload": payloadForAudit(payload),
		"attempt_count":  attempts,
		"next_retry_at":  nextRetryAt,
	}
	if stepRuns != nil {
		update["step_runs"] = stepRuns
	}
	if runErr != nil {
		update["last_error"] = runErr.Error()
	} else {
		update["last_error"] = ""
	}
	return update
}

// stepGate is what both engines decide before a step's first attempt (plan T52): the
// extra_params.condition gate, the branch_label gate and the handler lookup.
type stepGate struct {
	// run is false when the step does not run: row is then its audit row, skipped (err nil) or
	// failed (err set; a failed gate counts as the step's attempt).
	run            bool
	row            workflowmodels.WorkflowStepRun
	err            error
	handler        ActionHandler
	handlerKey     string
	handlerVersion int
}

// gateStep runs the pre-attempt checks shared by the linear and DAG engines against payload.
func (s *Service) gateStep(stepName, stepKey string, step workflowmodels.Step, payload []byte) stepGate {
	shouldRun, conditionErr := shouldRunWorkflowStep(step, payload)
	if conditionErr != nil {
		observeStep(step, metricStatusFailed, 0)
		return stepGate{row: s.failedStepRun(stepName, stepKey, step, "condition", 1, 1, conditionErr, 0, payload), err: conditionErr}
	}
	if !shouldRun {
		observeStep(step, metricStatusSkipped, 0)
		return stepGate{row: s.skippedStepRun(stepName, stepKey, step, payload)}
	}
	if branchLabel := strings.TrimSpace(step.BranchLabel); branchLabel != "" {
		if !conditionBranchMatches(branchLabel, strings.TrimSpace(step.ConditionStepKey), payload) {
			observeStep(step, metricStatusSkipped, 0)
			return stepGate{row: s.skippedStepRun(stepName, stepKey, step, payload)}
		}
	}
	handlerKey, handlerVersion := resolveImplementation(step)
	handler, ok := s.lookupAction(handlerKey, handlerVersion)
	if !ok {
		observeStep(step, metricStatusFailed, 0)
		err := fmt.Errorf("%w: %s@v%d for %s@v%d", ErrActionNotSupported, handlerKey, handlerVersion, step.Action, step.ResolvedActionVersion())
		return stepGate{row: s.failedStepRun(stepName, stepKey, step, handlerKey, handlerVersion, 1, err, 0, payload), err: err}
	}
	return stepGate{run: true, handler: handler, handlerKey: handlerKey, handlerVersion: handlerVersion}
}

// stepAttempt is one step handed to attemptStep after its gate let it run.
type stepAttempt struct {
	name, key    string
	step         workflowmodels.Step
	gate         stepGate
	payload      []byte
	startAttempt int
	// beforeAttempt runs before every attempt with this step's rows recorded so far (the linear
	// engine persists attempt_count there, plan T49). An error stops the run.
	beforeAttempt func(rows []workflowmodels.WorkflowStepRun) error
	// beforeInPlaceRetry runs after a failed attempt of a run that retries in place, before the
	// cooldown. An error stops the run.
	beforeInPlaceRetry func(rows []workflowmodels.WorkflowStepRun, attemptErr error, nextRetryAt int64) error
}

// stepAttemptResult is how attemptStep left the step.
type stepAttemptResult struct {
	output   []byte                           // the step's output when err is nil
	rows     []workflowmodels.WorkflowStepRun // one audit row per attempt (plus one for an interrupted attempt)
	attempts int                              // attempts started by this call
	err      error                            // nil: the step succeeded
	// nextRetryAt > 0 (with err): attempts are left and the next one belongs to resume_due_runs
	// (plan T48). Zero with err: the step is exhausted (or stopped by a failed cooldown).
	nextRetryAt int64
}

// attemptStep is the single attempt loop of both engines (plan T52). It starts at
// a.startAttempt; a step with no attempts left (they were used by interrupted executions) fails
// without running, so a step that crashes the worker is not retried forever. A failed attempt
// with attempts left is retried here only when the run retries in place (caller-bound or
// unversioned runs, T48); otherwise it is handed back with nextRetryAt for the caller to persist.
// The returned error is fatal for the run (a hook failed, e.g. the lease was lost); step failures
// are reported in the result.
func (s *Service) attemptStep(ctx context.Context, flow workflowmodels.FlowRecord, a stepAttempt) (stepAttemptResult, error) {
	maxAttempts := stepMaxAttempts(ctx, a.step)
	var result stepAttemptResult
	newRow := func(attempt int, status string, startedAt, finishedAt time.Time, output []byte, nextRetryAt int64, err error) workflowmodels.WorkflowStepRun {
		return newStepRun(stepRunSpec{
			name: a.name, key: a.key, step: a.step,
			implementationKey: a.gate.handlerKey, implementationVersion: a.gate.handlerVersion,
			attempt: attempt, status: status,
			startedAt: startedAt, finishedAt: finishedAt,
			input: a.payload, output: output,
			nextRetryAt: nextRetryAt, err: err,
		})
	}
	if a.startAttempt > maxAttempts {
		// The last attempt was counted (attempt_count) but never recorded: close it with a terminal
		// row so the history shows why the step failed and a DAG resume sees it as decided.
		result.err = fmt.Errorf("workflow: step %s has used all %d attempts (an attempt was interrupted)", a.key, maxAttempts)
		now := s.now()
		result.rows = append(result.rows, newRow(a.startAttempt-1, workflowmodels.WorkflowRunStatusFailed, now, now, a.payload, 0, result.err))
		return result, nil
	}
	for attempt := max(a.startAttempt, 1); attempt <= maxAttempts; attempt++ {
		if a.beforeAttempt != nil {
			if err := a.beforeAttempt(result.rows); err != nil {
				return result, err
			}
		}
		result.attempts++
		startedAt := s.now()
		// A DAG sibling that failed under fail_workflow cancels the layer: record the cancellation
		// as this attempt instead of starting a doomed call.
		err := ctx.Err()
		var output []byte
		if err == nil {
			output, err = s.runStep(ctx, a.gate.handler, flow, a.key, a.step, a.payload)
		}
		finishedAt := s.now()
		observeStep(a.step, stepOutcomeStatus(err), finishedAt.Sub(startedAt))
		if err == nil {
			result.output = append([]byte(nil), output...)
			result.err = nil
			result.rows = append(result.rows, newRow(attempt, workflowmodels.WorkflowRunStatusSucceeded, startedAt, finishedAt, result.output, 0, nil))
			return result, nil
		}

		result.err = err
		nextRetryAt := int64(0)
		if attempt < maxAttempts {
			// The next attempt belongs to ResumeDueRuns once next_retry_at is due (or to this
			// execution after the cooldown, for a run that retries in place).
			nextRetryAt = s.nextRetryAt(a.step)
		}
		// A deterministic failure (plan T103) ends the step now, exactly like an exhausted one.
		final := attempt >= maxAttempts || errors.Is(err, workflowmodels.ErrWorkflowStepNonRetryable)
		if final {
			nextRetryAt = 0
		}
		result.rows = append(result.rows, newRow(attempt, workflowmodels.WorkflowRunStatusFailed, startedAt, finishedAt, a.payload, nextRetryAt, err))
		if final {
			return result, nil
		}
		if !runFlagsFromContext(ctx).retryInPlace {
			result.nextRetryAt = nextRetryAt
			return result, nil
		}
		// Caller-bound run: retry under the caller's identity in this execution; the system cron
		// must not replay it as sudo.
		if a.beforeInPlaceRetry != nil {
			if err := a.beforeInPlaceRetry(result.rows, err, nextRetryAt); err != nil {
				return result, err
			}
		}
		if err := s.sleepHoldingLease(ctx, min(a.step.ResolvedBackoff(), maxInPlaceRetryBackoff)); err != nil {
			// Cancelled or lease lost during the cooldown: the step ends with that error.
			result.err = err
			return result, nil
		}
	}
	return result, nil
}

// stepOutcomeStatus is the metric status of one executed attempt.
func stepOutcomeStatus(err error) string {
	if err == nil {
		return metricStatusSuccess
	}
	return metricStatusFailed
}

// joinStepRuns returns history followed by rows in a new slice (never aliasing history).
func joinStepRuns(history, rows []workflowmodels.WorkflowStepRun) []workflowmodels.WorkflowStepRun {
	joined := make([]workflowmodels.WorkflowStepRun, 0, len(history)+len(rows))
	joined = append(joined, history...)
	return append(joined, rows...)
}

// stepIdempotencyKey is the key a step sends to systems that deduplicate (plan T56):
// `<run name>:<step key>`, stable across every attempt and resume of the step in one run. A
// sub-workflow child uses `<parent step key>:<label>` instead of its run name (plan T103 review):
// every retry of the calling step starts a new child run, and its steps must keep their keys.
func stepIdempotencyKey(ctx context.Context, stepKey string) string {
	runName := childIdempotencyBase(ctx)
	if runName == "" {
		runName = workflowmodels.WorkflowRequestIDFromContext(ctx)
	}
	stepKey = strings.TrimSpace(stepKey)
	if runName == "" || stepKey == "" {
		return ""
	}
	return runName + ":" + stepKey
}

// runStep runs one attempt of a step's handler.
func (s *Service) runStep(ctx context.Context, handler ActionHandler, flow workflowmodels.FlowRecord, stepKey string, step workflowmodels.Step, payload []byte) ([]byte, error) {
	topic := flow.Topic
	// Keep the run lease alive while the handler works; a lost lease cancels the step (T49).
	stepCtx, cancelStep := context.WithCancelCause(ctx)
	defer cancelStep(nil)
	stopHeartbeat := s.startRunLeaseHeartbeat(stepCtx, cancelStep)
	defer stopHeartbeat()
	ctx = stepCtx
	// Credential lookups and outbound rows are scoped to this workflow and its company (plan T41).
	ctx = workflowmodels.ContextWithWorkflowScope(ctx, flow.ID, flow.CompanyID)
	idempotencyKey := stepIdempotencyKey(ctx, stepKey)
	ctx = workflowmodels.ContextWithWorkflowStepIdempotencyKey(ctx, idempotencyKey)
	// Resolve {{ field }} and #field expressions in top-level string params against the current
	// payload before the handler runs, so all string params act as universal expression fields.
	authored := step.Params
	if bindings := payloadForAudit(payload); len(bindings) > 0 {
		step.Params = nodes.ResolveStringParams(step.Params, bindings)
	}
	// A dry run (plan T101) stubs side-effect steps before any credential is resolved.
	if workflowmodels.WorkflowDryRunFromContext(ctx) {
		code, version := resolveImplementation(step)
		if output, stubbed, err := nodes.DryRunStepOutput(code, version, stepKey, step.Params, libs.AuthoredCredentialRefs(authored), payload); stubbed || err != nil {
			return output, err
		}
	}
	// Resolve @cred:/@syscred: refs from the authored params only, never from interpolated
	// output: a payload value like "@syscred:x" must reach the handler as literal text (WF-S-1).
	resolved, err := libs.ResolveCredentialRefs(ctx, authored, step.Params)
	if err != nil {
		return nil, err
	}
	step.Params = resolved
	req := ActionRequest{
		Topic:          topic,
		Step:           step,
		Payload:        append([]byte(nil), payload...),
		CredentialRefs: libs.AuthoredCredentialRefs(authored),
		IdempotencyKey: idempotencyKey,
	}
	if timeout := step.ResolvedTimeout(); timeout > 0 {
		timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return callActionHandler(timeoutCtx, handler, stepKey, req)
	}
	return callActionHandler(ctx, handler, stepKey, req)
}

// callActionHandler calls handler and turns a panic into an ErrStepPanicked step error (plan
// T52), so the attempt is recorded and retried like any failure instead of killing the worker.
func callActionHandler(ctx context.Context, handler ActionHandler, stepKey string, req ActionRequest) (output []byte, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Errorf("workflow: step %s handler panicked: %v\n%s", stepKey, recovered, debug.Stack())
			output = nil
			err = fmt.Errorf("%w: step %s: %v", ErrStepPanicked, stepKey, recovered)
		}
	}()
	return handler(ctx, req)
}

// stepRunSpec is what differs between step_runs[] rows; everything else is
// derived from the step so the linear and DAG engines emit identically shaped rows.
type stepRunSpec struct {
	name, key             string
	step                  workflowmodels.Step
	implementationKey     string
	implementationVersion int
	attempt               int
	status                string
	startedAt, finishedAt time.Time
	input, output         []byte
	nextRetryAt           int64
	err                   error
}

// newStepRun is the single constructor for step_runs[] audit rows.
func newStepRun(spec stepRunSpec) workflowmodels.WorkflowStepRun {
	run := workflowmodels.WorkflowStepRun{
		StepName:              spec.name,
		StepKey:               spec.key,
		StepType:              spec.step.ResolvedType(),
		Action:                strings.TrimSpace(spec.step.Action),
		ActionVersion:         spec.step.ResolvedActionVersion(),
		ImplementationKey:     spec.implementationKey,
		ImplementationVersion: spec.implementationVersion,
		Attempt:               spec.attempt,
		Status:                spec.status,
		StartedAt:             spec.startedAt.Unix(),
		FinishedAt:            spec.finishedAt.Unix(),
		DurationMs:            spec.finishedAt.Sub(spec.startedAt).Milliseconds(),
		ContinueOnError:       spec.step.ContinueOnError,
		NextRetryAt:           spec.nextRetryAt,
		InputSnapshot:         payloadForAudit(spec.input),
		OutputSnapshot:        payloadForAudit(spec.output),
	}
	if spec.err != nil {
		run.Error = spec.err.Error()
	}
	return run
}

func (s *Service) failedStepRun(stepName, stepKey string, step workflowmodels.Step, implementationKey string, implementationVersion int, attempt int, err error, duration time.Duration, payload []byte) workflowmodels.WorkflowStepRun {
	now := s.now()
	return newStepRun(stepRunSpec{
		name: stepName, key: stepKey, step: step,
		implementationKey: implementationKey, implementationVersion: implementationVersion,
		attempt: attempt, status: workflowmodels.WorkflowRunStatusFailed,
		startedAt: now, finishedAt: now.Add(duration),
		input: payload, output: payload, err: err,
	})
}

func (s *Service) skippedStepRun(stepName, stepKey string, step workflowmodels.Step, payload []byte) workflowmodels.WorkflowStepRun {
	now := s.now()
	return newStepRun(stepRunSpec{
		name: stepName, key: stepKey, step: step,
		implementationKey: "condition", implementationVersion: 1,
		status:    workflowmodels.WorkflowRunStatusSkipped,
		startedAt: now, finishedAt: now,
		input: payload, output: payload,
	})
}

// resumeStateForRun resumes the state for run handled by this package.
// steps are the flow's runtime steps ResumeRun already loaded in the run's database.
func (s *Service) resumeStateForRun(run *workflowmodels.WorkflowRunRecord, flow *workflowmodels.FlowRecord, steps []workflowmodels.Step) (executionState, error) {
	if run == nil {
		log.Errorf("workflow: resume workflow run requires record")
		return executionState{}, fmt.Errorf("workflow: resume workflow run requires record")
	}
	if flow == nil {
		log.Errorf("workflow: resume workflow run requires flow")
		return executionState{}, fmt.Errorf("workflow: resume workflow run requires flow")
	}
	if strings.TrimSpace(run.CurrentStep) == "" {
		log.Errorf("workflow: workflow run %d has no current_step to resume", run.ID)
		return executionState{}, fmt.Errorf("workflow: workflow run %d has no current_step to resume", run.ID)
	}

	startIndex := -1
	for index, step := range steps {
		if step.ResolvedKey(index) == strings.TrimSpace(run.CurrentStep) {
			startIndex = index
			break
		}
	}
	if startIndex < 0 {
		log.Errorf("workflow: workflow run %d current_step %q does not exist in flow v%d", run.ID, run.CurrentStep, flow.Version)
		return executionState{}, fmt.Errorf("workflow: workflow run %d current_step %q does not exist in flow v%d", run.ID, run.CurrentStep, flow.Version)
	}

	stepAttemptCount := 0
	recordedAttempts := 0
	for _, stepRun := range run.StepRuns {
		if strings.TrimSpace(stepRun.StepKey) == strings.TrimSpace(run.CurrentStep) {
			stepAttemptCount++
		}
		if stepRun.Status != workflowmodels.WorkflowRunStatusSkipped {
			recordedAttempts++
		}
	}
	// attempt_count is persisted before every attempt (plan T49), so attempts with no step_runs
	// row were interrupted (worker crash, lost lease) on the current step and still count.
	if interrupted := run.AttemptCount - recordedAttempts; interrupted > 0 {
		stepAttemptCount += interrupted
	}

	// Resume from the persisted output payload, not the original input payload.
	// That lets successful upstream steps stay replay-safe while only the
	// unfinished step (or its remaining retries) is re-executed.
	currentPayload, err := payloadFromAudit(run.OutputPayload)
	if err != nil {
		log.Errorf("workflow: workflow run %d output_payload: %v", run.ID, err)
		return executionState{}, fmt.Errorf("workflow: workflow run %d output_payload: %w", run.ID, err)
	}
	return executionState{
		currentPayload: currentPayload,
		stepRuns:       append([]workflowmodels.WorkflowStepRun(nil), run.StepRuns...),
		totalAttempts:  run.AttemptCount,
		startStepIndex: startIndex,
		startAttempt:   stepAttemptCount + 1,
	}, nil
}

// resolveImplementation resolves the implementation handled by this package.
func resolveImplementation(step workflowmodels.Step) (string, int) {
	if entry, ok := workflowmodels.ResolveBuiltinWorkflowAction(strings.TrimSpace(step.Action), step.ResolvedActionVersion()); ok {
		return entry.HandlerKey, entry.HandlerVersion
	}
	return strings.TrimSpace(step.Action), step.ResolvedActionVersion()
}

// payloadForAudit converts raw workflow payload bytes into the audit shape stored on workflow runs.
// It prefers structured JSON when possible and falls back to a raw string so operators can inspect non-JSON input safely.
func payloadForAudit(payload []byte) map[string]any {
	if len(payload) == 0 {
		return map[string]any{}
	}

	var object map[string]any
	if err := json.Unmarshal(payload, &object); err == nil && object != nil {
		return object
	}
	return map[string]any{"raw": string(payload)}
}

// payloadFromAudit reconstructs workflow payload bytes from the persisted audit representation.
// It accepts both structured JSON payloads and raw-string fallbacks so resumed runs can rebuild the last known output.
func payloadFromAudit(payload map[string]any) ([]byte, error) {
	if len(payload) == 0 {
		return []byte("{}"), nil
	}
	if len(payload) == 1 {
		if raw, ok := payload["raw"].(string); ok {
			return []byte(raw), nil
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

// flowInsertedIDAsString normalizes different inserted-id shapes into the string form expected by workflow updates.
// It accepts driver-specific numeric and string values so create and update paths can share the same filter builder.
func flowInsertedIDAsString(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, typed != ""
	case int64:
		return fmt.Sprint(typed), true
	case int:
		return fmt.Sprint(typed), true
	default:
		return "", false
	}
}

// insertedIDAsInt64 converts ORM insert results into a stable int64 workflow-run identifier.
// It validates the insert acknowledgement path first so later workflow state writes do not run against an unknown record id.
func insertedIDAsInt64(result *orm.InsertResult) (int64, error) {
	if result == nil {
		log.Errorf("missing insert result")
		return 0, fmt.Errorf("missing insert result")
	}

	switch typed := result.InsertedID.(type) {
	case int64:
		return typed, nil
	case int:
		return int64(typed), nil
	case uint64:
		return int64(typed), nil
	case float64:
		return int64(typed), nil
	default:
		log.Errorf("unsupported inserted id type %T", result.InsertedID)
		return 0, fmt.Errorf("unsupported inserted id type %T", result.InsertedID)
	}
}

// int64FromString parses a trimmed decimal string into an int64 and reports whether the conversion succeeded.
// Callers use the boolean result to reject malformed ids without conflating them with a legitimate zero value.
func int64FromString(raw string) (int64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	value, err := json.Number(raw).Int64()
	if err != nil {
		return 0, false
	}
	return value, true
}

// LogFlowExecutionResult logs the flow execution result handled by this package.
func LogFlowExecutionResult(topic string, err error) {
	if err != nil {
		log.Errorf("workflow execution failed: topic=%s err=%v", topic, err)
		return
	}
	log.Infof("workflow execution succeeded: topic=%s", topic)
}
