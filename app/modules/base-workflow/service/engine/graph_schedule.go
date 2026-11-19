package engine

// File overview:
// - Responsibility: DAG-mode execution scheduling. Re-validates the workflow's persisted
//   `base_workflow_edge` rows with Kahn's algorithm (never trusts save-time validation as
//   final — a direct RPC write can bypass the canvas save path) and runs each frontier layer
//   through the existing per-step retry/audit logic via a bounded `errgroup`.
// - Key symbols: `executeRunDAG`, `loadWorkflowEdges`.
// - Design note (payload model): the module's execution model is one shared JSON payload
//   mutated step-by-step (see `workflow.md`), not per-branch item arrays. DAG mode keeps that
//   convention: every step in a layer reads the same payload snapshot from before the layer
//   started, and successful outputs are merged into the shared payload (by ascending step_key,
//   so merge order is deterministic) before the next layer starts. This is a explicit
//   consequence of the plan's payload model, not detailed in the plan's own scheduling
//   sketch — call this out for product/architecture review before it ships.

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"sort"
	"strings"

	"git.thebase.vn/base_hub/internal/modelruntime"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
	"golang.org/x/sync/errgroup"
)

// dagPendingStep is a step that passed its gate and is dispatched in the current layer.
type dagPendingStep struct {
	attempt stepAttempt
}

// dagStepOutcome carries one step's result back from its errgroup goroutine
// to the layer-merge loop, which runs single-threaded so step_runs[]
// ordering and shared-payload merges stay deterministic.
type dagStepOutcome struct {
	stepKey string
	result  stepAttemptResult
}

