package models

// File overview:
// - Responsibility: defines the `sys.model.action` kernel model — system-user-defined actions bound to a
//   model and shown in the Actions dropdown of the view modes listed in `view_type` — plus
//   `get_bound_actions`, the read method the SPA calls (per model + view mode) to build that dropdown.
// - Key symbols: `SysModelActionRecord`, `getBoundSysModelActions`, `validateSysModelActionRows`.
// - Pattern: an action only *references* what it runs (a sys.model.operation, ui.actions.window or
//   ui.actions.client row). Execution goes through the normal runtime RPC/navigation path, so the
//   target method's ACL, record rules and company scope still decide whether the user may run it.

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// SysModelActionRecord is one Actions-menu entry for `model_name`. Exactly one of
// operation_id / window_action_id / client_action_id is meaningful, chosen by action_type.
type SysModelActionRecord struct {
	orm.BaseModel
	Name      string `bun:"name,notnull" json:"name" meta:"index,label:Name,help:Stores the label shown in the Actions dropdown."`
	ModelName string `bun:"model_name,notnull" json:"model_name" meta:"index,label:Model,help:Stores the technical name of the model whose list view shows this action."`
	Sequence  int64  `bun:"sequence,default:10" json:"sequence" default:"10" meta:"label:Sequence,help:Orders actions inside the Actions dropdown (lowest first)."`
	Icon      string `bun:"icon" json:"icon" meta:"label:Icon,help:Stores the optional Lucide icon name shown next to the label."`
	// ActionType has no form default on purpose: the user picks it first and the form reveals
	// only the matching target fields (validateSysModelActionRow rejects an empty type).
	ActionType string `bun:"action_type" json:"action_type" selection:"(model,Model Action),(window,Window Action),(client,Client Action)" meta:"index,label:Action Type,help:Selects whether the action runs a model method, opens a window action, or runs a client action."`
	// OperationID references the generated method catalog; SyncSysModelOperations upserts by
	// name so this id survives restarts, and a removed method nulls it (action is then skipped).
	OperationID    *int64 `bun:"operation_id" json:"operation_id" meta:"kind:many2one,model:sys.model.operation,ondelete:SET NULL,index,label:Model Method,help:Links the public model-level method run by a model action."`
	WindowActionID *int64 `bun:"window_action_id" json:"window_action_id" meta:"kind:many2one,model:ui.actions.window,ondelete:SET NULL,index,label:Window Action,help:Links the window action opened by a window action."`
	ClientActionID *int64 `bun:"client_action_id" json:"client_action_id" meta:"kind:many2one,model:ui.actions.client,ondelete:SET NULL,index,label:Client Action,help:Links the client action run by a client action."`
	Confirm        string `bun:"confirm" json:"confirm" meta:"label:Confirmation,help:Stores an optional confirmation prompt shown before the action runs."`
	// ViewType lists the view modes (comma-separated, e.g. "form,list,kanban") whose Actions menu
	// shows this action. Empty means "use the action type's default" (sysModelActionDefaultViewType),
	// so rows created before the column existed keep a deterministic placement.
	ViewType string `bun:"view_type,default:''" json:"view_type" default:"" meta:"label:View Types,help:Comma-separated view modes (form,list,kanban,...) whose Actions menu shows this action; empty uses the action type default."`
}

const SysModelActionModelName = "sys.model.action"

// Action types accepted in action_type; each maps to exactly one reference column.
const (
	SysModelActionTypeModel  = "model"
	SysModelActionTypeWindow = "window"
	SysModelActionTypeClient = "client"
)

var sysModelActionRefColumn = map[string]string{
	SysModelActionTypeModel:  "operation_id",
	SysModelActionTypeWindow: "window_action_id",
	SysModelActionTypeClient: "client_action_id",
}

// Default view_type per action_type: a model action runs a model-level method, which only the
// form's Actions menu exposes by default; window/client actions fit every record-collection view.
const (
	SysModelActionViewTypeModelDefault = "form"
	SysModelActionViewTypeOtherDefault = "form,list,kanban"
)

var sysModelActionReadFields = []string{"id", "name", "model_name", "sequence", "icon", "action_type", "operation_id", "window_action_id", "client_action_id", "confirm", "view_type"}

