package engine

// File overview:
// - Responsibility: plan T103 engine behaviour: non-retryable step errors, on_error_step_key,
//   sub-workflow calls, for-each and wait/approval.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"

	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
	"git.thebase.vn/base_hub/modules/base-workflow/service/nodes"
)

func TestNonRetryableStepErrorEndsTheStepAtOnce(t *testing.T) {
	rec := &stepTestRecorder{}
	svc := stepTestService(rec, nil, map[string]ActionHandler{
		"verify": func(context.Context, ActionRequest) ([]byte, error) {
			return nil, fmt.Errorf("bad signature: %w", workflowmodels.ErrWorkflowStepNonRetryable)
		},
	})
	// A versioned flow hands retries to the cron; the non-retryable error must not park the run.
	flow := workflowmodels.FlowRecord{Topic: "t", Version: 1, IsActive: true}
	steps := []workflowmodels.Step{{Key: "v", Action: "verify", RetryPolicy: workflowmodels.RetryPolicy{MaxAttempts: 3}}}
	err := svc.executeFlow(context.Background(), flowRuntime{flow: flow, steps: steps}, []byte(`{}`))
	if err == nil || isRunRetryScheduled(err) {
		t.Fatalf("err = %v, want a terminal step error", err)
	}
	if rec.count("verify") != 1 {
		t.Fatalf("handler ran %d times, want 1", rec.count("verify"))
	}
	last := rec.last()
	if last["status"] != workflowmodels.WorkflowRunStatusDeadLetter || last["next_retry_at"] != int64(0) {
		t.Fatalf("final update = %#v", last)
	}
	if runs := stepRunsOf(t, last); len(runs) != 1 || runs[0].NextRetryAt != 0 {
		t.Fatalf("step runs = %#v", runs)
	}
}

func TestOnErrorStepKeyJumpsToHandlerAndSucceeds(t *testing.T) {
	rec := &stepTestRecorder{}
	var handlerInput map[string]any
	svc := stepTestService(rec, nil, map[string]ActionHandler{
		"boom": func(context.Context, ActionRequest) ([]byte, error) { return nil, errors.New("kaput") },
		"handler": func(_ context.Context, req ActionRequest) ([]byte, error) {
			_ = json.Unmarshal(req.Payload, &handlerInput)
			return req.Payload, nil
		},
	})
	once := workflowmodels.RetryPolicy{MaxAttempts: 1}
	steps := []workflowmodels.Step{
		{Key: "a", Action: "boom", RetryPolicy: once, OnErrorStepKey: "h"},
		{Key: "skipped_by_jump", Action: "middle"},
		{Key: "h", Action: "handler"},
	}
	flow := workflowmodels.FlowRecord{Topic: "t", Version: 1, IsActive: true}
	if err := svc.executeFlow(context.Background(), flowRuntime{flow: flow, steps: steps}, []byte(`{"x":1}`)); err != nil {
		t.Fatalf("executeFlow: %v", err)
	}
	if rec.count("middle") != 0 || rec.count("handler") != 1 {
		t.Fatalf("calls = %#v", rec.calls)
	}
	errObj, _ := handlerInput["_error"].(map[string]any)
	if handlerInput["x"] != 1.0 || errObj["step_key"] != "a" || errObj["message"] != "kaput" {
		t.Fatalf("handler input = %#v", handlerInput)
	}
	last := rec.last()
	if last["status"] != workflowmodels.WorkflowRunStatusSucceeded {
		t.Fatalf("final status = %#v", last["status"])
	}
	if got := stepRunStatus(stepRunsOf(t, last), "a"); got != workflowmodels.WorkflowRunStatusFailed {
		t.Fatalf("failed step row = %q", got)
	}
}

func TestOnErrorStepKeyIgnoresBackwardTarget(t *testing.T) {
	rec := &stepTestRecorder{}
	svc := stepTestService(rec, nil, map[string]ActionHandler{
		"boom": func(context.Context, ActionRequest) ([]byte, error) { return nil, errors.New("kaput") },
	})
	once := workflowmodels.RetryPolicy{MaxAttempts: 1}
	steps := []workflowmodels.Step{
		{Key: "first", Action: "first"},
		{Key: "b", Action: "boom", RetryPolicy: once, OnErrorStepKey: "first"},
	}
	flow := workflowmodels.FlowRecord{Topic: "t", Version: 1, IsActive: true}
	if err := svc.executeFlow(context.Background(), flowRuntime{flow: flow, steps: steps}, []byte(`{}`)); err == nil {
		t.Fatal("a backward target must not loop or succeed")
	}
	if rec.count("first") != 1 || rec.last()["status"] != workflowmodels.WorkflowRunStatusDeadLetter {
		t.Fatalf("calls = %#v status = %#v", rec.calls, rec.last()["status"])
	}
}

func TestResolvedStepCarriesOnErrorStepKey(t *testing.T) {
	step := workflowmodels.WorkflowActionRecord{StepKey: "a", ExtraParams: map[string]any{workflowmodels.StepOnErrorParam: " h "}}.ResolvedStep()
	if step.OnErrorStepKey != "h" {
		t.Fatalf("OnErrorStepKey = %q", step.OnErrorStepKey)
	}
	encoded, _ := json.Marshal(step)
	if !strings.Contains(string(encoded), `"on_error_step_key":"h"`) {
		t.Fatalf("snapshot json lacks the key: %s", encoded)
	}
}

// callTestEngine is a Service double holding several published workflows (id -> steps) whose
// "call:<id>" steps call another workflow the way call_workflow@v1 does.
type callTestEngine struct {
	mu       sync.Mutex
	nextID   int64
	created  map[int64]workflowmodels.WorkflowRunRecord
	statuses map[int64]string
	outputs  map[int64]map[string]any
}

