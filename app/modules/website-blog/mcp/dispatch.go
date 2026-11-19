package mcp

// File overview:
// - Responsibility: dispatches one website_blog.* MCP tool call through internal/mcpkit, so
//   ACL, record rules and company scope behave exactly like base.record.*.
// - Key symbols: Dispatch.

import (
	"context"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/mcpkit"
	"git.thebase.vn/base_hub/internal/orm"
)

// Dispatch routes one website_blog.* call; base-mcp already checked the module is installed.
func Dispatch(ctx context.Context, identity *orm.RBACIdentity, toolName string, arguments map[string]any) (any, error) {
	switch toolName {
	case "website_blog.record.search_read":
		return recordDispatch(ctx, identity, arguments, "search_read", mcpkit.RecordModeSearch)
	case "website_blog.record.read":
		return recordDispatch(ctx, identity, arguments, "read", mcpkit.RecordModeRead)
	case "website_blog.record.create":
		return recordDispatch(ctx, identity, arguments, "create", mcpkit.RecordModeCreate)
	case "website_blog.record.write":
		return recordDispatch(ctx, identity, arguments, "write", mcpkit.RecordModeWrite)
	case "website_blog.record.unlink":
		return recordDispatch(ctx, identity, arguments, "unlink", mcpkit.RecordModeUnlink)
	}
	if method, ok := postActionTools[toolName]; ok {
		return postAction(ctx, identity, arguments, method)
	}
	return nil, mcpkit.NewToolError(404, "tool_not_found", fmt.Sprintf("unknown mcp tool %q", toolName))
}

func recordDispatch(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any, methodName string, mode mcpkit.RecordMode) (any, error) {
	modelName := strings.TrimSpace(mcpkit.StringFromAny(arguments["model"]))
	if modelName == "" {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.model is required")
	}
	if !blogAllowedModels[modelName] {
		return nil, mcpkit.NewToolError(422, "invalid_params", fmt.Sprintf(
			"model %q is not one of the website_blog models this tool group accepts (%s, %s, %s, %s)",
			modelName, blogModelName, postModelName, tagModelName, categoryModelName,
		))
	}
	return mcpkit.RecordDispatch(ctx, identity, arguments, methodName, mode)
}

// postAction runs a post workflow RuntimeMethod for exactly one id.
func postAction(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any, methodName string) (any, error) {
	ids, err := mcpkit.RequireIDsArg(arguments)
	if err != nil {
		return nil, err
	}
	if len(ids) != 1 {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.ids must contain exactly one post id")
	}
	meta, method, err := mcpkit.LookupRuntimeMethod(postModelName, methodName)
	if err != nil {
		return nil, err
	}
	return mcpkit.ExecuteRuntimeMethod(ctx, identity, meta, method, ids, nil, nil)
}
