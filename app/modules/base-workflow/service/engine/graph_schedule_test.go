package engine

// File overview:
// - Responsibility: integration-style coverage for DAG-mode execution (executeRunDAG,
//   graph_schedule.go) — parallel frontier batches, dag_failure_policy branch/workflow
//   semantics, and resume-after-crash rebuilding remaining work from step_runs[].
// - Key symbols: focused Test cases using the same Service test-double injection pattern as
//   service_test.go.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

// dagTestFlow builds a DAG-mode FlowRecord plus the two-parallel-branch step
// set (a, b -> join) shared by the tests below: a and b have no upstream
// dependency (root frontier), join depends on both.
func dagTestFlow(failurePolicy string, maxParallel int) workflowmodels.FlowRecord {
	return workflowmodels.FlowRecord{
		BaseModel:        orm.BaseModel{ID: 90},
		Topic:            "topic.dag",
		Version:          1,
		IsActive:         true,
		ExecutionMode:    workflowmodels.WorkflowExecutionModeDAG,
		MaxParallelSteps: maxParallel,
		DagFailurePolicy: failurePolicy,
	}
}

func dagTestEdges() []workflowmodels.WorkflowGraphEdge {
	return []workflowmodels.WorkflowGraphEdge{
		{FromStepKey: "a", ToStepKey: "join"},
		{FromStepKey: "b", ToStepKey: "join"},
	}
}

// One attempt per step: these tests are about scheduling; retries (handed to resume_due_runs,
// plan T52) have their own test.
func dagTestSteps() []workflowmodels.Step {
	once := workflowmodels.RetryPolicy{MaxAttempts: 1}
	return []workflowmodels.Step{
		{Key: "a", Name: "step-a", Action: "step_a", RetryPolicy: once},
		{Key: "b", Name: "step-b", Action: "step_b", RetryPolicy: once},
		{Key: "join", Name: "step-join", Action: "step_join", RetryPolicy: once},
	}
}

func TestExecuteRunDAGRunsParallelBranchesAndJoinsSuccessfully(t *testing.T) {
	var calls sync.Map // action name -> call count
	var maxConcurrent int32
	var current int32
	var updates []orm.RecordMap

	svc := &Service{
		loadFlowEdges: func(context.Context, int64) ([]workflowmodels.WorkflowGraphEdge, error) {
			return dagTestEdges(), nil
		},
		createRun: func(_ context.Context, record workflowmodels.WorkflowRunRecord) (*orm.InsertResult, error) {
			return &orm.InsertResult{InsertedID: int64(1), Acknowledged: true}, nil
		},
		updateRun: func(_ context.Context, _ orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			updates = append(updates, cloneRecordMap(values))
			return &orm.UpdateResult{Acknowledged: true, MatchedCount: 1, ModifiedCount: 1}, nil
		},
		lookupAction: func(name string, _ int) (ActionHandler, bool) {
			return func(_ context.Context, req ActionRequest) ([]byte, error) {
				value, _ := calls.LoadOrStore(name, new(int32))
				atomic.AddInt32(value.(*int32), 1)

				n := atomic.AddInt32(&current, 1)
				for {
					m := atomic.LoadInt32(&maxConcurrent)
					if n <= m || atomic.CompareAndSwapInt32(&maxConcurrent, m, n) {
						break
					}
				}
				time.Sleep(5 * time.Millisecond)
				atomic.AddInt32(&current, -1)
				return req.Payload, nil
			}, true
		},
		now:   func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		sleep: func(context.Context, time.Duration) error { return nil },
	}

	runtime := flowRuntime{flow: dagTestFlow(workflowmodels.WorkflowDagFailurePolicyFailBranch, 4), steps: dagTestSteps()}
	if err := svc.executeFlow(context.Background(), runtime, []byte(`{"hello":"world"}`)); err != nil {
		t.Fatalf("executeFlow (dag) returned error: %v", err)
	}

	for _, action := range []string{"step_a", "step_b", "step_join"} {
		value, ok := calls.Load(action)
		if !ok || atomic.LoadInt32(value.(*int32)) != 1 {
			t.Fatalf("expected %s to run exactly once", action)
		}
	}
	if atomic.LoadInt32(&maxConcurrent) < 2 {
		t.Fatalf("expected a and b to run concurrently, max concurrent = %d", maxConcurrent)
	}

	last := updates[len(updates)-1]
	if got, _ := last["status"].(string); got != workflowmodels.WorkflowRunStatusSucceeded {
		t.Fatalf("expected succeeded final status, got %#v", last["status"])
	}
	stepRuns, ok := last["step_runs"].([]workflowmodels.WorkflowStepRun)
	if !ok || len(stepRuns) != 3 {
		t.Fatalf("expected 3 step runs (a, b, join), got %#v", last["step_runs"])
	}
}

