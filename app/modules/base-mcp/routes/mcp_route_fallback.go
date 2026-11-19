package routes

// File overview:
// - Responsibility: base-mcp's own hook into the shared lookup-miss fallback
//   pipeline (knowledge-search hint + debounced catalog resync) now owned by
//   internal/mcpkit (see mcpkit/fallback.go's file header for the full
//   rationale/gotchas -- unchanged by this move) plus InvalidateMCPCache, the
//   kernelmodules.MCPCacheInvalidatorFunc implementation this module
//   registers from mcp.go's init().
// - Key symbols: `InvalidateMCPCache`, plus aliases/wrapper vars for
//   `mcpResolveModel`, `mcpResolveRuntimeMethod`, `mcpNotFoundWithHint`.
// - mcpResolveModel/mcpResolveRuntimeMethod/mcpNotFoundWithHint/mcpKnowledgeHint/
//   mcpTriggerCatalogReload moved into internal/mcpkit so every module's mcp/
//   package (website, base-workflow, knowledge, base) can reuse the exact
//   same fallback behavior without importing modules/base-mcp -- see that
//   package's fallback.go file header for the "why this can never actually
//   fix a genuine miss" caveat, unchanged by the move.

import (
	"context"

	"git.thebase.vn/base_hub/internal/mcpkit"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// mcpResolveModel, mcpResolveRuntimeMethod, and mcpNotFoundWithHint are kept
// as wrapper vars so every existing call site in this package keeps
// compiling unchanged; the real implementation is internal/mcpkit's
// ResolveModel/ResolveRuntimeMethod/NotFoundWithHint (fallback.go).
var (
	mcpResolveModel     = mcpkit.ResolveModel
	mcpNotFoundWithHint = mcpkit.NotFoundWithHint
)

// InvalidateMCPCache is base-mcp's kernelmodules.MCPCacheInvalidatorFunc
// implementation, registered from this module's init() (see mcp.go) so
// install/uninstall/reinit can proactively refresh the module catalog rows
// right when the change commits, instead of waiting for the next MCP lookup
// miss to trigger it reactively. Not debounced -- lifecycle actions are rare
// (admin-triggered), unlike the hot lookup-miss path mcpkit.TriggerCatalogReload
// guards -- and the debounce timer is reset first so a miss right afterward
// doesn't skip its own resync attempt believing one just ran.
//
// Phase 7 (see .agents/planning/2026-09-11-mcp-module-scaffold-backend-plan.md)
// adds a second line here rebuilding the MCP tool-provider cache
// (mcp_provider_cache.go) so an install/uninstall/reinit is reflected in
// tools/list and dispatch immediately -- deferred to that phase so this
// phase-1 plumbing move stays a pure, independently-verifiable refactor.
func InvalidateMCPCache(ctx context.Context, moduleName string) {
	mcpkit.ResetCatalogReloadDebounce()
	if _, _, err := orm.SyncModuleCatalog(ctx); err != nil {
		log.Warnf("mcp: module catalog resync after %q lifecycle change failed: %v", moduleName, err)
	}
	rebuildMCPProviderCache(ctx)
}
