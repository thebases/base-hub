package mcp

// File overview:
// - Responsibility: dispatches one credential.* MCP tool call (record CRUD
//   restricted to sys.credential).
// - Key symbols: Dispatch.

import (
	"context"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/mcpkit"
	"git.thebase.vn/base_hub/internal/orm"
)

// Dispatch routes one credential.* MCP tool call. Registered as this
// provider's Dispatch func (see provider.go) -- base-mcp's provider cache
// already confirmed the base-credential module is installed for the
// caller's tenant DB before ever reaching here.
func Dispatch(ctx context.Context, identity *orm.RBACIdentity, toolName string, arguments map[string]any) (any, error) {
	switch toolName {
	case "credential.record.search_read":
		return credentialRecordDispatch(ctx, identity, arguments, "search_read", mcpkit.RecordModeSearch)
	case "credential.record.read":
		return credentialRecordDispatch(ctx, identity, arguments, "read", mcpkit.RecordModeRead)
	case "credential.record.create":
		return credentialRecordDispatch(ctx, identity, arguments, "create", mcpkit.RecordModeCreate)
	case "credential.record.write":
		return credentialRecordDispatch(ctx, identity, arguments, "write", mcpkit.RecordModeWrite)
	case "credential.record.unlink":
		return credentialRecordDispatch(ctx, identity, arguments, "unlink", mcpkit.RecordModeUnlink)
	default:
		return nil, mcpkit.NewToolError(404, "tool_not_found", fmt.Sprintf("unknown mcp tool %q", toolName))
	}
}

// credentialRecordDispatch backs credential.record.*: validates
// arguments.model is sys.credential, then delegates to the exact same
// mcpkit.RecordDispatch base.record.* already uses.
func credentialRecordDispatch(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any, methodName string, mode mcpkit.RecordMode) (any, error) {
	if err := requireCredentialAllowedModel(arguments); err != nil {
		return nil, err
	}
	return mcpkit.RecordDispatch(ctx, identity, arguments, methodName, mode)
}

func requireCredentialAllowedModel(arguments map[string]any) error {
	modelName := strings.TrimSpace(mcpkit.StringFromAny(arguments["model"]))
	if modelName == "" {
		return mcpkit.NewToolError(422, "invalid_params", "arguments.model is required")
	}
	if !credentialAllowedModels[modelName] {
		return mcpkit.NewToolError(422, "invalid_params", fmt.Sprintf(
			"model %q is not one of the credential.* models this tool group accepts", modelName,
		))
	}
	return nil
}
