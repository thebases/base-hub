package models

import (
	"context"
	"errors"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

type publishFixture struct {
	flows, actions, edges, versions *memRepo
	tx                              *fakeWorkflowTx
}

// newPublishFixture is workflow 11 with one parse_payload step "a" (no edges, no triggers).
func newPublishFixture(t *testing.T, flow orm.RecordMap) publishFixture {
	t.Helper()
	previous := workflowPublishRecordAccess
	workflowPublishRecordAccess = func(context.Context, string, string, []string) error { return nil }
	t.Cleanup(func() { workflowPublishRecordAccess = previous })
	base := orm.RecordMap{"id": int64(11), "name": "wf", "topic": "orders.in", "version": int64(1), "execution_mode": "linear", "max_run_seconds": int64(30)}
	for key, value := range flow {
		base[key] = value
	}
	newMemRepo(t, WorkflowTriggerModelName, 1)
	return publishFixture{
		flows: newMemRepo(t, FlowModelName, 50, base),
		actions: newMemRepo(t, WorkflowActionModelName, 100, orm.RecordMap{
			"id": int64(1), "workflow_id": int64(11), "sequence": int64(10), "step_key": "a",
			"name": "Parse", "action_code": "parse_payload", "action_version": int64(1), "is_active": true,
		}),
		edges:    newMemRepo(t, WorkflowEdgeModelName, 1),
		versions: newMemRepo(t, WorkflowVersionModelName, 1),
		tx:       stubWorkflowTx(t, map[int64]int64{11: 4}),
	}
}

func publishedSnapshotOf(t *testing.T, row orm.RecordMap) *WorkflowSnapshot {
	t.Helper()
	snapshot, err := DecodeWorkflowSnapshot(row["published_snapshot"])
	if err != nil || snapshot == nil {
		t.Fatalf("published_snapshot = %#v (%v), want a snapshot", row["published_snapshot"], err)
	}
	return snapshot
}

func TestPublishFlowFreezesSnapshotAndLaterSaveOnlyMarksDraft(t *testing.T) {
	fx := newPublishFixture(t, orm.RecordMap{"state": WorkflowStateDraft})
	ctx := context.Background()
	// Publish re-syncs the workflow's triggers (the registry skips never-published workflows).
	var synced []int64
	prevHooks := workflowTriggerRuntimeHooks
	workflowTriggerRuntimeHooks.SyncWorkflowTriggers = func(_ context.Context, id int64) { synced = append(synced, id) }
	t.Cleanup(func() { workflowTriggerRuntimeHooks = prevHooks })

	got, err := publishFlow(ctx, nil, []string{"11"}, nil, map[string]any{"expected_revision": int64(4)})
	if err != nil {
		t.Fatalf("publishFlow: %v", err)
	}
	if len(synced) != 1 || synced[0] != 11 {
		t.Fatalf("trigger re-sync = %v, want workflow 11", synced)
	}
	result := got.(orm.RecordMap)
	if result["published_version"] != 1 || result["state"] != WorkflowStatePublished || result["revision"] != int64(4) {
		t.Fatalf("result = %#v, want version 1 published at revision 4", result)
	}
	flow := fx.flows.all()[0]
	if flow["state"] != WorkflowStatePublished || int64Value(flow["published_version"]) != 1 || int64Value(flow["version"]) != 1 {
		t.Fatalf("flow = %#v, want published v1", flow)
	}
	snapshot := publishedSnapshotOf(t, flow)
	if len(snapshot.Steps) != 1 || snapshot.Steps[0].Key != "a" || snapshot.Steps[0].Action != "parse_payload" {
		t.Fatalf("snapshot steps = %#v, want step a", snapshot.Steps)
	}
	if snapshot.Flow.MaxRunSeconds != 30 || snapshot.Flow.ExecutionMode != "linear" {
		t.Fatalf("snapshot flow = %#v, want the execution settings", snapshot.Flow)
	}
	versions := fx.versions.all()
	if len(versions) != 1 || int64Value(versions[0]["version"]) != 1 || int64Value(versions[0]["workflow_id"]) != 11 {
		t.Fatalf("version rows = %#v, want one row for v1", versions)
	}
	if fx.tx.revisions[11] != 4 {
		t.Fatalf("revision = %d, publish must not bump it", fx.tx.revisions[11])
	}

	// A canvas save after publishing replaces the steps: the workflow is a draft again, and the
	// published snapshot (what triggers and /wh run) is unchanged.
	if _, err := replaceActionSequenceFlow(ctx, nil, []string{"11"}, nil, map[string]any{
		"items": []any{map[string]any{"step_key": "b", "name": "Notify", "action_code": "parse_payload", "action_version": int64(1)}},
	}); err != nil {
		t.Fatalf("replace_action_sequence: %v", err)
	}
	flow = fx.flows.all()[0]
	if flow["state"] != WorkflowStateDraft {
		t.Fatalf("state after save = %v, want draft", flow["state"])
	}
	after := publishedSnapshotOf(t, flow)
	if after.Version != 1 || len(after.Steps) != 1 || after.Steps[0].Key != "a" {
		t.Fatalf("published snapshot after save = %#v, want v1 with step a", after)
	}

	// The next publish freezes the new rows as v2; v1 stays stored for runs pinned to it.
	if _, err := publishFlow(ctx, nil, []string{"11"}, nil, nil); err != nil {
		t.Fatalf("second publish: %v", err)
	}
	flow = fx.flows.all()[0]
	if snap := publishedSnapshotOf(t, flow); snap.Version != 2 || len(snap.Steps) != 1 || snap.Steps[0].Key != "b" {
		t.Fatalf("snapshot after second publish = %#v, want v2 with step b", snap)
	}
	if len(fx.versions.all()) != 2 {
		t.Fatalf("version rows = %d, want 2", len(fx.versions.all()))
	}
}

func TestPublishFlowRefusesInvalidWorkflow(t *testing.T) {
	fx := newPublishFixture(t, orm.RecordMap{"state": WorkflowStateDraft})
	// An edge to a missing step is a validation error.
	fx.edges.rows[1] = orm.RecordMap{"id": int64(1), "workflow_id": int64(11), "from_step_key": "a", "to_step_key": "ghost", "edge_type": "success"}

	_, err := publishFlow(context.Background(), nil, []string{"11"}, nil, nil)
	var ormErr *orm.Error
	if !errors.As(err, &ormErr) || ormErr.Code != orm.ErrInvalidParams {
		t.Fatalf("err = %v, want invalid params", err)
	}
	flow := fx.flows.all()[0]
	if flow["state"] != WorkflowStateDraft || flow["published_snapshot"] != nil || len(fx.versions.all()) != 0 {
		t.Fatalf("flow = %#v versions = %d, want nothing published", flow, len(fx.versions.all()))
	}
}

func TestPublishFlowRevisionConflict(t *testing.T) {
	fx := newPublishFixture(t, orm.RecordMap{"state": WorkflowStateDraft})
	_, err := publishFlow(context.Background(), nil, []string{"11"}, nil, map[string]any{"expected_revision": int64(3)})
	var ormErr *orm.Error
	if !errors.As(err, &ormErr) || ormErr.Code != orm.ErrConflict {
		t.Fatalf("err = %v, want conflict", err)
	}
	if len(fx.versions.all()) != 0 {
		t.Fatal("a refused publish must store no version")
	}
}

func TestNextWorkflowPublishedVersion(t *testing.T) {
	cases := []struct {
		row  orm.RecordMap
		want int
	}{
		{orm.RecordMap{"state": WorkflowStateDraft, "version": int64(1)}, 1},                                    // new workflow
		{orm.RecordMap{"state": WorkflowStateDraft, "version": int64(3), "published_version": int64(3)}, 4},     // edited after publish
		{orm.RecordMap{"state": WorkflowStatePublished, "version": int64(5), "published_version": int64(0)}, 6}, // pre-T97, not snapshotted
	}
	for _, tc := range cases {
		if got := nextWorkflowPublishedVersion(tc.row); got != tc.want {
			t.Fatalf("next(%v) = %d, want %d", tc.row, got, tc.want)
		}
	}
}

func TestWorkflowCreateStartsDraftAndPublicationFieldsAreServerOwned(t *testing.T) {
	created, err := normalizeFlowValues(orm.RecordMap{"topic": "x", "state": WorkflowStatePublished, "published_version": 9, "published_snapshot": map[string]any{"version": 9}}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if created["state"] != WorkflowStateDraft || created["published_version"] != nil || created["published_snapshot"] != nil {
		t.Fatalf("create values = %#v, want draft without publication fields", created)
	}
	written, err := normalizeFlowValues(orm.RecordMap{"state": WorkflowStatePublished}, &flowState{Topic: "x"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := written["state"]; ok {
		t.Fatalf("write values = %#v, want state dropped", written)
	}
	if !workflowSnapshotFieldsTouched(orm.RecordMap{"max_run_seconds": 5}) || workflowSnapshotFieldsTouched(orm.RecordMap{"layout": map[string]any{}, "is_active": false}) {
		t.Fatal("only execution settings are snapshot fields")
	}
}

func TestBuildWorkflowSnapshotFromRawRows(t *testing.T) {
	// The backfill reads rows through to_jsonb: numbers are float64, timestamps are strings without
	// a zone, inactive actions are present.
	def := workflowDefinition{
		Steps: []workflowDefStep{
			{StepKey: "a", Columns: orm.RecordMap{"id": float64(1), "workflow_id": float64(11), "step_key": "a", "action_code": "parse_payload", "action_version": float64(1), "is_active": true, "timeout_seconds": float64(12), "created_at": "2026-01-02T03:04:05.123456", "retry_policy": map[string]any{"max_attempts": float64(5)}}},
			{StepKey: "off", Columns: orm.RecordMap{"id": float64(2), "step_key": "off", "action_code": "parse_payload", "is_active": false}},
		},
		Edges: []workflowDefEdge{{From: "a", To: "b", EdgeType: "success"}},
	}
	snapshot, err := buildWorkflowSnapshot(orm.RecordMap{"execution_mode": "dag", "max_parallel_steps": float64(2)}, def, 3, 7)
	if err != nil {
		t.Fatalf("buildWorkflowSnapshot: %v", err)
	}
	if snapshot.Version != 3 || snapshot.Revision != 7 || snapshot.Flow.ExecutionMode != "dag" || snapshot.Flow.MaxParallelSteps != 2 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if len(snapshot.Steps) != 1 || snapshot.Steps[0].TimeoutSeconds != 12 || snapshot.Steps[0].RetryPolicy.MaxAttempts != 5 {
		t.Fatalf("steps = %#v, want the one active step with its settings", snapshot.Steps)
	}
	if len(snapshot.Edges) != 1 || snapshot.Edges[0].ToStepKey != "b" {
		t.Fatalf("edges = %#v", snapshot.Edges)
	}
	// Round trip through the stored jsonb shape.
	stored, err := workflowSnapshotMap(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeWorkflowSnapshot(stored)
	if err != nil || decoded.Version != 3 || decoded.Steps[0].Key != "a" || decoded.Edges[0].FromStepKey != "a" {
		t.Fatalf("decoded = %#v (%v)", decoded, err)
	}
	if empty, err := DecodeWorkflowSnapshot(map[string]any{}); empty != nil || err != nil {
		t.Fatalf("empty object = %#v (%v), want none", empty, err)
	}
}

// TestMarkWorkflowDraftWritesUnconditionally: a publish still open in another transaction shows the
// row as draft; skipping the write on that read let publish commit state=published over changed
// rows. The draft write must happen whatever the current state reads.
func TestMarkWorkflowDraftWritesUnconditionally(t *testing.T) {
	var written []orm.RecordMap
	restore := orm.StubRuntimeRepository(FlowModelName, orm.RuntimeRepositoryStub{
		Read: func(context.Context, []string, []string) ([]orm.RecordMap, error) {
			return []orm.RecordMap{{"id": int64(4), "state": WorkflowStateDraft}}, nil
		},
		Write: func(_ context.Context, ids []string, values orm.RecordMap) (bool, error) {
			if len(ids) != 1 || ids[0] != "4" {
				t.Fatalf("ids = %v", ids)
			}
			written = append(written, values)
			return true, nil
		},
	})
	defer restore()
	if err := markWorkflowDraft(context.Background(), 4); err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 || written[0]["state"] != WorkflowStateDraft || len(written[0]) != 1 {
		t.Fatalf("writes = %v, want one state=draft write", written)
	}
}
