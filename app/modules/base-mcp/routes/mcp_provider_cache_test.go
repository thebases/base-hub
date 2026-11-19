package routes

// File overview:
// - Responsibility: coverage for mcp_provider_cache.go -- provider-cache
//   rebuild, duplicate-tool-name precedence, and TTL-based staleness.
// - Key symbols: focused Test cases against fake kernelmodules.MCPToolProvider
//   registrations.
// - DB-free scope note: queries.ModuleInstalled fails OPEN (returns true) on
//   a nil *pgxpool.Pool, and this test binary has no real Postgres
//   connection configured (orm.ResolvePGXPool("") resolves to nil here) --
//   so every registered fake provider below is treated as "installed" by
//   rebuildMCPProviderCacheForDB regardless of its module name. This still
//   exercises the cache's real rebuild/lookup/TTL logic end to end; only the
//   "not installed -> excluded" half of the install-state filter needs a
//   real tenant DB fixture to verify (already covered indirectly by
//   TestMCPRouteToolsCallEnforcesRBACParity's Postgres-backed RBAC-parity
//   test in mcp_route_test.go).
// - Isolation note: this package's own mcp_base_provider_test.go registers
//   the real "base" module provider once for the whole test binary, and
//   other packages' Bootstrap (website/knowledge/base-workflow) never runs
//   here at all -- so only "base" is ever present alongside these tests'
//   fakes. Each test below only registers/unregisters its own uniquely-named
//   fake providers (never a blanket wipe of the whole registry) so it can
//   never accidentally unregister "base" out from under a sibling test, and
//   assertions check for specific expected tool names/counts rather than an
//   exact total tool count, so the ambient "base" catalog never has to be
//   accounted for by hand.

import (
	"context"
	"errors"
	"testing"
	"time"

	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/orm"
)

// fakeMCPToolProvider is a minimal kernelmodules.MCPToolProvider for cache
// tests, independent of any real module's tool group.
type fakeMCPToolProvider struct {
	tools []kernelmodules.MCPToolDescriptor
}

func (p *fakeMCPToolProvider) Tools() []kernelmodules.MCPToolDescriptor { return p.tools }

func (p *fakeMCPToolProvider) Dispatch(_ context.Context, _ *orm.RBACIdentity, toolName string, _ map[string]any) (any, error) {
	return map[string]any{"echo": toolName}, nil
}

// clearProviderCacheMap resets only this package's own cache map (not the
// shared kernel provider registry), so a stale/short-TTL entry left behind
// by a previous test can never leak into the next one.
func clearProviderCacheMap(t *testing.T) {
	t.Helper()
	mcpProviderCacheMu.Lock()
	mcpProviderCacheByDB = map[string]*mcpProviderCacheEntry{}
	mcpProviderCacheMu.Unlock()
}

// TestRebuildMCPProviderCacheDuplicateToolNameKeepsFirstSortedRegistrant
// proves a duplicate tool name across two providers is resolved
// deterministically: the alphabetically-earliest module name wins, not
// whichever happened to register last or iterate first in a map.
func TestRebuildMCPProviderCacheDuplicateToolNameKeepsFirstSortedRegistrant(t *testing.T) {
	clearProviderCacheMap(t)
	kernelmodules.RegisterMCPToolProvider("zzz-cache-test-module", &fakeMCPToolProvider{
		tools: []kernelmodules.MCPToolDescriptor{{Name: "dup.cache.test.tool", Description: "from zzz"}},
	})
	kernelmodules.RegisterMCPToolProvider("aaa-cache-test-module", &fakeMCPToolProvider{
		tools: []kernelmodules.MCPToolDescriptor{{Name: "dup.cache.test.tool", Description: "from aaa"}},
	})
	t.Cleanup(func() {
		kernelmodules.UnregisterMCPToolProvider("zzz-cache-test-module")
		kernelmodules.UnregisterMCPToolProvider("aaa-cache-test-module")
		clearProviderCacheMap(t)
	})

	entry := rebuildMCPProviderCacheForDB(t.Context(), "test-dup-tool-name")
	provider, ok := entry.toolsByName["dup.cache.test.tool"]
	if !ok {
		t.Fatalf("dup.cache.test.tool not present in rebuilt cache")
	}
	fake, ok := provider.(*fakeMCPToolProvider)
	if !ok || fake.tools[0].Description != "from aaa" {
		t.Fatalf("dup.cache.test.tool resolved to %#v, want the aaa-cache-test-module (sorted-first) registrant", provider)
	}
	// Only one of the two module catalogs should carry the tool -- the loser
	// contributes zero tools once its only descriptor collided.
	if _, stillThere := entry.toolsByModule["zzz-cache-test-module"]; stillThere {
		t.Fatalf("zzz-cache-test-module should contribute no tools once its only tool name collided, got %#v", entry.toolsByModule["zzz-cache-test-module"])
	}
}

