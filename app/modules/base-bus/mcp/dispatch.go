package mcp

// File overview:
// - Responsibility: dispatches one bus.* MCP tool call (read-only record
//   inspection, plus the pinned publish/poll/recent/last_id/
//   get_default_channels wrappers).
// - Key symbols: Dispatch.

import (
	"context"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/mcpkit"
	"git.thebase.vn/base_hub/internal/orm"
	busmodels "git.thebase.vn/base_hub/modules/base-bus/models"
)

// Dispatch routes one bus.* MCP tool call. Registered as this provider's
// Dispatch func (see provider.go) -- base-mcp's provider cache already
// confirmed the base-bus module is installed for the caller's tenant DB
// before ever reaching here.
func Dispatch(ctx context.Context, identity *orm.RBACIdentity, toolName string, arguments map[string]any) (any, error) {
	switch toolName {
	case "bus.record.search_read":
		return busRecordDispatch(ctx, identity, arguments, "search_read", mcpkit.RecordModeSearch)
	case "bus.record.read":
		return busRecordDispatch(ctx, identity, arguments, "read", mcpkit.RecordModeRead)
	case "bus.publish":
		return mcpBusPublish(ctx, identity, arguments)
	case "bus.poll":
		return mcpBusPoll(ctx, identity, arguments)
	case "bus.recent":
		return mcpBusRecent(ctx, identity, arguments)
	case "bus.last_id":
		return mcpBusRunMethod(ctx, identity, "last_id", nil)
	case "bus.get_default_channels":
		return mcpBusRunMethod(ctx, identity, "get_default_channels", nil)
	default:
		return nil, mcpkit.NewToolError(404, "tool_not_found", fmt.Sprintf("unknown mcp tool %q", toolName))
	}
}

// busRecordDispatch backs bus.record.*: validates arguments.model is
// base.service.bus, then delegates to the exact same mcpkit.RecordDispatch
// base.record.* already uses.
func busRecordDispatch(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any, methodName string, mode mcpkit.RecordMode) (any, error) {
	modelName := strings.TrimSpace(mcpkit.StringFromAny(arguments["model"]))
	if modelName == "" {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.model is required")
	}
	if !busAllowedModels[modelName] {
		return nil, mcpkit.NewToolError(422, "invalid_params", fmt.Sprintf(
			"model %q is not one of the bus.* models this tool group accepts (%s)", modelName, busmodels.ServiceBusModelName,
		))
	}
	return mcpkit.RecordDispatch(ctx, identity, arguments, methodName, mode)
}

// mcpBusRunMethod is the shared entry point for every pinned bus.* runtime
// method call: it runs the method on base.service.bus under the caller's
// identity with the given positional args (this model's runtime methods take
// positional args, not kwargs).
//
// It deliberately skips the model ACL mcpkit.ExecuteRuntimeMethod applies:
// that ACL limits the raw event log to base.group_system (plan T16,
// serviceBusLogAccess), while poll/recent filter channels per caller
// themselves (AuthorizeServiceBusChannels). publish is an Internal method, so
// this tool is its only client entry and checks EnsureServiceBusPublishAllowed
// first.
func mcpBusRunMethod(ctx context.Context, identity *orm.RBACIdentity, methodName string, args []any) (any, error) {
	if identity == nil {
		return nil, mcpkit.NewToolError(401, "unauthorized", "bus tools need an authenticated caller")
	}
	requestCtx := mcpkit.RequestContext(ctx, identity)
	if methodName == "publish" {
		if err := busmodels.EnsureServiceBusPublishAllowed(requestCtx); err != nil {
			return nil, mcpkit.NewToolError(403, "permission_denied", err.Error())
		}
	}
	result, err := orm.ExecuteRuntimeMethod(requestCtx, busmodels.ServiceBusModelName, methodName, nil, args, nil)
	if err != nil {
		return nil, mcpkit.NewToolError(400, "invalid_operation", err.Error())
	}
	return result, nil
}

// mcpBusPublish backs bus.publish: a thin, pinned-method wrapper over
// base.service.bus's "publish" runtime method.
func mcpBusPublish(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any) (any, error) {
	channels, ok := arguments["channels"]
	if !ok {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.channels is required")
	}
	eventType := strings.TrimSpace(mcpkit.StringFromAny(arguments["event_type"]))
	if eventType == "" {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.event_type is required")
	}
	payload, ok := arguments["payload"]
	if !ok {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.payload is required")
	}
	return mcpBusRunMethod(ctx, identity, "publish", []any{channels, eventType, payload})
}

// mcpBusPoll backs bus.poll: a thin, pinned-method wrapper over
// base.service.bus's "poll" runtime method.
func mcpBusPoll(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any) (any, error) {
	channels := arguments["channels"]
	if channels == nil {
		channels = []any{}
	}
	lastID, _ := mcpkit.Int64FromArgument(arguments["last_id"])
	return mcpBusRunMethod(ctx, identity, "poll", []any{channels, lastID})
}

// mcpBusRecent backs bus.recent: a thin, pinned-method wrapper over
// base.service.bus's "recent" runtime method.
func mcpBusRecent(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any) (any, error) {
	channels := arguments["channels"]
	if channels == nil {
		channels = []any{}
	}
	limit, _ := mcpkit.Int64FromArgument(arguments["limit"])
	return mcpBusRunMethod(ctx, identity, "recent", []any{channels, limit})
}
