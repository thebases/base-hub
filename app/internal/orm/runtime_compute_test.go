package orm

// File overview:
// - Responsibility: verifies the SRS-F-018 computed-field contract — RPC write
//   rejection, non-stored search rejection, bootstrap handler validation, and
//   readonly metadata projection.
// - Key symbols: focused Test cases plus file-local fixtures.
// - Pattern: Locks down runtime invariants so refactors preserve the compute
//   handler contract end to end.

import (
	"context"
	"strings"
	"testing"
)

type runtimeComputeContractTestDocument struct {
	BaseModel
	Name string `bun:"name" json:"name"`
	// Non-stored computed field: read-path only, never SQL-backed.
	Summary string `bun:"summary" json:"summary" meta:"compute:_compute_summary,stored:false,label:Summary"`
}

// registerComputeContractMeta registers the shared fixture model used by the
// compute-contract tests; repeated registration reuses the same storage name so
// the global registry accepts it across tests in one binary.
func registerComputeContractMeta(t *testing.T) *ModelMetadata {
	t.Helper()
	const modelName = "test.runtime_compute_contract"
	meta, err := RegisterModelType[runtimeComputeContractTestDocument](ModelSpec{
		Name:       modelName,
		Label:      "Runtime Compute Contract",
		Definition: ModelDefinition(modelName, (*runtimeComputeContractTestDocument)(nil)),
	}.WithStorageName("runtime_compute_contract"))
	if err != nil {
		t.Fatalf("register model: %v", err)
	}
	return meta
}

// TestComputedFieldMetadataIsReadOnly verifies that a compute: meta tag forces
// readonly=true so SDUI forms never offer edits the runtime must reject.
func TestComputedFieldMetadataIsReadOnly(t *testing.T) {
	meta := registerComputeContractMeta(t)

	field, err := meta.Field("summary")
	if err != nil {
		t.Fatalf("lookup summary field: %v", err)
	}
	if field.Compute == "" {
		t.Fatalf("expected summary to carry its compute handler name, got empty")
	}
	if field.Stored {
		t.Fatalf("expected summary to be non-stored")
	}
	if !field.ReadOnly {
		t.Fatalf("expected computed field to be forced readonly in metadata")
	}
}

// TestRejectComputedFieldValues verifies the SRS-F-018 write contract: a value
// targeting a computed field fails with a validation error naming the field,
// while plain fields pass through untouched.
func TestRejectComputedFieldValues(t *testing.T) {
	meta := registerComputeContractMeta(t)

	err := rejectComputedFieldValues(meta, RecordMap{"summary": "client supplied"})
	if err == nil {
		t.Fatalf("expected write to computed field to be rejected")
	}
	if !strings.Contains(err.Error(), `"summary"`) {
		t.Fatalf("expected rejection to name the computed field, got %v", err)
	}

	if err := rejectComputedFieldValues(meta, RecordMap{"name": "ok"}); err != nil {
		t.Fatalf("expected plain field write to pass, got %v", err)
	}
}

// TestDomainCompileRejectsNonStoredComputedField verifies that a domain
// condition on a non-stored computed field fails with the explicit
// not-searchable contract error instead of compiling an undefined column.
func TestDomainCompileRejectsNonStoredComputedField(t *testing.T) {
	meta := registerComputeContractMeta(t)

	domain := Where("summary", "=", "anything")
	_, _, err := domain.CompileWithBaseAlias(meta, quoteIdent("runtime_compute_contract"))
	if err == nil {
		t.Fatalf("expected domain on non-stored computed field to be rejected")
	}
	if !strings.Contains(err.Error(), "not searchable") {
		t.Fatalf("expected explicit not-searchable error, got %v", err)
	}

	// A stored plain field on the same model must still compile.
	if _, _, err := Where("name", "=", "x").CompileWithBaseAlias(meta, quoteIdent("runtime_compute_contract")); err != nil {
		t.Fatalf("expected stored field domain to compile, got %v", err)
	}
}

// TestValidateComputeHandlers verifies bootstrap fail-fast: a computed field
// without a registered handler is reported, and registering the handler clears
// that specific report (other fixture models in the shared registry may still
// be missing handlers, so assertions stay scoped to this model's field).
func TestValidateComputeHandlers(t *testing.T) {
	meta := registerComputeContractMeta(t)
	needle := meta.Name + ".summary"

	// The fixture handler may already be registered by an earlier test in this
	// binary; only assert the missing report when it is genuinely absent.
	runtimeHookRegistry.mu.RLock()
	_, registered := runtimeHookRegistry.computes[meta.Name]["summary"]
	runtimeHookRegistry.mu.RUnlock()
	if !registered {
		err := ValidateComputeHandlers()
		if err == nil || !strings.Contains(err.Error(), needle) {
			t.Fatalf("expected validation to report %s, got %v", needle, err)
		}
	}

	RegisterCompute(meta.Name, "summary", func(_ context.Context, records RecordSet, _ FieldMetadata) (map[string]any, error) {
		out := make(map[string]any, len(records.IDs()))
		for _, id := range records.IDs() {
			out[id] = "computed"
		}
		return out, nil
	})

	if err := ValidateComputeHandlers(); err != nil && strings.Contains(err.Error(), needle) {
		t.Fatalf("expected %s to be resolved after registration, got %v", needle, err)
	}
}