// executeRunDAG re-validates the edge graph, then executes each Kahn's
// frontier layer with a bounded-concurrency errgroup. Every step goes through the
// same gate (condition, branch_label, handler) and attempt loop as linear mode
// (gateStep/attemptStep, plan T52), so step_runs[] rows, retries and attempt
// counting match. Retries of runs that are not retried in place are handed to
// resume_due_runs (T48): once the layer finishes, the run is persisted as
// waiting_retry and the resume re-dispatches only the undecided steps.
// dag_failure_policy=fail_branch (default) skips only a failed step's own
// dependents and lets the other branches finish; the run then ends dead_letter
// (failed for a synchronous run) unless every failed step has an outgoing
// "failure" edge, i.e. an error handler. fail_workflow aborts the remaining
// layers immediately and ends the run failed.
func (s *Service) executeRunDAG(ctx context.Context, runtime flowRuntime, state executionState, edges []workflowmodels.WorkflowGraphEdge) error {
	layers, err := workflowmodels.ValidateWorkflowGraph(edges)
	if err != nil {
		_ = s.markRun(ctx, runtime.flow.Topic, runtime.runID, s.runUpdate(workflowmodels.WorkflowRunStatusFailed, "", state.currentPayload, state.stepRuns, state.totalAttempts, err, 0))
		log.Errorf("workflow: dag execution start validation failed for workflow %s@v%d: %v", runtime.flow.Topic, runtime.flow.Version, err)
		return fmt.Errorf("workflow: dag execution start validation failed: %w", err)
	}
	layers = dagSeedEdgelessSteps(layers, runtime.steps)

	stepsByKey := dagStepsByKey(runtime.steps)
	namesByKey := dagStepNamesByKey(runtime.steps)
	currentPayload := append([]byte(nil), state.currentPayload...)
	stepRuns := append([]workflowmodels.WorkflowStepRun(nil), state.stepRuns...)
	totalAttempts := state.totalAttempts

	status, retryAttempts := dagResumeState(stepRuns)
	for key, interrupted := range state.dagInterrupted {
		if _, decided := status[key]; !decided {
			retryAttempts[key] += interrupted
		}
	}
	incomingByTarget := dagIncomingEdgesByTarget(edges)
	failWorkflowPolicy := runtime.flow.DagFailurePolicy == workflowmodels.WorkflowDagFailurePolicyFailWorkflow
	maxParallel := runtime.flow.MaxParallelSteps
	if maxParallel <= 0 {
		maxParallel = workflowmodels.DefaultMaxParallelSteps
	}

	branchFailed := false
	for _, layer := range layers {
		undecided := dagUndecidedStepKeys(layer, status)
		if len(undecided) == 0 {
			continue
		}
		if branchFailed && failWorkflowPolicy {
			for _, key := range undecided {
				stepRuns = append(stepRuns, s.skippedStepRun(namesByKey[key], key, stepsByKey[key], currentPayload))
				status[key] = workflowmodels.WorkflowRunStatusSkipped
			}
			continue
		}

		// edge_type gating: a step whose incoming edges are all unsatisfied by
		// their (already-resolved, since Kahn's layering guarantees predecessors
		// sit in strictly earlier layers) predecessor outcomes is never
		// dispatched at all — e.g. a "success" edge from a failed predecessor,
		// or a "manual" edge (never auto-followed). This is what lets
		// dag_failure_policy=fail_branch coexist with an explicit "failure"-type
		// error-handler edge: the handler step IS eligible off a failed
		// predecessor while plain "success" siblings are skipped. An eligible step
		// then passes the same gate as in linear mode (extra_params.condition,
		// branch_label, handler lookup); a step skipped there counts as skipped
		// for its own dependents' edges.
		pending := make([]dagPendingStep, 0, len(undecided))
		for _, key := range undecided {
			step, hasStep := stepsByKey[key]
			if !hasStep {
				// This graph node has no matching base.workflow.action row — e.g. a
				// stray edge that references the canvas's trigger placeholder node
				// (id "trigger:webhook") instead of a real configured action step.
				// Treat it as an already-satisfied structural root so downstream
				// steps still become eligible, instead of dispatching a zero-value
				// Step with an empty action code, which would fail every attempt
				// with the confusing "action not supported: @v1 for @v1".
				status[key] = workflowmodels.WorkflowRunStatusSucceeded
				continue
			}
			if !dagStepEligible(key, incomingByTarget[key], status, currentPayload) {
				stepRuns = append(stepRuns, s.skippedStepRun(namesByKey[key], key, step, currentPayload))
				status[key] = workflowmodels.WorkflowRunStatusSkipped
				continue
			}
			gate := s.gateStep(namesByKey[key], key, step, currentPayload)
			if !gate.run {
				stepRuns = append(stepRuns, gate.row)
				status[key] = workflowmodels.WorkflowRunStatusSkipped
				if gate.err != nil {
					// A gate failure counts as the step's attempt, as in linear mode.
					totalAttempts++
					status[key] = dagFailedStepStatus(step)
					branchFailed = branchFailed || !step.ContinueOnError
				}
				continue
			}
			pending = append(pending, dagPendingStep{attempt: stepAttempt{
				name: namesByKey[key], key: key, step: step, gate: gate,
				payload: currentPayload, startAttempt: retryAttempts[key] + 1,
			}})
		}
		if len(pending) > 0 && branchFailed && failWorkflowPolicy {
			// A gate already failed a step of this layer under fail_workflow: dispatch nothing more.
			for _, item := range pending {
				stepRuns = append(stepRuns, s.skippedStepRun(item.attempt.name, item.attempt.key, item.attempt.step, currentPayload))
				status[item.attempt.key] = workflowmodels.WorkflowRunStatusSkipped
			}
			pending = nil
		}
		if len(pending) == 0 {
			continue
		}

		// Persist one attempt per dispatched step before running them (plan T49): a crash mid-layer
		// still counts, and resumeStateForDAGRun charges those attempts to current_steps.
		pendingKeys := make([]string, 0, len(pending))
		for _, item := range pending {
			pendingKeys = append(pendingKeys, item.attempt.key)
		}
		totalAttempts += len(pending)
		if err := s.markRun(ctx, runtime.flow.Topic, runtime.runID, dagRunUpdate(s.runUpdate(workflowmodels.WorkflowRunStatusRunning, "", currentPayload, stepRuns, totalAttempts, nil, 0), pendingKeys)); err != nil {
			return err
		}

		// Under fail_workflow, cancelSiblingsOnFailure asks runDAGLayer to cancel
		// its shared context the moment any sibling in THIS layer fails, so
		// already-dispatched siblings still mid-flight get a chance to observe
		// ctx.Done() and abort early instead of running to completion after the
		// workflow is already doomed to fail.
		outcomes := s.runDAGLayer(ctx, runtime, pending, maxParallel, failWorkflowPolicy)

		mergedOutputs := make(map[string][]byte, len(outcomes))
		retryAt := int64(0)
		var retryErr error
		for _, outcome := range outcomes {
			stepRuns = append(stepRuns, outcome.result.rows...)
			// One attempt per step was counted before dispatch; add in-place retries, and give
			// back the one a step with no attempts left never started.
			totalAttempts += outcome.result.attempts - 1
			step := stepsByKey[outcome.stepKey]
			switch {
			case outcome.result.err == nil:
				status[outcome.stepKey] = workflowmodels.WorkflowRunStatusSucceeded
				mergedOutputs[outcome.stepKey] = outcome.result.output
			case outcome.result.nextRetryAt > 0:
				// Undecided: resume_due_runs makes the next attempt. Wait for the latest cooldown
				// so no step is retried before its own.
				retryAt = max(retryAt, outcome.result.nextRetryAt)
				retryErr = outcome.result.err
			default:
				status[outcome.stepKey] = dagFailedStepStatus(step)
				branchFailed = branchFailed || !step.ContinueOnError
			}
		}
		currentPayload = dagMergePayloads(currentPayload, mergedOutputs)

		if retryAt > 0 && !(branchFailed && failWorkflowPolicy) {
			if err := s.markRun(ctx, runtime.flow.Topic, runtime.runID, s.runUpdate(workflowmodels.WorkflowRunStatusRetryWaiting, "", currentPayload, stepRuns, totalAttempts, retryErr, retryAt)); err != nil {
				return err
			}
			return fmt.Errorf("%w (dag run %d, next attempt at %d): %v", ErrRunRetryScheduled, runtime.runID, retryAt, retryErr)
		}
		if err := s.markRun(ctx, runtime.flow.Topic, runtime.runID, s.runUpdate(workflowmodels.WorkflowRunStatusRunning, "", currentPayload, stepRuns, totalAttempts, nil, 0)); err != nil {
			return err
		}
		if branchFailed && failWorkflowPolicy {
			break
		}
	}

	if branchFailed && failWorkflowPolicy {
		dagErr := fmt.Errorf("workflow: dag run %d aborted (dag_failure_policy=fail_workflow)", runtime.runID)
		_ = s.markRun(ctx, runtime.flow.Topic, runtime.runID, s.runUpdate(workflowmodels.WorkflowRunStatusFailed, "", currentPayload, stepRuns, totalAttempts, dagErr, 0))
		return dagErr
	}
	// dag_failure_policy=fail_branch: the other branches ran to completion. A failure no
	// "failure" edge handles ends the run like an exhausted linear step (dead_letter, or failed
	// for a synchronous run), so it is visible and replayable instead of reported succeeded.
	if unhandled := dagUnhandledFailures(status, edges); len(unhandled) > 0 {
		dagErr := fmt.Errorf("workflow: dag run %d: step(s) %s failed with no failure edge (dag_failure_policy=fail_branch)", runtime.runID, strings.Join(unhandled, ", "))
		if err := s.markRun(ctx, runtime.flow.Topic, runtime.runID, s.runUpdate(exhaustedRunStatus(ctx), "", currentPayload, stepRuns, totalAttempts, dagErr, 0)); err != nil {
			return err
		}
		return dagErr
	}
	if err := s.markRun(ctx, runtime.flow.Topic, runtime.runID, s.runUpdate(workflowmodels.WorkflowRunStatusSucceeded, "", currentPayload, stepRuns, totalAttempts, nil, 0)); err != nil {
		return err
	}
	publishWorkflowOutput(ctx, currentPayload)
	return nil
}