// TestExecuteRunDAGSkipsEdgeNodeWithNoMatchingStep reproduces a saved DAG edge
// whose from_step_key references the canvas's trigger placeholder node
// ("trigger:webhook") instead of a real base.workflow.action row — e.g. a
// user connecting the trigger node directly in the DAG editor. Before this
// fix, that phantom node reached runDAGLayer with a zero-value Step (empty
// Action), and every attempt failed with the confusing "workflow: action not
// supported: @v1 for @v1". It must now be treated as an already-satisfied
// structural root so the real downstream step still runs.
func TestExecuteRunDAGSkipsEdgeNodeWithNoMatchingStep(t *testing.T) {
	var updates []orm.RecordMap
	var joinCalls int32

	svc := &Service{
		loadFlowEdges: func(context.Context, int64) ([]workflowmodels.WorkflowGraphEdge, error) {
			return []workflowmodels.WorkflowGraphEdge{
				{FromStepKey: "trigger:webhook", ToStepKey: "join"},
			}, nil
		},
		createRun: func(_ context.Context, record workflowmodels.WorkflowRunRecord) (*orm.InsertResult, error) {
			return &orm.InsertResult{InsertedID: int64(1), Acknowledged: true}, nil
		},
		updateRun: func(_ context.Context, _ orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			updates = append(updates, cloneRecordMap(values))
			return &orm.UpdateResult{Acknowledged: true, MatchedCount: 1, ModifiedCount: 1}, nil
		},
		lookupAction: func(name string, _ int) (ActionHandler, bool) {
			return func(_ context.Context, req ActionRequest) ([]byte, error) {
				atomic.AddInt32(&joinCalls, 1)
				return req.Payload, nil
			}, true
		},
		now:   func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		sleep: func(context.Context, time.Duration) error { return nil },
	}

	runtime := flowRuntime{
		flow:  dagTestFlow(workflowmodels.WorkflowDagFailurePolicyFailBranch, 4),
		steps: []workflowmodels.Step{{Key: "join", Name: "step-join", Action: "step_join"}},
	}
	if err := svc.executeFlow(context.Background(), runtime, []byte(`{"hello":"world"}`)); err != nil {
		t.Fatalf("executeFlow (dag) returned error: %v", err)
	}

	if atomic.LoadInt32(&joinCalls) != 1 {
		t.Fatalf("expected join to run exactly once, got %d calls", joinCalls)
	}

	last := updates[len(updates)-1]
	if got, _ := last["status"].(string); got != workflowmodels.WorkflowRunStatusSucceeded {
		t.Fatalf("expected succeeded final status, got %#v", last["status"])
	}
	stepRuns, ok := last["step_runs"].([]workflowmodels.WorkflowStepRun)
	if !ok || len(stepRuns) != 1 || stepRuns[0].StepKey != "join" {
		t.Fatalf("expected exactly one step run for join (no fabricated run for the phantom trigger node), got %#v", last["step_runs"])
	}
}

