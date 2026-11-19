package modules

// File overview:
// - Responsibility: lets any module's mcp/ package register its MCP tool
//   catalog + dispatch function without base-mcp importing that module (or
//   that module importing base-mcp) directly.
// - Key symbols: MCPToolDescriptor, MCPToolProvider, RegisterMCPToolProvider,
//   MCPToolProviders, MCPToolRegistration, RegisterMCPTools.
// - Pattern: mirrors the DatabaseProvisioning/KnowledgeDocumentProvider hook
//   idiom in this package -- base-mcp stays reachable without lifecycle code
//   importing modules/base-mcp, and a tool-contributing module never imports
//   modules/base-mcp either. Every module's mcp/provider.go calls
//   RegisterMCPTools(MCPToolRegistration{...}) directly from here, its only
//   import for this being internal/modules.
// - Naming note: the mcp-module-scaffold plan's design doc originally called
//   for the ergonomic struct-of-funcs registration call to be named plain
//   "Register" and to live in modules/base-mcp/routes (re-exporting storage
//   kept here) -- but this package already has a general module-bootstrap
//   `Register(Module)` (registry.go), so that name is taken, and routing the
//   call through modules/base-mcp/routes at all would mean every
//   tool-contributing module importing that package, exactly the inbound
//   dependency the "Decoupled Plugin Module Pattern" forbids. Resolution:
//   the ergonomic entry point lives here as RegisterMCPTools, called
//   directly by every module's mcp/provider.go -- modules/base-mcp/routes
//   never appears in that import path at all.

import (
	"context"
	"sort"
	"sync"

	"git.thebase.vn/base_hub/internal/orm"
)

// MCPToolDescriptor mirrors mcpkit.ToolDescriptor so a module's mcp/ package
// can build a tool catalog without importing modules/base-mcp/routes (inbound
// imports of a module are forbidden; this keeps the dependency direction
// outbound-only, base-mcp -> hook -> module).
type MCPToolDescriptor struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// MCPToolProvider is implemented by a module's mcp/ package so base-mcp can
// list and dispatch that module's tools without importing the module
// directly. Dispatch is only ever called with a toolName this provider
// declared via Tools().
type MCPToolProvider interface {
	Tools() []MCPToolDescriptor
	Dispatch(ctx context.Context, identity *orm.RBACIdentity, toolName string, arguments map[string]any) (any, error)
}

var (
	mcpToolProvidersMu sync.RWMutex
	mcpToolProviders   = map[string]MCPToolProvider{} // keyed by manifest module name
)

// RegisterMCPToolProvider lets a module's mcp/ package (from its own
// Bootstrap) register its tool contribution once. Re-registering the same
// name replaces it (process-restart/test idiom already used elsewhere in
// this package).
func RegisterMCPToolProvider(moduleName string, provider MCPToolProvider) {
	mcpToolProvidersMu.Lock()
	mcpToolProviders[moduleName] = provider
	mcpToolProvidersMu.Unlock()
}

// UnregisterMCPToolProvider removes moduleName's registration entirely (a
// no-op if it was never registered). Used by tests to reset this
// process-global registry between cases -- RegisterMCPToolProvider(name,
// nil) is not equivalent, since a nil MCPToolProvider stored in the map
// would panic the next time a cache rebuild calls its Tools()/Dispatch().
func UnregisterMCPToolProvider(moduleName string) {
	mcpToolProvidersMu.Lock()
	delete(mcpToolProviders, moduleName)
	mcpToolProvidersMu.Unlock()
}

// MCPToolProviders returns a snapshot of every registered provider keyed by
// module name. Callers (base-mcp) still must filter this against
// sys_module_module.state themselves -- registration only means "compiled in
// and bootstrapped", not "installed for this tenant DB".
func MCPToolProviders() map[string]MCPToolProvider {
	mcpToolProvidersMu.RLock()
	defer mcpToolProvidersMu.RUnlock()
	out := make(map[string]MCPToolProvider, len(mcpToolProviders))
	for k, v := range mcpToolProviders {
		out[k] = v
	}
	return out
}

// MCPToolProviderNamesSorted returns the registered provider module names in
// deterministic (sorted) order -- used by the provider-cache rebuild so a
// duplicate tool name across two providers always keeps the same "first
// registrant, sorted by module name" winner instead of depending on map
// iteration order.
func MCPToolProviderNamesSorted() []string {
	mcpToolProvidersMu.RLock()
	names := make([]string, 0, len(mcpToolProviders))
	for name := range mcpToolProviders {
		names = append(names, name)
	}
	mcpToolProvidersMu.RUnlock()
	sort.Strings(names)
	return names
}

// MCPToolRegistration is the one struct every module's mcp/provider.go builds
// and hands to RegisterMCPTools -- an "abstract" contribution record (module
// name, tool catalog func, dispatch func) instead of requiring a module to
// hand-implement MCPToolProvider's Tools()/Dispatch() method pair itself.
//
// This (not modules/base-mcp/routes) is where the ergonomic registration API
// has to live: the mcp-module-scaffold plan's design originally called for a
// module to call `mcproutes.Register(...)`, re-exported from this package
// under a friendlier name -- but that would mean the module imports
// modules/base-mcp/routes to make that call, which is exactly the inbound
// import "Decoupled Plugin Module Pattern" forbids (and would force
// Manifest.Depends: [base-mcp] onto every tool-contributing module). So every
// module's mcp/provider.go calls kernelmodules.RegisterMCPTools directly --
// its only import for this is internal/modules, which every module already
// depends on via the kernel.
type MCPToolRegistration struct {
	Module   string
	Tools    func() []MCPToolDescriptor
	Dispatch func(ctx context.Context, identity *orm.RBACIdentity, toolName string, arguments map[string]any) (any, error)
}

// mcpRegistrationAdapter satisfies MCPToolProvider by calling through to a
// wrapped MCPToolRegistration's own Tools()/Dispatch() funcs. Wraps the
// registration in a field (rather than a type conversion) since Go forbids a
// method and a field sharing one name on the same type, and
// MCPToolRegistration's own fields are already named Tools/Dispatch.
type mcpRegistrationAdapter struct {
	reg MCPToolRegistration
}

func (a mcpRegistrationAdapter) Tools() []MCPToolDescriptor {
	if a.reg.Tools == nil {
		return nil
	}
	return a.reg.Tools()
}

func (a mcpRegistrationAdapter) Dispatch(ctx context.Context, identity *orm.RBACIdentity, toolName string, arguments map[string]any) (any, error) {
	if a.reg.Dispatch == nil {
		return nil, nil
	}
	return a.reg.Dispatch(ctx, identity, toolName, arguments)
}

// RegisterMCPTools is the single entry point every module with an mcp/
// folder calls (from its own Bootstrap) to make its tools reachable through
// base-mcp. Re-registering the same Module name replaces the prior
// registration -- the same process-restart/test idiom RegisterMCPToolProvider
// itself already documents.
func RegisterMCPTools(reg MCPToolRegistration) {
	RegisterMCPToolProvider(reg.Module, mcpRegistrationAdapter{reg: reg})
}