// dagFailedStepStatus is the status a failed step gives its dependents' edges: a
// continue_on_error step counts as succeeded (its output is not merged).
func dagFailedStepStatus(step workflowmodels.Step) string {
	if step.ContinueOnError {
		return workflowmodels.WorkflowRunStatusSucceeded
	}
	return workflowmodels.WorkflowRunStatusFailed
}

// dagUnhandledFailures lists, sorted, the failed steps without an outgoing "failure" edge.
func dagUnhandledFailures(status map[string]string, edges []workflowmodels.WorkflowGraphEdge) []string {
	handled := make(map[string]struct{})
	for _, edge := range edges {
		if strings.EqualFold(strings.TrimSpace(edge.EdgeType), workflowmodels.WorkflowEdgeTypeFailure) {
			handled[strings.TrimSpace(edge.FromStepKey)] = struct{}{}
		}
	}
	unhandled := make([]string, 0)
	for key, stepStatus := range status {
		if stepStatus != workflowmodels.WorkflowRunStatusFailed {
			continue
		}
		if _, ok := handled[key]; !ok {
			unhandled = append(unhandled, key)
		}
	}
	sort.Strings(unhandled)
	return unhandled
}

// dagSeedEdgelessSteps adds every step that no edge mentions to the first layer: it has no
// dependency, so it runs with the roots. ValidateWorkflowGraph only knows nodes from edges, and
// such steps were silently never executed while the run still succeeded.
func dagSeedEdgelessSteps(layers [][]string, steps []workflowmodels.Step) [][]string {
	inGraph := make(map[string]struct{})
	for _, layer := range layers {
		for _, key := range layer {
			inGraph[key] = struct{}{}
		}
	}
	missing := make([]string, 0)
	for index, step := range steps {
		key := step.ResolvedKey(index)
		if _, ok := inGraph[key]; ok {
			continue
		}
		inGraph[key] = struct{}{}
		missing = append(missing, key)
	}
	if len(missing) == 0 {
		return layers
	}
	if len(layers) == 0 {
		layers = [][]string{nil}
	}
	first := append(append([]string(nil), layers[0]...), missing...)
	sort.Strings(first)
	seeded := append([][]string{first}, layers[1:]...)
	return seeded
}

