package modules

import (
	"context"
	"testing"
)

// TestInvalidateMCPCacheCallsRegisteredHook proves the Register/Invalidate
// round trip works and that InvalidateMCPCache is a safe no-op when nothing
// is registered (the state a standalone `go test` run of this package, or a
// binary without base-mcp compiled in, is always in).
func TestInvalidateMCPCacheCallsRegisteredHook(t *testing.T) {
	RegisterMCPCacheInvalidator(nil)
	// Must not panic with nothing registered.
	InvalidateMCPCache(context.Background(), "database")

	var gotModule string
	var callCount int
	RegisterMCPCacheInvalidator(func(_ context.Context, moduleName string) {
		callCount++
		gotModule = moduleName
	})
	t.Cleanup(func() { RegisterMCPCacheInvalidator(nil) })

	InvalidateMCPCache(context.Background(), "database")
	if callCount != 1 {
		t.Fatalf("registered invalidator called %d times, want 1", callCount)
	}
	if gotModule != "database" {
		t.Fatalf("registered invalidator moduleName = %q, want %q", gotModule, "database")
	}
}
