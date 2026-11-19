package mcp

// File overview:
// - Responsibility: dispatches one auth.* MCP tool call (record CRUD
//   restricted to sys.oauth_client).
// - Key symbols: Dispatch.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"git.thebase.vn/base_hub/internal/mcpkit"
	"git.thebase.vn/base_hub/internal/orm"
)

// Dispatch routes one auth.* MCP tool call. Registered as this provider's
// Dispatch func (see provider.go) -- base-mcp's provider cache already
// confirmed the base-auth module is installed for the caller's tenant DB
// before ever reaching here.
func Dispatch(ctx context.Context, identity *orm.RBACIdentity, toolName string, arguments map[string]any) (any, error) {
	switch toolName {
	case "auth.record.search_read":
		return authRecordDispatch(ctx, identity, arguments, "search_read", mcpkit.RecordModeSearch)
	case "auth.record.read":
		return authRecordDispatch(ctx, identity, arguments, "read", mcpkit.RecordModeRead)
	case "auth.record.create":
		return authRecordDispatch(ctx, identity, arguments, "create", mcpkit.RecordModeCreate)
	case "auth.record.write":
		return authRecordDispatch(ctx, identity, arguments, "write", mcpkit.RecordModeWrite)
	case "auth.record.unlink":
		return authRecordDispatch(ctx, identity, arguments, "unlink", mcpkit.RecordModeUnlink)
	default:
		return nil, mcpkit.NewToolError(404, "tool_not_found", fmt.Sprintf("unknown mcp tool %q", toolName))
	}
}

// authRecordDispatch backs auth.record.*: validates arguments.model is
// sys.oauth_client, then delegates to the exact same mcpkit.RecordDispatch
// base.record.* already uses, so ACL/record-rule/company scope enforcement
// is identical.
func authRecordDispatch(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any, methodName string, mode mcpkit.RecordMode) (any, error) {
	if err := requireAuthAllowedModel(arguments); err != nil {
		return nil, err
	}
	return mcpkit.RecordDispatch(ctx, identity, arguments, methodName, mode)
}

func requireAuthAllowedModel(arguments map[string]any) error {
	modelName := strings.TrimSpace(mcpkit.StringFromAny(arguments["model"]))
	if modelName == "" {
		return mcpkit.NewToolError(422, "invalid_params", "arguments.model is required")
	}
	if !authAllowedModels[modelName] {
		names := make([]string, 0, len(authAllowedModels))
		for name := range authAllowedModels {
			names = append(names, name)
		}
		sort.Strings(names)
		return mcpkit.NewToolError(422, "invalid_params", fmt.Sprintf(
			"model %q is not one of the auth.* models this tool group accepts (%s)", modelName, strings.Join(names, ", "),
		))
	}
	return nil
}
