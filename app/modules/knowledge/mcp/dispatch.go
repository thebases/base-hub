package mcp

// File overview:
// - Responsibility: dispatches one knowledge.* MCP tool call, delegating to
//   the document service through internal/modules.KnowledgeDocumentProviderHooks
//   -- the same kernel hook this module already registered itself against
//   (modules/knowledge/knowledge.go), so this file has no direct import of
//   modules/knowledge/routes even though it lives in this module's own tree.
// - Key symbols: Dispatch, knowledgeMCPDocError.
// - Moved verbatim out of modules/base-mcp/routes/mcp_tools_knowledge.go's
//   dispatchKnowledgeMCPTool, per the mcp-module-scaffold plan's Phase 2 --
//   only the error/string helpers now come from internal/mcpkit instead of
//   this package's own private helpers.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"git.thebase.vn/base_hub/internal/mcpkit"
	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/orm"
	knowledgemodels "git.thebase.vn/base_hub/modules/knowledge/models"
)

// Dispatch routes one knowledge.* MCP tool call. Registered as this
// provider's Dispatch func (see provider.go) -- base-mcp's provider cache
// already confirmed the knowledge module is installed for the caller's
// tenant DB before ever reaching here (mcp_provider_cache.go).
func Dispatch(ctx context.Context, identity *orm.RBACIdentity, toolName string, arguments map[string]any) (any, error) {
	provider, ok := kernelmodules.KnowledgeDocumentProviderHooks()
	if !ok {
		return nil, mcpkit.NewToolError(503, "internal_error", "knowledge module is not bootstrapped")
	}
	dbName := ""
	if identity != nil {
		dbName = identity.DBName
	}

	// Writes change documents every tenant caller (anonymous /md/* included)
	// reads: same rule as the /md/api/files* routes.
	switch toolName {
	case "knowledge.doc.create", "knowledge.doc.update", "knowledge.doc.rename", "knowledge.doc.delete":
		if identity == nil {
			return nil, mcpkit.NewToolError(401, "unauthorized", "knowledge write tools need an authenticated caller")
		}
		if err := knowledgemodels.EnsureKnowledgeWriteAllowed(mcpkit.RequestContext(ctx, identity)); err != nil {
			return nil, mcpkit.NewToolError(403, "permission_denied", err.Error())
		}
	}

	switch toolName {
	case "knowledge.doc.list":
		items, err := provider.ListDocuments(dbName)
		if err != nil {
			return nil, mcpkit.NewToolError(500, "internal_error", err.Error())
		}
		return map[string]any{"items": items}, nil
	case "knowledge.doc.find":
		query := strings.TrimSpace(mcpkit.StringFromAny(arguments["query"]))
		if query == "" {
			return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.query is required")
		}
		items, err := provider.FindDocuments(dbName, query)
		if err != nil {
			return nil, mcpkit.NewToolError(500, "internal_error", err.Error())
		}
		return map[string]any{"items": items}, nil
	case "knowledge.doc.search":
		query := strings.TrimSpace(mcpkit.StringFromAny(arguments["query"]))
		if query == "" {
			return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.query is required")
		}
		items, err := provider.SearchDocuments(ctx, dbName, query)
		if err != nil {
			return nil, mcpkit.NewToolError(500, "internal_error", err.Error())
		}
		return map[string]any{"items": items}, nil
	case "knowledge.doc.read":
		path := strings.TrimSpace(mcpkit.StringFromAny(arguments["path"]))
		if path == "" {
			return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.path is required")
		}
		content, err := provider.ReadDocument(dbName, path)
		if err != nil {
			return nil, knowledgeMCPDocError(err, fmt.Sprintf("document %q not found", path))
		}
		return map[string]any{"path": path, "content": string(content)}, nil
	case "knowledge.doc.create":
		path := strings.TrimSpace(mcpkit.StringFromAny(arguments["path"]))
		if path == "" {
			return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.path is required")
		}
		name := strings.TrimSpace(mcpkit.StringFromAny(arguments["name"]))
		content := mcpkit.StringFromAny(arguments["content"])
		createdPath, err := provider.CreateDocument(ctx, dbName, path, name, content)
		if err != nil {
			return nil, mcpkit.NewToolError(409, "invalid_operation", err.Error())
		}
		return map[string]any{"path": createdPath}, nil
	case "knowledge.doc.update":
		path := strings.TrimSpace(mcpkit.StringFromAny(arguments["path"]))
		if path == "" {
			return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.path is required")
		}
		content := mcpkit.StringFromAny(arguments["content"])
		updatedPath, err := provider.UpdateDocument(ctx, dbName, path, content)
		if err != nil {
			return nil, knowledgeMCPDocError(err, fmt.Sprintf("document %q not found", path))
		}
		return map[string]any{"path": updatedPath}, nil
	case "knowledge.doc.rename":
		fromPath := strings.TrimSpace(mcpkit.StringFromAny(arguments["from_path"]))
		toPath := strings.TrimSpace(mcpkit.StringFromAny(arguments["to_path"]))
		if fromPath == "" || toPath == "" {
			return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.from_path and arguments.to_path are required")
		}
		renamedPath, err := provider.RenameDocument(ctx, dbName, fromPath, toPath)
		if err != nil {
			return nil, mcpkit.NewToolError(409, "invalid_operation", err.Error())
		}
		return map[string]any{"path": renamedPath}, nil
	case "knowledge.doc.delete":
		path := strings.TrimSpace(mcpkit.StringFromAny(arguments["path"]))
		if path == "" {
			return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.path is required")
		}
		deletedPath, err := provider.DeleteDocument(ctx, dbName, path)
		if err != nil {
			return nil, knowledgeMCPDocError(err, fmt.Sprintf("document %q not found", path))
		}
		return map[string]any{"path": deletedPath}, nil
	default:
		return nil, mcpkit.NewToolError(404, "tool_not_found", fmt.Sprintf("unknown mcp tool %q", toolName))
	}
}

// knowledgeMCPDocError maps a knowledge-service error to a 404 not_found for
// the common "path does not exist" case, falling back to a generic
// invalid_operation error for anything else (e.g. an escaped/read-only path).
func knowledgeMCPDocError(err error, notFoundMessage string) error {
	if errors.Is(err, fs.ErrNotExist) {
		return mcpkit.NewToolError(404, "not_found", notFoundMessage)
	}
	return mcpkit.NewToolError(422, "invalid_operation", err.Error())
}