var SysModelActionModelSpec = orm.ModelSpec{
	Name:        SysModelActionModelName,
	Label:       "Model Actions",
	Description: "System-defined actions bound to a model and shown in its list-view Actions dropdown (model method, window action or client action)",
	Definition:  orm.ModelDefinition(SysModelActionModelName, (*SysModelActionRecord)(nil)),
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "get_bound_actions", Public: true, ModelOnly: true, ReadOnly: true, Call: getBoundSysModelActions},
	},
}

var SysModelActionModel = orm.Define[SysModelActionRecord](&Models, 190, SysModelActionModelSpec)

func init() {
	// Register after the action/operation catalogs it references.
	// Constraints run after the write inside the same transaction, so the handler validates the
	// merged row and an invalid definition rolls back instead of reaching the Actions menu.
	// Picking an action type pre-fills view_type with that type's default; the user may still edit it.
	orm.RegisterOnchange(SysModelActionModelName, []string{"action_type"}, sysModelActionTypeOnchange)
	orm.RegisterConstraint(SysModelActionModelName, []string{"name", "model_name", "action_type", "operation_id", "window_action_id", "client_action_id"}, validateSysModelActionRows)
}

// getBoundSysModelActions returns the active actions bound to kwargs.model, ordered by sequence,
// limited to those whose effective view_type contains kwargs.view_type (no filter when omitted),
// each with its resolved payload (operation model/method + declared params, window or client action dict).
// Definitions and referenced action catalogs are metadata internal users cannot read directly,
// so they are loaded sudo; running the action still goes through the user's own permissions.
// Actions whose reference is missing or broken are skipped (logged), never failing the list view.
func getBoundSysModelActions(ctx context.Context, _ *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	modelName := strings.TrimSpace(stringValue(mapValue(kwargs, "model")))
	if modelName == "" {
		modelName = strings.TrimSpace(stringArg(args, 0))
	}
	if modelName == "" {
		return nil, fmt.Errorf("sys.model.action get_bound_actions: model is required")
	}
	viewType := normalizeSysModelActionViewMode(stringValue(mapValue(kwargs, "view_type")))
	sudoCtx := sysModelActionSudoContext(ctx)
	rows, err := orm.RuntimeRepositorySearchRead(sudoCtx, orm.RuntimeModelRef(SysModelActionModelName, nil),
		orm.RecordMap{"model_name": modelName, "active": true, "__order": "sequence asc, id asc"}, sysModelActionReadFields)
	if err != nil {
		return nil, err
	}
	actions := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		// Filtered in Go, not SQL: an empty stored view_type resolves to the type default.
		if viewType != "" && !sysModelActionShowsInView(row, viewType) {
			continue
		}
		action, err := buildBoundSysModelAction(sudoCtx, row)
		if err != nil {
			log.Warnf("sys.model.action %v (%s) skipped: %v", row["id"], modelName, err)
			continue
		}
		actions = append(actions, action)
	}
	return actions, nil
}

// sysModelActionTypeOnchange suggests the default view_type for the picked action type.
func sysModelActionTypeOnchange(_ context.Context, _ orm.RecordSet, values orm.RecordMap, _ []string, _ map[string]any) (orm.RecordMap, error) {
	actionType := strings.TrimSpace(stringValue(values["action_type"]))
	if _, ok := sysModelActionRefColumn[actionType]; !ok {
		return nil, nil
	}
	return orm.RecordMap{"view_type": sysModelActionDefaultViewType(actionType)}, nil
}

// sysModelActionDefaultViewType is the view_type an action uses while its stored value is empty.
func sysModelActionDefaultViewType(actionType string) string {
	if actionType == SysModelActionTypeModel {
		return SysModelActionViewTypeModelDefault
	}
	return SysModelActionViewTypeOtherDefault
}

// sysModelActionShowsInView reports whether the row's effective view_type list contains viewMode.
func sysModelActionShowsInView(row orm.RecordMap, viewMode string) bool {
	viewTypes := strings.TrimSpace(stringValue(row["view_type"]))
	if viewTypes == "" {
		viewTypes = sysModelActionDefaultViewType(strings.TrimSpace(stringValue(row["action_type"])))
	}
	for _, item := range strings.Split(viewTypes, ",") {
		if normalizeSysModelActionViewMode(item) == viewMode {
			return true
		}
	}
	return false
}