// runDAGLayer executes one frontier batch under an errgroup capped at
// maxParallel, matching the internal/worker.Pool bounded-concurrency idiom
// already used for outbound delivery. Every step reads the same payload
// snapshot from before the layer started — see the file-level design note.
//
// cancelSiblingsOnFailure is dag_failure_policy=fail_workflow: when true, a
// failing step's goroutine returns an error, which errgroup uses to cancel
// gctx for every other goroutine in this same layer still in flight. Steps
// (and whatever they call, e.g. runStep's http/db calls) that respect ctx
// cancellation abort early instead of running to completion after the
// workflow is already going to be marked failed. Under fail_branch, the
// closure always returns nil — sibling branches must run to completion
// regardless of one branch's failure.
func (s *Service) runDAGLayer(ctx context.Context, runtime flowRuntime, pending []dagPendingStep, maxParallel int, cancelSiblingsOnFailure bool) []dagStepOutcome {
	outcomes := make([]dagStepOutcome, len(pending))
	group, gctx := errgroup.WithContext(ctx)
	group.SetLimit(maxParallel)

	for index, item := range pending {
		group.Go(func() (groupErr error) {
			// Handler panics are already step errors (runStep); this catches a panic anywhere
			// else in the step's path so it fails this step instead of the whole process.
			defer func() {
				if recovered := recover(); recovered != nil {
					log.Errorf("workflow: dag step %s panicked: %v\n%s", item.attempt.key, recovered, debug.Stack())
					outcomes[index] = s.dagPanicOutcome(item.attempt, recovered)
					if cancelSiblingsOnFailure {
						groupErr = outcomes[index].result.err
					}
				}
			}()
			// Fatal errors only come from attemptStep hooks, which the DAG engine does not set.
			result, _ := s.attemptStep(gctx, runtime.flow, item.attempt)
			outcomes[index] = dagStepOutcome{stepKey: item.attempt.key, result: result}
			if cancelSiblingsOnFailure && result.err != nil && result.nextRetryAt == 0 && !item.attempt.step.ContinueOnError {
				return fmt.Errorf("workflow: dag step %s failed under fail_workflow policy", item.attempt.key)
			}
			return nil
		})
	}
	// errgroup's own error is intentionally discarded beyond triggering
	// cancellation above: every outcome (success or failure) is already
	// captured in the outcomes slice so every sibling still gets audited.
	_ = group.Wait()
	return outcomes
}

