package models

// Tests for plans T59 (transaction + revision), T61 (copy/export/import keep the DAG and are
// portable) and T62 (step_key uniqueness, edges naming existing steps). They run on in-memory
// repositories (memRepo) and the fake transaction seam (stubWorkflowTx): the SQL of the real lock,
// revision bump and unique-index migration needs Postgres and is not exercised here.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func errorCode(err error) int {
	if err == nil {
		return 0
	}
	return orm.ErrorFrom(err).Code
}

type workflowFixture struct {
	tx       *fakeWorkflowTx
	flows    *memRepo
	actions  *memRepo
	edges    *memRepo
	triggers *memRepo
	catalog  *memRepo
	xmlIDs   *memRepo
}

func notifyAction(workflowID int64, id int64, stepKey string, sequence int64) orm.RecordMap {
	return orm.RecordMap{
		"id": id, "workflow_id": workflowID, "name": stepKey, "action_code": "notify", "action_version": int64(1),
		"step_key": stepKey, "sequence": sequence, "target_ref": "https://partner.local/hook", "is_active": true,
	}
}

func newWorkflowFixture(t *testing.T, revision int64) *workflowFixture {
	t.Helper()
	return &workflowFixture{
		tx: stubWorkflowTx(t, map[int64]int64{11: revision}),
		flows: newMemRepo(t, FlowModelName, 40, orm.RecordMap{
			"id": int64(11), "name": "Order Import", "topic": "orders.in", "version": int64(1), "is_active": true,
		}),
		actions: newMemRepo(t, WorkflowActionModelName, 100,
			notifyAction(11, 21, "a", 10), notifyAction(11, 22, "b", 20), notifyAction(11, 23, "c", 30)),
		edges: newMemRepo(t, WorkflowEdgeModelName, 200,
			orm.RecordMap{"id": int64(31), "workflow_id": int64(11), "from_step_key": "a", "to_step_key": "b", "edge_type": "success"},
			orm.RecordMap{"id": int64(32), "workflow_id": int64(11), "from_step_key": "b", "to_step_key": "c", "edge_type": "success"}),
		triggers: newMemRepo(t, WorkflowTriggerModelName, 300),
		catalog:  newMemRepo(t, WorkflowActionCatalogModelName, 400),
		xmlIDs:   newMemRepo(t, sysModelDataModelName, 500),
	}
}

func sequenceItems(keys ...string) []any {
	items := make([]any, 0, len(keys))
	for _, key := range keys {
		items = append(items, map[string]any{"action_code": "notify", "name": key, "step_key": key, "target_ref": "https://partner.local/hook"})
	}
	return items
}

func TestReplaceActionSequenceRefusesStaleRevision(t *testing.T) {
	fx := newWorkflowFixture(t, 5)

	_, err := replaceActionSequenceFlow(context.Background(), nil, nil, nil, map[string]any{
		"workflow_id": int64(11), "items": sequenceItems("x"), "expected_revision": float64(4),
	})
	if errorCode(err) != orm.ErrConflict {
		t.Fatalf("stale save error = %v (code %d), want Conflict", err, errorCode(err))
	}
	if got := len(fx.actions.where("workflow_id", 11)); got != 3 {
		t.Fatalf("actions after refused save = %d, want the 3 original steps untouched", got)
	}
	if fx.tx.revisions[11] != 5 {
		t.Fatalf("revision after refused save = %d, want 5", fx.tx.revisions[11])
	}

	result, err := replaceActionSequenceFlow(context.Background(), nil, nil, nil, map[string]any{
		"workflow_id": int64(11), "items": sequenceItems("a", "b"), "expected_revision": int64(5),
		"layout": map[string]any{"nodes": []any{}},
	})
	if err != nil {
		t.Fatalf("current-revision save error = %v", err)
	}
	if got := int64Value(result.(orm.RecordMap)["revision"]); got != 6 {
		t.Fatalf("returned revision = %d, want 6", got)
	}
	if len(fx.tx.locked) != 2 || fx.tx.locked[1] != 11 {
		t.Fatalf("locked workflows = %v, want the workflow row locked for each save", fx.tx.locked)
	}
	rows := fx.actions.where("workflow_id", 11)
	if len(rows) != 2 || stringValue(rows[0]["step_key"]) != "a" || int64Value(rows[1]["sequence"]) != 20 {
		t.Fatalf("actions after save = %#v, want a,b with sequence from array order", rows)
	}
	flow, _ := fx.flows.read(context.Background(), []string{"11"}, nil)
	if _, ok := flow[0]["layout"].(map[string]any); !ok {
		t.Fatalf("layout not written in the same save: %#v", flow[0])
	}
	// Step c is gone: its edge b->c is dropped, a->b stays (plan T62).
	edges := fx.edges.where("workflow_id", 11)
	if len(edges) != 1 || stringValue(edges[0]["to_step_key"]) != "b" {
		t.Fatalf("edges after save = %#v, want only a->b", edges)
	}
}

