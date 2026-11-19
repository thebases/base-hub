package mcp

// File overview:
// - Responsibility: dispatches one automation.* MCP tool call (CRUD, plus
//   the pinned automation.action.run wrapper).
// - Key symbols: Dispatch.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"git.thebase.vn/base_hub/internal/mcpkit"
	"git.thebase.vn/base_hub/internal/orm"
	automationmodels "git.thebase.vn/base_hub/modules/base-automation/models"
)

// automationRecordTool pins an automation.record.* tool to its mcpkit method and mode.
func automationRecordTool(method string, mode mcpkit.RecordMode) func(context.Context, *orm.RBACIdentity, map[string]any) (any, error) {
	return func(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any) (any, error) {
		return automationRecordDispatch(ctx, identity, arguments, method, mode)
	}
}

// toolHandlers is the single dispatch table for automation.* tools.
var toolHandlers = map[string]func(context.Context, *orm.RBACIdentity, map[string]any) (any, error){
	"automation.record.search_read": automationRecordTool("search_read", mcpkit.RecordModeSearch),
	"automation.record.read":        automationRecordTool("read", mcpkit.RecordModeRead),
	"automation.record.create":      automationRecordTool("create", mcpkit.RecordModeCreate),
	"automation.record.write":       automationRecordTool("write", mcpkit.RecordModeWrite),
	"automation.record.unlink":      automationRecordTool("unlink", mcpkit.RecordModeUnlink),
	"automation.action.run":         mcpAutomationActionRun,
}

// Dispatch routes one automation.* MCP tool call. Registered as this
// provider's Dispatch func (see provider.go) -- base-mcp's provider cache
// already confirmed the base-automation module is installed for the
// caller's tenant DB before ever reaching here.
func Dispatch(ctx context.Context, identity *orm.RBACIdentity, toolName string, arguments map[string]any) (any, error) {
	handler, ok := toolHandlers[toolName]
	if !ok {
		return nil, mcpkit.NewToolError(404, "tool_not_found", fmt.Sprintf("unknown mcp tool %q", toolName))
	}
	return handler(ctx, identity, arguments)
}

// automationRecordDispatch backs automation.record.*: validates
// arguments.model is one of the automation.* models, then delegates to the
// exact same mcpkit.RecordDispatch base.record.* already uses.
func automationRecordDispatch(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any, methodName string, mode mcpkit.RecordMode) (any, error) {
	if err := requireAutomationAllowedModel(arguments); err != nil {
		return nil, err
	}
	return mcpkit.RecordDispatch(ctx, identity, arguments, methodName, mode)
}

func requireAutomationAllowedModel(arguments map[string]any) error {
	modelName := strings.TrimSpace(mcpkit.StringFromAny(arguments["model"]))
	if modelName == "" {
		return mcpkit.NewToolError(422, "invalid_params", "arguments.model is required")
	}
	if !automationAllowedModels[modelName] {
		names := make([]string, 0, len(automationAllowedModels))
		for name := range automationAllowedModels {
			names = append(names, name)
		}
		sort.Strings(names)
		return mcpkit.NewToolError(422, "invalid_params", fmt.Sprintf(
			"model %q is not one of the automation.* models this tool group accepts (%s)", modelName, strings.Join(names, ", "),
		))
	}
	return nil
}

// mcpAutomationActionRun backs automation.action.run: a thin, pinned-method
// wrapper over automation.action's "run" runtime method.
func mcpAutomationActionRun(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any) (any, error) {
	actionID, ok := mcpkit.Int64FromArgument(arguments["action_id"])
	if !ok || actionID <= 0 {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.action_id is required")
	}
	meta, method, err := mcpkit.LookupRuntimeMethod(automationmodels.AutomationActionModelName, "run")
	if err != nil {
		return nil, err
	}
	kwargs := map[string]any{}
	if payload, ok := arguments["payload"].(map[string]any); ok {
		kwargs["payload"] = payload
	}
	return mcpkit.ExecuteRuntimeMethod(ctx, identity, meta, method, []string{fmt.Sprint(actionID)}, nil, kwargs)
}
