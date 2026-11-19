package mcp

// File overview:
// - Responsibility: the knowledge.* MCP tool catalog (list/find/search/read/
//   create/update/rename/delete over the markdown doc tree).
// - Key symbols: Tools.
// - Moved verbatim (only the descriptor type changed, from
//   modules/base-mcp/routes.ToolDescriptor to kernelmodules.MCPToolDescriptor
//   -- the shape internal/modules.MCPToolRegistration.Tools expects) out of
//   modules/base-mcp/routes/mcp_tools_knowledge.go's knowledgeTools(), per
//   the mcp-module-scaffold plan's Phase 2.

import (
	kernelmodules "git.thebase.vn/base_hub/internal/modules"
)

// Tools is the knowledge.* MCPToolDescriptor catalog, registered with
// base-mcp via Register (provider.go) so it's appended to tools/list only
// when the knowledge module is installed for the caller's tenant DB (the
// provider cache's own install-state filter, see
// modules/base-mcp/routes/mcp_provider_cache.go).
func Tools() []kernelmodules.MCPToolDescriptor {
	return []kernelmodules.MCPToolDescriptor{
		{
			Name:        "knowledge.doc.list",
			Description: "List every markdown document visible to the caller's tenant: read-only \"base\" (Home tenant only) and \"user_guide\" documents, plus the tenant's writable \"public\" documents. Each item's path is namespace-prefixed (e.g. \"public/notes/todo.md\").",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			Name:        "knowledge.doc.find",
			Description: "Find documents by name or keyword instead of needing the exact namespace-prefixed path knowledge.doc.read requires. Matches case-insensitively against each document's display name and filename (substring match, so a full name or just a keyword both work, e.g. \"user guide\" or \"mcp\" both match \"user_guide/base-mcp-user-guide.md\"). Returns every match with its path so a follow-up knowledge.doc.read call can use the exact path. Call this first whenever you don't already know a document's exact logical path.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"query": map[string]any{"type": "string", "description": "Document name or keyword to search for."}},
				"required":   []string{"query"},
			},
		},
		{
			Name:        "knowledge.doc.search",
			Description: "Rank documents by content relevance to a natural-language query, not just name/filename matching (unlike knowledge.doc.find). Uses Postgres full-text search when available, otherwise a content-indexed filesystem fallback -- both return the same shape. Prefer this over knowledge.doc.find when you're looking for documents *about* a topic rather than by a known name/keyword.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"query": map[string]any{"type": "string", "description": "Natural-language search query."}},
				"required":   []string{"query"},
			},
		},
		{
			Name:        "knowledge.doc.read",
			Description: "Read one markdown document's raw content by its namespace-prefixed logical path (e.g. \"public/notes/todo.md\", \"base/...\", \"user_guide/...\"). If you don't already know the exact path, call knowledge.doc.find first to look it up by name or keyword.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"path": map[string]any{"type": "string"}},
				"required":   []string{"path"},
			},
		},
		{
			Name:        "knowledge.doc.create",
			Description: "Create a new document in the writable \"public\" namespace. path is namespace-prefixed (e.g. \"public/notes/todo.md\"); fails if it already exists. \"base\"/\"user_guide\" are read-only and cannot be targeted. idempotency_key is required: a stable caller-chosen string so a retried call after a timeout replays the original result instead of erroring on \"already exists\".",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":            map[string]any{"type": "string"},
					"name":            map[string]any{"type": "string", "description": "Optional display label shown in the doc menu."},
					"content":         map[string]any{"type": "string"},
					"idempotency_key": map[string]any{"type": "string", "description": "Required. A stable caller-chosen string that dedupes retried calls."},
				},
				"required": []string{"path", "content", "idempotency_key"},
			},
		},
		{
			Name:        "knowledge.doc.update",
			Description: "Overwrite an existing \"public\" namespace document's content by its logical path.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":    map[string]any{"type": "string"},
					"content": map[string]any{"type": "string"},
				},
				"required": []string{"path", "content"},
			},
		},
		{
			Name:        "knowledge.doc.rename",
			Description: "Move/rename a \"public\" namespace document from from_path to to_path (both logical paths). Fails if to_path already exists.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"from_path": map[string]any{"type": "string"},
					"to_path":   map[string]any{"type": "string"},
				},
				"required": []string{"from_path", "to_path"},
			},
		},
		{
			Name:        "knowledge.doc.delete",
			Description: "Delete a \"public\" namespace document by its logical path.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"path": map[string]any{"type": "string"}},
				"required":   []string{"path"},
			},
		},
	}
}
