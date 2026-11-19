package engine

// File overview:
// - Responsibility: verifies the workflow output sink (final payload of a successful run), used by the
//   tcp trigger for its synchronous reply. Runs without a sink must be unaffected.

import (
	"context"
	"errors"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

func sinkTestService(handler ActionHandler) *Service {
	return &Service{
		loadFlowEdges: func(context.Context, int64) ([]workflowmodels.WorkflowGraphEdge, error) {
			return dagTestEdges(), nil
		},
		createRun: func(context.Context, workflowmodels.WorkflowRunRecord) (*orm.InsertResult, error) {
			return &orm.InsertResult{InsertedID: int64(1), Acknowledged: true}, nil
		},
		updateRun: func(context.Context, orm.RecordMap, orm.RecordMap) (*orm.UpdateResult, error) {
			return &orm.UpdateResult{Acknowledged: true, MatchedCount: 1, ModifiedCount: 1}, nil
		},
		lookupAction: func(string, int) (ActionHandler, bool) { return handler, true },
		now:          func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		sleep:        func(context.Context, time.Duration) error { return nil },
	}
}

func linearSinkFlow() flowRuntime {
	return flowRuntime{
		flow:  workflowmodels.FlowRecord{BaseModel: orm.BaseModel{ID: 5}, Topic: "topic.sink", Version: 1, IsActive: true},
		steps: []workflowmodels.Step{{Key: "s1", Name: "s1", Action: "x"}},
	}
}

func TestOutputSinkReceivesFinalPayloadLinear(t *testing.T) {
	svc := sinkTestService(func(_ context.Context, _ ActionRequest) ([]byte, error) {
		return []byte(`{"reply_hex":"0102"}`), nil
	})
	sink := &workflowmodels.WorkflowOutput{}
	ctx := workflowmodels.ContextWithWorkflowOutputSink(context.Background(), sink)
	if err := svc.executeFlow(ctx, linearSinkFlow(), []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	out, ok := sink.Get()
	if !ok || string(out) != `{"reply_hex":"0102"}` {
		t.Fatalf("sink = %q ok=%v", out, ok)
	}
}

func TestOutputSinkReceivesFinalPayloadDAG(t *testing.T) {
	svc := sinkTestService(func(_ context.Context, req ActionRequest) ([]byte, error) { return req.Payload, nil })
	sink := &workflowmodels.WorkflowOutput{}
	ctx := workflowmodels.ContextWithWorkflowOutputSink(context.Background(), sink)
	runtime := flowRuntime{flow: dagTestFlow(workflowmodels.WorkflowDagFailurePolicyFailBranch, 2), steps: dagTestSteps()}
	if err := svc.executeFlow(ctx, runtime, []byte(`{"hello":"world"}`)); err != nil {
		t.Fatal(err)
	}
	if out, ok := sink.Get(); !ok || len(out) == 0 {
		t.Fatalf("dag run must publish its final payload, got %q ok=%v", out, ok)
	}
}

func TestOutputSinkEmptyWhenRunFails(t *testing.T) {
	svc := sinkTestService(func(_ context.Context, _ ActionRequest) ([]byte, error) { return nil, errors.New("boom") })
	sink := &workflowmodels.WorkflowOutput{}
	ctx := workflowmodels.ContextWithWorkflowOutputSink(context.Background(), sink)
	if err := svc.executeFlow(ctx, linearSinkFlow(), []byte(`{}`)); err == nil {
		t.Fatal("expected failure")
	}
	if _, ok := sink.Get(); ok {
		t.Fatal("a failed run must not publish output")
	}
}

func TestRunWithoutSinkIsUnaffected(t *testing.T) {
	svc := sinkTestService(func(_ context.Context, req ActionRequest) ([]byte, error) { return req.Payload, nil })
	if err := svc.executeFlow(context.Background(), linearSinkFlow(), []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	var nilSink *workflowmodels.WorkflowOutput
	nilSink.Set([]byte("x")) // nil-safe
	if _, ok := nilSink.Get(); ok {
		t.Fatal("nil sink has no output")
	}
}