func newCallTestService(t *testing.T, eng *callTestEngine, flows map[int64][]workflowmodels.Step) *Service {
	t.Helper()
	eng.created, eng.statuses, eng.outputs = map[int64]workflowmodels.WorkflowRunRecord{}, map[int64]string{}, map[int64]map[string]any{}
	var svc *Service
	svc = &Service{
		loadFlowByID: func(_ context.Context, id int64) (*workflowmodels.FlowRecord, error) {
			steps, ok := flows[id]
			if !ok {
				return nil, ErrFlowNotFound
			}
			snapshot := versionTestSnapshotMap(t, &workflowmodels.WorkflowSnapshot{Version: 1, Steps: steps})
			return &workflowmodels.FlowRecord{BaseModel: orm.BaseModel{ID: id}, Name: fmt.Sprintf("wf%d", id), Topic: fmt.Sprintf("wf%d", id), IsActive: true, Version: 1, State: workflowmodels.WorkflowStatePublished, PublishedSnapshot: snapshot}, nil
		},
		createRun: func(_ context.Context, record workflowmodels.WorkflowRunRecord) (*orm.InsertResult, error) {
			eng.mu.Lock()
			defer eng.mu.Unlock()
			eng.nextID++
			eng.created[eng.nextID] = record
			return &orm.InsertResult{InsertedID: eng.nextID, Acknowledged: true}, nil
		},
		updateRun: func(_ context.Context, filter orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			eng.mu.Lock()
			defer eng.mu.Unlock()
			if status, ok := values["status"].(string); ok {
				eng.statuses[filter["id"].(int64)] = status
			}
			if output, ok := values["output_payload"].(map[string]any); ok {
				eng.outputs[filter["id"].(int64)] = output
			}
			return &orm.UpdateResult{Acknowledged: true, MatchedCount: 1, ModifiedCount: 1}, nil
		},
		lookupAction: func(name string, _ int) (ActionHandler, bool) {
			return func(ctx context.Context, req ActionRequest) ([]byte, error) {
				switch {
				case strings.HasPrefix(name, "call:"):
					var target int64
					fmt.Sscan(strings.TrimPrefix(name, "call:"), &target)
					return svc.CallWorkflow(ctx, target, req.Payload, "call")
				case name == "mark":
					var object map[string]any
					_ = json.Unmarshal(req.Payload, &object)
					object["marked_by_"+req.Step.Key] = true
					return json.Marshal(object)
				case name == "fail":
					return nil, errors.New("child failed")
				}
				return req.Payload, nil
			}, true
		},
		renewLease: func(context.Context, int64, string) (bool, error) { return true, nil },
		takeLease:  func(context.Context, int64, string) (bool, error) { return true, nil },
		now:        func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		sleep:      func(context.Context, time.Duration) error { return nil },
	}
	return svc
}

func callStep(key string, target int64) workflowmodels.Step {
	return workflowmodels.Step{Key: key, Action: fmt.Sprintf("call:%d", target), RetryPolicy: workflowmodels.RetryPolicy{MaxAttempts: 1}}
}

func runCallTestFlow(svc *Service, id int64) error {
	flow, err := svc.loadFlowByID(context.Background(), id)
	if err != nil {
		return err
	}
	runtime, err := svc.withDefaults().publishedRuntime(context.Background(), flow)
	if err != nil {
		return err
	}
	return svc.executeFlow(workflowmodels.ContextWithWorkflowRequestID(context.Background(), "root"), runtime, []byte(`{"in":1}`))
}

func TestCallWorkflowRunsChildAndUsesItsOutput(t *testing.T) {
	eng := &callTestEngine{}
	svc := newCallTestService(t, eng, map[int64][]workflowmodels.Step{
		1: {callStep("c", 2), {Key: "after", Action: "mark"}},
		2: {{Key: "child", Action: "mark"}},
	})
	if err := runCallTestFlow(svc, 1); err != nil {
		t.Fatalf("run: %v", err)
	}
	parent, child := eng.created[1], eng.created[2]
	if child.ParentRunID == nil || *child.ParentRunID != 1 || !child.CallerBound || child.WorkflowID == nil || *child.WorkflowID != 2 {
		t.Fatalf("child run = %+v", child)
	}
	if !strings.HasPrefix(child.Name, "root:c:call:") || parent.ParentRunID != nil {
		t.Fatalf("names: parent %q child %q", parent.Name, child.Name)
	}
	if eng.statuses[1] != workflowmodels.WorkflowRunStatusSucceeded || eng.statuses[2] != workflowmodels.WorkflowRunStatusSucceeded {
		t.Fatalf("statuses = %v", eng.statuses)
	}
	if out := eng.outputs[1]; out["marked_by_child"] != true || out["marked_by_after"] != true || out["in"] != 1.0 {
		t.Fatalf("parent output = %v", out)
	}
}

func TestCallWorkflowChildFailureFailsTheStep(t *testing.T) {
	eng := &callTestEngine{}
	svc := newCallTestService(t, eng, map[int64][]workflowmodels.Step{
		1: {callStep("c", 2)},
		2: {{Key: "boom", Action: "fail", RetryPolicy: workflowmodels.RetryPolicy{MaxAttempts: 2}}},
	})
	err := runCallTestFlow(svc, 1)
	if err == nil || !strings.Contains(err.Error(), "child failed") {
		t.Fatalf("err = %v", err)
	}
	// The child is retried in place, then ends failed (never dead_letter: its caller owns retries).
	if eng.statuses[2] != workflowmodels.WorkflowRunStatusFailed {
		t.Fatalf("child status = %q", eng.statuses[2])
	}
}

