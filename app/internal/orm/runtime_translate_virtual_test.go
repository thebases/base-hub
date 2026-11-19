package orm

import "testing"

// A non-stored related field has no column, so INSERT must exclude it (prod: tbl_edc_device_status.routing_rule).
func TestComputedVirtualColumnsExcludesNonStoredRelated(t *testing.T) {
	meta := &ModelMetadata{Fields: []FieldMetadata{
		{Name: "routing_rule", BunColumn: "routing_rule", Kind: FieldKindManyToOne, Related: []string{"device_id", "routing_rule_id"}},
		{Name: "serial", BunColumn: "serial", Stored: true},
	}}
	got := computedVirtualColumns(meta)
	if len(got) != 1 || got[0] != "routing_rule" {
		t.Fatalf("got %v, want [routing_rule]", got)
	}
}