// normalizeSysModelActionViewMode canonicalizes one view mode token (trim + lower case).
func normalizeSysModelActionViewMode(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// buildBoundSysModelAction resolves one definition row into the dropdown payload. The payload
// carries only what the action type needs, so the client dispatches on action_type alone.
func buildBoundSysModelAction(ctx context.Context, row orm.RecordMap) (map[string]any, error) {
	actionType := strings.TrimSpace(stringValue(row["action_type"]))
	refID, ok := sysModelActionRefID(row[sysModelActionRefColumn[actionType]])
	if !ok {
		return nil, fmt.Errorf("action_type %q has no target", actionType)
	}
	action := map[string]any{
		"id":          int64Value(row["id"]),
		"name":        strings.TrimSpace(stringValue(row["name"])),
		"icon":        strings.TrimSpace(stringValue(row["icon"])),
		"confirm":     strings.TrimSpace(stringValue(row["confirm"])),
		"action_type": actionType,
	}
	switch actionType {
	case SysModelActionTypeModel:
		operation, err := loadRunnableSysModelOperation(ctx, refID)
		if err != nil {
			return nil, err
		}
		action["method_model"] = operation["model_name"]
		action["method_name"] = operation["method_name"]
		// The popup's input fields come from the live registry (the code declaration), not the
		// operation snapshot, so they always match what ExecuteRuntimeMethod will enforce.
		action["params"] = sysModelActionMethodParams(stringValue(operation["model_name"]), stringValue(operation["method_name"]))
	case SysModelActionTypeWindow:
		row, err := loadUIActionWindowRow(ctx, refID)
		if err != nil {
			return nil, fmt.Errorf("window action %d: %w", refID, err)
		}
		dict, err := buildUIActionWindowDict(ctx, row)
		if err != nil {
			return nil, err
		}
		action["window_action"] = dict
	case SysModelActionTypeClient:
		row, err := loadUIActionClientRow(ctx, refID)
		if err != nil {
			return nil, fmt.Errorf("client action %d: %w", refID, err)
		}
		action["client_action"] = buildUIActionClientDict(row)
	default:
		return nil, fmt.Errorf("unsupported action_type %q", actionType)
	}
	return action, nil
}

// sysModelActionMethodParams returns the declared kwargs of model.method, or an empty list
// (never nil, so the client always receives a JSON array) when none are declared.
func sysModelActionMethodParams(modelName, methodName string) []orm.RuntimeMethodParam {
	meta, ok := orm.LookupModel(strings.TrimSpace(modelName))
	if !ok {
		return []orm.RuntimeMethodParam{}
	}
	method, ok := meta.LookupRuntimeMethod(strings.TrimSpace(methodName))
	if !ok || len(method.Params) == 0 {
		return []orm.RuntimeMethodParam{}
	}
	return method.Params
}

// validateSysModelActionRows re-reads every written row (constraints run post-write) and checks
// the full definition, because a write may change action_type without resending its target.
// The values written by THIS call are overlaid on the read-back row: the ORM invalidates its
// per-request record cache only after constraints run, so a field read earlier in the same
// request (access checks, record rules, onchange) would otherwise come back stale — e.g. a
// freshly picked operation_id reading as false.
func validateSysModelActionRows(ctx context.Context, records orm.RecordSet, changed orm.RecordMap) error {
	if len(records.IDs()) == 0 {
		return nil
	}
	sudoCtx := sysModelActionSudoContext(ctx)
	rows, err := orm.RuntimeRepositoryRead(sudoCtx, orm.RuntimeModelRef(SysModelActionModelName, nil), records.IDs(), sysModelActionReadFields)
	if err != nil {
		return err
	}
	for _, row := range rows {
		overlaySysModelActionChanges(row, changed)
		if err := validateSysModelActionRow(sudoCtx, row); err != nil {
			log.Errorf("sys.model.action: %v", err)
			return fmt.Errorf("sys.model.action: %w", err)
		}
	}
	return nil
}

// overlaySysModelActionChanges copies the written values of the validated fields onto row, so
// validation sees exactly what was just stored (sysModelActionRefID accepts the raw written
// shapes: id, [id, name], {id}).
func overlaySysModelActionChanges(row, changed orm.RecordMap) {
	for _, field := range sysModelActionReadFields {
		if value, ok := changed[field]; ok && field != "id" {
			row[field] = value
		}
	}
}

// validateSysModelActionRow enforces: a name, a registered binding model, a known action type,
// and a target matching that type (a model action's method must be runnable from the list).
func validateSysModelActionRow(ctx context.Context, row orm.RecordMap) error {
	if strings.TrimSpace(stringValue(row["name"])) == "" {
		return fmt.Errorf("name is required")
	}
	modelName := strings.TrimSpace(stringValue(row["model_name"]))
	if _, ok := orm.LookupModel(modelName); !ok {
		return fmt.Errorf("model %q is not registered", modelName)
	}
	actionType := strings.TrimSpace(stringValue(row["action_type"]))
	column, ok := sysModelActionRefColumn[actionType]
	if !ok {
		return fmt.Errorf("unsupported action_type %q (want model|window|client)", actionType)
	}
	refID, ok := sysModelActionRefID(row[column])
	if !ok {
		return fmt.Errorf("%s is required for a %s action", column, actionType)
	}
	if actionType != SysModelActionTypeModel {
		return nil
	}
	operation, err := loadRunnableSysModelOperation(ctx, refID)
	if err != nil {
		return err
	}
	// The method picker is filtered by the bound model; reject a stale pick left over after the
	// model changed, so an action only ever runs a method of the model whose list shows it.
	if stringValue(operation["model_name"]) != modelName {
		return fmt.Errorf("model method %q does not belong to model %q", stringValue(operation["name"]), modelName)
	}
	return nil
}

// loadRunnableSysModelOperation loads a catalog operation and rejects methods the list's Actions
// menu cannot call: non-public methods are unreachable over RPC, and path-id methods need a
// record selection the list view does not have.
func loadRunnableSysModelOperation(ctx context.Context, operationID int64) (orm.RecordMap, error) {
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(SysModelOperationModelName, nil),
		[]string{strconv.FormatInt(operationID, 10)}, []string{"id", "name", "model_name", "method_name", "is_public", "uses_path_id"})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("model method %d no longer exists", operationID)
	}
	operation := rows[0]
	name := stringValue(operation["name"])
	if !boolValue(operation["is_public"]) {
		return nil, fmt.Errorf("model method %q is not public", name)
	}
	if boolValue(operation["uses_path_id"]) {
		return nil, fmt.Errorf("model method %q requires a record id and cannot run from the Actions menu", name)
	}
	if _, ok := orm.LookupModel(stringValue(operation["model_name"])); !ok {
		return nil, fmt.Errorf("model method %q belongs to an unregistered model", name)
	}
	return operation, nil
}

