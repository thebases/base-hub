package engine

// File overview:
// - Responsibility: plan T46, run rows are written in the run's own database: create and every
//   status update receive the trigger/caller context (bound to its database), with a sudo env
//   added by the default hooks, never a bare context that falls back to the default database.

import (
	"context"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

func TestRunRowsAreWrittenInTheRunDatabase(t *testing.T) {
	tenant := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironmentWithDBName(nil, "tenant_a", 5, nil, false))
	var dbs []string
	record := func(ctx context.Context) {
		dbs = append(dbs, orm.EnvironmentFromContext(ctx).DBName())
	}
	svc := &Service{
		createRun: func(ctx context.Context, _ workflowmodels.WorkflowRunRecord) (*orm.InsertResult, error) {
			record(ctx)
			return &orm.InsertResult{InsertedID: int64(1), Acknowledged: true}, nil
		},
		updateRun: func(ctx context.Context, _ orm.RecordMap, _ orm.RecordMap) (*orm.UpdateResult, error) {
			record(ctx)
			return &orm.UpdateResult{Acknowledged: true, MatchedCount: 1, ModifiedCount: 1}, nil
		},
		readRun: func(ctx context.Context, _ int64) (*workflowmodels.WorkflowRunRecord, error) {
			return &workflowmodels.WorkflowRunRecord{
				BaseModel: orm.BaseModel{ID: 1}, Name: "r", Topic: "t", FlowVersion: 1,
				Status: workflowmodels.WorkflowRunStatusRetryWaiting, CurrentStep: "s",
				OutputPayload: map[string]any{"a": 1},
			}, nil
		},
		loadFlowVersion: func(_ context.Context, topic string, version int) (*workflowmodels.FlowRecord, error) {
			return &workflowmodels.FlowRecord{BaseModel: orm.BaseModel{ID: 2}, Topic: topic, Version: version, IsActive: true}, nil
		},
		loadFlowActions: func(ctx context.Context, _ int64) ([]workflowmodels.WorkflowActionRecord, error) {
			if got := orm.EnvironmentFromContext(ctx).DBName(); got != "tenant_a" {
				t.Fatalf("steps loaded from %q, want tenant_a", got)
			}
			return []workflowmodels.WorkflowActionRecord{{Sequence: 10, StepKey: "s", Name: "s", ActionCode: "s", ActionVersion: 1}}, nil
		},
		lookupAction: func(string, int) (ActionHandler, bool) {
			return func(_ context.Context, req ActionRequest) ([]byte, error) { return req.Payload, nil }, true
		},
		now:   func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		sleep: func(context.Context, time.Duration) error { return nil },
	}

	if err := svc.executeFlow(tenant, flowRuntime{
		flow:  workflowmodels.FlowRecord{BaseModel: orm.BaseModel{ID: 2}, Topic: "t", Version: 1, IsActive: true},
		steps: []workflowmodels.Step{{Key: "s", Name: "s", Action: "s", ActionVersion: 1}},
	}, []byte(`{}`)); err != nil {
		t.Fatalf("executeFlow: %v", err)
	}
	if err := svc.ResumeRun(tenant, 1); err != nil {
		t.Fatalf("ResumeRun: %v", err)
	}
	if len(dbs) < 4 {
		t.Fatalf("expected create + updates, got %v", dbs)
	}
	for _, db := range dbs {
		if db != "tenant_a" {
			t.Fatalf("run rows written to %v, want tenant_a only", dbs)
		}
	}
}

func TestWorkflowEngineContextKeepsDatabaseAndUser(t *testing.T) {
	tenant := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironmentWithDBName(nil, "tenant_a", 5, nil, false))
	env := orm.EnvironmentFromContext(workflowmodels.WorkflowEngineContext(tenant))
	if env.DBName() != "tenant_a" || env.UserID() != 5 || !env.IsSudo() {
		t.Fatalf("engine env = db %q uid %d sudo %v, want tenant_a/5/true", env.DBName(), env.UserID(), env.IsSudo())
	}
	if got := workflowmodels.WorkflowEngineContext(context.Background()); orm.EnvironmentFromContext(got) != nil {
		t.Fatalf("a context without env must stay without env")
	}
}
