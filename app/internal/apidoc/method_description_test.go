package apidoc

import (
	"testing"
	"testing/fstest"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestMethodDocumentDescription(t *testing.T) {
	orm.SetDescriptionFS(fstest.MapFS{"m/run.md": {Data: []byte("# Run")}})
	t.Cleanup(func() { orm.SetDescriptionFS(nil) })

	meta := orm.ModelMetadata{Name: "tbl.demo_item"}
	text := methodDocument(meta, orm.RuntimeMethodMetadata{Name: "ping", Description: "Pings it."}, "", "")
	if text.Doc != "Pings it." || text.DocFile != "" {
		t.Fatalf("text description: doc=%q file=%q", text.Doc, text.DocFile)
	}
	file := methodDocument(meta, orm.RuntimeMethodMetadata{Name: "ping", DescriptionFile: "m/run.md"}, "", "")
	if file.DocFile != "# Run" {
		t.Fatalf("file description: doc=%q file=%q", file.Doc, file.DocFile)
	}
}
