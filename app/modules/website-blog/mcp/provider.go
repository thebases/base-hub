package mcp

// File overview:
// - Responsibility: registers the website_blog.* MCP tool group with base-mcp's provider registry.
// - Key symbols: Register.

import kernelmodules "git.thebase.vn/base_hub/internal/modules"

// Register makes the tool catalog/dispatch reachable through base-mcp's provider cache (listed
// only on tenants where website-blog is installed) without base-mcp importing this package.
func Register() {
	kernelmodules.RegisterMCPTools(kernelmodules.MCPToolRegistration{
		Module:   "website-blog",
		Tools:    Tools,
		Dispatch: Dispatch,
	})
}