func TestExecuteRunDAGFailBranchSkipsOnlyDownstreamDependents(t *testing.T) {
	var updates []orm.RecordMap
	bCalls := 0
	joinCalls := 0

	svc := &Service{
		loadFlowEdges: func(context.Context, int64) ([]workflowmodels.WorkflowGraphEdge, error) {
			return dagTestEdges(), nil
		},
		createRun: func(_ context.Context, record workflowmodels.WorkflowRunRecord) (*orm.InsertResult, error) {
			return &orm.InsertResult{InsertedID: int64(2), Acknowledged: true}, nil
		},
		updateRun: func(_ context.Context, _ orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			updates = append(updates, cloneRecordMap(values))
			return &orm.UpdateResult{Acknowledged: true, MatchedCount: 1, ModifiedCount: 1}, nil
		},
		lookupAction: func(name string, _ int) (ActionHandler, bool) {
			switch name {
			case "step_a":
				return func(context.Context, ActionRequest) ([]byte, error) {
					return nil, errors.New("boom")
				}, true
			case "step_b":
				return func(_ context.Context, req ActionRequest) ([]byte, error) {
					bCalls++
					return req.Payload, nil
				}, true
			case "step_join":
				return func(_ context.Context, req ActionRequest) ([]byte, error) {
					joinCalls++
					return req.Payload, nil
				}, true
			default:
				return nil, false
			}
		},
		now:   func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		sleep: func(context.Context, time.Duration) error { return nil },
	}

	runtime := flowRuntime{flow: dagTestFlow(workflowmodels.WorkflowDagFailurePolicyFailBranch, 4), steps: dagTestSteps()}
	// Plan T52: the other branch still runs, but an unhandled failure ends the run dead_letter
	// (as an exhausted linear step does) instead of succeeded.
	if err := svc.executeFlow(context.Background(), runtime, []byte(`{"hello":"world"}`)); err == nil || !strings.Contains(err.Error(), "step(s) a failed") {
		t.Fatalf("err = %v, want the unhandled failure of a", err)
	}
	if bCalls != 1 {
		t.Fatalf("expected sibling branch b to still run once, got %d", bCalls)
	}
	if joinCalls != 0 {
		t.Fatalf("expected join (downstream of failed a) to never run, got %d calls", joinCalls)
	}

	last := updates[len(updates)-1]
	if got, _ := last["status"].(string); got != workflowmodels.WorkflowRunStatusDeadLetter {
		t.Fatalf("expected dead_letter final status after an unhandled branch failure, got %#v", last["status"])
	}
	stepRuns, ok := last["step_runs"].([]workflowmodels.WorkflowStepRun)
	if !ok {
		t.Fatalf("expected step_runs in final update, got %#v", last["step_runs"])
	}
	var sawFailedA, sawSkippedJoin bool
	for _, run := range stepRuns {
		if run.StepKey == "a" && run.Status == workflowmodels.WorkflowRunStatusFailed {
			sawFailedA = true
		}
		if run.StepKey == "join" && run.Status == workflowmodels.WorkflowRunStatusSkipped {
			sawSkippedJoin = true
		}
	}
	if !sawFailedA {
		t.Fatalf("expected a failed step_run entry, got %#v", stepRuns)
	}
	if !sawSkippedJoin {
		t.Fatalf("expected join to have a skipped step_run audit entry, got %#v", stepRuns)
	}
}

func TestExecuteRunDAGFailWorkflowAbortsRemainingLayers(t *testing.T) {
	var updates []orm.RecordMap
	joinCalls := 0

	svc := &Service{
		loadFlowEdges: func(context.Context, int64) ([]workflowmodels.WorkflowGraphEdge, error) {
			return dagTestEdges(), nil
		},
		createRun: func(_ context.Context, record workflowmodels.WorkflowRunRecord) (*orm.InsertResult, error) {
			return &orm.InsertResult{InsertedID: int64(3), Acknowledged: true}, nil
		},
		updateRun: func(_ context.Context, _ orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			updates = append(updates, cloneRecordMap(values))
			return &orm.UpdateResult{Acknowledged: true, MatchedCount: 1, ModifiedCount: 1}, nil
		},
		lookupAction: func(name string, _ int) (ActionHandler, bool) {
			switch name {
			case "step_a":
				return func(context.Context, ActionRequest) ([]byte, error) {
					return nil, errors.New("boom")
				}, true
			case "step_b":
				return func(_ context.Context, req ActionRequest) ([]byte, error) { return req.Payload, nil }, true
			case "step_join":
				return func(_ context.Context, req ActionRequest) ([]byte, error) {
					joinCalls++
					return req.Payload, nil
				}, true
			default:
				return nil, false
			}
		},
		now:   func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		sleep: func(context.Context, time.Duration) error { return nil },
	}

	runtime := flowRuntime{flow: dagTestFlow(workflowmodels.WorkflowDagFailurePolicyFailWorkflow, 4), steps: dagTestSteps()}
	if err := svc.executeFlow(context.Background(), runtime, []byte(`{"hello":"world"}`)); err == nil {
		t.Fatalf("expected fail_workflow run to return an error")
	}
	if joinCalls != 0 {
		t.Fatalf("expected join to never be dispatched once fail_workflow aborted, got %d calls", joinCalls)
	}

	last := updates[len(updates)-1]
	if got, _ := last["status"].(string); got != workflowmodels.WorkflowRunStatusFailed {
		t.Fatalf("expected failed final status under fail_workflow, got %#v", last["status"])
	}
}