// sysModelActionRefID reads a many2one value in any shape the runtime repository returns
// (id, *id, [id, display_name], {id: ...}) and reports whether it is a positive id.
func sysModelActionRefID(raw any) (int64, bool) {
	switch typed := raw.(type) {
	case *int64:
		if typed == nil {
			return 0, false
		}
		return *typed, *typed > 0
	case [2]any:
		// Runtime read/search_read return a many2one as a fixed [2]any{id, display_name};
		// without this case get_bound_actions skipped every action as "has no target".
		return sysModelActionRefID(typed[0])
	case []any:
		if len(typed) == 0 {
			return 0, false
		}
		return sysModelActionRefID(typed[0])
	case map[string]any:
		return sysModelActionRefID(typed["id"])
	case orm.RecordMap:
		return sysModelActionRefID(typed["id"])
	}
	id, ok := int64FromAny(raw)
	return id, ok && id > 0
}

// sysModelActionSudoContext elevates the request environment for catalog lookups only
// (same pattern as FindSysReportDefinition).
func sysModelActionSudoContext(ctx context.Context) context.Context {
	if env := orm.EnvironmentFromContext(ctx); env != nil {
		return orm.ContextWithEnvironment(ctx, env.WithUser(env.UserID(), true))
	}
	return ctx
}
