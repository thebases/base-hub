package modules

// File overview:
// - Responsibility: lets the optional base-mcp module supply a callback that
//   install/uninstall/reinit call proactively after they commit, so base-mcp
//   can refresh whatever it caches about the module catalog right away
//   instead of only reacting the next time a lookup happens to miss.
// - Key symbols: MCPCacheInvalidatorFunc, RegisterMCPCacheInvalidator, InvalidateMCPCache.
// - Pattern: Mirrors the DatabaseProvisioning/KnowledgeDocumentProvider hook idiom --
//   base-mcp stays reachable without lifecycle.go importing modules/base-mcp directly.

import "context"

// MCPCacheInvalidatorFunc is called with the module name that was just
// installed, uninstalled, or reinitialized.
type MCPCacheInvalidatorFunc func(ctx context.Context, moduleName string)

var mcpCacheInvalidatorFn MCPCacheInvalidatorFunc

// RegisterMCPCacheInvalidator lets the base-mcp module's init() supply its
// cache-refresh callback once it has bootstrapped.
func RegisterMCPCacheInvalidator(fn MCPCacheInvalidatorFunc) {
	mcpCacheInvalidatorFn = fn
}

// InvalidateMCPCache invokes the registered callback, if any. A no-op when
// base-mcp isn't installed/compiled in, so install/uninstall/reinit never
// fail on account of this being best-effort housekeeping, not a correctness
// requirement.
func InvalidateMCPCache(ctx context.Context, moduleName string) {
	if mcpCacheInvalidatorFn == nil {
		return
	}
	mcpCacheInvalidatorFn(ctx, moduleName)
}
