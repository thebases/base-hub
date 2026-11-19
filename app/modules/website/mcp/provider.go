package mcp

// File overview:
// - Responsibility: registers this module's MCP tool catalog/dispatch with
//   base-mcp's provider registry, per the mcp-module-scaffold plan's
//   "MCP contribution layer" scaffold convention (code-pattern.md).
// - Key symbols: Register.
// - Pattern: the one mandatory call every module's mcp/ folder makes from its
//   own Bootstrap() -- see modules/knowledge/mcp/provider.go for the first
//   module to adopt this convention.

import (
	kernelmodules "git.thebase.vn/base_hub/internal/modules"
)

// Register makes this module's website.* tool catalog/dispatch reachable
// through base-mcp's provider cache, without either package importing the
// other directly (internal/modules is the only shared import).
func Register() {
	kernelmodules.RegisterMCPTools(kernelmodules.MCPToolRegistration{
		Module:   "website",
		Tools:    Tools,
		Dispatch: Dispatch,
	})
}