func TestCallWorkflowRefusesRecursionAndDepth(t *testing.T) {
	eng := &callTestEngine{}
	svc := newCallTestService(t, eng, map[int64][]workflowmodels.Step{
		1: {callStep("c", 2)},
		2: {callStep("back", 1)},
	})
	err := runCallTestFlow(svc, 1)
	if !errors.Is(err, ErrWorkflowCallRefused) || !errors.Is(err, workflowmodels.ErrWorkflowStepNonRetryable) {
		t.Fatalf("recursion err = %v", err)
	}

	chain := map[int64][]workflowmodels.Step{}
	for id := int64(1); id <= 8; id++ {
		chain[id] = []workflowmodels.Step{callStep("next", id+1)}
	}
	chain[9] = []workflowmodels.Step{{Key: "leaf", Action: "mark"}}
	eng = &callTestEngine{}
	svc = newCallTestService(t, eng, chain)
	err = runCallTestFlow(svc, 1)
	if !errors.Is(err, ErrWorkflowCallRefused) || !strings.Contains(err.Error(), "nested") {
		t.Fatalf("depth err = %v", err)
	}
	// Root + 5 nested children ran; the 6th nested call was refused.
	if len(eng.created) != 1+maxWorkflowCallDepth {
		t.Fatalf("runs created = %d", len(eng.created))
	}
}

func TestChildCallContextStopCauses(t *testing.T) {
	parent, cancel := context.WithCancelCause(context.Background())
	child, stop := childCallContext(parent, workflowCallFrame{}, "x")
	defer stop()
	cancel(fmt.Errorf("%w: run 1", ErrRunCancelled))
	<-child.Done()
	if !errors.Is(runStopCause(child), ErrRunCancelled) {
		t.Fatalf("cancel cause = %v", context.Cause(child))
	}

	parent, cancelTimeout := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancelTimeout()
	child, stop = childCallContext(parent, workflowCallFrame{}, "x")
	defer stop()
	<-child.Done()
	if !errors.Is(runStopCause(child), ErrRunTimedOut) {
		t.Fatalf("deadline cause = %v", context.Cause(child))
	}
}

// approvalTestEngine keeps one run row in memory so a parked run can be read back and decided.
type approvalTestEngine struct {
	mu     sync.Mutex
	row    orm.RecordMap
	calls  map[string]int
	inputs map[string]map[string]any
	// uids is the user each step handler ran as; claims counts won claims.
	uids   map[string]int64
	claims int
}

func (e *approvalTestEngine) record() *workflowmodels.WorkflowRunRecord {
	e.mu.Lock()
	defer e.mu.Unlock()
	encoded, _ := json.Marshal(e.row)
	var run workflowmodels.WorkflowRunRecord
	_ = json.Unmarshal(encoded, &run)
	run.ID = 50
	return &run
}

func newApprovalTestService(t *testing.T, eng *approvalTestEngine, steps []workflowmodels.Step) *Service {
	t.Helper()
	eng.calls, eng.inputs, eng.uids = map[string]int{}, map[string]map[string]any{}, map[string]int64{}
	snapshot := versionTestSnapshotMap(t, &workflowmodels.WorkflowSnapshot{Version: 1, Steps: steps})
	flow := &workflowmodels.FlowRecord{BaseModel: orm.BaseModel{ID: 9}, Topic: "appr", IsActive: true, Version: 1, State: workflowmodels.WorkflowStatePublished, PublishedSnapshot: snapshot}
	return &Service{
		loadActiveFlow: func(context.Context, string) (*workflowmodels.FlowRecord, error) { return flow, nil },
		// A decision loads the run's workflow by workflow_id (loadRunFlow).
		loadFlowByID: func(context.Context, int64) (*workflowmodels.FlowRecord, error) { return flow, nil },
		createRun: func(_ context.Context, record workflowmodels.WorkflowRunRecord) (*orm.InsertResult, error) {
			eng.mu.Lock()
			defer eng.mu.Unlock()
			eng.row = recordToMap(record)
			return &orm.InsertResult{InsertedID: int64(50), Acknowledged: true}, nil
		},
		updateRun: func(_ context.Context, _ orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			eng.mu.Lock()
			defer eng.mu.Unlock()
			for k, v := range values {
				eng.row[k] = v
			}
			return &orm.UpdateResult{Acknowledged: true, MatchedCount: 1, ModifiedCount: 1}, nil
		},
		readRun: func(context.Context, int64) (*workflowmodels.WorkflowRunRecord, error) { return eng.record(), nil },
		claimApproval: func(_ context.Context, _ int64, owner, tokenHash string, decision orm.RecordMap) (bool, error) {
			eng.mu.Lock()
			defer eng.mu.Unlock()
			if eng.row["status"] != workflowmodels.WorkflowRunStatusWaitingApproval || (tokenHash != "" && eng.row["resume_token_hash"] != tokenHash) ||
				fmt.Sprint(eng.row["current_step"]) != fmt.Sprint(decision["expect_current_step"]) || fmt.Sprint(eng.row["attempt_count"]) != fmt.Sprint(decision["expect_attempt_count"]) {
				return false, nil
			}
			eng.claims++
			for k, v := range decision {
				if !strings.HasPrefix(k, "expect_") {
					eng.row[k] = v
				}
			}
			eng.row["resume_token_hash"], eng.row["approval_expires_at"], eng.row["lease_owner"] = "", int64(0), ""
			if decision["status"] == workflowmodels.WorkflowRunStatusPending {
				eng.row["lease_owner"] = owner
			}
			return true, nil
		},
		releaseApproval: func(_ context.Context, _ int64, owner string, restore orm.RecordMap) (bool, error) {
			eng.mu.Lock()
			defer eng.mu.Unlock()
			if eng.row["status"] != workflowmodels.WorkflowRunStatusPending || eng.row["lease_owner"] != owner {
				return false, nil
			}
			for k, v := range restore {
				eng.row[k] = v
			}
			eng.row["resume_token_hash"], eng.row["status"], eng.row["lease_owner"] = restore["token_hash"], workflowmodels.WorkflowRunStatusWaitingApproval, ""
			return true, nil
		},
		creatorContext: func(ctx context.Context, uid int64) (context.Context, error) {
			return orm.ContextWithEnvironment(ctx, orm.NewEnvironmentWithDBName(nil, "appr", uid, nil, false)), nil
		},
		lookupAction: func(name string, _ int) (ActionHandler, bool) {
			if name == workflowmodels.WaitApprovalActionCode {
				return func(context.Context, ActionRequest) ([]byte, error) {
					t.Error("the wait_approval handler ran in a real run")
					return nil, errors.New("unexpected")
				}, true
			}
			return func(ctx context.Context, req ActionRequest) ([]byte, error) {
				eng.mu.Lock()
				eng.calls[req.Step.Key]++
				if env := orm.EnvironmentFromContext(ctx); env != nil {
					eng.uids[req.Step.Key] = env.UserID()
				}
				var in map[string]any
				_ = json.Unmarshal(req.Payload, &in)
				eng.inputs[req.Step.Key] = in
				eng.mu.Unlock()
				return req.Payload, nil
			}, true
		},
		renewLease: func(context.Context, int64, string) (bool, error) { return true, nil },
		takeLease:  func(context.Context, int64, string) (bool, error) { return true, nil },
		now:        func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		sleep:      func(context.Context, time.Duration) error { return nil },
	}
}