func TestReplaceActionSequenceWithoutExpectedRevisionStillBumps(t *testing.T) {
	fx := newWorkflowFixture(t, 2)
	if _, err := replaceActionSequenceFlow(context.Background(), nil, nil, nil, map[string]any{
		"workflow_id": int64(11), "items": sequenceItems("a"),
	}); err != nil {
		t.Fatalf("save error = %v", err)
	}
	if fx.tx.revisions[11] != 3 {
		t.Fatalf("revision = %d, want 3", fx.tx.revisions[11])
	}
	if _, err := replaceActionSequenceFlow(context.Background(), nil, nil, nil, map[string]any{
		"workflow_id": int64(11), "items": sequenceItems("a"), "expected_revision": "x",
	}); errorCode(err) != orm.ErrInvalidParams {
		t.Fatalf("bad expected_revision error = %v, want InvalidParams", err)
	}
}

func TestReplaceActionSequenceStepKeys(t *testing.T) {
	fx := newWorkflowFixture(t, 0)

	_, err := replaceActionSequenceFlow(context.Background(), nil, nil, nil, map[string]any{
		"workflow_id": int64(11), "items": sequenceItems("a", "a"),
	})
	if errorCode(err) != orm.ErrInvalidParams {
		t.Fatalf("duplicate explicit step_key error = %v, want InvalidParams", err)
	}
	if len(fx.tx.locked) != 0 {
		t.Fatalf("a refused sequence must not open the transaction, locked = %v", fx.tx.locked)
	}

	// Two unnamed steps of one type get distinct generated keys instead of failing.
	_, err = replaceActionSequenceFlow(context.Background(), nil, nil, nil, map[string]any{
		"workflow_id": int64(11),
		"items": []any{
			map[string]any{"action_code": "parse_payload"},
			map[string]any{"action_code": "parse_payload"},
		},
	})
	if err != nil {
		t.Fatalf("generated keys save error = %v", err)
	}
	rows := fx.actions.where("workflow_id", 11)
	if len(rows) != 2 || stringValue(rows[0]["step_key"]) == stringValue(rows[1]["step_key"]) {
		t.Fatalf("generated step keys = %#v, want two distinct keys", rows)
	}
}