// dagPanicOutcome records a recovered panic as one failed, terminal attempt of the step.
func (s *Service) dagPanicOutcome(a stepAttempt, recovered any) dagStepOutcome {
	err := fmt.Errorf("%w: step %s: %v", ErrStepPanicked, a.key, recovered)
	return dagStepOutcome{stepKey: a.key, result: stepAttemptResult{
		rows:     []workflowmodels.WorkflowStepRun{s.failedStepRun(a.name, a.key, a.step, a.gate.handlerKey, a.gate.handlerVersion, max(a.startAttempt, 1), err, 0, a.payload)},
		attempts: 1,
		err:      err,
	}}
}

// dagStepsByKey indexes runtime steps by their resolved step key so the
// scheduler can look a step up by the key edges reference.
func dagStepsByKey(steps []workflowmodels.Step) map[string]workflowmodels.Step {
	byKey := make(map[string]workflowmodels.Step, len(steps))
	for index, step := range steps {
		byKey[step.ResolvedKey(index)] = step
	}
	return byKey
}

// dagStepNamesByKey resolves each step's display name with its sequence index, as linear mode does.
func dagStepNamesByKey(steps []workflowmodels.Step) map[string]string {
	byKey := make(map[string]string, len(steps))
	for index, step := range steps {
		byKey[step.ResolvedKey(index)] = step.ResolvedName(index)
	}
	return byKey
}

// dagResumeState rebuilds each already-decided step key's terminal status
// and how many attempts each still-undecided step has already recorded, from
// a resumed run's persisted step_runs[]. Succeeded and skipped rows are
// decided (they must not re-run). A failed row with next_retry_at set had
// attempts left: the step stays undecided and its attempt loop continues from
// the next attempt. A failed row without next_retry_at is terminal (the step
// was exhausted, failed its gate, ran out of attempts after an interruption or
// was cancelled), so the step is decided as failed — or succeeded for a
// continue_on_error step — and is not run again (plan T52).
func dagResumeState(stepRuns []workflowmodels.WorkflowStepRun) (map[string]string, map[string]int) {
	status := make(map[string]string)
	attempts := make(map[string]int)
	for _, run := range stepRuns {
		key := strings.TrimSpace(run.StepKey)
		if key == "" {
			continue
		}
		switch run.Status {
		case workflowmodels.WorkflowRunStatusSucceeded, workflowmodels.WorkflowRunStatusSkipped:
			status[key] = run.Status
		case workflowmodels.WorkflowRunStatusFailed:
			attempts[key]++
			if run.NextRetryAt == 0 {
				status[key] = workflowmodels.WorkflowRunStatusFailed
				if run.ContinueOnError {
					status[key] = workflowmodels.WorkflowRunStatusSucceeded
				}
			}
		}
	}
	return status, attempts
}

// dagUndecidedStepKeys returns the layer's step keys with no terminal status
// yet, preserving the deterministic step_key sort order ValidateWorkflowGraph
// already produced.
func dagUndecidedStepKeys(layer []string, status map[string]string) []string {
	undecided := make([]string, 0, len(layer))
	for _, key := range layer {
		if _, decided := status[key]; decided {
			continue
		}
		undecided = append(undecided, key)
	}
	return undecided
}

// dagIncomingEdgesByTarget groups edges by their destination step key so
// dagStepEligible can look up "what feeds into this step" in O(1).
func dagIncomingEdgesByTarget(edges []workflowmodels.WorkflowGraphEdge) map[string][]workflowmodels.WorkflowGraphEdge {
	byTarget := make(map[string][]workflowmodels.WorkflowGraphEdge, len(edges))
	for _, edge := range edges {
		byTarget[edge.ToStepKey] = append(byTarget[edge.ToStepKey], edge)
	}
	return byTarget
}

