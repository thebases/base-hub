package orm

import (
	"context"
	"testing"
)

// TestEnsureRuntimeModelAccessExposesMethodName: access handlers see which runtime method they
// are checking, since every non-read-only custom method arrives as operation "write".
func TestEnsureRuntimeModelAccessExposesMethodName(t *testing.T) {
	const modelName = "test.access_method_name_seam"
	var seen, op string
	RegisterAccessHandler(modelName, func(ctx context.Context, _ *Environment, _ *ModelMetadata, operation string, _ []string, _ RecordMap) error {
		seen, op = RuntimeMethodNameFromContext(ctx), operation
		return nil
	})
	model := &ModelMetadata{Name: modelName}
	if err := EnsureRuntimeModelAccess(context.Background(), model, RuntimeMethod{Name: "set_active"}, []string{"1"}); err != nil {
		t.Fatalf("access: %v", err)
	}
	if seen != "set_active" || op != "write" {
		t.Fatalf("handler saw method %q operation %q, want set_active/write", seen, op)
	}
	if got := RuntimeMethodNameFromContext(context.Background()); got != "" {
		t.Fatalf("outside an access check the method name must be empty, got %q", got)
	}
}
