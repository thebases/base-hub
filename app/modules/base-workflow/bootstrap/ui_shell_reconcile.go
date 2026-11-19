package bootstrap

// File overview:
// - Responsibility: reconciles workflow-specific UI shell seed rows that must stay aligned with
//   the frontend view-entry contract even after XML metadata already exists in older databases.
//   Lives in its own package (not `modules/workflow/models`) because it manipulates other
//   modules' tables (`sys.model.data`, `ui.actions.window`, `ui.menu`), not any record owned by
//   the workflow module itself.
// - Key symbols: `ReconcileWorkflowUIShell`.
// - Pattern: Uses the generic ORM runtime API so startup fixes reuse the same write normalization as regular model updates.

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

const (
	sysModelDataModelName         = "sys.model.data"
	uiActionWindowModelName       = "ui.actions.window"
	uiMenuModelName               = "ui.menu"
	workflowMenuRoutePath         = "/web/base.workflow"
	workflowCanvasMenuRoutePath   = "/web/base.workflow/canvas"
	workflowMenuName              = "Workflow"
	workflowCanvasMenuName        = "Workflow Canvas"
	workflowMenuBID               = "menu.workflow_view"
	workflowCanvasMenuBID         = "menu.workflow_canvas_view"
	workflowWindowActionName      = "Workflow"
	workflowCanvasActionName      = "Workflow Canvas"
	workflowWindowActionBID       = "workflow.action_window_workflow"
	workflowCanvasWindowActionBID = "workflow.action_workflow_canvas_standalone"
	workflowWindowViewMode        = "list,kanban,canvas"
	workflowWindowListViewBID     = "workflow.view_base_etl_flow_list"
	// rootCompanyID mirrors internal/models.rootCompanyID (unexported there):
	// ui.menu/ui.actions.window writes are root-company-gated, and this
	// startup seeder has no RPC session to inherit a company from.
	rootCompanyID int64 = 1
)

var executeRuntimeMethod = orm.ExecuteRuntimeMethod

type workflowBIDTarget struct {
	Model   string
	ModelId string
}

// ReconcileWorkflowUIShell updates the persisted Workflow act_window and menu
// rows so existing databases honor the current XML declaration without
// requiring operators to force-reset all module data.
func ReconcileWorkflowUIShell(ctx context.Context) error {
	// This runs during server bootstrap with no bound RPC/session Environment,
	// so ExecuteRuntimeMethod would otherwise fall back to a company-less,
	// non-sudo default that the admin-shared-model root-company gate (see
	// internal/models/sys_admin_company_access.go) rejects on every write to
	// ui.menu/ui.actions.window. Bind a sudo Environment scoped to the root
	// company so these seed writes pass that gate like any other root-company
	// operator write would.
	ctx = orm.ContextWithEnvironment(ctx, orm.NewEnvironment(orm.GetBunDB(), 0, map[string]any{
		orm.ContextKeyCurrentCompanyID:  rootCompanyID,
		orm.ContextKeyAllowedCompanyIDs: []int64{rootCompanyID},
	}, true))

	if err := reconcileWorkflowWindowAction(ctx); err != nil {
		return err
	}
	if err := reconcileWorkflowMenu(ctx); err != nil {
		return err
	}
	if err := reconcileWorkflowCanvasMenu(ctx); err != nil {
		return err
	}
	return nil
}

