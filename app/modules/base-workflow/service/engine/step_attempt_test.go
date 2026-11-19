package engine

// File overview:
// - Responsibility: plan T52 (one gate + attempt loop for linear and DAG: edge-less DAG steps,
//   DAG condition/branch gates, fail_branch status, DAG retries handed to resume_due_runs,
//   interrupted DAG attempts, panic recovery), T56 (step idempotency key), T58 (snapshot cap) and
//   T63 (unknown condition operator).

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

// stepTestRecorder records run writes and handler calls of a Service test double.
type stepTestRecorder struct {
	mu      sync.Mutex
	updates []orm.RecordMap
	calls   map[string]int
}

func (r *stepTestRecorder) call(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls[name]++
}

func (r *stepTestRecorder) count(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[name]
}

func (r *stepTestRecorder) last() orm.RecordMap {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.updates[len(r.updates)-1]
}

// stepTestService builds a Service whose handlers are looked up in handlers by action name;
// an unknown name echoes the payload. Every call is counted.
func stepTestService(rec *stepTestRecorder, edges []workflowmodels.WorkflowGraphEdge, handlers map[string]ActionHandler) *Service {
	rec.calls = map[string]int{}
	return &Service{
		loadFlowEdges: func(context.Context, int64) ([]workflowmodels.WorkflowGraphEdge, error) { return edges, nil },
		createRun: func(context.Context, workflowmodels.WorkflowRunRecord) (*orm.InsertResult, error) {
			return &orm.InsertResult{InsertedID: int64(50), Acknowledged: true}, nil
		},
		updateRun: func(_ context.Context, _ orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			rec.mu.Lock()
			rec.updates = append(rec.updates, cloneRecordMap(values))
			rec.mu.Unlock()
			return &orm.UpdateResult{Acknowledged: true, MatchedCount: 1, ModifiedCount: 1}, nil
		},
		lookupAction: func(name string, _ int) (ActionHandler, bool) {
			return func(ctx context.Context, req ActionRequest) ([]byte, error) {
				rec.call(name)
				if handler, ok := handlers[name]; ok {
					return handler(ctx, req)
				}
				return req.Payload, nil
			}, true
		},
		renewLease: func(context.Context, int64, string) (bool, error) { return true, nil },
		takeLease:  func(context.Context, int64, string) (bool, error) { return true, nil },
		now:        func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		sleep:      func(context.Context, time.Duration) error { return nil },
	}
}

func stepRunsOf(t *testing.T, update orm.RecordMap) []workflowmodels.WorkflowStepRun {
	t.Helper()
	runs, ok := update["step_runs"].([]workflowmodels.WorkflowStepRun)
	if !ok {
		t.Fatalf("update has no step_runs: %#v", update)
	}
	return runs
}

func stepRunStatus(runs []workflowmodels.WorkflowStepRun, key string) string {
	status := ""
	for _, run := range runs {
		if run.StepKey == key {
			status = run.Status
		}
	}
	return status
}

func TestDAGRunsStepsWithoutEdges(t *testing.T) {
	rec := &stepTestRecorder{}
	svc := stepTestService(rec, []workflowmodels.WorkflowGraphEdge{{FromStepKey: "a", ToStepKey: "b"}}, nil)
	steps := []workflowmodels.Step{
		{Key: "a", Action: "step_a"},
		{Key: "b", Action: "step_b"},
		{Key: "lonely", Action: "step_lonely"}, // no edge mentions it
	}
	if err := svc.executeFlow(context.Background(), flowRuntime{flow: dagTestFlow("", 4), steps: steps}, []byte(`{}`)); err != nil {
		t.Fatalf("executeFlow: %v", err)
	}
	if rec.count("step_lonely") != 1 {
		t.Fatalf("edge-less step ran %d time(s), want 1", rec.count("step_lonely"))
	}
	if got := stepRunStatus(stepRunsOf(t, rec.last()), "lonely"); got != workflowmodels.WorkflowRunStatusSucceeded {
		t.Fatalf("lonely step_run status = %q, want succeeded", got)
	}
}

