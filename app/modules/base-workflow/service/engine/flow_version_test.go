package engine

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

// versionTestSnapshot is a snapshot whose single "capture" step has key stepKey.
func versionTestSnapshot(t *testing.T, version int, stepKey string) *workflowmodels.WorkflowSnapshot {
	t.Helper()
	return &workflowmodels.WorkflowSnapshot{
		Version: version,
		Flow:    workflowmodels.FlowRecord{ExecutionMode: workflowmodels.WorkflowExecutionModeLinear, MaxRunSeconds: 60},
		Steps:   []workflowmodels.Step{{Key: stepKey, Name: stepKey, Action: "capture", ActionVersion: 1}},
	}
}

func versionTestSnapshotMap(t *testing.T, snapshot *workflowmodels.WorkflowSnapshot) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

type versionTestRecorder struct {
	executed []string
	created  []workflowmodels.WorkflowRunRecord
	updates  []orm.RecordMap
}

// versionTestService runs "capture" steps, recording their keys; draft rows hold step "draft".
func versionTestService(t *testing.T, rec *versionTestRecorder, flow *workflowmodels.FlowRecord) *Service {
	t.Helper()
	return &Service{
		loadActiveFlow:  func(context.Context, string) (*workflowmodels.FlowRecord, error) { return flow, nil },
		loadFlowVersion: func(context.Context, string, int) (*workflowmodels.FlowRecord, error) { return flow, nil },
		loadFlowActions: func(context.Context, int64) ([]workflowmodels.WorkflowActionRecord, error) {
			return []workflowmodels.WorkflowActionRecord{{Sequence: 10, Name: "draft", StepKey: "draft", ActionCode: "capture", ActionVersion: 1}}, nil
		},
		loadSnapshot: func(context.Context, int64, int) (*workflowmodels.WorkflowSnapshot, error) {
			t.Fatal("unexpected history read")
			return nil, nil
		},
		createRun: func(_ context.Context, record workflowmodels.WorkflowRunRecord) (*orm.InsertResult, error) {
			rec.created = append(rec.created, record)
			return &orm.InsertResult{InsertedID: int64(99), Acknowledged: true}, nil
		},
		updateRun: func(_ context.Context, _ orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			rec.updates = append(rec.updates, values)
			return &orm.UpdateResult{Acknowledged: true, MatchedCount: 1, ModifiedCount: 1}, nil
		},
		lookupAction: func(name string, _ int) (ActionHandler, bool) {
			if name != "capture" {
				return nil, false
			}
			return func(_ context.Context, req ActionRequest) ([]byte, error) {
				rec.executed = append(rec.executed, req.Step.Key)
				return req.Payload, nil
			}, true
		},
		now:   func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		sleep: func(context.Context, time.Duration) error { return nil },
	}
}

// A save after publish (draft rows differ) does not change what a delivery executes; the test-run
// executes the draft and is stored unversioned.
func TestExecuteTopicRunsPublishedSnapshotNotDraftRows(t *testing.T) {
	flow := &workflowmodels.FlowRecord{
		BaseModel: orm.BaseModel{ID: 11}, Topic: "orders.in", IsActive: true, Version: 2,
		State: workflowmodels.WorkflowStateDraft, PublishedVersion: 2,
		PublishedSnapshot: versionTestSnapshotMap(t, versionTestSnapshot(t, 2, "published")),
	}
	rec := &versionTestRecorder{}
	svc := versionTestService(t, rec, flow)

	if err := svc.ExecuteTopic(context.Background(), "orders.in", []byte(`{}`)); err != nil {
		t.Fatalf("ExecuteTopic: %v", err)
	}
	if len(rec.executed) != 1 || rec.executed[0] != "published" || rec.created[0].FlowVersion != 2 {
		t.Fatalf("executed %v flow_version %d, want the published step at version 2", rec.executed, rec.created[0].FlowVersion)
	}

	rec.executed = nil
	testCtx := workflowmodels.ContextWithWorkflowTriggerType(context.Background(), workflowmodels.WorkflowTriggerTypeManualTest)
	if err := svc.ExecuteTopic(testCtx, "orders.in", []byte(`{}`)); err != nil {
		t.Fatalf("test-run: %v", err)
	}
	if len(rec.executed) != 1 || rec.executed[0] != "draft" || rec.created[1].FlowVersion != 0 {
		t.Fatalf("test-run executed %v flow_version %d, want the draft step unversioned", rec.executed, rec.created[1].FlowVersion)
	}
}