func approvalSteps(onError string) []workflowmodels.Step {
	return []workflowmodels.Step{
		{Key: "before", Action: "echo"},
		{Key: "wait", Action: workflowmodels.WaitApprovalActionCode, ActionVersion: 1, OnErrorStepKey: onError, Params: map[string]any{"expires_in_seconds": 60}},
		{Key: "after", Action: "echo"},
		{Key: "on_reject", Action: "echo"},
	}
}

// parkApprovalRun starts a run that parks at "wait" and returns its pending token.
func parkApprovalRun(t *testing.T, svc *Service, eng *approvalTestEngine) *workflowmodels.WorkflowApprovalPending {
	t.Helper()
	return parkApprovalRunAs(t, svc, eng, context.Background())
}

// parkApprovalRunAs parks the run under ctx's identity (a non-sudo env makes it caller-bound).
func parkApprovalRunAs(t *testing.T, svc *Service, eng *approvalTestEngine, ctx context.Context) *workflowmodels.WorkflowApprovalPending {
	t.Helper()
	flow, _ := svc.loadActiveFlow(ctx, "appr")
	runtime, err := svc.withDefaults().publishedRuntime(ctx, flow)
	if err != nil {
		t.Fatal(err)
	}
	err = svc.executeFlow(workflowmodels.ContextWithWorkflowRequestID(ctx, "run-a"), runtime, []byte(`{"amount":5}`))
	var pending *workflowmodels.WorkflowApprovalPending
	if !errors.As(err, &pending) || !isRunRetryScheduled(err) {
		t.Fatalf("park err = %v, want an accepted approval wait", err)
	}
	row := eng.record()
	if row.Status != workflowmodels.WorkflowRunStatusWaitingApproval || row.CurrentStep != "wait" || row.LeaseOwner != "" ||
		row.ResumeTokenHash != workflowmodels.HashWorkflowResumeToken(pending.Token) || row.ApprovalExpiresAt != 1_700_000_060 || len(pending.Token) < 40 {
		t.Fatalf("parked row = %+v pending = %+v", row, pending)
	}
	if eng.calls["after"] != 0 {
		t.Fatal("a step after the wait ran before approval")
	}
	return pending
}

func TestApprovalParksAndApproveContinues(t *testing.T) {
	eng := &approvalTestEngine{}
	svc := newApprovalTestService(t, eng, approvalSteps(""))
	pending := parkApprovalRun(t, svc, eng)

	if _, err := svc.DecideApproval(context.Background(), 50, workflowmodels.WorkflowApprovalDecision{Approved: true, TokenHash: workflowmodels.HashWorkflowResumeToken("wrong")}); err == nil || !strings.Contains(err.Error(), "invalid resume token") {
		t.Fatalf("wrong token err = %v", err)
	}
	result, err := svc.DecideApproval(context.Background(), 50, workflowmodels.WorkflowApprovalDecision{Approved: true, By: 2, Comment: "ok", TokenHash: workflowmodels.HashWorkflowResumeToken(pending.Token)})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	// A sudo-started run is handed to resume_due_runs at once (plan T103 review).
	if result["status"] != workflowmodels.WorkflowRunStatusRetryWaiting || eng.calls["after"] != 0 {
		t.Fatalf("result = %v calls = %v", result, eng.calls)
	}
	if err := svc.ResumeRun(context.Background(), 50); err != nil {
		t.Fatalf("resume after approval: %v", err)
	}
	if row := eng.record(); row.Status != workflowmodels.WorkflowRunStatusSucceeded {
		t.Fatalf("status after resume = %s", row.Status)
	}
	approval, _ := eng.inputs["after"]["_approval"].(map[string]any)
	if approval["approved"] != true || approval["by"] != 2.0 || approval["comment"] != "ok" || eng.inputs["after"]["amount"] != 5.0 {
		t.Fatalf("after input = %v", eng.inputs["after"])
	}
	// The wait step keeps its waiting row and gets a succeeded decision row.
	runs := eng.record().StepRuns
	if stepRunStatus(runs, "wait") != workflowmodels.WorkflowRunStatusSucceeded || runs[1].Status != workflowmodels.WorkflowRunStatusWaitingApproval {
		t.Fatalf("step runs = %+v", runs)
	}
	// Deciding again finds nothing to decide.
	if _, err := svc.DecideApproval(context.Background(), 50, workflowmodels.WorkflowApprovalDecision{Approved: true}); err == nil {
		t.Fatal("a finished run was decided again")
	}
}