func TestReplaceWorkflowEdgesRejectsOrphanAndStaleRevision(t *testing.T) {
	fx := newWorkflowFixture(t, 7)

	_, err := replaceWorkflowEdgesFlow(context.Background(), nil, nil, nil, map[string]any{
		"workflow_id": int64(11),
		"edges":       []any{map[string]any{"from_step_key": "a", "to_step_key": "ghost"}},
	})
	if errorCode(err) != orm.ErrInvalidParams {
		t.Fatalf("orphan edge error = %v, want InvalidParams", err)
	}
	if fx.tx.failed != 1 || len(fx.edges.where("workflow_id", 11)) != 2 || fx.tx.revisions[11] != 7 {
		t.Fatalf("refused edge save changed state: failed=%d edges=%d revision=%d", fx.tx.failed, len(fx.edges.where("workflow_id", 11)), fx.tx.revisions[11])
	}

	_, err = replaceWorkflowEdgesFlow(context.Background(), nil, nil, nil, map[string]any{
		"workflow_id": int64(11), "expected_revision": int64(6),
		"edges": []any{map[string]any{"from_step_key": "a", "to_step_key": "c"}},
	})
	if errorCode(err) != orm.ErrConflict {
		t.Fatalf("stale edge save error = %v, want Conflict", err)
	}

	result, err := replaceWorkflowEdgesFlow(context.Background(), nil, nil, nil, map[string]any{
		"workflow_id": int64(11), "expected_revision": int64(7),
		"edges": []any{map[string]any{"from_step_key": "a", "to_step_key": "c"}},
	})
	if err != nil {
		t.Fatalf("edge save error = %v", err)
	}
	if int64Value(result.(orm.RecordMap)["revision"]) != 8 {
		t.Fatalf("edge save result = %#v, want revision 8", result)
	}
	edges := fx.edges.where("workflow_id", 11)
	if len(edges) != 1 || stringValue(edges[0]["to_step_key"]) != "c" {
		t.Fatalf("edges = %#v, want only a->c", edges)
	}
}

func TestWorkflowActionStepKeyUniqueAndEdgeCascade(t *testing.T) {
	fx := newWorkflowFixture(t, 0)
	ctx := context.Background()

	_, err := createWorkflowAction(ctx, nil, nil, []any{orm.RecordMap{
		"workflow_id": int64(11), "action_code": "parse_payload", "step_key": "b",
	}}, nil)
	if errorCode(err) != orm.ErrConflict {
		t.Fatalf("duplicate step_key create error = %v, want Conflict", err)
	}
	// Another workflow may reuse the key.
	fx.flows.rows[12] = orm.RecordMap{"id": int64(12), "name": "Other", "topic": "other", "version": int64(1)}
	if _, err := createWorkflowAction(ctx, nil, nil, []any{orm.RecordMap{
		"workflow_id": int64(12), "action_code": "parse_payload", "step_key": "b",
	}}, nil); err != nil {
		t.Fatalf("same key in another workflow error = %v", err)
	}

	if _, err := writeWorkflowAction(ctx, nil, []string{"21"}, []any{orm.RecordMap{"step_key": "c"}}, nil); errorCode(err) != orm.ErrConflict {
		t.Fatalf("rename onto an existing key error = %v, want Conflict", err)
	}

	// Renaming b to b2 rewrites both edges that touch it.
	if _, err := writeWorkflowAction(ctx, nil, []string{"22"}, []any{orm.RecordMap{"step_key": "b2"}}, nil); err != nil {
		t.Fatalf("rename error = %v", err)
	}
	for _, edge := range fx.edges.where("workflow_id", 11) {
		if stringValue(edge["from_step_key"]) == "b" || stringValue(edge["to_step_key"]) == "b" {
			t.Fatalf("edge still names the old key after rename: %#v", edge)
		}
	}
	if got := len(fx.edges.where("from_step_key", "b2")) + len(fx.edges.where("to_step_key", "b2")); got != 2 {
		t.Fatalf("edges on renamed step = %d, want 2", got)
	}

	// Deleting c drops its edge b2->c, keeps a->b2.
	if _, err := unlinkWorkflowAction(ctx, nil, []string{"23"}, nil, nil); err != nil {
		t.Fatalf("unlink error = %v", err)
	}
	edges := fx.edges.where("workflow_id", 11)
	if len(edges) != 1 || stringValue(edges[0]["to_step_key"]) != "b2" {
		t.Fatalf("edges after deleting c = %#v, want only a->b2", edges)
	}

	// An edge must name existing steps.
	if _, err := createWorkflowEdge(ctx, nil, nil, []any{orm.RecordMap{
		"workflow_id": int64(11), "from_step_key": "a", "to_step_key": "c",
	}}, nil); errorCode(err) != orm.ErrInvalidParams {
		t.Fatalf("orphan edge create error = %v, want InvalidParams", err)
	}
	if _, err := createWorkflowEdge(ctx, nil, nil, []any{orm.RecordMap{
		"workflow_id": int64(11), "from_step_key": "a", "to_step_key": "a",
	}}, nil); errorCode(err) != orm.ErrInvalidParams {
		t.Fatalf("self-loop create error = %v, want InvalidParams", err)
	}
}