func TestExecuteTopicRefusesNeverPublishedWorkflow(t *testing.T) {
	flow := &workflowmodels.FlowRecord{BaseModel: orm.BaseModel{ID: 11}, Topic: "orders.in", IsActive: true, Version: 1, State: workflowmodels.WorkflowStateDraft}
	rec := &versionTestRecorder{}
	err := versionTestService(t, rec, flow).ExecuteTopic(context.Background(), "orders.in", []byte(`{}`))
	if !errors.Is(err, ErrFlowNotFound) || len(rec.created) != 0 {
		t.Fatalf("err = %v runs = %d, want ErrFlowNotFound and no run", err, len(rec.created))
	}
}

func versionTestRun(version int) func(context.Context, int64) (*workflowmodels.WorkflowRunRecord, error) {
	return func(context.Context, int64) (*workflowmodels.WorkflowRunRecord, error) {
		return &workflowmodels.WorkflowRunRecord{
			BaseModel: orm.BaseModel{ID: 33}, Name: "run-33", Topic: "orders.in", FlowVersion: version,
			Status: workflowmodels.WorkflowRunStatusRetryWaiting, TriggerType: workflowmodels.WorkflowTriggerTypeKafka, CurrentStep: "v2",
			OutputPayload: map[string]any{}, AttemptCount: 1,
		}, nil
	}
}

// A run pinned to v2 resumes on v2 from the history even though v3 is published and the rows changed.
func TestResumeRunUsesPinnedVersion(t *testing.T) {
	flow := &workflowmodels.FlowRecord{
		BaseModel: orm.BaseModel{ID: 11}, Topic: "orders.in", IsActive: true, Version: 3,
		State: workflowmodels.WorkflowStateDraft, PublishedVersion: 3,
		PublishedSnapshot: versionTestSnapshotMap(t, versionTestSnapshot(t, 3, "v3")),
	}
	rec := &versionTestRecorder{}
	svc := versionTestService(t, rec, flow)
	svc.readRun = versionTestRun(2)
	svc.loadFlowActions = func(context.Context, int64) ([]workflowmodels.WorkflowActionRecord, error) {
		t.Fatal("a pinned run must not read the action rows")
		return nil, nil
	}
	svc.loadSnapshot = func(_ context.Context, workflowID int64, version int) (*workflowmodels.WorkflowSnapshot, error) {
		if workflowID != 11 || version != 2 {
			t.Fatalf("history read workflow %d version %d, want 11 v2", workflowID, version)
		}
		return versionTestSnapshot(t, 2, "v2"), nil
	}
	if err := svc.ResumeRun(context.Background(), 33); err != nil {
		t.Fatalf("ResumeRun: %v", err)
	}
	if len(rec.executed) != 1 || rec.executed[0] != "v2" {
		t.Fatalf("executed %v, want the v2 step", rec.executed)
	}
}

// A pinned version that is no longer stored closes the run instead of running other definitions.
func TestResumeRunClosesRunWhoseVersionIsGone(t *testing.T) {
	flow := &workflowmodels.FlowRecord{
		BaseModel: orm.BaseModel{ID: 11}, Topic: "orders.in", IsActive: true, Version: 3,
		State: workflowmodels.WorkflowStatePublished, PublishedVersion: 3,
		PublishedSnapshot: versionTestSnapshotMap(t, versionTestSnapshot(t, 3, "v3")),
	}
	rec := &versionTestRecorder{}
	svc := versionTestService(t, rec, flow)
	svc.readRun = versionTestRun(2)
	svc.loadSnapshot = func(context.Context, int64, int) (*workflowmodels.WorkflowSnapshot, error) { return nil, nil }

	err := svc.ResumeRun(context.Background(), 33)
	if !errors.Is(err, ErrFlowNotFound) || len(rec.executed) != 0 {
		t.Fatalf("err = %v executed %v, want ErrFlowNotFound and nothing run", err, rec.executed)
	}
	last := rec.updates[len(rec.updates)-1]
	if last["status"] != workflowmodels.WorkflowRunStatusDeadLetter {
		t.Fatalf("final status = %v, want dead_letter", last["status"])
	}
}