func TestExecuteRunDAGZeroEdgesFallsBackToLinear(t *testing.T) {
	linearCalled := false

	svc := &Service{
		loadFlowEdges: func(context.Context, int64) ([]workflowmodels.WorkflowGraphEdge, error) {
			return nil, nil
		},
		createRun: func(_ context.Context, record workflowmodels.WorkflowRunRecord) (*orm.InsertResult, error) {
			return &orm.InsertResult{InsertedID: int64(4), Acknowledged: true}, nil
		},
		updateRun: func(_ context.Context, _ orm.RecordMap, _ orm.RecordMap) (*orm.UpdateResult, error) {
			return &orm.UpdateResult{Acknowledged: true, MatchedCount: 1, ModifiedCount: 1}, nil
		},
		lookupAction: func(name string, _ int) (ActionHandler, bool) {
			return func(_ context.Context, req ActionRequest) ([]byte, error) {
				linearCalled = true
				return req.Payload, nil
			}, true
		},
		now:   func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		sleep: func(context.Context, time.Duration) error { return nil },
	}

	// execution_mode is "dag" but there are zero persisted edges — this must
	// take the byte-identical linear path (regression guard), not error out
	// trying to schedule an empty DAG.
	runtime := flowRuntime{
		flow: workflowmodels.FlowRecord{
			BaseModel: orm.BaseModel{ID: 91}, Topic: "topic.dag-empty", Version: 1, IsActive: true,
			ExecutionMode: workflowmodels.WorkflowExecutionModeDAG,
		},
		steps: []workflowmodels.Step{{Key: "only-step", Action: "step_a"}},
	}
	if err := svc.executeFlow(context.Background(), runtime, []byte(`{"hello":"world"}`)); err != nil {
		t.Fatalf("executeFlow returned error: %v", err)
	}
	if !linearCalled {
		t.Fatalf("expected the single step to run via the linear fallback path")
	}
}

func TestResumeRunDAGOnlyReExecutesIncompleteBranches(t *testing.T) {
	aCalls := 0
	bCalls := 0
	joinCalls := 0
	var updates []orm.RecordMap

	svc := &Service{
		readRun: func(context.Context, int64) (*workflowmodels.WorkflowRunRecord, error) {
			return &workflowmodels.WorkflowRunRecord{
				BaseModel:   orm.BaseModel{ID: 77},
				Name:        "req-77",
				Topic:       "topic.dag-resume",
				FlowVersion: 5,
				Status:      workflowmodels.WorkflowRunStatusRunning,
				OutputPayload: map[string]any{
					"hello": "world",
				},
				StepRuns: []workflowmodels.WorkflowStepRun{
					{StepKey: "a", Action: "step_a", Status: workflowmodels.WorkflowRunStatusSucceeded},
				},
				AttemptCount: 1,
			}, nil
		},
		loadFlowVersion: func(context.Context, string, int) (*workflowmodels.FlowRecord, error) {
			flow := dagTestFlow(workflowmodels.WorkflowDagFailurePolicyFailBranch, 4)
			flow.Topic = "topic.dag-resume"
			flow.Version = 5
			return &flow, nil
		},
		loadFlowActions: func(context.Context, int64) ([]workflowmodels.WorkflowActionRecord, error) {
			return []workflowmodels.WorkflowActionRecord{
				{Sequence: 10, StepKey: "a", Name: "step-a", ActionCode: "step_a", ActionVersion: 1},
				{Sequence: 20, StepKey: "b", Name: "step-b", ActionCode: "step_b", ActionVersion: 1},
				{Sequence: 30, StepKey: "join", Name: "step-join", ActionCode: "step_join", ActionVersion: 1},
			}, nil
		},
		loadFlowEdges: func(context.Context, int64) ([]workflowmodels.WorkflowGraphEdge, error) {
			return dagTestEdges(), nil
		},
		updateRun: func(_ context.Context, _ orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			updates = append(updates, cloneRecordMap(values))
			return &orm.UpdateResult{Acknowledged: true, MatchedCount: 1, ModifiedCount: 1}, nil
		},
		lookupAction: func(name string, _ int) (ActionHandler, bool) {
			switch name {
			case "step_a":
				return func(_ context.Context, req ActionRequest) ([]byte, error) {
					aCalls++
					return req.Payload, nil
				}, true
			case "step_b":
				return func(_ context.Context, req ActionRequest) ([]byte, error) {
					bCalls++
					return req.Payload, nil
				}, true
			case "step_join":
				return func(_ context.Context, req ActionRequest) ([]byte, error) {
					joinCalls++
					return req.Payload, nil
				}, true
			default:
				return nil, false
			}
		},
		now:   func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		sleep: func(context.Context, time.Duration) error { return nil },
	}

	if err := svc.ResumeRun(context.Background(), 77); err != nil {
		t.Fatalf("ResumeRun (dag) returned error: %v", err)
	}
	if aCalls != 0 {
		t.Fatalf("expected already-succeeded step a not to re-run, got %d calls", aCalls)
	}
	if bCalls != 1 {
		t.Fatalf("expected incomplete branch b to run once, got %d", bCalls)
	}
	if joinCalls != 1 {
		t.Fatalf("expected join to run once both branches resolved, got %d", joinCalls)
	}

	last := updates[len(updates)-1]
	if got, _ := last["status"].(string); got != workflowmodels.WorkflowRunStatusSucceeded {
		t.Fatalf("expected succeeded final status, got %#v", last["status"])
	}
}

