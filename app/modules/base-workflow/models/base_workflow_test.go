package models

// File overview:
// - Responsibility: verifies workflow model state, metadata, and persistence behavior for base workflow.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down workflow model semantics so request IDs, run state, and action definitions remain replay-safe and observable.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestFlowModelSpecInheritsDefaultUnlinkRuntimeMethod(t *testing.T) {
	meta, err := orm.RegisterModelType[FlowRecord](FlowModelSpec)
	if err != nil {
		t.Fatalf("RegisterModelType() error = %v", err)
	}

	method, ok := meta.LookupRuntimeMethod("unlink")
	if !ok {
		t.Fatalf("expected base.workflow to inherit default unlink runtime method")
	}
	if method.Name != "unlink" {
		t.Fatalf("unlink method name = %q, want unlink", method.Name)
	}
	if !method.Public {
		t.Fatalf("expected inherited unlink runtime method to stay public")
	}
	if method.ReadOnly {
		t.Fatalf("expected inherited unlink runtime method to be writable")
	}
}

func TestFlowModelSpecRegistersCopyRuntimeMethods(t *testing.T) {
	meta, err := orm.RegisterModelType[FlowRecord](FlowModelSpec)
	if err != nil {
		t.Fatalf("RegisterModelType() error = %v", err)
	}

	copyDataMethod, ok := meta.LookupRuntimeMethod("copy_data")
	if !ok {
		t.Fatalf("expected base.workflow to expose copy_data")
	}
	if !copyDataMethod.Public || !copyDataMethod.ReadOnly {
		t.Fatalf("copy_data flags = %#v, want public read-only method", copyDataMethod)
	}

	copyMethod, ok := meta.LookupRuntimeMethod("copy")
	if !ok {
		t.Fatalf("expected base.workflow to expose copy")
	}
	if !copyMethod.Public || copyMethod.ReadOnly {
		t.Fatalf("copy flags = %#v, want public writable method", copyMethod)
	}
}

func TestNormalizeFlowValuesAppliesDefinitionDefaults(t *testing.T) {
	values, err := normalizeFlowValues(orm.RecordMap{
		"topic": "topic.alpha",
	}, nil, true)
	if err != nil {
		t.Fatalf("normalizeFlowValues() error = %v", err)
	}
	if got := values["version"]; got != int64(1) {
		t.Fatalf("version = %#v, want 1", got)
	}
	if got := values["is_active"]; got != true {
		t.Fatalf("is_active = %#v, want true", got)
	}
	if got, ok := values["layout"]; ok {
		t.Fatalf("layout = %#v, want field omitted when unset", got)
	}
}

func TestNormalizeFlowValuesRejectsBlankTopic(t *testing.T) {
	_, err := normalizeFlowValues(orm.RecordMap{
		"topic": "   ",
	}, nil, true)
	if err == nil {
		t.Fatalf("expected blank topic to fail")
	}
}

func TestNormalizeFlowValuesNormalizesLayoutObject(t *testing.T) {
	values, err := normalizeFlowValues(orm.RecordMap{
		"topic": "topic.alpha",
		"layout": orm.RecordMap{
			"nodes": []any{
				map[string]any{"id": "trigger"},
			},
		},
	}, nil, true)
	if err != nil {
		t.Fatalf("normalizeFlowValues() error = %v", err)
	}

	layout, ok := values["layout"].(map[string]any)
	if !ok {
		t.Fatalf("layout = %#v, want map[string]any", values["layout"])
	}
	nodes, ok := layout["nodes"].([]any)
	if !ok || len(nodes) != 1 {
		t.Fatalf("layout.nodes = %#v, want one node", layout["nodes"])
	}
}