func TestCopyFlowKeepsDAGAndDeactivatesTransportTriggers(t *testing.T) {
	fx := newWorkflowFixture(t, 0)
	fx.flows.rows[11]["execution_mode"] = WorkflowExecutionModeDAG
	for _, trigger := range []orm.RecordMap{
		{"id": int64(51), "workflow_id": int64(11), "trigger_type": "webhook", "topic": "orders.in", "is_active": true, "config": map[string]any{}},
		{"id": int64(52), "workflow_id": int64(11), "trigger_type": "mqtt", "topic": "devices/+/up", "is_active": true, "config": map[string]any{"qos": int64(1)}},
		{"id": int64(53), "workflow_id": int64(11), "trigger_type": "kafka", "topic": "orders", "is_active": true, "config": map[string]any{"consumer_group": "g1"}},
	} {
		fx.triggers.rows[int64Value(trigger["id"])] = trigger
	}

	result, err := copyFlow(context.Background(), nil, []string{"11"}, nil, nil)
	if err != nil {
		t.Fatalf("copyFlow() error = %v", err)
	}
	newID := int64Value(result.(orm.RecordMap)["id"])
	if newID == 11 || newID == 0 {
		t.Fatalf("copy id = %d", newID)
	}
	copied := fx.flows.rows[newID]
	if stringValue(copied["execution_mode"]) != WorkflowExecutionModeDAG {
		t.Fatalf("copy execution_mode = %#v, want dag", copied["execution_mode"])
	}
	if got := len(fx.actions.where("workflow_id", newID)); got != 3 {
		t.Fatalf("copied actions = %d, want 3", got)
	}
	if got := len(fx.edges.where("workflow_id", newID)); got != 2 {
		t.Fatalf("copied edges = %d, want 2 (the DAG must survive a copy)", got)
	}
	for _, trigger := range fx.triggers.where("workflow_id", newID) {
		isWebhook := stringValue(trigger["trigger_type"]) == WorkflowTriggerTypeWebhook
		if boolValue(trigger["is_active"]) != isWebhook {
			t.Fatalf("copied trigger %v is_active = %v, want only the webhook active", trigger["trigger_type"], trigger["is_active"])
		}
		if isWebhook && stringValue(trigger["topic"]) != stringValue(copied["topic"]) {
			t.Fatalf("copied webhook topic = %q, want the copy's topic %q", trigger["topic"], copied["topic"])
		}
	}
	if len(fx.tx.locked) != 1 || fx.tx.locked[0] != 11 {
		t.Fatalf("copy locked = %v, want the source workflow", fx.tx.locked)
	}
}