// TestExecuteRunDAGFailureEdgeRoutesToErrorHandler covers edge_type=failure:
// a plain "success" sibling of a failed predecessor must stay skipped, while
// a "failure"-typed edge from that same predecessor must actually dispatch
// its error-handler target — the scoping gap flagged in
// 2026-07-23-workflow-dag-kahns-algorithm-test-coverage.md ("edge_type is
// schema-only, the scheduler doesn't yet branch on it").
func TestExecuteRunDAGFailureEdgeRoutesToErrorHandler(t *testing.T) {
	var updates []orm.RecordMap
	okCalls, handlerCalls := 0, 0

	edges := []workflowmodels.WorkflowGraphEdge{
		{FromStepKey: "a", ToStepKey: "ok_sibling", EdgeType: workflowmodels.WorkflowEdgeTypeSuccess},
		{FromStepKey: "a", ToStepKey: "err_handler", EdgeType: workflowmodels.WorkflowEdgeTypeFailure},
	}
	steps := []workflowmodels.Step{
		{Key: "a", Name: "step-a", Action: "step_a", RetryPolicy: workflowmodels.RetryPolicy{MaxAttempts: 1}},
		{Key: "ok_sibling", Name: "step-ok", Action: "step_ok"},
		{Key: "err_handler", Name: "step-err", Action: "step_err"},
	}

	svc := &Service{
		loadFlowEdges: func(context.Context, int64) ([]workflowmodels.WorkflowGraphEdge, error) { return edges, nil },
		createRun: func(_ context.Context, record workflowmodels.WorkflowRunRecord) (*orm.InsertResult, error) {
			return &orm.InsertResult{InsertedID: int64(10), Acknowledged: true}, nil
		},
		updateRun: func(_ context.Context, _ orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			updates = append(updates, cloneRecordMap(values))
			return &orm.UpdateResult{Acknowledged: true, MatchedCount: 1, ModifiedCount: 1}, nil
		},
		lookupAction: func(name string, _ int) (ActionHandler, bool) {
			switch name {
			case "step_a":
				return func(context.Context, ActionRequest) ([]byte, error) { return nil, errors.New("boom") }, true
			case "step_ok":
				return func(_ context.Context, req ActionRequest) ([]byte, error) { okCalls++; return req.Payload, nil }, true
			case "step_err":
				return func(_ context.Context, req ActionRequest) ([]byte, error) { handlerCalls++; return req.Payload, nil }, true
			default:
				return nil, false
			}
		},
		now:   func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		sleep: func(context.Context, time.Duration) error { return nil },
	}

	runtime := flowRuntime{flow: dagTestFlow(workflowmodels.WorkflowDagFailurePolicyFailBranch, 4), steps: steps}
	if err := svc.executeFlow(context.Background(), runtime, []byte(`{"hello":"world"}`)); err != nil {
		t.Fatalf("expected fail_branch run to still reach succeeded, got %v", err)
	}
	if okCalls != 0 {
		t.Fatalf("expected the success-typed sibling to stay skipped after a failed, got %d calls", okCalls)
	}
	if handlerCalls != 1 {
		t.Fatalf("expected the failure-typed error handler to run exactly once, got %d calls", handlerCalls)
	}
}