func TestNormalizeFlowValuesAcceptsStringifiedLayoutObject(t *testing.T) {
	values, err := normalizeFlowValues(orm.RecordMap{
		"topic":  "topic.alpha",
		"layout": `{"nodes":[{"id":"trigger"}]}`,
	}, nil, true)
	if err != nil {
		t.Fatalf("normalizeFlowValues() error = %v", err)
	}

	layout, ok := values["layout"].(map[string]any)
	if !ok {
		t.Fatalf("layout = %#v, want map[string]any", values["layout"])
	}
	nodes, ok := layout["nodes"].([]any)
	if !ok || len(nodes) != 1 {
		t.Fatalf("layout.nodes = %#v, want one node", layout["nodes"])
	}
}

func TestNormalizeFlowValuesRejectsNonObjectLayout(t *testing.T) {
	_, err := normalizeFlowValues(orm.RecordMap{
		"topic":  "topic.alpha",
		"layout": []any{"bad"},
	}, nil, true)
	if err == nil {
		t.Fatalf("expected non-object layout to fail")
	}
}

func TestWriteFlowReadsOnlyScalarFieldsBeforeLayoutWrite(t *testing.T) {
	var readCalls [][]string
	layoutWriteSeen := false

	restoreFlow := orm.StubRuntimeRepository(FlowModelName, orm.RuntimeRepositoryStub{
		Read: func(_ context.Context, ids []string, fields []string) ([]orm.RecordMap, error) {
			readCalls = append(readCalls, append([]string(nil), fields...))
			if len(ids) != 1 || ids[0] != "11" {
				t.Fatalf("flow ids = %#v, want [11]", ids)
			}
			return []orm.RecordMap{{
				"id":        int64(11),
				"topic":     "topic.alpha",
				"version":   int64(1),
				"is_active": true,
				"group_id":  nil,
			}}, nil
		},
		Write: func(_ context.Context, ids []string, values orm.RecordMap) (bool, error) {
			if len(ids) != 1 || ids[0] != "11" {
				t.Fatalf("flow write ids = %#v, want [11]", ids)
			}
			if _, ok := values["flow_map"]; ok {
				return true, nil
			}
			layout, ok := values["layout"].(map[string]any)
			if !ok || len(layout) == 0 {
				t.Fatalf("layout write = %#v, want normalized object", values["layout"])
			}
			layoutWriteSeen = true
			return true, nil
		},
	})
	defer restoreFlow()
	restoreAction := orm.StubRuntimeRepository(WorkflowActionModelName, orm.RuntimeRepositoryStub{
		SearchRead: func(_ context.Context, filter orm.RecordMap, fields []string) ([]orm.RecordMap, error) {
			if int64Value(filter["workflow_id"]) != 11 {
				t.Fatalf("workflow_id filter = %#v, want 11", filter["workflow_id"])
			}
			_ = fields
			return nil, nil
		},
	})
	defer restoreAction()

	if _, err := writeFlow(context.Background(), nil, []string{"11"}, []any{
		orm.RecordMap{
			"layout": map[string]any{
				"nodes": []any{map[string]any{"id": "trigger"}},
			},
		},
	}, nil); err != nil {
		t.Fatalf("writeFlow() error = %v", err)
	}

	expected := []string{"id", "topic", "version", "is_active", "group_id", "company_id"}
	if len(readCalls) == 0 {
		t.Fatalf("expected at least one flow read")
	}
	firstRead := readCalls[0]
	if len(firstRead) != len(expected) {
		t.Fatalf("first read fields = %#v, want %#v", firstRead, expected)
	}
	for index := range expected {
		if firstRead[index] != expected[index] {
			t.Fatalf("first read fields = %#v, want %#v", firstRead, expected)
		}
	}
	if !layoutWriteSeen {
		t.Fatalf("expected layout write before flow_map regeneration")
	}
}

