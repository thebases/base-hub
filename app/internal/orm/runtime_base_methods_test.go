package orm

// File overview:
// - Responsibility: verifies ORM runtime semantics, repository behavior, and registry coordination for runtime base methods.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down runtime invariants so refactors preserve transaction, RBAC, registry, and storage behavior.

import "testing"

type runtimeFalseNormalizationTestDocument struct {
	BaseModel
	Name      string `bun:"name" json:"name"`
	Active    bool   `bun:"active" json:"active"`
	PartnerID *int64 `bun:"partner_id" json:"partner_id" meta:"kind:many2one,model:tbl.partner"`
}

// registerFalseNormMeta registers the shared fixture model used by all false-
// normalization tests, updating the storage name on subsequent calls so the
// global registry does not reject the same Go type under different names.
func registerFalseNormMeta(t *testing.T) *ModelMetadata {
	t.Helper()
	const modelName = "test.runtime_false_normalize_values"
	meta, err := RegisterModelType[runtimeFalseNormalizationTestDocument](ModelSpec{
		Name:       modelName,
		Label:      "Runtime False Normalize Values",
		Definition: ModelDefinition(modelName, (*runtimeFalseNormalizationTestDocument)(nil)),
	}.WithStorageName("runtime_false_normalize_values"))
	if err != nil {
		t.Fatalf("register model: %v", err)
	}
	return meta
}

// TestNormalizePayloadCanonicalizes verifies that normalizePayload only maps field
// names to their canonical storage form without converting values. False-value
// coercion now lives exclusively in RuntimeCreate/Write (the ORM layer).
func TestNormalizePayloadCanonicalizes(t *testing.T) {
	meta := registerFalseNormMeta(t)

	normalized, err := normalizePayload(meta, RecordMap{
		"name":   false,
		"active": false,
	})
	if err != nil {
		t.Fatalf("normalize payload: %v", err)
	}

	// normalizePayload only canonicalizes keys — values must reach the ORM layer
	// unchanged so RuntimeCreate/Write can apply the single rule set for all
	// mutation paths.
	if got, exists := normalized["name"]; !exists || got != false {
		t.Fatalf("expected name to remain false after canonicalization, got %#v (exists=%v)", got, exists)
	}
	if got, exists := normalized["active"]; !exists || got != false {
		t.Fatalf("expected active to remain false after canonicalization, got %#v (exists=%v)", got, exists)
	}
}

// TestNormalizePayloadSkipsUnknownFields verifies that fields not registered on
// the model are silently dropped instead of returning an error.
func TestNormalizePayloadSkipsUnknownFields(t *testing.T) {
	meta := registerFalseNormMeta(t)

	normalized, err := normalizePayload(meta, RecordMap{
		"name":          "test",
		"unknown_field": "should be silently dropped",
	})
	if err != nil {
		t.Fatalf("expected unknown field to be skipped, got error: %v", err)
	}
	if _, exists := normalized["unknown_field"]; exists {
		t.Fatalf("expected unknown field to be dropped from payload")
	}
	if got := normalized["name"]; got != "test" {
		t.Fatalf("expected known field to survive, got %#v", got)
	}
}

// TestRuntimeNormalizeFalseValuesByFieldKind verifies the three coercion rules
// applied at the ORM layer: (a) relation false→null/integer, (b) boolean
// false→false/convert, (c) others false→null.
func TestRuntimeNormalizeFalseValuesByFieldKind(t *testing.T) {
	meta := registerFalseNormMeta(t)

	normalized, err := runtimeNormalizeFalseValues(meta, RecordMap{
		"partner_id": false,
		"active":     false,
		"name":       false,
	})
	if err != nil {
		t.Fatalf("normalize false values: %v", err)
	}

	// (a) many2one false → null (kept as nil in the map)
	if got := normalized["partner_id"]; got != nil {
		t.Fatalf("expected many2one false to become nil, got %#v", got)
	}
	// (b) boolean false → preserved as an explicit false write
	if got := normalized["active"]; got != false {
		t.Fatalf("expected boolean false to be preserved, got %#v", got)
	}
	// (c) string field false → null
	if got := normalized["name"]; got != nil {
		t.Fatalf("expected string false to become nil, got %#v", got)
	}
}

// TestRuntimeNormalizeFalseValuesManyToOneTupleConvertsToInteger verifies that
// display tuples sent by relation widgets collapse to the integer FK value.
func TestRuntimeNormalizeFalseValuesManyToOneTupleConvertsToInteger(t *testing.T) {
	meta := registerFalseNormMeta(t)

	normalized, err := runtimeNormalizeFalseValues(meta, RecordMap{
		"partner_id": []any{int64(42), "Display Name"},
	})
	if err != nil {
		t.Fatalf("normalize m2o tuple: %v", err)
	}

	// (a) many2one display tuple → int64 ID extracted from first element
	if got := normalized["partner_id"]; got != int64(42) {
		t.Fatalf("expected many2one tuple to normalize to int64(42), got %#v", got)
	}
}

// TestRuntimeNormalizeFalseValuesBooleanStringConverts verifies that boolean
// fields accept canonical string payloads as real Go bools.
func TestRuntimeNormalizeFalseValuesBooleanStringConverts(t *testing.T) {
	meta := registerFalseNormMeta(t)

	normalized, err := runtimeNormalizeFalseValues(meta, RecordMap{
		"active": "true",
	})
	if err != nil {
		t.Fatalf("normalize bool string: %v", err)
	}

	// (b) boolean string → real Go bool
	if got := normalized["active"]; got != true {
		t.Fatalf("expected boolean string 'true' to normalize to true, got %#v", got)
	}
}