func TestDAGHonorsStepConditionAndBranchLabel(t *testing.T) {
	rec := &stepTestRecorder{}
	edges := []workflowmodels.WorkflowGraphEdge{
		{FromStepKey: "check", ToStepKey: "gated"},
		{FromStepKey: "check", ToStepKey: "no_branch"},
		{FromStepKey: "gated", ToStepKey: "after_gated"},
	}
	svc := stepTestService(rec, edges, map[string]ActionHandler{
		"check": func(context.Context, ActionRequest) ([]byte, error) {
			return []byte(`{"amount":5,"_condition_result":true}`), nil
		},
	})
	steps := []workflowmodels.Step{
		{Key: "check", Action: "check"},
		// extra_params.condition does not match: skipped, and so is its dependent.
		{Key: "gated", Action: "gated", Params: map[string]any{"condition": map[string]any{"field": "amount", "equals": 10}}},
		{Key: "after_gated", Action: "after_gated"},
		// "no" branch of a true condition: skipped.
		{Key: "no_branch", Action: "no_branch", BranchLabel: "no"},
	}
	if err := svc.executeFlow(context.Background(), flowRuntime{flow: dagTestFlow("", 4), steps: steps}, []byte(`{}`)); err != nil {
		t.Fatalf("executeFlow: %v", err)
	}
	for _, name := range []string{"gated", "after_gated", "no_branch"} {
		if rec.count(name) != 0 {
			t.Fatalf("%s ran %d time(s), want skipped", name, rec.count(name))
		}
	}
	last := rec.last()
	if last["status"] != workflowmodels.WorkflowRunStatusSucceeded {
		t.Fatalf("final status = %#v, want succeeded", last["status"])
	}
	runs := stepRunsOf(t, last)
	for _, key := range []string{"gated", "after_gated", "no_branch"} {
		if got := stepRunStatus(runs, key); got != workflowmodels.WorkflowRunStatusSkipped {
			t.Fatalf("%s step_run status = %q, want skipped", key, got)
		}
	}
}

func TestDAGConditionErrorFailsTheStep(t *testing.T) {
	rec := &stepTestRecorder{}
	svc := stepTestService(rec, []workflowmodels.WorkflowGraphEdge{{FromStepKey: "a", ToStepKey: "b"}}, nil)
	steps := []workflowmodels.Step{
		{Key: "a", Action: "step_a", Params: map[string]any{"condition": map[string]any{"field": "x", "gt": "one"}}},
		{Key: "b", Action: "step_b"},
	}
	err := svc.executeFlow(context.Background(), flowRuntime{flow: dagTestFlow("", 4), steps: steps}, []byte(`{"x":5}`))
	if err == nil {
		t.Fatalf("expected the condition error to fail the run")
	}
	if rec.count("step_a") != 0 || rec.count("step_b") != 0 {
		t.Fatalf("steps ran after a condition error: %#v", rec.calls)
	}
	if got := rec.last()["status"]; got != workflowmodels.WorkflowRunStatusDeadLetter {
		t.Fatalf("final status = %#v, want dead_letter", got)
	}
}