func TestRenderWorkflowFlowMapMarkdownBuildsMermaidFlow(t *testing.T) {
	markdown := renderWorkflowFlowMapMarkdown(
		orm.RecordMap{
			"name":    "bank_account_import_v1",
			"topic":   "bank",
			"version": int64(1),
		},
		[]orm.RecordMap{
			{
				"sequence":       int64(10),
				"name":           "Parse Bank Payload",
				"action_code":    "parse_payload",
				"action_version": int64(1),
				"step_key":       "parse_bank_payload",
			},
			{
				"sequence":       int64(20),
				"name":           "Create Partner Bank Account",
				"action_code":    "crud_model",
				"action_version": int64(1),
				"step_key":       "create_partner_bank_account",
				"extra_params": map[string]any{
					"condition": map[string]any{"field": "partner_id", "exists": true},
				},
			},
		},
	)

	if !strings.Contains(markdown, "# Workflow Flow Map: bank_account_import_v1") {
		t.Fatalf("expected markdown title, got %q", markdown)
	}
	if !strings.Contains(markdown, "```mermaid") {
		t.Fatalf("expected mermaid code fence, got %q", markdown)
	}
	if !strings.Contains(markdown, "trigger --> parse_bank_payload") {
		t.Fatalf("expected trigger edge, got %q", markdown)
	}
	if !strings.Contains(markdown, "parse_bank_payload --> create_partner_bank_account") {
		t.Fatalf("expected step edge, got %q", markdown)
	}
	if !strings.Contains(markdown, "[conditional]") {
		t.Fatalf("expected conditional marker, got %q", markdown)
	}
}

func TestFlowRecordUnmarshalJSONTreatsFalseGroupIDAsNil(t *testing.T) {
	var record FlowRecord
	if err := json.Unmarshal([]byte(`{"name":"Test","topic":"demo.topic","version":1,"is_active":true,"group_id":false}`), &record); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if record.GroupID != nil {
		t.Fatalf("group_id = %#v, want nil", record.GroupID)
	}
}

func TestFlowRecordUnmarshalJSONTreatsFalseRelationFieldsAsEmpty(t *testing.T) {
	var record FlowRecord
	err := json.Unmarshal([]byte(`{
		"name":"Test",
		"topic":"demo.topic",
		"version":1,
		"is_active":true,
		"group_id":false,
		"action_ids":false,
		"run_ids":false
	}`), &record)
	if err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if record.GroupID != nil {
		t.Fatalf("group_id = %#v, want nil", record.GroupID)
	}
	if record.ActionIDs != nil {
		t.Fatalf("action_ids = %#v, want nil", record.ActionIDs)
	}
	if record.RunIDs != nil {
		t.Fatalf("run_ids = %#v, want nil", record.RunIDs)
	}
}

func TestFlowRecordUnmarshalJSONExtractsRelationTupleIDs(t *testing.T) {
	var record FlowRecord
	err := json.Unmarshal([]byte(`{
		"name":"Test",
		"topic":"demo.topic",
		"version":1,
		"is_active":true,
		"action_ids":[[11,"Parse"],[12,"Save"]],
		"run_ids":[21,22]
	}`), &record)
	if err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(record.ActionIDs) != 2 || record.ActionIDs[0] != 11 || record.ActionIDs[1] != 12 {
		t.Fatalf("action_ids = %#v, want [11 12]", record.ActionIDs)
	}
	if len(record.RunIDs) != 2 || record.RunIDs[0] != 21 || record.RunIDs[1] != 22 {
		t.Fatalf("run_ids = %#v, want [21 22]", record.RunIDs)
	}
}

