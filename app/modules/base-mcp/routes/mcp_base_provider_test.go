package routes

// File overview:
// - Responsibility: registers modules/base/mcp's real base.* MCP tool
//   provider for this package's own test binary, since base.* tools no
//   longer live in this package after the mcp-module-scaffold plan's Phase 5
//   -- dispatchMCPTool now reaches them only through the registered
//   kernelmodules.MCPToolProvider (mcp_provider_cache.go), exactly like a
//   real running server (modules/base's Bootstrap calls this same
//   Register()). A _test.go file importing another module directly is an
//   established exception to the "no module imports another module's
//   package" rule elsewhere in this package's tests too (e.g.
//   modules/base-auth/service is already imported by mcp_route_test.go for
//   its own fixtures) -- production code never takes this import, only test
//   code, so it creates no real inter-module coupling or Manifest.Depends
//   requirement.

import (
	basemcp "git.thebase.vn/base_hub/modules/base/mcp"
)

// init runs once per test binary regardless of which Test function executes,
// so every test in this package that exercises a base.* tool through
// dispatchMCPTool/HandleJSONRPC (e.g. TestMCPRouteToolsCallEnforcesRBACParity)
// gets the real implementation, not a "tool_not_found".
func init() {
	basemcp.Register()
}
