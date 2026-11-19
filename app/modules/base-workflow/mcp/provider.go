package mcp

// File overview:
// - Responsibility: registers this module's MCP tool catalog/dispatch with
//   base-mcp's provider registry, per the mcp-module-scaffold plan's
//   "MCP contribution layer" scaffold convention (code-pattern.md).
// - Key symbols: Register.

import (
	kernelmodules "git.thebase.vn/base_hub/internal/modules"
)

// Register makes this module's workflow.* tool catalog/dispatch reachable
// through base-mcp's provider cache, without either package importing the
// other directly (internal/modules is the only shared import). Module name
// is "base-workflow" -- the manifest-declared name (see manifest.md), not
// the "workflow" Go package name -- since provider lookups are keyed by
// manifest module name (module-lifecycle-context.md's "module identity"
// invariant).
func Register() {
	kernelmodules.RegisterMCPTools(kernelmodules.MCPToolRegistration{
		Module:   "base-workflow",
		Tools:    Tools,
		Dispatch: Dispatch,
	})
}