func TestApprovalRejectFailsOrFollowsOnError(t *testing.T) {
	eng := &approvalTestEngine{}
	svc := newApprovalTestService(t, eng, approvalSteps(""))
	parkApprovalRun(t, svc, eng)
	result, err := svc.DecideApproval(context.Background(), 50, workflowmodels.WorkflowApprovalDecision{Approved: false, By: 3, Comment: "no", Author: true})
	if err != nil || result["status"] != workflowmodels.WorkflowRunStatusFailed || !strings.Contains(result["last_error"].(string), "rejected") {
		t.Fatalf("reject = %v, %v", result, err)
	}
	if eng.calls["after"] != 0 || eng.calls["on_reject"] != 0 {
		t.Fatalf("calls after reject = %v", eng.calls)
	}

	eng = &approvalTestEngine{}
	svc = newApprovalTestService(t, eng, approvalSteps("on_reject"))
	parkApprovalRun(t, svc, eng)
	result, err = svc.DecideApproval(context.Background(), 50, workflowmodels.WorkflowApprovalDecision{Approved: false, By: 3, Comment: "no", Author: true})
	if err != nil || result["status"] != workflowmodels.WorkflowRunStatusRetryWaiting || eng.record().CurrentStep != "on_reject" {
		t.Fatalf("reject with on_error = %v, %v", result, err)
	}
	if err := svc.ResumeRun(context.Background(), 50); err != nil || eng.record().Status != workflowmodels.WorkflowRunStatusSucceeded {
		t.Fatalf("resume at on_error target: %v status %s", err, eng.record().Status)
	}
	errObj, _ := eng.inputs["on_reject"]["_error"].(map[string]any)
	if eng.calls["after"] != 0 || eng.calls["on_reject"] != 1 || errObj["step_key"] != "wait" {
		t.Fatalf("calls = %v on_reject input = %v", eng.calls, eng.inputs["on_reject"])
	}
}

func TestApprovalRefusedWhenTheCallerIsBlocked(t *testing.T) {
	for name, ctx := range map[string]context.Context{
		"tcp":   workflowmodels.ContextWithWorkflowOutputSink(context.Background(), &workflowmodels.WorkflowOutput{}),
		"child": context.WithValue(context.Background(), workflowCallContextKey{}, workflowCallFrame{output: &workflowmodels.WorkflowOutput{}}),
	} {
		eng := &approvalTestEngine{}
		svc := newApprovalTestService(t, eng, approvalSteps(""))
		flow, _ := svc.loadActiveFlow(ctx, "appr")
		runtime, _ := svc.withDefaults().publishedRuntime(ctx, flow)
		err := svc.executeFlow(ctx, runtime, []byte(`{}`))
		if err == nil || !errors.Is(err, workflowmodels.ErrWorkflowStepNonRetryable) && !strings.Contains(err.Error(), "cannot wait") {
			t.Fatalf("%s: err = %v", name, err)
		}
		if row := eng.record(); row.Status != workflowmodels.WorkflowRunStatusFailed || eng.calls["after"] != 0 {
			t.Fatalf("%s: row status %s calls %v", name, row.Status, eng.calls)
		}
	}
}

func TestApprovalDryRunPassesThrough(t *testing.T) {
	out, err := nodes.WaitApprovalAction(workflowmodels.ContextWithWorkflowDryRun(context.Background()), nodes.WorkflowActionRequest{Payload: []byte(`{"a":1}`)})
	if err != nil || !strings.Contains(string(out), `"_approval"`) {
		t.Fatalf("dry run = %s, %v", out, err)
	}
	if _, err := nodes.WaitApprovalAction(context.Background(), nodes.WorkflowActionRequest{Payload: []byte(`{}`)}); !errors.Is(err, workflowmodels.ErrWorkflowStepNonRetryable) {
		t.Fatalf("DAG/handler call err = %v", err)
	}
}

