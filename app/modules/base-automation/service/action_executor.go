package service

// File overview:
// - Responsibility: dispatches one automation.action by its action_type — standalone implementation
//   using orm.RuntimeRepository* directly (no dependency on the workflow module's crud_model@v1,
//   per the module's `depends: [base]`-only constraint).
// - Key symbols: `ExecuteAction`.

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	automationmodels "git.thebase.vn/base_hub/modules/base-automation/models"
)

// ExecuteAction loads one automation.action row and runs it against payload,
// returning the action's result (empty map for sys_model operations that do
// not naturally produce one, e.g. unlink).
func ExecuteAction(ctx context.Context, actionID int64, payload map[string]any) (map[string]any, error) {
	// RuntimeModelRef falls back to a name-only handle when the registry
	// lookup misses, which keeps the runtime-repository test stub seam
	// (orm.StubRuntimeRepository) usable in unit tests the same way the
	// workflow module's model tests already rely on.
	actionModel := orm.RuntimeModelRef(automationmodels.AutomationActionModelName, nil)

	id := strconv.FormatInt(actionID, 10)
	rows, err := orm.RuntimeRepositoryRead(ctx, actionModel, []string{id}, nil)
	if err != nil {
		return nil, fmt.Errorf("automation.action %d: read: %w", actionID, err)
	}
	if len(rows) == 0 {
		return nil, orm.NotFoundf("automation.action %d not found", actionID)
	}
	action := rows[0]
	if err := ensureActionActive(action, actionID); err != nil {
		return nil, err
	}

	switch stringField(action, "action_type") {
	case "lua":
		// Stored `values` are the default payload, overridden per key by the caller's payload,
		// the same convention go_handler actions use.
		return RunLuaAction(ctx, stringField(action, "lua_code"), mergeActionPayload(objectField(action, "values"), payload))
	case "sys_model":
		return executeSysModelAction(ctx, action)
	case "go_handler":
		return executeGoHandlerAction(ctx, action, payload)
	default:
		return nil, fmt.Errorf("automation.action %d has unsupported action_type %q", actionID, stringField(action, "action_type"))
	}
}

// executeGoHandlerAction dispatches to a Go function registered via
// RegisterGoHandler, so a module's compiled cron/automation logic (e.g.
// workflow's resume/reap jobs, credential's token refresh) runs through the
// same claim-then-execute bridge every other automation.cron row uses,
// instead of that module registering its own kernel sys.cron handler.
func executeGoHandlerAction(ctx context.Context, action orm.RecordMap, payload map[string]any) (map[string]any, error) {
	key := stringField(action, "handler_key")
	handler, ok := lookupGoHandler(key)
	if !ok {
		return nil, fmt.Errorf("automation.action go_handler %q is not registered", key)
	}
	// The action's stored Values field is the configured default payload
	// (e.g. {"limit": 50}); a caller-supplied payload (manual "Run" with
	// kwargs) overrides individual keys rather than replacing it wholesale.
	merged := mergeActionPayload(objectField(action, "values"), payload)
	if err := handler(ctx, merged); err != nil {
		return nil, err
	}
	return map[string]any{}, nil
}

// mergeActionPayload shallow-merges override onto base without mutating
// either input map.
func mergeActionPayload(base, override map[string]any) map[string]any {
	merged := make(map[string]any, len(base)+len(override))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range override {
		merged[k] = v
	}
	return merged
}

// executeSysModelAction runs one declarative CRUD operation against the
// action's target model using the shared runtime repository primitives —
// the same primitives modules/workflow/service/runtime.go's crud_model@v1
// uses, reimplemented here rather than imported so base-automation keeps its
// single `depends: [base]` edge.
func executeSysModelAction(ctx context.Context, action orm.RecordMap) (map[string]any, error) {
	// RuntimeModelRef falls back to a name-only handle when the target isn't
	// in the registry; the runtime repository calls below still fail loudly
	// for an unregistered/unstubbed model via requireRuntimeExecutor, so this
	// stays just as safe in production while keeping the test stub seam usable.
	targetModel := orm.RuntimeModelRef(stringField(action, "model"), nil)

	values := objectField(action, "values")
	domain := objectField(action, "domain")

	switch stringField(action, "operation") {
	case "create":
		created, err := orm.RuntimeRepositoryCreate(ctx, targetModel, orm.RecordMap(values))
		if err != nil {
			return nil, err
		}
		return map[string]any(created), nil
	case "write":
		ids, err := domainRecordIDs(ctx, targetModel, domain)
		if err != nil {
			return nil, err
		}
		if _, err := orm.RuntimeRepositoryWrite(ctx, targetModel, ids, orm.RecordMap(values)); err != nil {
			return nil, err
		}
		return map[string]any{"ids": ids}, nil
	case "unlink":
		ids, err := domainRecordIDs(ctx, targetModel, domain)
		if err != nil {
			return nil, err
		}
		if _, err := orm.RuntimeRepositoryUnlink(ctx, targetModel, ids); err != nil {
			return nil, err
		}
		return map[string]any{"ids": ids}, nil
	case "find_or_create":
		return findOrCreate(ctx, targetModel, domain, values)
	default:
		return nil, fmt.Errorf("automation.action sys_model operation %q is not supported", stringField(action, "operation"))
	}
}

