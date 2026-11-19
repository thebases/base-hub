package orm

import "testing"

func TestSequenceCodeForField(t *testing.T) {
	model := &ModelMetadata{Name: "tbl.demo"}
	if got := sequenceCodeForField(model, FieldMetadata{Name: "number", UseSequence: true}); got != "tbl.demo.number" {
		t.Fatalf("default code = %q", got)
	}
	if got := sequenceCodeForField(model, FieldMetadata{Name: "number", SequenceCode: "custom.code"}); got != "custom.code" {
		t.Fatalf("override code = %q", got)
	}
}

func TestUseSequenceMetaTagParsed(t *testing.T) {
	type rec struct {
		BaseModel
		Number string `bun:"number" json:"number" meta:"use_sequence,sequence_code:demo.seq"`
	}
	def := ModelDefinition("tbl.demo_seq", (*rec)(nil))
	for _, f := range def.Fields {
		if f.Name == "number" {
			if !f.UseSequence || f.SequenceCode != "demo.seq" {
				t.Fatalf("field = %+v", f)
			}
			return
		}
	}
	t.Fatal("number field missing")
}
