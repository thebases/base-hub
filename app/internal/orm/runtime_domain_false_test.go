package orm

import "testing"

// A raw false sentinel on a non-boolean scalar must become nil so the domain
// compiler emits IS NULL instead of `col = false` (PostgreSQL SQLSTATE 22007
// on timestamptz columns).
func TestNormalizeDomainScalarValueFalseOnDate(t *testing.T) {
	field := FieldMetadata{Name: "date_done", Kind: FieldKindDateTime}
	if got := normalizeDomainScalarValue(field, false); got != nil {
		t.Fatalf("false on datetime field = %#v, want nil", got)
	}
	boolField := FieldMetadata{Name: "is_closed", Kind: FieldKindBoolean}
	if got := normalizeDomainScalarValue(boolField, false); got != false {
		t.Fatalf("false on boolean field = %#v, want false", got)
	}
}