// TestExecuteRunDAGConditionEdgeGatesOnPayload covers edge_type=condition:
// the edge only fires when its condition_expr (the same JSON condition-object
// shape step.Params["condition"] uses) evaluates true against the payload
// produced by the already-succeeded predecessor.
func TestExecuteRunDAGConditionEdgeGatesOnPayload(t *testing.T) {
	var updates []orm.RecordMap
	gatedCalls := 0

	edges := []workflowmodels.WorkflowGraphEdge{
		{FromStepKey: "a", ToStepKey: "gated", EdgeType: workflowmodels.WorkflowEdgeTypeCondition, ConditionExpr: `{"field":"proceed","equals":true}`},
	}
	steps := []workflowmodels.Step{
		{Key: "a", Name: "step-a", Action: "step_a"},
		{Key: "gated", Name: "step-gated", Action: "step_gated"},
	}

	svc := &Service{
		loadFlowEdges: func(context.Context, int64) ([]workflowmodels.WorkflowGraphEdge, error) { return edges, nil },
		createRun: func(_ context.Context, record workflowmodels.WorkflowRunRecord) (*orm.InsertResult, error) {
			return &orm.InsertResult{InsertedID: int64(11), Acknowledged: true}, nil
		},
		updateRun: func(_ context.Context, _ orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			updates = append(updates, cloneRecordMap(values))
			return &orm.UpdateResult{Acknowledged: true, MatchedCount: 1, ModifiedCount: 1}, nil
		},
		lookupAction: func(name string, _ int) (ActionHandler, bool) {
			switch name {
			case "step_a":
				return func(context.Context, ActionRequest) ([]byte, error) { return []byte(`{"proceed":true}`), nil }, true
			case "step_gated":
				return func(_ context.Context, req ActionRequest) ([]byte, error) { gatedCalls++; return req.Payload, nil }, true
			default:
				return nil, false
			}
		},
		now:   func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		sleep: func(context.Context, time.Duration) error { return nil },
	}

	runtime := flowRuntime{flow: dagTestFlow(workflowmodels.WorkflowDagFailurePolicyFailBranch, 4), steps: steps}
	if err := svc.executeFlow(context.Background(), runtime, []byte(`{"hello":"world"}`)); err != nil {
		t.Fatalf("executeFlow (dag) returned error: %v", err)
	}
	if gatedCalls != 1 {
		t.Fatalf("expected the condition-typed edge to fire once proceed=true, got %d calls", gatedCalls)
	}

	last := updates[len(updates)-1]
	stepRuns, _ := last["step_runs"].([]workflowmodels.WorkflowStepRun)
	for _, run := range stepRuns {
		if run.StepKey == "gated" && run.Status != workflowmodels.WorkflowRunStatusSucceeded {
			t.Fatalf("expected gated step_run to be succeeded, got %#v", run.Status)
		}
	}
}