func TestResumeDueRunsExpiresApprovalWaitsFirst(t *testing.T) {
	var order []string
	svc := &Service{
		expireApprovals: func(context.Context, int64) ([]workflowmodels.WorkflowExpiredApproval, error) {
			order = append(order, "expire")
			return []workflowmodels.WorkflowExpiredApproval{{ID: 4, TriggerType: "kafka"}}, nil
		},
		claimDueRuns: func(context.Context, int64, int64) ([]int64, error) {
			order = append(order, "claim")
			return nil, nil
		},
	}
	if _, err := svc.ResumeDueRuns(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "expire,claim" {
		t.Fatalf("order = %v", order)
	}
}

// Plan T103 review, finding 2: the decision is written by the claim itself, so a crash right
// after the claim (nothing else runs) still leaves the decision and the next step stored.
func TestApprovalDecisionIsDurableInTheClaim(t *testing.T) {
	eng := &approvalTestEngine{}
	svc := newApprovalTestService(t, eng, approvalSteps(""))
	pending := parkApprovalRun(t, svc, eng)
	parked := eng.record()
	result, err := svc.DecideApproval(context.Background(), 50, workflowmodels.WorkflowApprovalDecision{Approved: true, By: 2, Comment: "ok", TokenHash: workflowmodels.HashWorkflowResumeToken(pending.Token)})
	if err != nil || result["status"] != workflowmodels.WorkflowRunStatusRetryWaiting {
		t.Fatalf("approve = %v, %v", result, err)
	}
	row := eng.record()
	approval, _ := row.OutputPayload["_approval"].(map[string]any)
	if eng.claims != 1 || row.CurrentStep != "after" || row.NextRetryAt != 1_700_000_000 || row.LeaseOwner != "" || row.AdmissionPending ||
		approval["approved"] != true || row.AttemptCount != parked.AttemptCount+1 || stepRunStatus(row.StepRuns, "wait") != workflowmodels.WorkflowRunStatusSucceeded {
		t.Fatalf("row after claim = %+v", row)
	}
	if eng.calls["after"] != 0 {
		t.Fatal("a sudo run continued inside the approve request")
	}
}

// approveCallerBound parks a run started by user 7 (caller-bound) and returns its token.
func approveCallerBound(t *testing.T, svc *Service, eng *approvalTestEngine) string {
	t.Helper()
	starter := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironmentWithDBName(nil, "appr", 7, nil, false))
	pending := parkApprovalRunAs(t, svc, eng, starter)
	if row := eng.record(); !row.CallerBound || row.StartedByUID != 7 {
		t.Fatalf("parked row caller_bound %v started_by %d", row.CallerBound, row.StartedByUID)
	}
	return workflowmodels.HashWorkflowResumeToken(pending.Token)
}

// Findings 3b and 5: the starter cannot approve their own run (reject stays open), a test run
// needs base.group_system, and a caller-bound run continues as its starter, not the decider.
func TestApprovalSegregationAndStarterIdentity(t *testing.T) {
	eng := &approvalTestEngine{}
	svc := newApprovalTestService(t, eng, approvalSteps(""))
	token := approveCallerBound(t, svc, eng)
	decider := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironmentWithDBName(nil, "appr", 9, nil, false))
	if _, err := svc.DecideApproval(decider, 50, workflowmodels.WorkflowApprovalDecision{Approved: true, By: 7, TokenHash: token}); err == nil || !strings.Contains(err.Error(), "started by you") {
		t.Fatalf("self approval err = %v", err)
	}
	if eng.claims != 0 {
		t.Fatal("a refused decision claimed the run")
	}
	result, err := svc.DecideApproval(decider, 50, workflowmodels.WorkflowApprovalDecision{Approved: true, By: 9, TokenHash: token})
	if err != nil || result["status"] != workflowmodels.WorkflowRunStatusSucceeded {
		t.Fatalf("approve = %v, %v", result, err)
	}
	if eng.uids["after"] != 7 {
		t.Fatalf("after ran as user %d, want the starter 7", eng.uids["after"])
	}

	eng = &approvalTestEngine{}
	svc = newApprovalTestService(t, eng, approvalSteps(""))
	token = approveCallerBound(t, svc, eng)
	if _, err := svc.DecideApproval(decider, 50, workflowmodels.WorkflowApprovalDecision{Approved: false, By: 7, TokenHash: token}); err != nil {
		t.Fatalf("the starter could not reject: %v", err)
	}

	eng = &approvalTestEngine{}
	svc = newApprovalTestService(t, eng, approvalSteps(""))
	token = approveCallerBound(t, svc, eng)
	eng.row["flow_version"] = 0
	if _, err := svc.DecideApproval(decider, 50, workflowmodels.WorkflowApprovalDecision{Approved: true, By: 9, TokenHash: token}); err == nil || !strings.Contains(err.Error(), "test run") {
		t.Fatalf("operator approval of a test run err = %v", err)
	}
}

// Finding 4: a caller-bound decision goes through max_concurrent_runs; a refusal puts the wait
// back with the same token, expiry and checkpoint and answers 409.
func TestApprovalAdmissionRefusalRestoresTheWait(t *testing.T) {
	eng := &approvalTestEngine{}
	svc := newApprovalTestService(t, eng, approvalSteps(""))
	token := approveCallerBound(t, svc, eng)
	parked := eng.record()
	flow, _ := svc.loadFlowByID(context.Background(), 9)
	limited := *flow
	// The limit is pinned in the published snapshot (RuntimeFlow).
	limited.PublishedSnapshot = versionTestSnapshotMap(t, &workflowmodels.WorkflowSnapshot{Version: 1, Flow: workflowmodels.FlowRecord{MaxConcurrentRuns: 1}, Steps: approvalSteps("")})
	svc.loadFlowByID = func(context.Context, int64) (*workflowmodels.FlowRecord, error) { return &limited, nil }
	svc.admit = func(context.Context, runAdmission) (string, error) { return workflowmodels.WorkflowRunRejected, nil }
	decider := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironmentWithDBName(nil, "appr", 9, nil, false))
	_, err := svc.DecideApproval(decider, 50, workflowmodels.WorkflowApprovalDecision{Approved: true, By: 9, TokenHash: token})
	if err == nil || !strings.Contains(err.Error(), "concurrency limit") {
		t.Fatalf("err = %v", err)
	}
	row := eng.record()
	if row.Status != workflowmodels.WorkflowRunStatusWaitingApproval || row.ResumeTokenHash != token || row.ApprovalExpiresAt != parked.ApprovalExpiresAt ||
		row.CurrentStep != "wait" || row.AttemptCount != parked.AttemptCount || len(row.StepRuns) != len(parked.StepRuns) || eng.calls["after"] != 0 {
		t.Fatalf("restored row = %+v", row)
	}
	// The same token decides it once admission allows.
	svc.admit = func(context.Context, runAdmission) (string, error) { return workflowmodels.WorkflowRunAdmitted, nil }
	if result, err := svc.DecideApproval(decider, 50, workflowmodels.WorkflowApprovalDecision{Approved: true, By: 9, TokenHash: token}); err != nil || result["status"] != workflowmodels.WorkflowRunStatusSucceeded {
		t.Fatalf("approve after restore = %v, %v", result, err)
	}
}