func reconcileWorkflowWindowAction(ctx context.Context) error {
	target, err := lookupWorkflowBIDTarget(ctx, workflowWindowActionBID, uiActionWindowModelName)
	if err != nil {
		return err
	}
	if target.ModelId == "" {
		return nil
	}
	listViewTarget, err := lookupWorkflowBIDTarget(ctx, workflowWindowListViewBID, "ui.view")
	if err != nil {
		return err
	}

	row, err := readWorkflowRecord(ctx, uiActionWindowModelName, target.ModelId, []string{"name", "sys_model", "view_mode", "view_id"})
	if err != nil {
		return err
	}
	values := orm.RecordMap{}
	if workflowStringValue(row["name"]) != workflowWindowActionName {
		values["name"] = workflowWindowActionName
	}
	if workflowStringValue(row["sys_model"]) != workflowmodels.FlowModelName {
		values["sys_model"] = workflowmodels.FlowModelName
	}
	if workflowStringValue(row["view_mode"]) != workflowWindowViewMode {
		values["view_mode"] = workflowWindowViewMode
	}
	if listViewTarget.ModelId != "" && workflowRelationIDString(row["view_id"]) != listViewTarget.ModelId {
		values["view_id"] = listViewTarget.ModelId
	}
	if len(values) == 0 {
		return nil
	}

	log.Infof(
		"workflow UI shell reconcile: updating %s[%s] fields=%v",
		uiActionWindowModelName,
		target.ModelId,
		recordKeys(values),
	)
	return writeWorkflowRecord(ctx, uiActionWindowModelName, target.ModelId, values)
}

func reconcileWorkflowMenu(ctx context.Context) error {
	target, err := lookupWorkflowBIDTarget(ctx, workflowMenuBID, uiMenuModelName)
	if err != nil {
		return err
	}
	if target.ModelId == "" {
		return nil
	}

	row, err := readWorkflowRecord(ctx, uiMenuModelName, target.ModelId, []string{"name", "route_path", "action"})
	if err != nil {
		return err
	}
	values := orm.RecordMap{}
	if workflowStringValue(row["name"]) != workflowMenuName {
		values["name"] = workflowMenuName
	}
	if workflowStringValue(row["route_path"]) != workflowMenuRoutePath {
		values["route_path"] = workflowMenuRoutePath
	}
	if !workflowMenuActionMatches(row["action"], workflowWindowActionBID, workflowWindowActionName, workflowMenuRoutePath) {
		values["action"] = orm.RecordMap{
			"type":      uiActionWindowModelName,
			"bid":       workflowWindowActionBID,
			"name":      workflowWindowActionName,
			"sys_model": workflowmodels.FlowModelName,
			"url":       workflowMenuRoutePath,
		}
	}
	if len(values) == 0 {
		return nil
	}

	log.Infof(
		"workflow UI shell reconcile: updating %s[%s] fields=%v",
		uiMenuModelName,
		target.ModelId,
		recordKeys(values),
	)
	return writeWorkflowRecord(ctx, uiMenuModelName, target.ModelId, values)
}

func reconcileWorkflowCanvasMenu(ctx context.Context) error {
	target, err := lookupWorkflowBIDTarget(ctx, workflowCanvasMenuBID, uiMenuModelName)
	if err != nil {
		return err
	}
	if target.ModelId == "" {
		return nil
	}

	row, err := readWorkflowRecord(ctx, uiMenuModelName, target.ModelId, []string{"name", "route_path", "action"})
	if err != nil {
		return err
	}
	values := orm.RecordMap{}
	if workflowStringValue(row["name"]) != workflowCanvasMenuName {
		values["name"] = workflowCanvasMenuName
	}
	if workflowStringValue(row["route_path"]) != workflowCanvasMenuRoutePath {
		values["route_path"] = workflowCanvasMenuRoutePath
	}
	if !workflowMenuActionMatches(row["action"], workflowCanvasWindowActionBID, workflowCanvasActionName, workflowCanvasMenuRoutePath) {
		values["action"] = orm.RecordMap{
			"type":      uiActionWindowModelName,
			"bid":       workflowCanvasWindowActionBID,
			"name":      workflowCanvasActionName,
			"sys_model": workflowmodels.FlowModelName,
			"url":       workflowCanvasMenuRoutePath,
		}
	}
	if len(values) == 0 {
		return nil
	}

	log.Infof(
		"workflow UI shell reconcile: updating %s[%s] fields=%v",
		uiMenuModelName,
		target.ModelId,
		recordKeys(values),
	)
	return writeWorkflowRecord(ctx, uiMenuModelName, target.ModelId, values)
}

