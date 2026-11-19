package bootstrap

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

func TestReconcileWorkflowUIShellUpdatesPersistedWorkflowMenuAndAction(t *testing.T) {
	original := executeRuntimeMethod
	defer func() {
		executeRuntimeMethod = original
	}()

	writes := []struct {
		model  string
		ids    []string
		values orm.RecordMap
	}{}

	executeRuntimeMethod = func(_ context.Context, modelName, methodName string, ids []string, args []any, kwargs map[string]any) (any, error) {
		switch {
		case modelName == sysModelDataModelName && methodName == "search_read":
			bidValue := args[0].([]any)[0].([]any)[2]
			switch bidValue {
			case workflowWindowActionBID:
				return []orm.RecordMap{{"model": uiActionWindowModelName, "model_id": int64(21)}}, nil
			case workflowWindowListViewBID:
				return []orm.RecordMap{{"model": "ui.view", "model_id": int64(41)}}, nil
			case workflowMenuBID:
				return []orm.RecordMap{{"model": uiMenuModelName, "model_id": int64(31)}}, nil
			case workflowCanvasMenuBID:
				return []orm.RecordMap{{"model": uiMenuModelName, "model_id": int64(32)}}, nil
			default:
				return []orm.RecordMap{}, nil
			}
		case modelName == uiActionWindowModelName && methodName == "read":
			return []orm.RecordMap{{
				"name":      workflowWindowActionName,
				"sys_model": workflowmodels.FlowModelName,
				"view_mode": "canvas,list,kanban",
				"view_id":   int64(99),
			}}, nil
		case modelName == uiMenuModelName && methodName == "read":
			if len(ids) == 1 && ids[0] == "31" {
				return []orm.RecordMap{{
					"name":       workflowMenuName,
					"route_path": workflowMenuRoutePath,
					"action": orm.RecordMap{
						"type":      "ui.actions.url",
						"url":       workflowMenuRoutePath,
						"sys_model": workflowmodels.FlowModelName,
					},
				}}, nil
			}
			return []orm.RecordMap{{
				"name":       workflowCanvasMenuName,
				"route_path": workflowMenuRoutePath,
				"action": orm.RecordMap{
					"type":      uiActionWindowModelName,
					"bid":       workflowCanvasWindowActionBID,
					"name":      workflowCanvasActionName,
					"url":       workflowMenuRoutePath,
					"sys_model": workflowmodels.FlowModelName,
				},
			}}, nil
		case methodName == "write":
			writes = append(writes, struct {
				model  string
				ids    []string
				values orm.RecordMap
			}{
				model:  modelName,
				ids:    append([]string(nil), ids...),
				values: kwargs["values"].(orm.RecordMap),
			})
			return true, nil
		default:
			return nil, fmt.Errorf("unexpected call %s.%s", modelName, methodName)
		}
	}

	if err := ReconcileWorkflowUIShell(context.Background()); err != nil {
		t.Fatalf("ReconcileWorkflowUIShell() error = %v", err)
	}

	if len(writes) != 3 {
		t.Fatalf("writes = %d, want 3", len(writes))
	}
	if writes[0].model != uiActionWindowModelName || !reflect.DeepEqual(writes[0].ids, []string{"21"}) {
		t.Fatalf("window action write = %#v", writes[0])
	}
	if got := writes[0].values["view_mode"]; got != workflowWindowViewMode {
		t.Fatalf("window action view_mode = %#v, want %q", got, workflowWindowViewMode)
	}
	if got := writes[0].values["view_id"]; got != "41" {
		t.Fatalf("window action view_id = %#v, want %q", got, "41")
	}
	if writes[1].model != uiMenuModelName || !reflect.DeepEqual(writes[1].ids, []string{"31"}) {
		t.Fatalf("menu write = %#v", writes[1])
	}
	action, ok := writes[1].values["action"].(orm.RecordMap)
	if !ok {
		t.Fatalf("menu action payload = %#v", writes[1].values["action"])
	}
	if action["bid"] != workflowWindowActionBID {
		t.Fatalf("menu action bid = %#v, want %q", action["bid"], workflowWindowActionBID)
	}
	if writes[2].model != uiMenuModelName || !reflect.DeepEqual(writes[2].ids, []string{"32"}) {
		t.Fatalf("canvas menu write = %#v", writes[2])
	}
	canvasAction, ok := writes[2].values["action"].(orm.RecordMap)
	if !ok {
		t.Fatalf("canvas menu action payload = %#v", writes[2].values["action"])
	}
	if got := writes[2].values["route_path"]; got != workflowCanvasMenuRoutePath {
		t.Fatalf("canvas menu route_path = %#v, want %q", got, workflowCanvasMenuRoutePath)
	}
	if canvasAction["bid"] != workflowCanvasWindowActionBID {
		t.Fatalf("canvas menu action bid = %#v, want %q", canvasAction["bid"], workflowCanvasWindowActionBID)
	}
}