// TestExecuteRunDAGManualEdgeNeverAutoDispatches covers edge_type=manual: a
// step reachable only via a manual edge must never be auto-dispatched by the
// scheduler (no out-of-band manual-trigger execution path exists yet), and
// must still land a "skipped" audit row rather than silently vanishing.
func TestExecuteRunDAGManualEdgeNeverAutoDispatches(t *testing.T) {
	var updates []orm.RecordMap
	manualCalls := 0

	edges := []workflowmodels.WorkflowGraphEdge{
		{FromStepKey: "a", ToStepKey: "manual_only", EdgeType: workflowmodels.WorkflowEdgeTypeManual},
	}
	steps := []workflowmodels.Step{
		{Key: "a", Name: "step-a", Action: "step_a"},
		{Key: "manual_only", Name: "step-manual", Action: "step_manual"},
	}

	svc := &Service{
		loadFlowEdges: func(context.Context, int64) ([]workflowmodels.WorkflowGraphEdge, error) { return edges, nil },
		createRun: func(_ context.Context, record workflowmodels.WorkflowRunRecord) (*orm.InsertResult, error) {
			return &orm.InsertResult{InsertedID: int64(12), Acknowledged: true}, nil
		},
		updateRun: func(_ context.Context, _ orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			updates = append(updates, cloneRecordMap(values))
			return &orm.UpdateResult{Acknowledged: true, MatchedCount: 1, ModifiedCount: 1}, nil
		},
		lookupAction: func(name string, _ int) (ActionHandler, bool) {
			switch name {
			case "step_a":
				return func(_ context.Context, req ActionRequest) ([]byte, error) { return req.Payload, nil }, true
			case "step_manual":
				return func(_ context.Context, req ActionRequest) ([]byte, error) { manualCalls++; return req.Payload, nil }, true
			default:
				return nil, false
			}
		},
		now:   func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		sleep: func(context.Context, time.Duration) error { return nil },
	}

	runtime := flowRuntime{flow: dagTestFlow(workflowmodels.WorkflowDagFailurePolicyFailBranch, 4), steps: steps}
	if err := svc.executeFlow(context.Background(), runtime, []byte(`{"hello":"world"}`)); err != nil {
		t.Fatalf("executeFlow (dag) returned error: %v", err)
	}
	if manualCalls != 0 {
		t.Fatalf("expected the manual-typed edge to never auto-dispatch, got %d calls", manualCalls)
	}

	last := updates[len(updates)-1]
	stepRuns, _ := last["step_runs"].([]workflowmodels.WorkflowStepRun)
	var sawSkippedManual bool
	for _, run := range stepRuns {
		if run.StepKey == "manual_only" && run.Status == workflowmodels.WorkflowRunStatusSkipped {
			sawSkippedManual = true
		}
	}
	if !sawSkippedManual {
		t.Fatalf("expected manual_only to have a skipped step_run audit entry, got %#v", stepRuns)
	}
}

// TestExecuteRunDAGFailWorkflowCancelsInFlightSiblingsInSameLayer covers the
// mid-layer cancellation gap: under dag_failure_policy=fail_workflow, a
// sibling still running in the SAME frontier layer as a step that just
// failed must observe ctx cancellation (via runDAGLayer's shared gctx) and
// abort, instead of running to completion after the workflow is already
// doomed to fail.
func TestExecuteRunDAGFailWorkflowCancelsInFlightSiblingsInSameLayer(t *testing.T) {
	bStarted := make(chan struct{})
	bOutcome := make(chan bool, 1) // true == observed cancellation, false == ran to completion

	svc := &Service{
		loadFlowEdges: func(context.Context, int64) ([]workflowmodels.WorkflowGraphEdge, error) {
			return dagTestEdges(), nil
		},
		createRun: func(_ context.Context, record workflowmodels.WorkflowRunRecord) (*orm.InsertResult, error) {
			return &orm.InsertResult{InsertedID: int64(13), Acknowledged: true}, nil
		},
		updateRun: func(_ context.Context, _ orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			return &orm.UpdateResult{Acknowledged: true, MatchedCount: 1, ModifiedCount: 1}, nil
		},
		lookupAction: func(name string, _ int) (ActionHandler, bool) {
			switch name {
			case "step_a":
				return func(context.Context, ActionRequest) ([]byte, error) { return nil, errors.New("boom") }, true
			case "step_b":
				return func(ctx context.Context, req ActionRequest) ([]byte, error) {
					close(bStarted)
					select {
					case <-ctx.Done():
						bOutcome <- true
						return nil, ctx.Err()
					case <-time.After(2 * time.Second):
						bOutcome <- false
						return req.Payload, nil
					}
				}, true
			case "step_join":
				return func(_ context.Context, req ActionRequest) ([]byte, error) { return req.Payload, nil }, true
			default:
				return nil, false
			}
		},
		now:   func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		sleep: func(context.Context, time.Duration) error { return nil },
	}

	runtime := flowRuntime{flow: dagTestFlow(workflowmodels.WorkflowDagFailurePolicyFailWorkflow, 4), steps: dagTestSteps()}
	if err := svc.executeFlow(context.Background(), runtime, []byte(`{"hello":"world"}`)); err == nil {
		t.Fatalf("expected fail_workflow run to return an error")
	}

	select {
	case <-bStarted:
	case <-time.After(time.Second):
		t.Fatalf("expected sibling b to have started before a failed")
	}

	select {
	case observedCancellation := <-bOutcome:
		if !observedCancellation {
			t.Fatalf("expected sibling b to observe ctx cancellation instead of running to completion")
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for sibling b's outcome")
	}
}
