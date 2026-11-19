package models

// File overview:
// - Responsibility: defines the `automation.action` model — a user-defined action that either runs a
//   sandboxed Lua script or a declarative CRUD operation against a runtime model (`sys_model`).
// - Key symbols: `AutomationActionRecord`, `AutomationActionModel`, `prepareAutomationAction`, `runAutomationAction`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay
//   centralized instead of being scattered across handlers.

import (
	"context"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
)

// AutomationActionRecord stores one user-defined automation action. Balance
// mutations and other business writes only ever happen through the ORM
// runtime repository from ExecuteAction, so every action run lands in the
// same audit path (created_at/updated_at, standard write logging) as any
// other model write — no separate audit trail is needed here.
type AutomationActionRecord struct {
	orm.BaseModel
	Name        string         `bun:"name" json:"name" meta:"index,label:Name,help:Stores the human readable name for this automation action."`
	ActionType  string         `bun:"action_type" json:"action_type" meta:"index,label:Action Type,help:Selects whether this action runs a Lua script, a declarative model operation, or a registered Go handler." selection:"(lua,Lua Script),(sys_model,Sys Model),(go_handler,Go Handler)"`
	LuaCode     string         `bun:"lua_code,nullzero" json:"lua_code" meta:"kind:text,label:Lua Code,help:Stores the sandboxed Lua script executed when action_type is lua."`
	Model       string         `bun:"model,nullzero" json:"model" meta:"label:Model,help:Stores the target runtime model name executed when action_type is sys_model."`
	Operation   string         `bun:"operation,nullzero" json:"operation" meta:"label:Operation,help:Selects the CRUD operation executed against Model when action_type is sys_model." selection:"(create,Create),(write,Write),(unlink,Unlink),(find_or_create,Find Or Create)"`
	Domain      map[string]any `bun:"domain,type:jsonb,nullzero" json:"domain" meta:"kind:json,label:Domain,help:Stores the search filter used to locate target records for write, unlink, and find_or_create."`
	Values      map[string]any `bun:"values,type:jsonb,nullzero" json:"values" meta:"kind:json,label:Values,help:Stores the field values applied on create or write when action_type is sys_model, or the default payload passed to the script (lua) or handler (go_handler)."`
	HandlerKey  string         `bun:"handler_key,nullzero" json:"handler_key" meta:"label:Handler Key,help:Stores the key a Go module registered via RegisterGoHandler (e.g. workflow.resume_due_runs) executed when action_type is go_handler."`
	Description string         `bun:"description,nullzero" json:"description" meta:"kind:text,label:Description,help:Describes the business purpose of this automation action."`
	CompanyID   *int64         `bun:"company_id" json:"company_id" meta:"kind:many2one,model:tbl.company,ondelete:SET NULL,index,label:Company ID,help:Links this record to the related tbl.company record for company id."`
}

const AutomationActionModelName = "automation.action"

var AutomationActionModel = orm.Define[AutomationActionRecord](&Models, 10, orm.ModelSpec{
	Name:        AutomationActionModelName,
	Label:       "Automation Actions",
	Description: "User-defined automation actions executed by internal crons or run on demand",
	Prepare:     prepareAutomationAction,
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "create", Public: true, ModelOnly: true, Call: createAutomationAction},
		{Name: "run", Public: true, UsesPathID: true, Call: runAutomationAction},
		guardedActionFindOrCreate(),
	},
})

// createAutomationAction keeps the list payload the default create does not accept; each row
// goes through the guarded create (Prepare, company pin and check).
func createAutomationAction(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	return createRecordsFromArgs(ctx, orm.RuntimeModelRef(AutomationActionModelName, model), args, kwargs)
}

// prepareAutomationAction runs inside the guarded create/write: field validation per action_type,
// then the superadmin-only guard on executable content (automation_action_access.go).
func prepareAutomationAction(ctx context.Context, op orm.WriteOp) error {
	normalized, err := normalizeAutomationActionValues(op.Values, op.Create)
	if err != nil {
		return err
	}
	replaceRecordMap(op.Values, normalized)
	if op.Create {
		return ensureActionCreateAllowed(ctx, op.Values)
	}
	return ensureActionWriteAllowed(ctx, op.Model, op.IDs, op.Values)
}

