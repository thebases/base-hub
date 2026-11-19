package mcp

// File overview:
// - Responsibility: dispatches one project.* MCP tool call (record CRUD
//   restricted to tbl.project).
// - Key symbols: Dispatch.

import (
	"context"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/mcpkit"
	"git.thebase.vn/base_hub/internal/orm"
)

// Dispatch routes one project.* MCP tool call. Registered as this provider's
// Dispatch func (see provider.go) -- base-mcp's provider cache already
// confirmed the project module is installed for the caller's tenant DB
// before ever reaching here.
func Dispatch(ctx context.Context, identity *orm.RBACIdentity, toolName string, arguments map[string]any) (any, error) {
	switch toolName {
	case "project.record.search_read":
		return projectRecordDispatch(ctx, identity, arguments, "search_read", mcpkit.RecordModeSearch)
	case "project.record.read":
		return projectRecordDispatch(ctx, identity, arguments, "read", mcpkit.RecordModeRead)
	case "project.record.create":
		return projectRecordDispatch(ctx, identity, arguments, "create", mcpkit.RecordModeCreate)
	case "project.record.write":
		return projectRecordDispatch(ctx, identity, arguments, "write", mcpkit.RecordModeWrite)
	case "project.record.unlink":
		return projectRecordDispatch(ctx, identity, arguments, "unlink", mcpkit.RecordModeUnlink)
	default:
		return nil, mcpkit.NewToolError(404, "tool_not_found", fmt.Sprintf("unknown mcp tool %q", toolName))
	}
}

// projectRecordDispatch backs project.record.*: validates arguments.model is
// tbl.project, then delegates to the exact same mcpkit.RecordDispatch
// base.record.* already uses.
func projectRecordDispatch(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any, methodName string, mode mcpkit.RecordMode) (any, error) {
	if err := requireProjectAllowedModel(arguments); err != nil {
		return nil, err
	}
	return mcpkit.RecordDispatch(ctx, identity, arguments, methodName, mode)
}

func requireProjectAllowedModel(arguments map[string]any) error {
	modelName := strings.TrimSpace(mcpkit.StringFromAny(arguments["model"]))
	if modelName == "" {
		return mcpkit.NewToolError(422, "invalid_params", "arguments.model is required")
	}
	if !projectAllowedModels[modelName] {
		return mcpkit.NewToolError(422, "invalid_params", fmt.Sprintf(
			"model %q is not one of the project.* models this tool group accepts", modelName,
		))
	}
	return nil
}
