package orm

import "testing"

func TestReorderRecordsByIDsPreservesRequestedOrder(t *testing.T) {
	t.Parallel()

	records := []RecordMap{
		{"id": int64(3), "name": "third"},
		{"id": int64(1), "name": "first"},
		{"id": int64(2), "name": "second"},
		{"id": int64(99), "name": "extra"},
	}

	ordered := reorderRecordsByIDs(records, []string{"1", "2", "3"})
	got := []string{
		ordered[0]["name"].(string),
		ordered[1]["name"].(string),
		ordered[2]["name"].(string),
		ordered[3]["name"].(string),
	}
	want := []string{"first", "second", "third", "extra"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected order at index %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestBuildRuntimeSelectColumnsSkipsVirtualAndDeduplicatesFields(t *testing.T) {
	t.Parallel()

	columns := buildRuntimeSelectColumns(&ModelMetadata{
		Fields: []FieldMetadata{
			{Name: "id", BunColumn: "id", Kind: FieldKindID, Stored: true},
			{Name: "name", BunColumn: "name", Kind: FieldKindText, Stored: true, Translate: true},
			{Name: "tags", BunColumn: "tag_ids", Kind: FieldKindManyToMany, Stored: true},
			{Name: "name", BunColumn: "name_shadow", Kind: FieldKindText, Stored: true},
		},
	})

	if len(columns) != 2 {
		t.Fatalf("expected 2 SQL-backed columns, got %d", len(columns))
	}
	if columns[1].FieldName != "name" || !columns[1].Translate {
		t.Fatalf("expected translated name column, got %+v", columns[1])
	}
}