// Pre-T97 rows that the backfill has not snapshotted yet keep running from their rows.
func TestLegacyPublishedRowsRunFromRows(t *testing.T) {
	flow := &workflowmodels.FlowRecord{BaseModel: orm.BaseModel{ID: 11}, Topic: "orders.in", IsActive: true, Version: 4, State: workflowmodels.WorkflowStatePublished}
	rec := &versionTestRecorder{}
	if err := versionTestService(t, rec, flow).ExecuteTopic(context.Background(), "orders.in", []byte(`{}`)); err != nil {
		t.Fatalf("ExecuteTopic: %v", err)
	}
	if len(rec.executed) != 1 || rec.executed[0] != "draft" || rec.created[0].FlowVersion != 4 {
		t.Fatalf("executed %v flow_version %d, want the rows at version 4", rec.executed, rec.created[0].FlowVersion)
	}
}

// DAG runs of a snapshot take the snapshot's edges, never the edge rows.
func TestSnapshotRuntimeCarriesEdges(t *testing.T) {
	snapshot := versionTestSnapshot(t, 5, "a")
	snapshot.Flow.ExecutionMode = workflowmodels.WorkflowExecutionModeDAG
	snapshot.Edges = []workflowmodels.WorkflowGraphEdge{{FromStepKey: "a", ToStepKey: "b", EdgeType: "success"}}
	runtime, err := snapshotRuntime(workflowmodels.FlowRecord{BaseModel: orm.BaseModel{ID: 11}, Topic: "t", IsActive: true, MaxRunSeconds: 999}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !runtime.pinned || len(runtime.edges) != 1 || runtime.flow.Version != 5 || runtime.flow.ExecutionMode != workflowmodels.WorkflowExecutionModeDAG || runtime.flow.MaxRunSeconds != 60 || runtime.flow.ID != 11 {
		t.Fatalf("runtime = %#v, want the snapshot settings on the live identity", runtime)
	}
}

// A run resumes on the workflow its workflow_id names, never on whatever workflow now owns its
// topic (a renamed topic reused by another workflow), and is closed when its own workflow no
// longer serves that topic. Replay and approval decisions resolve the flow the same way.
func TestResumeRunLoadsWorkflowByIDNotTopic(t *testing.T) {
	own := &workflowmodels.FlowRecord{
		BaseModel: orm.BaseModel{ID: 11}, Topic: "orders.in", IsActive: true, Version: 2,
		State: workflowmodels.WorkflowStatePublished, PublishedVersion: 2,
		PublishedSnapshot: versionTestSnapshotMap(t, versionTestSnapshot(t, 2, "own")),
	}
	other := &workflowmodels.FlowRecord{
		BaseModel: orm.BaseModel{ID: 12}, Topic: "orders.in", IsActive: true, Version: 2,
		State: workflowmodels.WorkflowStatePublished, PublishedVersion: 2,
		PublishedSnapshot: versionTestSnapshotMap(t, versionTestSnapshot(t, 2, "other")),
	}
	rec := &versionTestRecorder{}
	svc := versionTestService(t, rec, other)
	readRun := versionTestRun(2)
	svc.readRun = func(ctx context.Context, id int64) (*workflowmodels.WorkflowRunRecord, error) {
		run, err := readRun(ctx, id)
		workflowID := int64(11)
		run.WorkflowID, run.CurrentStep = &workflowID, "own"
		return run, err
	}
	loaded := own
	svc.loadFlowByID = func(_ context.Context, id int64) (*workflowmodels.FlowRecord, error) {
		if id != 11 {
			t.Fatalf("loaded workflow %d, want the run's workflow 11", id)
		}
		return loaded, nil
	}
	svc.loadFlowVersion = func(context.Context, string, int) (*workflowmodels.FlowRecord, error) {
		t.Fatal("a run with a workflow_id must not be resolved by topic")
		return nil, nil
	}
	if err := svc.ResumeRun(context.Background(), 33); err != nil {
		t.Fatalf("ResumeRun: %v", err)
	}
	if len(rec.executed) != 1 || rec.executed[0] != "own" {
		t.Fatalf("executed %v, want the own workflow's step", rec.executed)
	}

	// A topic renamed since the run started does not matter: the workflow id is authoritative.
	renamed := *own
	renamed.Topic = "orders.v2"
	loaded = &renamed
	rec.executed, rec.updates = nil, nil
	if err := svc.ResumeRun(context.Background(), 33); err != nil {
		t.Fatalf("ResumeRun after a topic rename: %v", err)
	}
	if len(rec.executed) != 1 || rec.executed[0] != "own" {
		t.Fatalf("executed %v after a topic rename, want the own workflow's step", rec.executed)
	}
}