// TestMcpLookupToolProviderAndToolsByModuleFromCache covers the two
// transport-facing read paths (dispatchMCPTool/mcpListTools) against a fresh
// cache build. Assertions check for this test's own tool names specifically
// (see file header) rather than an exact total, since the real "base"
// provider is also registered for this test binary.
func TestMcpLookupToolProviderAndToolsByModuleFromCache(t *testing.T) {
	clearProviderCacheMap(t)
	kernelmodules.RegisterMCPToolProvider("fake-cache-test-module", &fakeMCPToolProvider{
		tools: []kernelmodules.MCPToolDescriptor{
			{Name: "fake.cache.test.tool.one", Description: "one"},
			{Name: "fake.cache.test.tool.two", Description: "two"},
		},
	})
	t.Cleanup(func() {
		kernelmodules.UnregisterMCPToolProvider("fake-cache-test-module")
		clearProviderCacheMap(t)
	})

	if _, ok := mcpLookupToolProvider(t.Context(), nil, "fake.cache.test.tool.one"); !ok {
		t.Fatal("expected fake.cache.test.tool.one to resolve to a provider")
	}
	if _, ok := mcpLookupToolProvider(t.Context(), nil, "does.not.exist"); ok {
		t.Fatal("expected an unregistered tool name to resolve to no provider")
	}

	toolNames := map[string]bool{}
	for _, tool := range mcpToolsByModuleFromCache(t.Context(), nil) {
		toolNames[tool.Name] = true
	}
	if !toolNames["fake.cache.test.tool.one"] || !toolNames["fake.cache.test.tool.two"] {
		t.Fatalf("mcpToolsByModuleFromCache = %v, want it to include this test's two fake tool names", toolNames)
	}
}

// TestMCPProviderCacheTTLFallbackRebuildsAfterExpiry proves a lookup past
// mcpProviderCacheTTL forces a synchronous rebuild instead of serving stale
// data forever -- the safety net InvalidateMCPCache's proactive rebuild
// exists alongside, not instead of.
func TestMCPProviderCacheTTLFallbackRebuildsAfterExpiry(t *testing.T) {
	const dbName = "test-provider-cache-ttl"
	clearProviderCacheMap(t)
	t.Cleanup(func() {
		kernelmodules.UnregisterMCPToolProvider("late-cache-test-module")
		clearProviderCacheMap(t)
	})

	// Build the cache once, then force it stale by rewinding its builtAt past
	// the TTL window.
	rebuildMCPProviderCacheForDB(t.Context(), dbName)
	mcpProviderCacheMu.Lock()
	mcpProviderCacheByDB[dbName].builtAt = time.Now().Add(-mcpProviderCacheTTL - time.Second)
	mcpProviderCacheMu.Unlock()

	// Register a provider only after that stale build -- a fresh lookup must
	// notice the entry is past TTL and rebuild rather than reusing the
	// now-outdated snapshot.
	kernelmodules.RegisterMCPToolProvider("late-cache-test-module", &fakeMCPToolProvider{
		tools: []kernelmodules.MCPToolDescriptor{{Name: "late.cache.test.tool", Description: "late"}},
	})

	if _, ok := mcpLookupToolProvider(t.Context(), &orm.RBACIdentity{DBName: dbName}, "late.cache.test.tool"); !ok {
		t.Fatal("expected the TTL-expired cache to rebuild and pick up the newly registered provider")
	}
}