func TestExportImportV2IsPortableAndKeepsDAG(t *testing.T) {
	fx := newWorkflowFixture(t, 0)
	fx.flows.rows[11]["execution_mode"] = WorkflowExecutionModeDAG
	fx.flows.rows[11]["max_parallel_steps"] = int64(2)
	fx.flows.rows[11]["group_id"] = [2]any{int64(3), "Partners"}
	fx.flows.rows[11]["layout"] = map[string]any{"nodes": []any{}}
	fx.actions.rows[21]["catalog_id"] = [2]any{int64(7), "Notify"}
	fx.actions.rows[21]["authorization"] = "@cred:partner_token"
	fx.actions.rows[21]["company_id"] = [2]any{int64(1), "Main"}
	fx.catalog.rows[7] = orm.RecordMap{"id": int64(7), "code": "notify", "version": int64(1), "name": "Notify"}
	fx.xmlIDs.rows[1] = orm.RecordMap{"id": int64(1), "model": "tbl.groups", "model_id": "3", "complete_name": "partner.group_partner"}
	fx.triggers.rows[61] = orm.RecordMap{"id": int64(61), "workflow_id": int64(11), "trigger_type": "mqtt", "topic": "orders/in", "is_active": true, "config": map[string]any{"qos": int64(1)}}

	exportedAny, err := exportFlow(context.Background(), nil, []string{"11"}, nil, nil)
	if err != nil {
		t.Fatalf("exportFlow() error = %v", err)
	}
	raw, err := json.Marshal(exportedAny)
	if err != nil {
		t.Fatalf("marshal export: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("unmarshal export: %v", err)
	}
	if int64Value(document["version"]) != 2 {
		t.Fatalf("export version = %#v, want 2", document["version"])
	}
	flow := document["flow"].(map[string]any)
	if flow["group"] != "partner.group_partner" || flow["execution_mode"] != "dag" {
		t.Fatalf("exported flow = %#v, want group by external id and DAG fields", flow)
	}
	firstAction := document["actions"].([]any)[0].(map[string]any)
	for _, field := range []string{"id", "catalog_id", "company_id", "workflow_id"} {
		if _, exists := firstAction[field]; exists {
			t.Fatalf("exported action carries database field %q: %#v", field, firstAction)
		}
	}
	if firstAction["action"] != "notify@1" {
		t.Fatalf("exported action spec = %#v, want notify@1", firstAction["action"])
	}
	if creds := document["credentials"].([]any); len(creds) != 1 || creds[0] != "@cred:partner_token" {
		t.Fatalf("exported credentials = %#v", document["credentials"])
	}
	if len(document["edges"].([]any)) != 2 {
		t.Fatalf("exported edges = %#v, want 2", document["edges"])
	}

	// Import into "another database": same group and catalog code under different ids.
	target := newWorkflowFixture(t, 0)
	delete(target.flows.rows, 11)
	for id := range target.actions.rows {
		delete(target.actions.rows, id)
	}
	for id := range target.edges.rows {
		delete(target.edges.rows, id)
	}
	target.catalog.rows[70] = orm.RecordMap{"id": int64(70), "code": "notify", "version": int64(1), "name": "Notify"}
	target.xmlIDs.rows[1] = orm.RecordMap{"id": int64(1), "model": "tbl.groups", "model_id": "9", "complete_name": "partner.group_partner"}

	importedAny, err := importFlow(context.Background(), nil, nil, []any{document}, nil)
	if err != nil {
		t.Fatalf("importFlow() error = %v", err)
	}
	newID := int64Value(importedAny.(orm.RecordMap)["workflow_id"])
	created := target.flows.rows[newID]
	if workflowIDFromAny(created["group_id"]) != 9 {
		t.Fatalf("imported group_id = %#v, want 9 (resolved by external id, not the source id 3)", created["group_id"])
	}
	if created["execution_mode"] != "dag" || int64Value(created["max_parallel_steps"]) != 2 {
		t.Fatalf("imported DAG settings = %#v", created)
	}
	if stringValue(created["topic"]) == "orders.in" {
		t.Fatalf("import reused the source topic")
	}
	actions := target.actions.where("workflow_id", newID)
	if len(actions) != 3 {
		t.Fatalf("imported actions = %d, want 3", len(actions))
	}
	for _, action := range actions {
		if workflowIDFromAny(action["catalog_id"]) != 70 {
			t.Fatalf("imported catalog_id = %#v, want this database's row 70 (never the source id 7)", action["catalog_id"])
		}
	}
	if got := len(target.edges.where("workflow_id", newID)); got != 2 {
		t.Fatalf("imported edges = %d, want 2", got)
	}
	importedTriggers := target.triggers.where("workflow_id", newID)
	if len(importedTriggers) != 1 || boolValue(importedTriggers[0]["is_active"]) {
		t.Fatalf("imported triggers = %#v, want the mqtt trigger inactive", importedTriggers)
	}
	if target.tx.committed != 1 {
		t.Fatalf("import transactions committed = %d, want 1", target.tx.committed)
	}
}

func TestImportVersionAndGroupRules(t *testing.T) {
	fx := newWorkflowFixture(t, 0)
	fx.xmlIDs.rows[1] = orm.RecordMap{"id": int64(1), "model": "tbl.groups", "model_id": "2", "complete_name": workflowRestrictedGroupXMLID}

	if _, err := importFlow(context.Background(), nil, nil, []any{map[string]any{"version": 3, "flow": map[string]any{}}}, nil); errorCode(err) != orm.ErrInvalidParams {
		t.Fatalf("future version error = %v, want InvalidParams", err)
	}
	if _, err := importFlow(context.Background(), nil, nil, []any{map[string]any{"version": 2}}, nil); errorCode(err) != orm.ErrInvalidParams {
		t.Fatalf("missing flow error = %v, want InvalidParams", err)
	}
	if _, err := importFlow(context.Background(), nil, nil, []any{map[string]any{"version": 2, "flow": map[string]any{"group": "nope.group"}}}, nil); errorCode(err) != orm.ErrInvalidParams {
		t.Fatalf("unknown group error = %v, want InvalidParams", err)
	}

	// v1 carried a raw database id: it is never mapped verbatim; the import falls back to the
	// system group instead of becoming public.
	result, err := importFlow(context.Background(), nil, nil, []any{map[string]any{
		"flow": map[string]any{"name": "legacy", "topic": "legacy", "group_id": []any{float64(3), "Partners"}},
	}}, nil)
	if err != nil {
		t.Fatalf("v1 import error = %v", err)
	}
	created := fx.flows.rows[int64Value(result.(orm.RecordMap)["workflow_id"])]
	if workflowIDFromAny(created["group_id"]) != 2 {
		t.Fatalf("v1 group_id = %#v, want the system group 2", created["group_id"])
	}

	// A failing step aborts the whole import inside its transaction.
	before := len(fx.tx.locked) + fx.tx.failed
	_, err = importFlow(context.Background(), nil, nil, []any{map[string]any{
		"version": 2,
		"flow":    map[string]any{"name": "broken"},
		"actions": []any{map[string]any{"action": "notify@1", "step_key": "a", "target_ref": "https://x"}, map[string]any{"action": "notify@x", "step_key": "b"}},
	}}, nil)
	if errorCode(err) != orm.ErrInvalidParams || fx.tx.failed+len(fx.tx.locked) != before+1 {
		t.Fatalf("broken import error = %v failed=%d, want InvalidParams inside one failed transaction", err, fx.tx.failed)
	}
}

func TestWorkflowImportPayloadFromMCPKwargs(t *testing.T) {
	payload := workflowImportExportPayload(nil, map[string]any{"payload": map[string]any{"flow": map[string]any{"name": "x"}}})
	if payload["flow"] == nil {
		t.Fatalf("payload kwarg not read: %#v", payload)
	}
	payload = workflowImportExportPayload(nil, map[string]any{"flow": map[string]any{"name": "x"}, "version": 2})
	if payload["flow"] == nil || payload["version"] != 2 {
		t.Fatalf("flat kwargs not read: %#v", payload)
	}
}

func TestWorkflowRevisionIsServerOwned(t *testing.T) {
	normalized, err := normalizeFlowValues(orm.RecordMap{"revision": 99, "expected_revision": 3, "topic": "t"}, nil, true)
	if err != nil {
		t.Fatalf("normalizeFlowValues() error = %v", err)
	}
	if _, exists := normalized["revision"]; exists {
		t.Fatalf("client revision kept: %#v", normalized)
	}
	if _, exists := normalized["expected_revision"]; exists {
		t.Fatalf("expected_revision kept as a column: %#v", normalized)
	}
}

func TestRunWorkflowMutationPropagatesBodyError(t *testing.T) {
	fake := stubWorkflowTx(t, map[int64]int64{5: 1})
	boom := errors.New("boom")
	if _, err := runWorkflowMutation(context.Background(), "test", 5, nil, func(context.Context) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("error = %v, want boom", err)
	}
	if fake.revisions[5] != 1 || fake.failed != 1 {
		t.Fatalf("failed body bumped the revision: %d (failed=%d)", fake.revisions[5], fake.failed)
	}
}