// domainRecordIDs resolves the record IDs matched by domain, failing loudly
// on an empty domain so write/unlink can never accidentally target every row.
func domainRecordIDs(ctx context.Context, model *orm.ModelMetadata, domain map[string]any) ([]string, error) {
	if len(domain) == 0 {
		return nil, fmt.Errorf("automation.action domain is required for write/unlink")
	}
	rows, err := orm.RuntimeRepositorySearchRead(ctx, model, orm.RecordMap(domain), []string{"id"})
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, stringField(row, "id"))
	}
	return ids, nil
}

// findOrCreate writes Values onto the first record matched by domain, or
// creates a new record from Values when nothing matches.
func findOrCreate(ctx context.Context, model *orm.ModelMetadata, domain, values map[string]any) (map[string]any, error) {
	if len(domain) > 0 {
		rows, err := orm.RuntimeRepositorySearchRead(ctx, model, orm.RecordMap(domain), nil)
		if err != nil {
			return nil, err
		}
		if len(rows) > 0 {
			existing := rows[0]
			if len(values) > 0 {
				id := stringField(existing, "id")
				if _, err := orm.RuntimeRepositoryWrite(ctx, model, []string{id}, orm.RecordMap(values)); err != nil {
					return nil, err
				}
			}
			return map[string]any(existing), nil
		}
	}
	created, err := orm.RuntimeRepositoryCreate(ctx, model, orm.RecordMap(values))
	if err != nil {
		return nil, err
	}
	return map[string]any(created), nil
}

// ensureActionActive refuses an archived (active=false) action on every
// execution path (manual run, cron bridge, workflow lua node). Forbidden, not
// InvalidParams: the request is well-formed, the record's state forbids running
// it. A row without an active value (legacy/stub) is treated as active.
func ensureActionActive(action orm.RecordMap, actionID int64) error {
	if active, ok := action["active"].(bool); ok && !active {
		return orm.Forbiddenf("automation.action %d is inactive", actionID)
	}
	return nil
}

func stringField(record orm.RecordMap, key string) string {
	value, _ := record[key].(string)
	return value
}

func objectField(record orm.RecordMap, key string) map[string]any {
	value, _ := record[key].(map[string]any)
	if value == nil {
		return map[string]any{}
	}
	return value
}

// RunLuaActionByID runs one stored `lua` automation.action as a pipeline step: the caller's
// globals (e.g. raw/topic/step_key) sit next to `payload`, the action's stored `values` are the
// default payload keys under the incoming payload, and a script that returns no table keeps the
// (mutated) payload. This is the entry point base-workflow's lua_script node uses to run a
// custom action selected by id; non-lua actions are rejected so a node cannot fire sys_model or
// go_handler actions through it.
func RunLuaActionByID(ctx context.Context, actionID int64, payload map[string]any, globals map[string]any) (map[string]any, error) {
	actionModel := orm.RuntimeModelRef(automationmodels.AutomationActionModelName, nil)
	rows, err := orm.RuntimeRepositoryRead(ctx, actionModel, []string{strconv.FormatInt(actionID, 10)}, nil)
	if err != nil {
		return nil, fmt.Errorf("automation.action %d: read: %w", actionID, err)
	}
	if len(rows) == 0 {
		return nil, orm.NotFoundf("automation.action %d not found", actionID)
	}
	action := rows[0]
	if kind := stringField(action, "action_type"); kind != "lua" {
		return nil, fmt.Errorf("automation.action %d is %q, not a lua action", actionID, kind)
	}
	if err := ensureActionActive(action, actionID); err != nil {
		return nil, err
	}
	return RunLuaScript(ctx, LuaScript{
		Code:        stringField(action, "lua_code"),
		Payload:     mergeActionPayload(objectField(action, "values"), payload),
		Globals:     globals,
		Host:        luaActionHost,
		KeepPayload: true,
	})
}

// ResolveActionID maps an automation.action seed external_id (e.g. "edc.automation.action.mqtt_accept")
// to its row id through sys_model_data. Seed rows cannot reference an action by id from a JSON
// column such as a workflow step's extra_params, so steps carry the stable external_id instead.
func ResolveActionID(ctx context.Context, ref string) (int64, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return 0, fmt.Errorf("automation action reference is empty")
	}
	db := orm.DBFromContext(ctx)
	if db == nil {
		return 0, fmt.Errorf("automation action %q: no database bound to context", ref)
	}
	var modelID string
	err := db.NewRaw(
		`SELECT model_id FROM sys_model_data WHERE complete_name = ? AND model = ? ORDER BY id LIMIT 1`,
		ref, automationmodels.AutomationActionModelName,
	).Scan(ctx, &modelID)
	if err != nil || strings.TrimSpace(modelID) == "" {
		return 0, orm.NotFoundf("automation.action %q not found", ref)
	}
	id, err := strconv.ParseInt(strings.TrimSpace(modelID), 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("automation action %q has invalid id %q", ref, modelID)
	}
	return id, nil
}

// RunLuaActionByRef runs the lua action identified by its seed external_id.
func RunLuaActionByRef(ctx context.Context, ref string, payload map[string]any, globals map[string]any) (map[string]any, error) {
	id, err := ResolveActionID(ctx, ref)
	if err != nil {
		return nil, err
	}
	return RunLuaActionByID(ctx, id, payload, globals)
}