func lookupWorkflowBIDTarget(ctx context.Context, bidValue string, expectedModel string) (workflowBIDTarget, error) {
	result, err := executeRuntimeMethod(
		ctx,
		sysModelDataModelName,
		"search_read",
		nil,
		[]any{[]any{[]any{"complete_name", "=", bidValue}}},
		map[string]any{
			"fields": []string{"model", "model_id"},
			"limit":  1,
		},
	)
	if err != nil {
		return workflowBIDTarget{}, fmt.Errorf("workflow UI shell reconcile lookup %s: %w", bidValue, err)
	}
	rows := workflowRecordRows(result)
	if len(rows) == 0 {
		return workflowBIDTarget{}, nil
	}

	modelName := workflowStringValue(rows[0]["model"])
	if expectedModel != "" && modelName != expectedModel {
		return workflowBIDTarget{}, fmt.Errorf(
			"workflow UI shell reconcile bid %s points to model %q, expected %q",
			bidValue,
			modelName,
			expectedModel,
		)
	}
	return workflowBIDTarget{
		Model:   modelName,
		ModelId: workflowIDString(rows[0]["model_id"]),
	}, nil
}

func readWorkflowRecord(ctx context.Context, modelName string, id string, fields []string) (orm.RecordMap, error) {
	result, err := executeRuntimeMethod(ctx, modelName, "read", []string{id}, []any{fields}, nil)
	if err != nil {
		return nil, fmt.Errorf("workflow UI shell reconcile read %s[%s]: %w", modelName, id, err)
	}
	rows := workflowRecordRows(result)
	if len(rows) == 0 {
		return nil, fmt.Errorf("workflow UI shell reconcile read %s[%s]: record not found", modelName, id)
	}
	return rows[0], nil
}

func writeWorkflowRecord(ctx context.Context, modelName string, id string, values orm.RecordMap) error {
	_, err := executeRuntimeMethod(
		ctx,
		modelName,
		"write",
		[]string{id},
		nil,
		map[string]any{"values": values},
	)
	if err != nil {
		return fmt.Errorf("workflow UI shell reconcile write %s[%s]: %w", modelName, id, err)
	}
	return nil
}

func workflowMenuActionMatches(value any, bidValue string, actionName string, routePath string) bool {
	action, ok := value.(map[string]any)
	if !ok {
		recordMap, ok := value.(orm.RecordMap)
		if !ok {
			return false
		}
		action = map[string]any(recordMap)
	}
	return workflowStringValue(action["type"]) == uiActionWindowModelName &&
		workflowStringValue(action["bid"]) == bidValue &&
		workflowStringValue(action["name"]) == actionName &&
		workflowStringValue(action["sys_model"]) == workflowmodels.FlowModelName &&
		workflowStringValue(action["url"]) == routePath
}

func workflowRecordRows(value any) []orm.RecordMap {
	switch typed := value.(type) {
	case []orm.RecordMap:
		return typed
	case []map[string]any:
		out := make([]orm.RecordMap, 0, len(typed))
		for _, row := range typed {
			out = append(out, orm.RecordMap(row))
		}
		return out
	case []any:
		out := make([]orm.RecordMap, 0, len(typed))
		for _, row := range typed {
			switch cast := row.(type) {
			case orm.RecordMap:
				out = append(out, cast)
			case map[string]any:
				out = append(out, orm.RecordMap(cast))
			}
		}
		return out
	default:
		return nil
	}
}

func workflowStringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []byte:
		return strings.TrimSpace(string(typed))
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func workflowIDString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func workflowRelationIDString(value any) string {
	switch typed := value.(type) {
	case []any:
		if len(typed) == 0 {
			return ""
		}
		return workflowIDString(typed[0])
	case [2]any:
		return workflowIDString(typed[0])
	default:
		return workflowIDString(value)
	}
}

func recordKeys(values orm.RecordMap) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}