// runAutomationAction is the manual test/trigger surface for a user-defined
// action: it executes the same code path a due automation.cron row would
// take, so "Run Now" in the UI is a faithful preview of scheduled behavior.
func runAutomationAction(ctx context.Context, _ *orm.ModelMetadata, ids []string, _ []any, kwargs map[string]any) (any, error) {
	if len(ids) != 1 {
		return nil, orm.InvalidParamsf("automation.action.run requires exactly one id")
	}
	actionID, ok := int64FromAny(ids[0])
	if !ok {
		return nil, orm.InvalidParamsf("automation.action.run requires a numeric id")
	}
	// The executor loads the action without record rules; the method ACL only
	// checked the model, so the caller must be allowed to write this row.
	if err := parentRecordAccess(ctx, AutomationActionModelName, "write", ids[:1]); err != nil {
		return nil, err
	}
	payload, _ := mapValue(kwargs, "payload").(map[string]any)
	// The hook is cleared on runtime shutdown (service.InitRuntime's teardown);
	// a late RPC must get an error, not a nil-func panic.
	executor := actionExecutorHook
	if executor == nil {
		return nil, fmt.Errorf("automation.action.run: action executor is not initialized")
	}
	return executor(ctx, actionID, payload)
}

// parentRecordAccess checks ACL and record rules on a row these methods load through the
// repository (which skips record rules); a variable so tests can observe the check (T35).
var parentRecordAccess = orm.EnsureRecordAccess

// actionExecutorHook is bound by the service package during InitRuntime so
// this model package never imports service (which imports models), avoiding
// an import cycle while still letting `run` dispatch real execution.
var actionExecutorHook func(ctx context.Context, actionID int64, payload map[string]any) (map[string]any, error)

// RegisterActionExecutor lets the service package supply the real executor.
func RegisterActionExecutor(fn func(ctx context.Context, actionID int64, payload map[string]any) (map[string]any, error)) {
	actionExecutorHook = fn
}

// normalizeAutomationActionValues validates the fields required by each
// action_type so a half-configured action can never be saved and later fail
// silently at execution time.
func normalizeAutomationActionValues(values orm.RecordMap, creating bool) (orm.RecordMap, error) {
	working := cloneRecordMap(values)

	if name, ok := stringFromAny(working["name"]); ok {
		working["name"] = strings.TrimSpace(name)
	} else if creating {
		return nil, orm.InvalidParamsf("automation.action name is required")
	}

	actionType, hasType := stringFromAny(working["action_type"])
	if hasType {
		working["action_type"] = strings.TrimSpace(actionType)
	} else if creating {
		return nil, orm.InvalidParamsf("automation.action action_type is required")
	}

	switch working["action_type"] {
	case "lua":
		if code, ok := stringFromAny(working["lua_code"]); !ok || strings.TrimSpace(code) == "" {
			if creating {
				return nil, orm.InvalidParamsf("automation.action lua_code is required when action_type is lua")
			}
		}
	case "sys_model":
		if creating {
			if modelName, ok := stringFromAny(working["model"]); !ok || strings.TrimSpace(modelName) == "" {
				return nil, orm.InvalidParamsf("automation.action model is required when action_type is sys_model")
			}
			operation, ok := stringFromAny(working["operation"])
			if !ok || strings.TrimSpace(operation) == "" {
				return nil, orm.InvalidParamsf("automation.action operation is required when action_type is sys_model")
			}
			switch operation {
			case "create", "write", "unlink", "find_or_create":
			default:
				return nil, orm.InvalidParamsf("automation.action operation %q is not supported", operation)
			}
		}
	case "go_handler":
		if creating {
			if key, ok := stringFromAny(working["handler_key"]); !ok || strings.TrimSpace(key) == "" {
				return nil, orm.InvalidParamsf("automation.action handler_key is required when action_type is go_handler")
			}
		}
	default:
		if hasType {
			return nil, orm.InvalidParamsf("automation.action action_type %q is not supported", working["action_type"])
		}
	}

	return working, nil
}
