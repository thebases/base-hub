package orm

import "testing"

func TestBuildModelHelperHTMLUsesFieldHelp(t *testing.T) {
	meta := ModelMetadata{
		Name: "test.helper",
		Fields: []FieldMetadata{
			{Name: "name", Label: "Name", Help: "Shown to users"},
			{Name: "code", Label: "Code", Help: ""},
			{Name: "note", Help: "Escapes <tags> safely"},
		},
	}

	got := BuildModelHelperHTML(meta)
	want := "<ul><li><strong>Name</strong>: Shown to users</li><li><strong>note</strong>: Escapes &lt;tags&gt; safely</li></ul>"
	if got != want {
		t.Fatalf("helper html = %q, want %q", got, want)
	}
}

func TestBuildModelJSONDTOIncludesFields(t *testing.T) {
	meta := ModelMetadata{
		Name:        "test.dto",
		Label:       "Test DTO",
		Description: "Example",
		Fields: []FieldMetadata{
			{Name: "name", Label: "Name", Help: "Shown to users"},
		},
	}

	dto := BuildModelJSONDTO(meta)
	if dto["name"] != "test.dto" {
		t.Fatalf("dto name = %#v, want test.dto", dto["name"])
	}
	fields, ok := dto["fields"].([]any)
	if !ok || len(fields) != 1 {
		t.Fatalf("dto fields = %#v, want one field", dto["fields"])
	}
}
