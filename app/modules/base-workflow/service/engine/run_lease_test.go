package engine

// File overview:
// - Responsibility: plan T49 (run lease, fencing, heartbeat, interrupted attempts, pre-execution
//   failures) and the T48 review rules (caller-bound runs retry in place and are never resumed
//   by the system; the model-trigger chain survives a resume).

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

func leaseTestFlow(maxAttempts int) flowRuntime {
	return flowRuntime{
		flow: workflowmodels.FlowRecord{BaseModel: orm.BaseModel{ID: 1}, Topic: "lease", Version: 1, IsActive: true},
		steps: []workflowmodels.Step{{Key: "s", Name: "s", Action: "s", ActionVersion: 1,
			RetryPolicy: workflowmodels.RetryPolicy{MaxAttempts: maxAttempts}}},
	}
}

func leaseTestService(updates *[]orm.RecordMap, handler ActionHandler) *Service {
	return &Service{
		createRun: func(context.Context, workflowmodels.WorkflowRunRecord) (*orm.InsertResult, error) {
			return &orm.InsertResult{InsertedID: int64(3), Acknowledged: true}, nil
		},
		updateRun: func(_ context.Context, _ orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			*updates = append(*updates, cloneRecordMap(values))
			return &orm.UpdateResult{Acknowledged: true, MatchedCount: 1, ModifiedCount: 1}, nil
		},
		lookupAction: func(string, int) (ActionHandler, bool) { return handler, true },
		renewLease:   func(context.Context, int64, string) (bool, error) { return true, nil },
		takeLease:    func(context.Context, int64, string) (bool, error) { return true, nil },
		now:          func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		sleep:        func(context.Context, time.Duration) error { return nil },
	}
}

func TestLostLeaseStopsTheRunWithoutWriting(t *testing.T) {
	var updates []orm.RecordMap
	svc := leaseTestService(&updates, func(_ context.Context, req ActionRequest) ([]byte, error) { return req.Payload, nil })
	renewals := 0
	svc.renewLease = func(context.Context, int64, string) (bool, error) {
		renewals++
		return renewals <= 2, nil // reaped and claimed elsewhere after two writes
	}
	err := svc.executeFlow(context.Background(), leaseTestFlow(1), []byte(`{}`))
	if !errors.Is(err, ErrRunLeaseLost) {
		t.Fatalf("err = %v, want ErrRunLeaseLost", err)
	}
	if len(updates) != 2 {
		t.Fatalf("writes after the lease was lost: %d updates, want 2", len(updates))
	}
}

func TestHeartbeatCancelsAStepThatLostItsLease(t *testing.T) {
	previous := runLeaseHeartbeat
	runLeaseHeartbeat = 5 * time.Millisecond
	t.Cleanup(func() { runLeaseHeartbeat = previous })

	var updates []orm.RecordMap
	var lost atomic.Bool
	var cause atomic.Value
	svc := leaseTestService(&updates, func(ctx context.Context, _ ActionRequest) ([]byte, error) {
		lost.Store(true) // from now on another worker owns the run
		<-ctx.Done()
		cause.Store(context.Cause(ctx))
		return nil, ctx.Err()
	})
	svc.renewLease = func(context.Context, int64, string) (bool, error) { return !lost.Load(), nil }

	err := svc.executeFlow(context.Background(), leaseTestFlow(1), []byte(`{}`))
	if !errors.Is(err, ErrRunLeaseLost) {
		t.Fatalf("err = %v, want ErrRunLeaseLost", err)
	}
	if got, _ := cause.Load().(error); !errors.Is(got, ErrRunLeaseLost) {
		t.Fatalf("step cancel cause = %v, want ErrRunLeaseLost", got)
	}
}

func TestLeaseIsReleasedWhenTheRunLeavesRunning(t *testing.T) {
	var updates []orm.RecordMap
	svc := leaseTestService(&updates, func(_ context.Context, req ActionRequest) ([]byte, error) { return req.Payload, nil })
	if err := svc.executeFlow(context.Background(), leaseTestFlow(1), []byte(`{}`)); err != nil {
		t.Fatalf("executeFlow: %v", err)
	}
	last := updates[len(updates)-1]
	if last["status"] != workflowmodels.WorkflowRunStatusSucceeded || last["lease_owner"] != "" || last["lease_until"] != int64(0) {
		t.Fatalf("final update = %#v, want succeeded with the lease released", last)
	}
	if _, set := updates[0]["lease_owner"]; set {
		t.Fatalf("a running update must not release the lease: %#v", updates[0])
	}
}