// stubModuleInstalled swaps the install-state probe for one test.
func stubModuleInstalled(t *testing.T, probe func(ctx context.Context, dbName, moduleName string) (bool, error)) {
	t.Helper()
	previous := mcpModuleInstalled
	mcpModuleInstalled = probe
	t.Cleanup(func() { mcpModuleInstalled = previous })
}

// A failed install-state probe must not pin a partial tool list for the whole TTL.
func TestMCPProviderCacheDoesNotCacheOnProbeError(t *testing.T) {
	const dbName = "test-provider-cache-probe-error"
	clearProviderCacheMap(t)
	kernelmodules.RegisterMCPToolProvider("flaky-cache-test-module", &fakeMCPToolProvider{
		tools: []kernelmodules.MCPToolDescriptor{{Name: "flaky.cache.test.tool"}},
	})
	t.Cleanup(func() {
		kernelmodules.UnregisterMCPToolProvider("flaky-cache-test-module")
		clearProviderCacheMap(t)
	})
	fail := true
	stubModuleInstalled(t, func(_ context.Context, _, moduleName string) (bool, error) {
		if fail && moduleName == "flaky-cache-test-module" {
			return false, errors.New("db hiccup")
		}
		return true, nil
	})

	identity := &orm.RBACIdentity{DBName: dbName}
	if _, ok := mcpLookupToolProvider(t.Context(), identity, "flaky.cache.test.tool"); ok {
		t.Fatal("tool of a module whose probe failed must not resolve on that call")
	}
	mcpProviderCacheMu.RLock()
	_, cached := mcpProviderCacheByDB[dbName]
	mcpProviderCacheMu.RUnlock()
	if cached {
		t.Fatal("partial snapshot must not be cached")
	}
	fail = false
	if _, ok := mcpLookupToolProvider(t.Context(), identity, "flaky.cache.test.tool"); !ok {
		t.Fatal("next lookup must rebuild once the probe recovers")
	}
}

// A cancelled first caller must not make the shared rebuild fail for everyone.
func TestMCPProviderCacheRebuildIgnoresCallerCancellation(t *testing.T) {
	const dbName = "test-provider-cache-cancel"
	clearProviderCacheMap(t)
	t.Cleanup(func() { clearProviderCacheMap(t) })
	stubModuleInstalled(t, func(ctx context.Context, _, _ string) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if _, ok := ctx.Deadline(); !ok {
			return false, errors.New("rebuild must run under its own timeout")
		}
		return true, nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	rebuildMCPProviderCacheForDB(ctx, dbName)
	mcpProviderCacheMu.RLock()
	_, cached := mcpProviderCacheByDB[dbName]
	mcpProviderCacheMu.RUnlock()
	if !cached {
		t.Fatal("rebuild under a cancelled caller context should still complete and cache")
	}
}

// An invalidation during a rebuild must win over that rebuild's stale snapshot.
func TestMCPProviderCacheInvalidationDuringRebuildDropsSnapshot(t *testing.T) {
	const dbName = "test-provider-cache-invalidate-race"
	clearProviderCacheMap(t)
	t.Cleanup(func() { clearProviderCacheMap(t) })
	stubModuleInstalled(t, func(context.Context, string, string) (bool, error) {
		rebuildMCPProviderCache(context.Background())
		return true, nil
	})
	rebuildMCPProviderCacheForDB(t.Context(), dbName)
	mcpProviderCacheMu.RLock()
	_, cached := mcpProviderCacheByDB[dbName]
	mcpProviderCacheMu.RUnlock()
	if cached {
		t.Fatal("snapshot built across an invalidation must not be stored")
	}
}
