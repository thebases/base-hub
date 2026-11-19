package routes

// File overview:
// - Responsibility: base-mcp's in-memory cache over every registered
//   kernelmodules.MCPToolProvider (internal/modules/mcp_tool_hooks.go),
//   filtered per tenant DB by sys_module_module.state -- the single source
//   dispatchMCPTool/mcpListTools use once every module's mcp/ package
//   contributes its tools through this generic path instead of a hardcoded
//   strings.HasPrefix branch per module.
// - Key symbols: mcpLookupToolProvider, mcpToolsByModuleFromCache,
//   rebuildMCPProviderCache, mcpProviderCacheTTL.
// - Pattern: mirrors the existing mcpCatalogReloadDebounce idiom
//   (mcp_route_fallback.go) -- a short TTL fallback so any lifecycle path
//   that somehow bypasses InvalidateMCPCache still self-heals within
//   mcpProviderCacheTTL instead of silently drifting.

import (
	"context"
	"sort"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/queries"
	"git.thebase.vn/base_hub/internal/services/log"
)

// mcpProviderCacheTTL mirrors mcpCatalogReloadDebounce's role: a defensive
// upper bound on how stale the provider cache is allowed to get before a
// lookup forces a synchronous rebuild, independent of the proactive
// InvalidateMCPCache hook every install/uninstall/reinit already calls.
const mcpProviderCacheTTL = 30 * time.Second

// mcpProviderCacheBuildTimeout bounds one rebuild's install-state queries. The
// rebuild is shared by every caller collapsed into the singleflight, so it runs
// detached from the first caller's cancellation and needs its own deadline.
const mcpProviderCacheBuildTimeout = 10 * time.Second

// mcpModuleInstalled is the per-module install-state probe; a variable only so
// tests can simulate a failing tenant DB.
var mcpModuleInstalled = func(ctx context.Context, dbName, moduleName string) (bool, error) {
	return queries.ModuleInstalled(ctx, orm.ResolvePGXPool(dbName), moduleName)
}

// mcpProviderCacheEntry is one tenant DB's snapshot: which tools exist and
// which provider backs each, plus the per-module catalog for tools/list.
type mcpProviderCacheEntry struct {
	builtAt       time.Time
	toolsByName   map[string]kernelmodules.MCPToolProvider
	toolsByModule map[string][]ToolDescriptor
}

var (
	mcpProviderCacheMu   sync.RWMutex
	mcpProviderCacheByDB = map[string]*mcpProviderCacheEntry{}
	// mcpProviderCacheGen is bumped by rebuildMCPProviderCache (guarded by
	// mcpProviderCacheMu) so a rebuild that started before an invalidation
	// cannot store its now-stale snapshot after the map was cleared.
	mcpProviderCacheGen uint64
)

// rebuildMCPProviderCacheForDB snapshots every registered provider
// (kernelmodules.MCPToolProviders), keeping only the ones whose module is
// actually installed for dbName (queries.ModuleInstalled) -- registration
// only means "compiled in and bootstrapped," not "installed for this
// tenant." Iterates module names in sorted order (MCPToolProviderNamesSorted)
// so a duplicate tool name across two providers deterministically keeps the
// first (alphabetically-earliest-module) registrant, logging a warning
// instead of silently overwriting or panicking -- this is a scaffolding
// defect if it ever happens, never expected in normal operation.
//
// A snapshot is cached only when every install-state probe succeeded: a probe
// error (DB hiccup, timeout) yields a partial catalog that is returned to this
// caller alone, so it cannot hide tools from every caller for the whole TTL.
func rebuildMCPProviderCacheForDB(ctx context.Context, dbName string) *mcpProviderCacheEntry {
	mcpProviderCacheMu.RLock()
	gen := mcpProviderCacheGen
	mcpProviderCacheMu.RUnlock()

	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), mcpProviderCacheBuildTimeout)
	defer cancel()
	providers := kernelmodules.MCPToolProviders()
	entry := &mcpProviderCacheEntry{
		builtAt:       time.Now(),
		toolsByName:   map[string]kernelmodules.MCPToolProvider{},
		toolsByModule: map[string][]ToolDescriptor{},
	}
	complete := true
	for _, moduleName := range kernelmodules.MCPToolProviderNamesSorted() {
		provider, ok := providers[moduleName]
		if !ok {
			continue
		}
		installed, err := mcpModuleInstalled(ctx, dbName, moduleName)
		if err != nil {
			log.Warnf("mcp: provider cache for db %q: install state of module %q: %v", dbName, moduleName, err)
			complete = false
			continue
		}
		if !installed {
			continue
		}
		var descriptors []ToolDescriptor
		for _, d := range provider.Tools() {
			if _, exists := entry.toolsByName[d.Name]; exists {
				log.Warnf("mcp: tool %q from module %q ignored -- already registered by an earlier module (duplicate tool name is a scaffolding defect)", d.Name, moduleName)
				continue
			}
			entry.toolsByName[d.Name] = provider
			descriptors = append(descriptors, ToolDescriptor{Name: d.Name, Description: d.Description, InputSchema: d.InputSchema})
		}
		if len(descriptors) > 0 {
			entry.toolsByModule[moduleName] = descriptors
		}
	}
	if !complete {
		return entry
	}
	mcpProviderCacheMu.Lock()
	if gen == mcpProviderCacheGen {
		mcpProviderCacheByDB[dbName] = entry
	}
	mcpProviderCacheMu.Unlock()
	return entry
}