func TestCopyDataFlowBuildsDuplicateSafeParentValues(t *testing.T) {
	restoreFlow := orm.StubRuntimeRepository(FlowModelName, orm.RuntimeRepositoryStub{
		Read: func(_ context.Context, ids []string, fields []string) ([]orm.RecordMap, error) {
			if len(ids) != 1 || ids[0] != "11" {
				t.Fatalf("ids = %#v, want [11]", ids)
			}
			_ = fields
			return []orm.RecordMap{{
				"id":          int64(11),
				"name":        "Bank Sync",
				"topic":       "bank.sync",
				"flow_map":    "old map",
				"action_ids":  []any{int64(21)},
				"run_ids":     []any{int64(31)},
				"description": "source",
			}}, nil
		},
	})
	defer restoreFlow()

	result, err := copyDataFlow(context.Background(), nil, []string{"11"}, nil, nil)
	if err != nil {
		t.Fatalf("copyDataFlow() error = %v", err)
	}
	values, ok := result.(orm.RecordMap)
	if !ok {
		t.Fatalf("result = %T, want orm.RecordMap", result)
	}
	if _, exists := values["id"]; exists {
		t.Fatalf("copied values should not include id: %#v", values)
	}
	if _, exists := values["flow_map"]; exists {
		t.Fatalf("copied values should not include flow_map: %#v", values)
	}
	if _, exists := values["action_ids"]; exists {
		t.Fatalf("copied values should not include action_ids: %#v", values)
	}
	if got := stringValue(values["name"]); !strings.HasPrefix(got, "Bank Sync-copy-") {
		t.Fatalf("name = %q, want copy-safe suffix", got)
	}
	if got := stringValue(values["topic"]); !strings.HasPrefix(got, "bank.sync-copy-") {
		t.Fatalf("topic = %q, want copy-safe suffix", got)
	}
}

