package models

import (
	"context"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestExportImportFlowRoundTrip(t *testing.T) {
	var createdFlowValues orm.RecordMap
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
					"layout":      map[string]any{"nodes": []any{map[string]any{"id": "n1", "x": float64(10), "y": float64(20)}}},
				}}, nil
			case "44":
				return []orm.RecordMap{{
					"id":      int64(44),
					"name":    stringValue(createdFlowValues["name"]),
					"topic":   stringValue(createdFlowValues["topic"]),
					"state":   stringValue(createdFlowValues["state"]), // created as a draft (plan T97)
					"version": int64(1),
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

	var createdActionValues []orm.RecordMap
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
				// writeWorkflowFlowMap re-reads the target workflow's actions to
				// regenerate flow_map — before any action has been created yet
				// (createFlow's own post-create call) and again after (the
				// explicit call at the end of importFlow).
				if len(createdActionValues) == 0 {
					return nil, nil
				}
				return createdActionValues, nil
			default:
				t.Fatalf("action search workflow_id = %#v, want 11 or 44", filter["workflow_id"])
			}
			return nil, nil
		},
		Create: func(_ context.Context, values orm.RecordMap) (orm.RecordMap, error) {
			createdActionValues = append(createdActionValues, cloneRecordMap(values))
			out := cloneRecordMap(values)
			out["id"] = int64(81)
			out["workflow_id"] = []any{int64(44), "Order Import-copy"}
			return out, nil
		},
	})
	defer restoreActions()

	restoreActionCatalog := orm.StubRuntimeRepository(WorkflowActionCatalogModelName, orm.RuntimeRepositoryStub{
		// Import looks the catalog row up again by code@version in the target database.
		SearchRead: func(_ context.Context, filter orm.RecordMap, _ []string) ([]orm.RecordMap, error) {
			if stringValue(filter["code"]) != "notify" || int64Value(filter["version"]) != 1 {
				t.Fatalf("catalog search filter = %#v, want notify@1", filter)
			}
			return []orm.RecordMap{{"id": int64(7)}}, nil
		},
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

	var createdTriggerValues []orm.RecordMap
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

	stubWorkflowTx(t, nil)
	newMemRepo(t, WorkflowEdgeModelName, 1)
	exportedAny, err := exportFlow(context.Background(), nil, []string{"11"}, nil, nil)
	if err != nil {
		t.Fatalf("exportFlow() error = %v", err)
	}
	exported, ok := exportedAny.(orm.RecordMap)
	if !ok {
		t.Fatalf("exportFlow() result = %T, want orm.RecordMap", exportedAny)
	}
	if got := exported["version"]; got != workflowExportVersion {
		t.Fatalf("exported version = %#v, want %d", got, workflowExportVersion)
	}
	exportedActions, ok := exported["actions"].([]orm.RecordMap)
	if !ok || len(exportedActions) != 1 {
		t.Fatalf("exported actions = %#v, want one action", exported["actions"])
	}
	if _, exists := exportedActions[0]["id"]; exists {
		t.Fatalf("exported action should not include id: %#v", exportedActions[0])
	}
	if _, exists := exportedActions[0]["workflow_id"]; exists {
		t.Fatalf("exported action should not include workflow_id: %#v", exportedActions[0])
	}

	importedAny, err := importFlow(context.Background(), nil, nil, []any{map[string]any(exported)}, nil)
	if err != nil {
		t.Fatalf("importFlow() error = %v", err)
	}
	imported, ok := importedAny.(orm.RecordMap)
	if !ok {
		t.Fatalf("importFlow() result = %T, want orm.RecordMap", importedAny)
	}
	if got := int64Value(imported["workflow_id"]); got != 44 {
		t.Fatalf("imported workflow_id = %d, want 44", got)
	}
	if got := stringValue(createdFlowValues["name"]); got == "Order Import" || got == "" {
		t.Fatalf("imported name = %q, want a fresh unique name distinct from the source", got)
	}
	if got := stringValue(createdFlowValues["topic"]); got == "orders.in" || got == "" {
		t.Fatalf("imported topic = %q, want a fresh unique topic distinct from the source (never collide with a live webhook)", got)
	}

	if len(createdActionValues) != 1 {
		t.Fatalf("createdActionValues = %#v, want one action", createdActionValues)
	}
	if got := workflowIDFromAny(createdActionValues[0]["workflow_id"]); got != 44 {
		t.Fatalf("action workflow_id = %#v, want 44", createdActionValues[0]["workflow_id"])
	}
	if got := stringValue(createdActionValues[0]["action_code"]); got != "notify" {
		t.Fatalf("action_code = %q, want notify (unchanged through the round trip)", got)
	}
	if got := stringValue(createdActionValues[0]["step_key"]); got != "notify_partner" {
		t.Fatalf("step_key = %q, want notify_partner (unchanged through the round trip)", got)
	}

	if len(createdTriggerValues) != 1 {
		t.Fatalf("createdTriggerValues = %#v, want one trigger", createdTriggerValues)
	}
	if got := workflowIDFromAny(createdTriggerValues[0]["workflow_id"]); got != 44 {
		t.Fatalf("trigger workflow_id = %#v, want 44", createdTriggerValues[0]["workflow_id"])
	}
}
