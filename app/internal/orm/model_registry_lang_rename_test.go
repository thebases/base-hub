package orm

import "testing"

func TestResolveDefinitionFieldForStructPrefersRenamedJSONField(t *testing.T) {
	definition := NewModel("base.users")
	definition.AddField(NewField("lang_id", "lang", FieldKindManyToOne))

	field := resolveDefinitionFieldForStruct(definition, "lang", "lang_id", "", "lang")
	if field == nil {
		t.Fatalf("resolveDefinitionFieldForStruct() returned nil")
	}
	if field.Name != "lang_id" {
		t.Fatalf("resolveDefinitionFieldForStruct() name = %q, want %q", field.Name, "lang_id")
	}
	if field.Column != "lang" {
		t.Fatalf("resolveDefinitionFieldForStruct() column = %q, want %q", field.Column, "lang")
	}
}