func TestDAGRetryIsHandedToResumeDueRuns(t *testing.T) {
	rec := &stepTestRecorder{}
	failures := 1
	svc := stepTestService(rec, dagTestEdges(), map[string]ActionHandler{
		"step_a": func(_ context.Context, req ActionRequest) ([]byte, error) {
			if failures > 0 {
				failures--
				return nil, errors.New("flaky")
			}
			return []byte(`{"a":true}`), nil
		},
	})
	steps := dagTestSteps()
	steps[0].RetryPolicy = workflowmodels.RetryPolicy{MaxAttempts: 2}
	flow := dagTestFlow(workflowmodels.WorkflowDagFailurePolicyFailBranch, 4)

	err := svc.executeFlow(context.Background(), flowRuntime{flow: flow, steps: steps}, []byte(`{}`))
	if !errors.Is(err, ErrRunRetryScheduled) {
		t.Fatalf("err = %v, want ErrRunRetryScheduled", err)
	}
	if rec.count("step_a") != 1 || rec.count("step_b") != 1 || rec.count("step_join") != 0 {
		t.Fatalf("calls after first execution = %#v, want a=1 b=1 join=0 (no in-process retry)", rec.calls)
	}
	parked := rec.last()
	if parked["status"] != workflowmodels.WorkflowRunStatusRetryWaiting {
		t.Fatalf("status = %#v, want waiting_retry", parked["status"])
	}
	if next, _ := parked["next_retry_at"].(int64); next <= 0 {
		t.Fatalf("next_retry_at = %#v, want set", parked["next_retry_at"])
	}

	// The cron resumes the stored run: only a runs again (attempt 2), then join.
	stored := &workflowmodels.WorkflowRunRecord{
		BaseModel: orm.BaseModel{ID: 50}, Name: "r", Topic: flow.Topic, FlowVersion: flow.Version,
		Status: workflowmodels.WorkflowRunStatusRetryWaiting, AttemptCount: parked["attempt_count"].(int),
		OutputPayload: parked["output_payload"].(map[string]any), StepRuns: stepRunsOf(t, parked),
	}
	svc.readRun = func(context.Context, int64) (*workflowmodels.WorkflowRunRecord, error) { return stored, nil }
	svc.loadFlowVersion = func(context.Context, string, int) (*workflowmodels.FlowRecord, error) { return &flow, nil }
	svc.loadFlowActions = func(context.Context, int64) ([]workflowmodels.WorkflowActionRecord, error) {
		return []workflowmodels.WorkflowActionRecord{
			{Sequence: 10, StepKey: "a", ActionCode: "step_a", ActionVersion: 1, RetryPolicy: workflowmodels.RetryPolicy{MaxAttempts: 2}},
			{Sequence: 20, StepKey: "b", ActionCode: "step_b", ActionVersion: 1, RetryPolicy: workflowmodels.RetryPolicy{MaxAttempts: 1}},
			{Sequence: 30, StepKey: "join", ActionCode: "step_join", ActionVersion: 1, RetryPolicy: workflowmodels.RetryPolicy{MaxAttempts: 1}},
		}, nil
	}
	if err := svc.ResumeRun(context.Background(), 50); err != nil {
		t.Fatalf("ResumeRun: %v", err)
	}
	if rec.count("step_a") != 2 || rec.count("step_b") != 1 || rec.count("step_join") != 1 {
		t.Fatalf("calls after resume = %#v, want a=2 b=1 join=1", rec.calls)
	}
	last := rec.last()
	if last["status"] != workflowmodels.WorkflowRunStatusSucceeded {
		t.Fatalf("final status = %#v, want succeeded", last["status"])
	}
	if got := last["attempt_count"]; got != 4 {
		t.Fatalf("attempt_count = %#v, want 4 (a twice, b, join)", got)
	}
	for _, run := range stepRunsOf(t, last) {
		if run.StepKey == "a" && run.Status == workflowmodels.WorkflowRunStatusSucceeded && run.Attempt != 2 {
			t.Fatalf("a succeeded on attempt %d, want 2", run.Attempt)
		}
	}
}

