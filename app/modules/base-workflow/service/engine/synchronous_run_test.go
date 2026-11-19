package engine

// File overview:
// - Responsibility: plan T50, a run with a waiting caller (reply sink, tcp trigger) gets one
//   attempt, ends failed (not waiting_retry / dead_letter), and is never resumed later.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

func TestSynchronousRunGetsOneAttemptAndEndsFailed(t *testing.T) {
	var updates []orm.RecordMap
	calls := 0
	svc := &Service{
		createRun: func(context.Context, workflowmodels.WorkflowRunRecord) (*orm.InsertResult, error) {
			return &orm.InsertResult{InsertedID: int64(4), Acknowledged: true}, nil
		},
		updateRun: func(_ context.Context, _ orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			updates = append(updates, cloneRecordMap(values))
			return &orm.UpdateResult{Acknowledged: true, MatchedCount: 1, ModifiedCount: 1}, nil
		},
		lookupAction: func(string, int) (ActionHandler, bool) {
			return func(context.Context, ActionRequest) ([]byte, error) {
				calls++
				return nil, context.DeadlineExceeded
			}, true
		},
		now:   func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		sleep: func(context.Context, time.Duration) error { return nil },
	}
	ctx := workflowmodels.ContextWithWorkflowTriggerType(context.Background(), workflowmodels.WorkflowTriggerTypeTCP)
	ctx = workflowmodels.ContextWithWorkflowOutputSink(ctx, &workflowmodels.WorkflowOutput{})

	err := svc.executeFlow(ctx, flowRuntime{
		flow:  workflowmodels.FlowRecord{BaseModel: orm.BaseModel{ID: 1}, Topic: "tcp:9000", Version: 1, IsActive: true},
		steps: []workflowmodels.Step{{Key: "auth", Name: "auth", Action: "auth", ActionVersion: 1, RetryPolicy: workflowmodels.RetryPolicy{MaxAttempts: 3}}},
	}, []byte(`{"payload_hex":"0200"}`))
	if err == nil || errors.Is(err, ErrRunRetryScheduled) {
		t.Fatalf("err = %v, want the step failure (no scheduled retry)", err)
	}
	if calls != 1 {
		t.Fatalf("attempts = %d, want 1", calls)
	}
	for _, update := range updates {
		if update["status"] == workflowmodels.WorkflowRunStatusRetryWaiting {
			t.Fatalf("synchronous run was parked as waiting_retry")
		}
	}
	if got := updates[len(updates)-1]["status"]; got != workflowmodels.WorkflowRunStatusFailed {
		t.Fatalf("final status = %#v, want failed", got)
	}
}

func TestResumeRunClosesSynchronousRun(t *testing.T) {
	var updates []orm.RecordMap
	svc := &Service{
		readRun: func(context.Context, int64) (*workflowmodels.WorkflowRunRecord, error) {
			return &workflowmodels.WorkflowRunRecord{
				BaseModel: orm.BaseModel{ID: 6}, Name: "tcp-run", Topic: "tcp:9000", FlowVersion: 1,
				TriggerType: workflowmodels.WorkflowTriggerTypeTCP,
				Status:      workflowmodels.WorkflowRunStatusRetryWaiting, CurrentStep: "auth",
				OutputPayload: map[string]any{"payload_hex": "0200"},
			}, nil
		},
		loadFlowVersion: func(_ context.Context, topic string, version int) (*workflowmodels.FlowRecord, error) {
			return &workflowmodels.FlowRecord{BaseModel: orm.BaseModel{ID: 1}, Topic: topic, Version: version, IsActive: true}, nil
		},
		loadFlowActions: func(context.Context, int64) ([]workflowmodels.WorkflowActionRecord, error) {
			t.Fatalf("a synchronous run must not load its steps to run again")
			return nil, nil
		},
		updateRun: func(_ context.Context, _ orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			updates = append(updates, cloneRecordMap(values))
			return &orm.UpdateResult{Acknowledged: true, MatchedCount: 1, ModifiedCount: 1}, nil
		},
		lookupAction: func(string, int) (ActionHandler, bool) {
			t.Fatalf("a synchronous run must not execute again")
			return nil, false
		},
		now: func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	}
	// The close is reported (the resume batch logs why the run ended) but nothing executes.
	if err := svc.ResumeRun(context.Background(), 6); err == nil || !strings.Contains(err.Error(), "not resumed after its reply deadline") {
		t.Fatalf("ResumeRun: %v, want the close cause", err)
	}
	if len(updates) != 1 || updates[0]["status"] != workflowmodels.WorkflowRunStatusFailed {
		t.Fatalf("updates = %#v, want one failed update", updates)
	}
	// The closed run is final, so its checkpoint goes through the tcp audit default (hash).
	if output, _ := updates[0]["output_payload"].(map[string]any); output["payload_hex"] != nil {
		t.Fatalf("closed tcp run kept its clear payload: %#v", output)
	}
}