func TestReconcileWorkflowUIShellSkipsAlreadyAlignedRows(t *testing.T) {
	original := executeRuntimeMethod
	defer func() {
		executeRuntimeMethod = original
	}()

	writeCount := 0
	executeRuntimeMethod = func(_ context.Context, modelName, methodName string, ids []string, args []any, kwargs map[string]any) (any, error) {
		switch {
		case modelName == sysModelDataModelName && methodName == "search_read":
			bidValue := args[0].([]any)[0].([]any)[2]
			switch bidValue {
			case workflowWindowActionBID:
				return []orm.RecordMap{{"model": uiActionWindowModelName, "model_id": "21"}}, nil
			case workflowWindowListViewBID:
				return []orm.RecordMap{{"model": "ui.view", "model_id": "41"}}, nil
			case workflowMenuBID:
				return []orm.RecordMap{{"model": uiMenuModelName, "model_id": "31"}}, nil
			case workflowCanvasMenuBID:
				return []orm.RecordMap{{"model": uiMenuModelName, "model_id": "32"}}, nil
			default:
				return []orm.RecordMap{}, nil
			}
		case modelName == uiActionWindowModelName && methodName == "read":
			return []orm.RecordMap{{
				"name":      workflowWindowActionName,
				"sys_model": workflowmodels.FlowModelName,
				"view_mode": workflowWindowViewMode,
				"view_id":   int64(41),
			}}, nil
		case modelName == uiMenuModelName && methodName == "read":
			if len(ids) == 1 && ids[0] == "31" {
				return []orm.RecordMap{{
					"name":       workflowMenuName,
					"route_path": workflowMenuRoutePath,
					"action": orm.RecordMap{
						"type":      uiActionWindowModelName,
						"bid":       workflowWindowActionBID,
						"name":      workflowWindowActionName,
						"sys_model": workflowmodels.FlowModelName,
						"url":       workflowMenuRoutePath,
					},
				}}, nil
			}
			return []orm.RecordMap{{
				"name":       workflowCanvasMenuName,
				"route_path": workflowCanvasMenuRoutePath,
				"action": orm.RecordMap{
					"type":      uiActionWindowModelName,
					"bid":       workflowCanvasWindowActionBID,
					"name":      workflowCanvasActionName,
					"sys_model": workflowmodels.FlowModelName,
					"url":       workflowCanvasMenuRoutePath,
				},
			}}, nil
		case methodName == "write":
			writeCount++
			return true, nil
		default:
			return nil, fmt.Errorf("unexpected call %s.%s", modelName, methodName)
		}
	}

	if err := ReconcileWorkflowUIShell(context.Background()); err != nil {
		t.Fatalf("ReconcileWorkflowUIShell() error = %v", err)
	}
	if writeCount != 0 {
		t.Fatalf("writeCount = %d, want 0", writeCount)
	}
}