// approverGroupSteps is approvalSteps with the wait's approver_group_id set to group (0 = none).
func approverGroupSteps(group int64) []workflowmodels.Step {
	steps := approvalSteps("")
	if group > 0 {
		steps[1].Params = map[string]any{"expires_in_seconds": 60, "approver_group_id": group}
	}
	return steps
}

// withApproverGroupMembers makes uids the members of every approver group and returns svc.
func withApproverGroupMembers(svc *Service, uids ...int64) *Service {
	svc.inApprovalGroup = func(ctx context.Context, _ int64) (bool, error) {
		env := orm.EnvironmentFromContext(ctx)
		return env != nil && slices.Contains(uids, env.UserID()), nil
	}
	return svc
}

func userCtx(uid int64) context.Context {
	return orm.ContextWithEnvironment(context.Background(), orm.NewEnvironmentWithDBName(nil, "appr", uid, nil, false))
}

// An operator in the run's approver group decides without the token; anyone else needs it, and
// the starter, even as a member, can only reject.
func TestApprovalGroupMemberDecidesWithoutToken(t *testing.T) {
	eng := &approvalTestEngine{}
	svc := withApproverGroupMembers(newApprovalTestService(t, eng, approverGroupSteps(12)), 7, 9)
	approveCallerBound(t, svc, eng)
	if row := eng.record(); row.ApprovalGroupID == nil || *row.ApprovalGroupID != 12 {
		t.Fatalf("parked approval_group_id = %v, want 12", row.ApprovalGroupID)
	}
	if _, err := svc.DecideApproval(userCtx(11), 50, workflowmodels.WorkflowApprovalDecision{Approved: true, By: 11}); err == nil || !strings.Contains(err.Error(), "approver group") {
		t.Fatalf("non-member without token err = %v", err)
	}
	if _, err := svc.DecideApproval(userCtx(7), 50, workflowmodels.WorkflowApprovalDecision{Approved: true, By: 7}); err == nil || !strings.Contains(err.Error(), "started by you") {
		t.Fatalf("starter member approval err = %v", err)
	}
	if eng.claims != 0 {
		t.Fatal("a refused decision claimed the run")
	}
	result, err := svc.DecideApproval(userCtx(9), 50, workflowmodels.WorkflowApprovalDecision{Approved: true, By: 9, Comment: "ok"})
	if err != nil || result["status"] != workflowmodels.WorkflowRunStatusSucceeded || eng.uids["after"] != 7 {
		t.Fatalf("member approve = %v, %v (after ran as %d)", result, err, eng.uids["after"])
	}

	eng = &approvalTestEngine{}
	svc = withApproverGroupMembers(newApprovalTestService(t, eng, approverGroupSteps(12)), 7)
	approveCallerBound(t, svc, eng)
	if result, err := svc.DecideApproval(userCtx(7), 50, workflowmodels.WorkflowApprovalDecision{Approved: false, By: 7}); err != nil || result["status"] != workflowmodels.WorkflowRunStatusFailed {
		t.Fatalf("starter member reject = %v, %v", result, err)
	}
}

// The approver group is the one stored on the parked run, not the current definition's.
func TestApprovalGroupIsReadFromTheRun(t *testing.T) {
	republish := func(svc *Service, steps []workflowmodels.Step) {
		flow, _ := svc.loadFlowByID(context.Background(), 9)
		changed := *flow
		changed.PublishedSnapshot = versionTestSnapshotMap(t, &workflowmodels.WorkflowSnapshot{Version: 2, Steps: steps})
		svc.loadFlowByID = func(context.Context, int64) (*workflowmodels.FlowRecord, error) { return &changed, nil }
		svc.loadActiveFlow = func(context.Context, string) (*workflowmodels.FlowRecord, error) { return &changed, nil }
	}
	// Parked without a group, republished with one: a member still needs the token.
	eng := &approvalTestEngine{}
	svc := withApproverGroupMembers(newApprovalTestService(t, eng, approverGroupSteps(0)), 9)
	approveCallerBound(t, svc, eng)
	if eng.record().ApprovalGroupID != nil {
		t.Fatalf("parked approval_group_id = %v, want none", *eng.record().ApprovalGroupID)
	}
	republish(svc, approverGroupSteps(12))
	if _, err := svc.DecideApproval(userCtx(9), 50, workflowmodels.WorkflowApprovalDecision{Approved: true, By: 9}); err == nil || !strings.Contains(err.Error(), "resume token") {
		t.Fatalf("group added after the park err = %v", err)
	}
	// Parked with a group, republished without it: the member decides.
	eng = &approvalTestEngine{}
	svc = withApproverGroupMembers(newApprovalTestService(t, eng, approverGroupSteps(12)), 9)
	approveCallerBound(t, svc, eng)
	republish(svc, approverGroupSteps(0))
	if _, err := svc.DecideApproval(userCtx(9), 50, workflowmodels.WorkflowApprovalDecision{Approved: false, By: 9}); err != nil {
		t.Fatalf("group removed after the park: %v", err)
	}
}