func TestDAGPersistsAttemptsBeforeRunning(t *testing.T) {
	rec := &stepTestRecorder{}
	var seen orm.RecordMap
	svc := stepTestService(rec, []workflowmodels.WorkflowGraphEdge{{FromStepKey: "a", ToStepKey: "b"}}, map[string]ActionHandler{
		"step_a": func(_ context.Context, req ActionRequest) ([]byte, error) {
			seen = rec.last() // the write made just before the attempt
			return req.Payload, nil
		},
	})
	steps := []workflowmodels.Step{{Key: "a", Action: "step_a"}, {Key: "b", Action: "step_b"}}
	if err := svc.executeFlow(context.Background(), flowRuntime{flow: dagTestFlow("", 4), steps: steps}, []byte(`{}`)); err != nil {
		t.Fatalf("executeFlow: %v", err)
	}
	if seen["attempt_count"] != 1 {
		t.Fatalf("attempt_count before a ran = %#v, want 1", seen["attempt_count"])
	}
	if current, _ := seen["current_steps"].([]string); len(current) != 1 || current[0] != "a" {
		t.Fatalf("current_steps before a ran = %#v, want [a]", seen["current_steps"])
	}
	if got := rec.last()["attempt_count"]; got != 2 {
		t.Fatalf("final attempt_count = %#v, want 2", got)
	}
}

func TestDAGResumeDeadLettersAStepWhoseAttemptsWereInterrupted(t *testing.T) {
	rec := &stepTestRecorder{}
	svc := stepTestService(rec, []workflowmodels.WorkflowGraphEdge{{FromStepKey: "a", ToStepKey: "b"}}, nil)
	flow := dagTestFlow(workflowmodels.WorkflowDagFailurePolicyFailBranch, 4)
	// a's only attempt was persisted (attempt_count=1, current_steps=[a]) and the worker died.
	stored := &workflowmodels.WorkflowRunRecord{
		BaseModel: orm.BaseModel{ID: 50}, Name: "r", Topic: flow.Topic, FlowVersion: flow.Version,
		Status: workflowmodels.WorkflowRunStatusRetryWaiting, AttemptCount: 1, CurrentSteps: []string{"a"},
		OutputPayload: map[string]any{"x": 1},
	}
	svc.readRun = func(context.Context, int64) (*workflowmodels.WorkflowRunRecord, error) { return stored, nil }
	svc.loadFlowVersion = func(context.Context, string, int) (*workflowmodels.FlowRecord, error) { return &flow, nil }
	svc.loadFlowActions = func(context.Context, int64) ([]workflowmodels.WorkflowActionRecord, error) {
		return []workflowmodels.WorkflowActionRecord{
			{Sequence: 10, StepKey: "a", ActionCode: "step_a", ActionVersion: 1, RetryPolicy: workflowmodels.RetryPolicy{MaxAttempts: 1}},
			{Sequence: 20, StepKey: "b", ActionCode: "step_b", ActionVersion: 1},
		}, nil
	}
	err := svc.ResumeRun(context.Background(), 50)
	if err == nil {
		t.Fatalf("expected the exhausted step to fail the run")
	}
	if rec.count("step_a") != 0 || rec.count("step_b") != 0 {
		t.Fatalf("calls = %#v, want none (a has no attempts left, b depends on it)", rec.calls)
	}
	last := rec.last()
	if last["status"] != workflowmodels.WorkflowRunStatusDeadLetter {
		t.Fatalf("final status = %#v, want dead_letter", last["status"])
	}
	runs := stepRunsOf(t, last)
	if got := stepRunStatus(runs, "a"); got != workflowmodels.WorkflowRunStatusFailed {
		t.Fatalf("a step_run status = %q, want failed (interrupted)", got)
	}
	if got := last["attempt_count"]; got != 1 {
		t.Fatalf("attempt_count = %#v, want 1 (the interrupted attempt is not counted twice)", got)
	}
}