func TestCopyFlowDuplicatesActionsAndTriggers(t *testing.T) {
	var createdFlowValues orm.RecordMap
	var createdActionValues []orm.RecordMap
	var createdTriggerValues []orm.RecordMap

	restoreFlow := orm.StubRuntimeRepository(FlowModelName, orm.RuntimeRepositoryStub{
		Read: func(_ context.Context, ids []string, fields []string) ([]orm.RecordMap, error) {
			_ = fields
			if len(ids) != 1 {
				t.Fatalf("flow read ids = %#v, want one id", ids)
			}
			switch ids[0] {
			case "11":
				return []orm.RecordMap{{
					"id":          int64(11),
					"name":        "Order Import",
					"topic":       "orders.in",
					"version":     int64(3),
					"is_active":   true,
					"group_id":    nil,
					"description": "source flow",
				}}, nil
			case "44":
				return []orm.RecordMap{{
					"id":      int64(44),
					"name":    stringValue(createdFlowValues["name"]),
					"topic":   stringValue(createdFlowValues["topic"]),
					"state":   stringValue(createdFlowValues["state"]), // created as a draft (plan T97)
					"version": int64(3),
				}}, nil
			default:
				t.Fatalf("unexpected flow read ids = %#v", ids)
				return nil, nil
			}
		},
		Create: func(_ context.Context, values orm.RecordMap) (orm.RecordMap, error) {
			createdFlowValues = cloneRecordMap(values)
			out := cloneRecordMap(values)
			out["id"] = int64(44)
			return out, nil
		},
		Write: func(_ context.Context, ids []string, values orm.RecordMap) (bool, error) {
			if len(ids) != 1 || ids[0] != "44" {
				t.Fatalf("flow write ids = %#v, want [44]", ids)
			}
			if len(values) == 1 && values["state"] == WorkflowStateDraft {
				return true, nil // markWorkflowDraft writes unconditionally
			}
			if _, ok := values["flow_map"]; !ok {
				t.Fatalf("flow write values = %#v, want flow_map regeneration", values)
			}
			return true, nil
		},
	})
	defer restoreFlow()

	restoreActions := orm.StubRuntimeRepository(WorkflowActionModelName, orm.RuntimeRepositoryStub{
		SearchRead: func(_ context.Context, filter orm.RecordMap, fields []string) ([]orm.RecordMap, error) {
			_ = fields
			switch int64Value(filter["workflow_id"]) {
			case 11:
				return []orm.RecordMap{{
					"id":          int64(21),
					"workflow_id": [2]any{int64(11), "Order Import"},
					"catalog_id":  [2]any{int64(7), "Notify"},
					"sequence":    int64(10),
					"name":        "Notify Partner",
					"action_code": "notify",
					"target_ref":  "https://partner.local/hook",
					"step_key":    "notify_partner",
					"is_active":   true,
				}}, nil
			case 44:
				return nil, nil
			default:
				t.Fatalf("action search workflow_id = %#v, want 11 or 44", filter["workflow_id"])
			}
			return nil, nil
		},
		Create: func(_ context.Context, values orm.RecordMap) (orm.RecordMap, error) {
			createdActionValues = append(createdActionValues, cloneRecordMap(values))
			out := cloneRecordMap(values)
			out["id"] = int64(81)
			out["workflow_id"] = []any{int64(44), "Order Import Copy"}
			return out, nil
		},
	})
	defer restoreActions()
	restoreActionCatalog := orm.StubRuntimeRepository(WorkflowActionCatalogModelName, orm.RuntimeRepositoryStub{
		Read: func(_ context.Context, ids []string, fields []string) ([]orm.RecordMap, error) {
			_ = fields
			if len(ids) != 1 || ids[0] != "7" {
				t.Fatalf("catalog read ids = %#v, want [7]", ids)
			}
			return []orm.RecordMap{{
				"id":          int64(7),
				"name":        "Notify",
				"code":        "notify",
				"version":     int64(1),
				"is_active":   true,
				"description": "Notify action",
			}}, nil
		},
	})
	defer restoreActionCatalog()

	restoreTriggers := orm.StubRuntimeRepository(WorkflowTriggerModelName, orm.RuntimeRepositoryStub{
		SearchRead: func(_ context.Context, filter orm.RecordMap, fields []string) ([]orm.RecordMap, error) {
			_ = fields
			if int64Value(filter["workflow_id"]) != 11 {
				t.Fatalf("trigger search workflow_id = %#v, want 11", filter["workflow_id"])
			}
			return []orm.RecordMap{{
				"id":           int64(22),
				"workflow_id":  [2]any{int64(11), "Order Import"},
				"trigger_type": "webhook",
				"topic":        "orders.in",
				"is_active":    true,
				"config":       map[string]any{},
			}}, nil
		},
		Create: func(_ context.Context, values orm.RecordMap) (orm.RecordMap, error) {
			createdTriggerValues = append(createdTriggerValues, cloneRecordMap(values))
			out := cloneRecordMap(values)
			out["id"] = int64(91)
			return out, nil
		},
	})
	defer restoreTriggers()

	txFake := stubWorkflowTx(t, nil)
	newMemRepo(t, WorkflowEdgeModelName, 1)
	result, err := copyFlow(context.Background(), nil, []string{"11"}, nil, nil)
	if err != nil {
		t.Fatalf("copyFlow() error = %v", err)
	}
	copied, ok := result.(orm.RecordMap)
	if !ok {
		t.Fatalf("result = %T, want orm.RecordMap", result)
	}
	if got := int64Value(copied["id"]); got != 44 {
		t.Fatalf("copied id = %d, want 44", got)
	}
	if len(createdActionValues) != 1 {
		t.Fatalf("createdActionValues = %#v, want one action", createdActionValues)
	}
	if got := workflowIDFromAny(createdActionValues[0]["workflow_id"]); got != 44 {
		t.Fatalf("action workflow_id = %#v, want 44", createdActionValues[0]["workflow_id"])
	}
	if len(createdTriggerValues) != 1 {
		t.Fatalf("createdTriggerValues = %#v, want one trigger", createdTriggerValues)
	}
	if got := workflowIDFromAny(createdTriggerValues[0]["workflow_id"]); got != 44 {
		t.Fatalf("trigger workflow_id = %#v, want 44", createdTriggerValues[0]["workflow_id"])
	}
	if got := stringValue(createdTriggerValues[0]["topic"]); got != stringValue(createdFlowValues["topic"]) {
		t.Fatalf("trigger topic = %q, want copied workflow topic %q", got, stringValue(createdFlowValues["topic"]))
	}
	// The copy ran in one transaction locked on the source workflow (plan T59).
	if len(txFake.locked) != 1 || txFake.locked[0] != 11 || txFake.committed != 1 {
		t.Fatalf("copy transaction locked=%v committed=%d, want one transaction locked on 11", txFake.locked, txFake.committed)
	}
}
