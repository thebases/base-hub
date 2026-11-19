package models

// Authoring guard for automation.action (plan T24).
//
// An action's executable content (Lua code, the sys_model target/operation/domain/values, the
// go_handler key and its default payload) runs under sudo whenever an automation.cron row fires,
// and sys_model operations write through the runtime repository without ACL or record rules. The
// module ACL lets base.group_system author actions, so without this guard a group_system user could
// reach anything a superadmin can. Only a superadmin (sudo env) may set or change those fields;
// group_system keeps renaming, describing, archiving, scheduling and running existing actions.
// Seeds, boot and other internal callers without an RPC identity are not restricted.

import (
	"context"
	"encoding/json"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
)

// automationActionExecutableFields decide what an action does when it runs.
var automationActionExecutableFields = []string{"action_type", "lua_code", "model", "operation", "domain", "values", "handler_key"}

// automationRestrictedCaller reports whether ctx is a non-sudo RPC caller.
func automationRestrictedCaller(ctx context.Context) bool {
	env := orm.EnvironmentFromContext(ctx)
	if env == nil || env.IsSudo() {
		return false
	}
	_, rpc := env.ContextValue(orm.RBACContextAuthModeKey)
	return rpc
}

// ensureActionCreateAllowed refuses a restricted caller creating an action with executable content.
func ensureActionCreateAllowed(ctx context.Context, values orm.RecordMap) error {
	if !automationRestrictedCaller(ctx) {
		return nil
	}
	for _, field := range automationActionExecutableFields {
		if raw, ok := values[field]; ok && !isEmptyActionValue(raw) {
			return orm.Forbiddenf("permission denied: only a superadmin can set %s on %s", field, AutomationActionModelName)
		}
	}
	return nil
}

// ensureActionWriteAllowed refuses a restricted caller changing executable content on any of ids.
// Fields sent unchanged (a form posting the whole record) pass, so renames keep working.
func ensureActionWriteAllowed(ctx context.Context, model *orm.ModelMetadata, ids []string, values orm.RecordMap) error {
	if !automationRestrictedCaller(ctx) {
		return nil
	}
	fields := make([]string, 0, len(automationActionExecutableFields))
	for _, field := range automationActionExecutableFields {
		if _, ok := values[field]; ok {
			fields = append(fields, field)
		}
	}
	if len(fields) == 0 {
		return nil
	}
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(AutomationActionModelName, model), ids, fields)
	if err != nil {
		return err
	}
	if len(rows) != len(ids) {
		return orm.NotFoundf("%s: some records do not exist", AutomationActionModelName)
	}
	for _, row := range rows {
		for _, field := range fields {
			if !sameActionValue(row[field], values[field]) {
				return orm.Forbiddenf("permission denied: only a superadmin can change %s on %s", field, AutomationActionModelName)
			}
		}
	}
	return nil
}

// guardedActionFindOrCreate wraps the kernel find_or_create, which writes values straight to the
// repository and so would skip the create/write overrides. Restricted callers cannot use it.
func guardedActionFindOrCreate() orm.RuntimeMethod {
	base, ok := orm.LookupRuntimeMethod("find_or_create")
	if !ok || base.Call == nil {
		panic("base-automation models: default runtime method find_or_create is not registered")
	}
	call := base.Call
	base.Call = func(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
		if automationRestrictedCaller(ctx) {
			return nil, orm.Forbiddenf("permission denied: use create or write on %s", AutomationActionModelName)
		}
		return call(ctx, model, ids, args, kwargs)
	}
	return base
}

// isEmptyActionValue treats nil, blank strings and empty JSON objects as "not set" (nullzero columns).
func isEmptyActionValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	case map[string]any:
		return len(typed) == 0
	case orm.RecordMap:
		return len(typed) == 0
	}
	return false
}

// sameActionValue compares a stored value with an incoming one by their JSON form, so a jsonb
// object read back from the DB equals the same object sent by the client.
func sameActionValue(stored, incoming any) bool {
	if isEmptyActionValue(stored) && isEmptyActionValue(incoming) {
		return true
	}
	if storedText, ok := stored.(string); ok {
		if incomingText, ok := incoming.(string); ok {
			return strings.TrimSpace(storedText) == strings.TrimSpace(incomingText)
		}
	}
	left, errLeft := json.Marshal(stored)
	right, errRight := json.Marshal(incoming)
	return errLeft == nil && errRight == nil && string(left) == string(right)
}