func TestStepHandlerPanicBecomesAStepError(t *testing.T) {
	panicky := map[string]ActionHandler{
		"step_a": func(context.Context, ActionRequest) ([]byte, error) { panic("nil map write") },
	}
	once := workflowmodels.RetryPolicy{MaxAttempts: 1}

	t.Run("linear", func(t *testing.T) {
		rec := &stepTestRecorder{}
		svc := stepTestService(rec, nil, panicky)
		flow := workflowmodels.FlowRecord{BaseModel: orm.BaseModel{ID: 1}, Topic: "panic", Version: 1, IsActive: true}
		err := svc.executeFlow(context.Background(), flowRuntime{flow: flow, steps: []workflowmodels.Step{{Key: "a", Action: "step_a", RetryPolicy: once}}}, []byte(`{}`))
		if !errors.Is(err, ErrStepPanicked) {
			t.Fatalf("err = %v, want ErrStepPanicked", err)
		}
		if got := rec.last()["status"]; got != workflowmodels.WorkflowRunStatusDeadLetter {
			t.Fatalf("final status = %#v, want dead_letter", got)
		}
	})
	t.Run("dag", func(t *testing.T) {
		rec := &stepTestRecorder{}
		svc := stepTestService(rec, dagTestEdges(), panicky)
		err := svc.executeFlow(context.Background(), flowRuntime{flow: dagTestFlow("", 4), steps: dagTestSteps()}, []byte(`{}`))
		if err == nil {
			t.Fatalf("expected the panicked step to fail the run")
		}
		runs := stepRunsOf(t, rec.last())
		var panicked bool
		for _, run := range runs {
			if run.StepKey == "a" && strings.Contains(run.Error, "panicked") {
				panicked = true
			}
		}
		if !panicked || rec.count("step_b") != 1 {
			t.Fatalf("want a failed with the panic and b still run; runs=%#v calls=%#v", runs, rec.calls)
		}
	})
}

func TestStepIdempotencyKeyIsStableAcrossAttempts(t *testing.T) {
	var mu sync.Mutex
	var keys, ctxKeys []string
	svc := &Service{
		createRun: func(context.Context, workflowmodels.WorkflowRunRecord) (*orm.InsertResult, error) {
			return &orm.InsertResult{InsertedID: int64(1), Acknowledged: true}, nil
		},
		updateRun: func(context.Context, orm.RecordMap, orm.RecordMap) (*orm.UpdateResult, error) {
			return &orm.UpdateResult{Acknowledged: true, MatchedCount: 1, ModifiedCount: 1}, nil
		},
		lookupAction: func(string, int) (ActionHandler, bool) {
			return func(ctx context.Context, req ActionRequest) ([]byte, error) {
				mu.Lock()
				defer mu.Unlock()
				keys = append(keys, req.IdempotencyKey)
				ctxKeys = append(ctxKeys, workflowmodels.WorkflowStepIdempotencyKeyFromContext(ctx))
				if len(keys) == 1 {
					return nil, errors.New("flaky")
				}
				return req.Payload, nil
			}, true
		},
		now:   func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		sleep: func(context.Context, time.Duration) error { return nil },
	}
	ctx := workflowmodels.ContextWithWorkflowRequestID(context.Background(), "req-56")
	// ExecuteSteps runs are unversioned, so the retry happens in place.
	err := svc.ExecuteSteps(ctx, "topic", []workflowmodels.Step{{Key: "post", Action: "post", RetryPolicy: workflowmodels.RetryPolicy{MaxAttempts: 2}}}, []byte(`{}`))
	if err != nil {
		t.Fatalf("ExecuteSteps: %v", err)
	}
	if len(keys) != 2 || keys[0] != "req-56:post" || keys[1] != keys[0] {
		t.Fatalf("idempotency keys = %#v, want req-56:post twice", keys)
	}
	if ctxKeys[0] != keys[0] || ctxKeys[1] != keys[0] {
		t.Fatalf("ctx idempotency keys = %#v, want %q", ctxKeys, keys[0])
	}
}