func resumeTestService(run *workflowmodels.WorkflowRunRecord, updates *[]orm.RecordMap, calls *int) *Service {
	svc := leaseTestService(updates, func(_ context.Context, req ActionRequest) ([]byte, error) {
		*calls++
		return req.Payload, nil
	})
	svc.readRun = func(context.Context, int64) (*workflowmodels.WorkflowRunRecord, error) { return run, nil }
	svc.loadFlowVersion = func(_ context.Context, topic string, version int) (*workflowmodels.FlowRecord, error) {
		return &workflowmodels.FlowRecord{BaseModel: orm.BaseModel{ID: 1}, Topic: topic, Version: version, IsActive: true}, nil
	}
	svc.loadFlowActions = func(context.Context, int64) ([]workflowmodels.WorkflowActionRecord, error) {
		return []workflowmodels.WorkflowActionRecord{{Sequence: 10, StepKey: "s", Name: "s", ActionCode: "s", ActionVersion: 1,
			RetryPolicy: workflowmodels.RetryPolicy{MaxAttempts: 2}}}, nil
	}
	return svc
}

func TestResumeCountsInterruptedAttempts(t *testing.T) {
	// Attempt 1 failed (recorded), attempt 2 started (attempt_count=2) and the worker died.
	run := &workflowmodels.WorkflowRunRecord{
		BaseModel: orm.BaseModel{ID: 9}, Name: "r", Topic: "lease", FlowVersion: 1,
		Status: workflowmodels.WorkflowRunStatusRetryWaiting, CurrentStep: "s", AttemptCount: 2,
		OutputPayload: map[string]any{"a": 1},
		StepRuns:      []workflowmodels.WorkflowStepRun{{StepKey: "s", Attempt: 1, Status: workflowmodels.WorkflowRunStatusFailed}},
	}
	var updates []orm.RecordMap
	calls := 0
	svc := resumeTestService(run, &updates, &calls)
	err := svc.ResumeRun(context.Background(), 9)
	if err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("err = %v, want the attempts-used error", err)
	}
	if calls != 0 {
		t.Fatalf("a step with no attempts left ran %d more time(s)", calls)
	}
	if last := updates[len(updates)-1]; last["status"] != workflowmodels.WorkflowRunStatusDeadLetter {
		t.Fatalf("final status = %#v, want dead_letter", last["status"])
	}
}

func TestResumeClosesRunsThatMustNotRunAgain(t *testing.T) {
	cases := map[string]struct {
		run        workflowmodels.WorkflowRunRecord
		flowErr    error
		wantStatus string
	}{
		"caller bound":     {run: workflowmodels.WorkflowRunRecord{CallerBound: true, FlowVersion: 1}, wantStatus: workflowmodels.WorkflowRunStatusFailed},
		"no flow version":  {run: workflowmodels.WorkflowRunRecord{FlowVersion: 0}, wantStatus: workflowmodels.WorkflowRunStatusDeadLetter},
		"flow load failed": {run: workflowmodels.WorkflowRunRecord{FlowVersion: 1}, flowErr: errors.New("version deleted"), wantStatus: workflowmodels.WorkflowRunStatusDeadLetter},
	}
	for name, tc := range cases {
		run := tc.run
		run.ID, run.Name, run.Topic, run.CurrentStep = 9, "r", "lease", "s"
		run.Status = workflowmodels.WorkflowRunStatusRetryWaiting
		var updates []orm.RecordMap
		calls := 0
		svc := resumeTestService(&run, &updates, &calls)
		if tc.flowErr != nil {
			svc.loadFlowVersion = func(context.Context, string, int) (*workflowmodels.FlowRecord, error) { return nil, tc.flowErr }
		}
		if err := svc.ResumeRun(context.Background(), 9); err == nil {
			t.Fatalf("%s: expected the close cause", name)
		}
		if calls != 0 || len(updates) != 1 || updates[0]["status"] != tc.wantStatus {
			t.Fatalf("%s: calls=%d updates=%#v, want one %s update and no execution", name, calls, updates, tc.wantStatus)
		}
	}
}

func TestResumeRefusesARunLeasedElsewhere(t *testing.T) {
	run := &workflowmodels.WorkflowRunRecord{BaseModel: orm.BaseModel{ID: 9}, Topic: "lease", FlowVersion: 1,
		Status: workflowmodels.WorkflowRunStatusRunning, CurrentStep: "s"}
	var updates []orm.RecordMap
	calls := 0
	svc := resumeTestService(run, &updates, &calls)
	svc.takeLease = func(context.Context, int64, string) (bool, error) { return false, nil }
	if err := svc.ResumeRun(context.Background(), 9); err == nil || len(updates) != 0 || calls != 0 {
		t.Fatalf("err=%v updates=%d calls=%d, want a conflict and nothing touched", err, len(updates), calls)
	}
}