// dagStepEligible reports whether a step should be dispatched this layer,
// given its incoming edges and the (already-final, per Kahn's layering
// guaranteeing predecessors sit in strictly earlier layers) status of every
// predecessor. A root step (no incoming edges) is always eligible. A step
// with incoming edges requires ALL of them satisfied (AND semantics) —
// matching a join node that fans in multiple branches (e.g. "a"->"join" and
// "b"->"join": join only runs once both a and b actually succeeded; one
// failed predecessor on a plain "success" edge is enough to keep the join
// skipped, same as the old blanket downstream-of-failure skip, but now via a
// single per-edge-type check instead of a separate cascade walk). A step fed
// by only a "failure"-type edge is a single-edge case of the same rule: it's
// eligible exactly when that one edge is satisfied.
func dagStepEligible(stepKey string, incoming []workflowmodels.WorkflowGraphEdge, status map[string]string, payload []byte) bool {
	if len(incoming) == 0 {
		return true
	}
	for _, edge := range incoming {
		predecessorStatus, decided := status[edge.FromStepKey]
		if !decided {
			return false
		}
		if !dagEdgeSatisfied(edge, predecessorStatus, payload) {
			return false
		}
	}
	return true
}

// dagEdgeSatisfied decides whether one incoming edge counts toward making its
// target eligible, given the edge's type and its already-resolved
// predecessor's terminal status. This is the piece that used to be entirely
// unconsulted at run time — edge_type was schema-only.
func dagEdgeSatisfied(edge workflowmodels.WorkflowGraphEdge, predecessorStatus string, payload []byte) bool {
	switch strings.ToLower(strings.TrimSpace(edge.EdgeType)) {
	case workflowmodels.WorkflowEdgeTypeFailure:
		// Error-handler branch: only fires off a predecessor that failed.
		return predecessorStatus == workflowmodels.WorkflowRunStatusFailed
	case workflowmodels.WorkflowEdgeTypeAlways:
		// Fires regardless of predecessor outcome (succeeded, failed, or skipped).
		return true
	case workflowmodels.WorkflowEdgeTypeCondition:
		// Only evaluated off a successful predecessor; a failed/skipped
		// predecessor never produced the payload the condition expects.
		if predecessorStatus != workflowmodels.WorkflowRunStatusSucceeded {
			return false
		}
		matched, err := evaluateEdgeCondition(edge.ConditionExpr, payload)
		if err != nil {
			log.Errorf("workflow: dag edge %s->%s condition_expr invalid, treating as unsatisfied: %v", edge.FromStepKey, edge.ToStepKey, err)
			return false
		}
		return matched
	case workflowmodels.WorkflowEdgeTypeManual:
		// Structural only — counted for cycle detection/layering, never
		// auto-followed. No manual-trigger execution path exists yet; a step
		// reachable only through manual edges is skipped, not dispatched.
		return false
	default:
		// "success" (and any blank/legacy value defaults to it): only fires
		// off a predecessor that actually succeeded.
		return predecessorStatus == workflowmodels.WorkflowRunStatusSucceeded
	}
}

// evaluateEdgeCondition parses a condition-type edge's condition_expr as the
// same JSON condition-object shape step.Params["condition"] already uses
// (see evaluateWorkflowCondition in service.go), rather than inventing a
// second expression syntax for edges. An empty condition_expr always matches.
func evaluateEdgeCondition(conditionExpr string, payload []byte) (bool, error) {
	raw := strings.TrimSpace(conditionExpr)
	if raw == "" {
		return true, nil
	}
	var condition any
	if err := json.Unmarshal([]byte(raw), &condition); err != nil {
		return false, fmt.Errorf("workflow: edge condition_expr must be a JSON condition object: %w", err)
	}
	return evaluateWorkflowCondition(condition, payloadForAudit(payload))
}

// dagMergePayloads folds successful steps' outputs into the shared JSON
// payload in ascending step_key order (deterministic merge tie-break),
// matching the module's single-shared-payload execution model.
func dagMergePayloads(base []byte, outputs map[string][]byte) []byte {
	if len(outputs) == 0 {
		return base
	}
	merged := payloadForAudit(base)
	keys := make([]string, 0, len(outputs))
	for key := range outputs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for field, value := range payloadForAudit(outputs[key]) {
			merged[field] = value
		}
	}
	encoded, err := payloadFromAudit(merged)
	if err != nil {
		return base
	}
	return encoded
}