func TestStepSnapshotsAreCapped(t *testing.T) {
	big := map[string]any{"blob": strings.Repeat("x", maxStepSnapshotBytes)}
	small := map[string]any{"ok": true}
	values := orm.RecordMap{
		"status":         workflowmodels.WorkflowRunStatusRunning,
		"output_payload": big,
		"step_runs":      []workflowmodels.WorkflowStepRun{{StepKey: "s", InputSnapshot: small, OutputSnapshot: big}},
	}
	for _, mode := range []string{workflowmodels.WorkflowAuditPayloadFull, workflowmodels.WorkflowAuditPayloadHash} {
		ctx := contextWithAuditPolicy(context.Background(), newAuditPolicy(mode, ""))
		stored := auditRunValues(ctx, values)
		run := stored["step_runs"].([]workflowmodels.WorkflowStepRun)[0]
		if run.InputSnapshot["ok"] != true && mode == workflowmodels.WorkflowAuditPayloadFull {
			t.Fatalf("small snapshot changed: %#v", run.InputSnapshot)
		}
		if mode == workflowmodels.WorkflowAuditPayloadFull {
			if run.OutputSnapshot[snapshotTruncatedKey] != true || run.OutputSnapshot[snapshotBytesKey].(int) <= maxStepSnapshotBytes {
				t.Fatalf("big snapshot not truncated: %d keys", len(run.OutputSnapshot))
			}
			if digest, _ := run.OutputSnapshot[snapshotDigestKey].(string); len(digest) != 64 {
				t.Fatalf("digest = %q", digest)
			}
		}
		// The resume checkpoint stays complete while the run can resume.
		if out := stored["output_payload"].(map[string]any); out["blob"] == nil {
			t.Fatalf("%s: output_payload was truncated", mode)
		}
		// Writing the stored rows again (markRun re-sends step_runs) keeps them as they are.
		again := auditRunValues(ctx, orm.RecordMap{"step_runs": stored["step_runs"]})
		rerun := again["step_runs"].([]workflowmodels.WorkflowStepRun)[0]
		if len(rerun.OutputSnapshot) != len(run.OutputSnapshot) || rerun.OutputSnapshot[snapshotDigestKey] != run.OutputSnapshot[snapshotDigestKey] ||
			rerun.OutputSnapshot[auditMarkerKey] != run.OutputSnapshot[auditMarkerKey] {
			t.Fatalf("%s: second write changed the snapshot: %#v -> %#v", mode, run.OutputSnapshot, rerun.OutputSnapshot)
		}
	}
	// A stored truncated snapshot is kept as is by every mode (it holds no payload any more).
	truncated := capStepSnapshot(big)
	for _, mode := range []string{workflowmodels.WorkflowAuditPayloadHash, workflowmodels.WorkflowAuditPayloadNone} {
		if got := newAuditPolicy(mode, "").apply(truncated); got[snapshotDigestKey] != truncated[snapshotDigestKey] {
			t.Fatalf("%s re-encoded a truncated snapshot: %#v", mode, got)
		}
	}
	// A caller payload with extra keys next to "_truncated" is not mistaken for a stored one.
	spoof := map[string]any{snapshotTruncatedKey: true, snapshotBytesKey: 1, snapshotDigestKey: strings.Repeat("a", 64), "pan": "4111111111111111"}
	if got := newAuditPolicy(workflowmodels.WorkflowAuditPayloadFull, "").apply(spoof); got["pan"] != auditRedactedValue {
		t.Fatalf("spoofed truncation marker skipped redaction: %#v", got)
	}
}