func TestCallerBoundRunRetriesInPlace(t *testing.T) {
	var updates []orm.RecordMap
	var created workflowmodels.WorkflowRunRecord
	attempts := 0
	svc := leaseTestService(&updates, func(_ context.Context, req ActionRequest) ([]byte, error) {
		attempts++
		if attempts == 1 {
			return nil, errors.New("flaky")
		}
		return req.Payload, nil
	})
	svc.createRun = func(_ context.Context, record workflowmodels.WorkflowRunRecord) (*orm.InsertResult, error) {
		created = record
		return &orm.InsertResult{InsertedID: int64(3), Acknowledged: true}, nil
	}
	caller := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironmentWithDBName(nil, "db", 8, nil, false))
	if err := svc.executeFlow(caller, leaseTestFlow(2), []byte(`{}`)); err != nil {
		t.Fatalf("executeFlow: %v", err)
	}
	if !created.CallerBound || attempts != 2 {
		t.Fatalf("caller_bound=%v attempts=%d, want true / 2", created.CallerBound, attempts)
	}
	for _, update := range updates {
		if update["status"] == workflowmodels.WorkflowRunStatusRetryWaiting {
			t.Fatalf("caller-bound run handed to the system cron")
		}
	}
}

func TestTriggerChainSurvivesResume(t *testing.T) {
	var created workflowmodels.WorkflowRunRecord
	var updates []orm.RecordMap
	svc := leaseTestService(&updates, func(_ context.Context, req ActionRequest) ([]byte, error) { return nil, errors.New("x") })
	svc.createRun = func(_ context.Context, record workflowmodels.WorkflowRunRecord) (*orm.InsertResult, error) {
		created = record
		return &orm.InsertResult{InsertedID: int64(3), Acknowledged: true}, nil
	}
	_ = svc.executeFlow(contextWithModelTriggerChain(context.Background(), []int64{4, 5}), leaseTestFlow(2), []byte(`{}`))
	if len(created.TriggerChain) != 2 || created.TriggerChain[1] != 5 {
		t.Fatalf("trigger_chain = %v, want [4 5]", created.TriggerChain)
	}

	run := &workflowmodels.WorkflowRunRecord{BaseModel: orm.BaseModel{ID: 9}, Name: "r", Topic: "lease", FlowVersion: 1,
		Status: workflowmodels.WorkflowRunStatusRetryWaiting, CurrentStep: "s", TriggerChain: []int64{4, 5},
		OutputPayload: map[string]any{}}
	calls := 0
	resumed := resumeTestService(run, &updates, &calls)
	var seen []int64
	resumed.lookupAction = func(string, int) (ActionHandler, bool) {
		return func(ctx context.Context, req ActionRequest) ([]byte, error) {
			seen = modelTriggerChainFromContext(ctx)
			return req.Payload, nil
		}, true
	}
	if err := resumed.ResumeRun(context.Background(), 9); err != nil {
		t.Fatalf("ResumeRun: %v", err)
	}
	if len(seen) != 2 || seen[0] != 4 {
		t.Fatalf("resumed step saw chain %v, want [4 5]", seen)
	}
}

func TestHeartbeatSurvivesATransientRenewError(t *testing.T) {
	previous := runLeaseHeartbeat
	runLeaseHeartbeat = 5 * time.Millisecond
	t.Cleanup(func() { runLeaseHeartbeat = previous })

	var updates []orm.RecordMap
	var failures atomic.Int32
	svc := leaseTestService(&updates, func(ctx context.Context, req ActionRequest) ([]byte, error) {
		select {
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		case <-time.After(60 * time.Millisecond): // several heartbeats, all failing transiently
			return req.Payload, nil
		}
	})
	inStep := atomic.Bool{}
	svc.renewLease = func(context.Context, int64, string) (bool, error) {
		if inStep.Load() {
			failures.Add(1)
			return false, errors.New("connection reset")
		}
		return true, nil
	}
	svc.lookupAction = func(string, int) (ActionHandler, bool) {
		return func(ctx context.Context, req ActionRequest) ([]byte, error) {
			inStep.Store(true)
			defer inStep.Store(false)
			select {
			case <-ctx.Done():
				return nil, context.Cause(ctx)
			case <-time.After(60 * time.Millisecond):
				return req.Payload, nil
			}
		}, true
	}
	if err := svc.executeFlow(context.Background(), leaseTestFlow(1), []byte(`{}`)); err != nil {
		t.Fatalf("executeFlow: %v (a DB hiccup must not cancel the step)", err)
	}
	if failures.Load() == 0 {
		t.Fatalf("heartbeat never ran; the test did not exercise the error path")
	}
}

func TestResumeSkipsAClaimedRunReclaimedElsewhere(t *testing.T) {
	run := &workflowmodels.WorkflowRunRecord{BaseModel: orm.BaseModel{ID: 9}, Topic: "lease", FlowVersion: 1,
		Status: workflowmodels.WorkflowRunStatusRunning, CurrentStep: "s"}
	var updates []orm.RecordMap
	calls := 0
	svc := resumeTestService(run, &updates, &calls)
	svc.renewLease = func(context.Context, int64, string) (bool, error) { return false, nil }
	claimed := contextWithRunLease(context.Background(), 0, "batch-token")
	if err := svc.ResumeRun(claimed, 9); err != nil || len(updates) != 0 || calls != 0 {
		t.Fatalf("err=%v updates=%d calls=%d, want a quiet skip", err, len(updates), calls)
	}
}