// Finding 1: every retry of a calling step starts a new child run (unique name) whose step
// idempotency keys stay the same.
func TestChildStepIdempotencyKeysAreStableAcrossRetries(t *testing.T) {
	step := workflowmodels.ContextWithWorkflowStepIdempotencyKey(workflowmodels.ContextWithWorkflowRequestID(context.Background(), "root"), "root:c")
	first, stop1 := childCallContext(step, workflowCallFrame{}, "item3")
	defer stop1()
	second, stop2 := childCallContext(step, workflowCallFrame{}, "item3")
	defer stop2()
	if workflowmodels.WorkflowRequestIDFromContext(first) == workflowmodels.WorkflowRequestIDFromContext(second) {
		t.Fatal("two children share a run name")
	}
	if a, b := stepIdempotencyKey(first, "post"), stepIdempotencyKey(second, "post"); a != "root:c:item3:post" || a != b {
		t.Fatalf("child keys = %q, %q", a, b)
	}
	if got := stepIdempotencyKey(step, "post"); got != "root:post" {
		t.Fatalf("parent key = %q", got)
	}
}

// Finding 6: a caller-bound call is refused (not retryable) when the caller is outside the target's group.
func TestCallWorkflowAppliesTargetGroupGate(t *testing.T) {
	eng := &callTestEngine{}
	svc := newCallTestService(t, eng, map[int64][]workflowmodels.Step{2: {{Key: "child", Action: "mark"}}})
	load := svc.loadFlowByID
	group := int64(7)
	svc.loadFlowByID = func(ctx context.Context, id int64) (*workflowmodels.FlowRecord, error) {
		flow, err := load(ctx, id)
		if flow != nil {
			flow.GroupID = &group
		}
		return flow, err
	}
	caller := func(groups ...int64) context.Context {
		return orm.ContextWithEnvironment(context.Background(), orm.NewEnvironmentWithDBName(nil, "db", 5, map[string]any{
			orm.RBACContextAuthModeKey: "bearer", orm.RBACContextGroupIDsKey: groups,
		}, false))
	}
	_, err := svc.CallWorkflow(caller(3), 2, []byte(`{}`), "call")
	if !errors.Is(err, ErrWorkflowCallRefused) || !errors.Is(err, workflowmodels.ErrWorkflowStepNonRetryable) || len(eng.created) != 0 {
		t.Fatalf("outside the group: err = %v runs %d", err, len(eng.created))
	}
	if _, err := svc.CallWorkflow(caller(7), 2, []byte(`{}`), "call"); err != nil {
		t.Fatalf("group member: %v", err)
	}
}

// Finding 7: engine-owned keys are stripped from trigger input, never from a replay-from-step input.
func TestStripReservedPayloadKeys(t *testing.T) {
	got := string(stripReservedPayloadKeys([]byte(`{"a":12345678901234567890,"_hmac_verified":true,"_approval":{"approved":true},"_error":1,"_for_each":1,"_dry_run":true,"_dry_run_intended":[]}`)))
	if got != `{"a":12345678901234567890}` {
		t.Fatalf("stripped = %s", got)
	}
	for _, input := range []string{`[1]`, `"x"`, `{"a":1}`} {
		if got := string(stripReservedPayloadKeys([]byte(input))); got != input {
			t.Fatalf("%s changed to %s", input, got)
		}
	}
	eng := &callTestEngine{}
	svc := newCallTestService(t, eng, map[int64][]workflowmodels.Step{1: {{Key: "s", Action: "mark"}}})
	flow, _ := svc.loadFlowByID(context.Background(), 1)
	runtime, _ := svc.withDefaults().publishedRuntime(context.Background(), flow)
	if _, err := svc.executeFlowFrom(context.Background(), runtime, []byte(`{"_approval":{"approved":true},"x":1}`), 0); err != nil {
		t.Fatal(err)
	}
	if _, spoofed := eng.created[1].InputPayload["_approval"]; spoofed {
		t.Fatalf("trigger input kept _approval: %v", eng.created[1].InputPayload)
	}
}

// Finding 3a: replay_run never hands out a resume token, and replaying a run that waited for
// approval needs base.group_system.
func TestReplayOfApprovalRunNeedsAuthorAndHidesToken(t *testing.T) {
	eng := &approvalTestEngine{}
	svc := newApprovalTestService(t, eng, approvalSteps(""))
	svc.readRun = func(_ context.Context, id int64) (*workflowmodels.WorkflowRunRecord, error) {
		if id == 50 {
			return eng.record(), nil
		}
		workflowID := int64(9)
		return &workflowmodels.WorkflowRunRecord{
			BaseModel: orm.BaseModel{ID: 9}, Name: "orig", Topic: "appr", WorkflowID: &workflowID, FlowVersion: 1,
			Status: workflowmodels.WorkflowRunStatusFailed, TriggerType: workflowmodels.WorkflowTriggerTypeKafka,
			AuditMode: workflowmodels.WorkflowAuditPayloadFull, InputPayload: map[string]any{"amount": 5.0},
			StepRuns: []workflowmodels.WorkflowStepRun{{StepKey: "wait", Action: workflowmodels.WaitApprovalActionCode, Status: workflowmodels.WorkflowRunStatusFailed}},
		}, nil
	}
	prev := ensureWorkflowAuthor
	t.Cleanup(func() { ensureWorkflowAuthor = prev })
	ensureWorkflowAuthor = func(context.Context) error { return orm.Forbiddenf("not system") }
	if _, err := svc.ReplayRun(context.Background(), 9, ""); err == nil || !strings.Contains(err.Error(), "only base.group_system") || eng.row != nil {
		t.Fatalf("operator replay err = %v row %v", err, eng.row)
	}
	ensureWorkflowAuthor = func(context.Context) error { return nil }
	result, err := svc.ReplayRun(context.Background(), 9, "")
	if err != nil || result["status"] != workflowmodels.WorkflowRunStatusWaitingApproval {
		t.Fatalf("author replay = %v, %v", result, err)
	}
	if _, leaked := result["resume_token"]; leaked {
		t.Fatalf("replay answer carries a resume token: %v", result)
	}
}