func TestUnknownConditionOperatorIsAnError(t *testing.T) {
	_, err := evaluateWorkflowCondition(map[string]any{"field": "amount", "greater_than": 100}, map[string]any{"amount": 5})
	if !errors.Is(err, ErrUnknownConditionOperator) {
		t.Fatalf("err = %v, want ErrUnknownConditionOperator", err)
	}
	// Known operators still work, including nested in all/any/not.
	matched, err := evaluateWorkflowCondition(map[string]any{"all": []any{
		map[string]any{"field": "amount", "equals": 5},
		map[string]any{"not": map[string]any{"field": "status", "in": []any{"void"}}},
	}}, map[string]any{"amount": 5, "status": "ok"})
	if err != nil || !matched {
		t.Fatalf("matched=%v err=%v, want true", matched, err)
	}
	if _, err := evaluateWorkflowCondition(map[string]any{"any": []any{map[string]any{"field": "a", "equal": 1}}}, map[string]any{}); !errors.Is(err, ErrUnknownConditionOperator) {
		t.Fatalf("nested unknown operator: err = %v", err)
	}
}

func TestDAGResumeDoesNotRerunAnExhaustedStep(t *testing.T) {
	rec := &stepTestRecorder{}
	edges := []workflowmodels.WorkflowGraphEdge{{FromStepKey: "a", ToStepKey: "handler", EdgeType: workflowmodels.WorkflowEdgeTypeFailure}}
	svc := stepTestService(rec, edges, nil)
	flow := dagTestFlow(workflowmodels.WorkflowDagFailurePolicyFailBranch, 4)
	// a used its last attempt (no next_retry_at) before the worker died; handler never ran.
	stored := &workflowmodels.WorkflowRunRecord{
		BaseModel: orm.BaseModel{ID: 50}, Name: "r", Topic: flow.Topic, FlowVersion: flow.Version,
		Status: workflowmodels.WorkflowRunStatusRetryWaiting, AttemptCount: 1,
		OutputPayload: map[string]any{"x": 1},
		StepRuns:      []workflowmodels.WorkflowStepRun{{StepKey: "a", Attempt: 1, Status: workflowmodels.WorkflowRunStatusFailed}},
	}
	svc.readRun = func(context.Context, int64) (*workflowmodels.WorkflowRunRecord, error) { return stored, nil }
	svc.loadFlowVersion = func(context.Context, string, int) (*workflowmodels.FlowRecord, error) { return &flow, nil }
	svc.loadFlowActions = func(context.Context, int64) ([]workflowmodels.WorkflowActionRecord, error) {
		return []workflowmodels.WorkflowActionRecord{
			{Sequence: 10, StepKey: "a", ActionCode: "step_a", ActionVersion: 1, RetryPolicy: workflowmodels.RetryPolicy{MaxAttempts: 3}},
			{Sequence: 20, StepKey: "handler", ActionCode: "step_handler", ActionVersion: 1},
		}, nil
	}
	if err := svc.ResumeRun(context.Background(), 50); err != nil {
		t.Fatalf("ResumeRun: %v", err)
	}
	if rec.count("step_a") != 0 || rec.count("step_handler") != 1 {
		t.Fatalf("calls = %#v, want a=0 (terminal) handler=1", rec.calls)
	}
	if got := rec.last()["status"]; got != workflowmodels.WorkflowRunStatusSucceeded {
		t.Fatalf("final status = %#v, want succeeded (failure handled by its edge)", got)
	}
}

func TestResumeDueRunsRecoversAPanickingRun(t *testing.T) {
	svc := &Service{
		claimDueRuns: func(context.Context, int64, int64) ([]int64, error) { return []int64{7, 8}, nil },
		readRun: func(_ context.Context, id int64) (*workflowmodels.WorkflowRunRecord, error) {
			if id == 7 {
				panic("corrupt row")
			}
			return nil, nil // run 8: not found, an ordinary error
		},
		now: func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	}
	resumed, err := svc.ResumeDueRuns(context.Background(), 10)
	if err == nil || !strings.Contains(err.Error(), "7: panic: corrupt row") || !strings.Contains(err.Error(), "8: ") {
		t.Fatalf("err = %v, want run 7 reported as a panic and run 8 as not found", err)
	}
	if resumed != 0 {
		t.Fatalf("resumed = %d, want 0", resumed)
	}
}
