package mcp

// File overview:
// - Responsibility: registers this module's MCP tool catalog/dispatch with
//   base-mcp's provider registry, per the mcp-module-scaffold plan's
//   "MCP contribution layer" scaffold convention (code-pattern.md).
// - Key symbols: Register.
// - Pattern: same one-call convention every migrated tool group uses (see
//   modules/knowledge/mcp/provider.go, modules/website/mcp/provider.go,
//   modules/base-workflow/mcp/provider.go). This is the last of the four
//   Phase 1-5 migrations in the mcp-module-scaffold plan: after this,
//   modules/base-mcp/routes registers no tools of its own -- it is pure
//   transport (JSON-RPC envelope, sessions, cancellation, idempotency
//   wrapper, the provider cache/aggregation, and dispatch delegation), plus
//   base.batch.execute's orchestration wrapper (kept in mcp_route.go since it
//   needs the transport's tenant-tx/dispatch plumbing, not base-specific
//   logic -- see that file's mcpBatchExecute doc comment).

import (
	kernelmodules "git.thebase.vn/base_hub/internal/modules"
)

// Register makes this module's base.* tool catalog/dispatch reachable
// through base-mcp's provider cache, without either package importing the
// other directly (internal/modules is the only shared import).
func Register() {
	kernelmodules.RegisterMCPTools(kernelmodules.MCPToolRegistration{
		Module:   "base",
		Tools:    Tools,
		Dispatch: Dispatch,
	})
}