// dagRunUpdate layers "current_steps" onto an existing runUpdate() record
// map so DAG-mode in-flight batches are visible without touching
// current_step (kept for linear-mode display/back-compat).
func dagRunUpdate(update orm.RecordMap, pending []string) orm.RecordMap {
	update["current_steps"] = append([]string(nil), pending...)
	return update
}

// resumeStateForDAGRun rebuilds executionState for a DAG-mode resume.
// Unlike resumeStateForRun (linear mode's current_step-indexed resume), a
// DAG run's resume point is not a single index — it's re-derived by
// executeRunDAG's dagResumeState directly from the persisted step_runs[],
// treating any already-succeeded/skipped step key as a pre-satisfied
// in-degree contributor rather than re-running it. This function only needs
// to carry the persisted payload and step run history forward; the
// start-index/start-attempt fields are meaningless for DAG scheduling and
// left at their zero values.
func (s *Service) resumeStateForDAGRun(run *workflowmodels.WorkflowRunRecord) (executionState, error) {
	if run == nil {
		log.Errorf("workflow: resume dag workflow run requires record")
		return executionState{}, fmt.Errorf("workflow: resume dag workflow run requires record")
	}
	currentPayload, err := payloadFromAudit(run.OutputPayload)
	if err != nil {
		log.Errorf("workflow: resume dag workflow run %d output_payload: %v", run.ID, err)
		return executionState{}, fmt.Errorf("workflow: resume dag workflow run %d output_payload: %w", run.ID, err)
	}
	return executionState{
		currentPayload: currentPayload,
		stepRuns:       append([]workflowmodels.WorkflowStepRun(nil), run.StepRuns...),
		totalAttempts:  run.AttemptCount,
		dagInterrupted: dagInterruptedAttempts(run),
	}, nil
}

// dagInterruptedAttempts charges attempts that were persisted but never recorded (plan T49/T52).
// executeRunDAG counts one attempt per dispatched step together with current_steps before the
// layer runs, and records the rows only after it; so when attempt_count exceeds the recorded
// (non-skipped) rows, the execution died mid-layer and each undecided step of current_steps lost
// one attempt. A step that keeps crashing the worker therefore runs out of attempts.
func dagInterruptedAttempts(run *workflowmodels.WorkflowRunRecord) map[string]int {
	recorded := 0
	for _, stepRun := range run.StepRuns {
		if stepRun.Status != workflowmodels.WorkflowRunStatusSkipped {
			recorded++
		}
	}
	interrupted := run.AttemptCount - recorded
	if interrupted <= 0 || len(run.CurrentSteps) == 0 {
		return nil
	}
	decided, _ := dagResumeState(run.StepRuns)
	out := make(map[string]int)
	for _, key := range run.CurrentSteps {
		key = strings.TrimSpace(key)
		if _, done := decided[key]; done || key == "" || interrupted == 0 {
			continue
		}
		out[key]++
		interrupted--
	}
	return out
}

// loadWorkflowEdges loads the persisted base.workflow.edge rows for one
// workflow, converted into the pure Kahn's-algorithm shape.
func loadWorkflowEdges(ctx context.Context, workflowID int64) ([]workflowmodels.WorkflowGraphEdge, error) {
	if workflowID <= 0 {
		log.Errorf("workflow: load workflow edges requires a positive workflow id")
		return nil, fmt.Errorf("workflow: load workflow edges requires a positive workflow id")
	}
	rows, err := modelruntime.NewService().SearchRead(ctx, workflowmodels.WorkflowEdgeModelName, orm.RecordMap{
		"workflow_id": workflowID,
		"__order":     "id asc",
	}, []string{"from_step_key", "to_step_key", "edge_type", "condition_expr"})
	if err != nil {
		log.Errorf("workflow: load workflow edges workflow_id=%d: %v", workflowID, err)
		return nil, fmt.Errorf("workflow: load workflow edges workflow_id=%d: %w", workflowID, err)
	}
	return workflowmodels.WorkflowGraphEdgesFromRecords(rows), nil
}