// mcpProviderCacheEntryFor returns the fresh-enough cache entry for
// identity's tenant DB, rebuilding synchronously when absent or past
// mcpProviderCacheTTL. Cached per dbName (not one global snapshot) since
// module install state is per tenant DB -- the same reason
// websiteModuleEnabled/workflowModuleEnabled/knowledgeModuleEnabled each took
// a ctx/identity pair before this cache existed.
func mcpProviderCacheEntryFor(ctx context.Context, identity *orm.RBACIdentity) *mcpProviderCacheEntry {
	dbName := ""
	if identity != nil {
		dbName = identity.DBName
	}
	mcpProviderCacheMu.RLock()
	entry, ok := mcpProviderCacheByDB[dbName]
	mcpProviderCacheMu.RUnlock()
	if ok && time.Since(entry.builtAt) <= mcpProviderCacheTTL {
		return entry
	}
	// Collapse concurrent rebuilds after TTL expiry into one per tenant DB.
	v, _, _ := mcpProviderCacheFlight.Do(dbName, func() (any, error) {
		return rebuildMCPProviderCacheForDB(ctx, dbName), nil
	})
	return v.(*mcpProviderCacheEntry)
}

var mcpProviderCacheFlight singleflight.Group

// mcpLookupToolProvider is dispatchMCPTool's generic replacement for the
// three hardcoded strings.HasPrefix("website."/"knowledge."/"workflow.")
// branches: any tool name a provider declared via Tools() resolves here,
// regardless of which module owns it.
func mcpLookupToolProvider(ctx context.Context, identity *orm.RBACIdentity, toolName string) (kernelmodules.MCPToolProvider, bool) {
	entry := mcpProviderCacheEntryFor(ctx, identity)
	provider, ok := entry.toolsByName[toolName]
	return provider, ok
}

// mcpToolsByModuleFromCache is mcpListTools' generic replacement for the
// three-way tool-catalog concatenation (websiteTools()/knowledgeTools()/
// workflowTools()): flattens every installed provider's catalog, in sorted
// module-name order for a deterministic tools/list response.
func mcpToolsByModuleFromCache(ctx context.Context, identity *orm.RBACIdentity) []ToolDescriptor {
	entry := mcpProviderCacheEntryFor(ctx, identity)
	names := make([]string, 0, len(entry.toolsByModule))
	for name := range entry.toolsByModule {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []ToolDescriptor
	for _, name := range names {
		out = append(out, entry.toolsByModule[name]...)
	}
	return out
}

// rebuildMCPProviderCache is InvalidateMCPCache's hook into this cache. A
// lifecycle event (install/uninstall/reinit) doesn't carry which tenant DB it
// applies to at this call site, so rather than guess, it clears every cached
// tenant entry -- the next lookup for any dbName rebuilds fresh. Cheap: this
// is an admin-triggered, rare event, not a hot path.
func rebuildMCPProviderCache(ctx context.Context) {
	mcpProviderCacheMu.Lock()
	mcpProviderCacheByDB = map[string]*mcpProviderCacheEntry{}
	mcpProviderCacheGen++
	mcpProviderCacheMu.Unlock()
}
