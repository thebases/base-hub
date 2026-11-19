package routes

// File overview:
// - Responsibility: mcpListTools, the tools/list catalog builder --
//   previously defined inside mcp_tools_website.go (a historical accident of
//   that being the first migrated-out tool group's file); relocated here now
//   that website.* itself moved into modules/website/mcp, since this
//   function assembles the catalog across every tool group, not just
//   website's.
// - Key symbols: mcpListTools.

import (
	"context"

	"git.thebase.vn/base_hub/internal/orm"
)

// mcpListTools returns every installed module's registered MCP tool group
// (base.*, website.*, knowledge.*, workflow.*, and any future module's mcp/
// package), via mcpToolsByModuleFromCache -- the generic,
// install-state-filtered path every migrated module's tools flow through
// (see mcp_provider_cache.go). As of the mcp-module-scaffold plan's Phase 5,
// base.* is a registered provider too (modules/base/mcp), so this package
// registers no tools of its own and mcpListTools no longer hardcodes any
// catalog directly.
func mcpListTools(ctx context.Context, identity *orm.RBACIdentity) []ToolDescriptor {
	return mcpToolsByModuleFromCache(ctx, identity)
}
