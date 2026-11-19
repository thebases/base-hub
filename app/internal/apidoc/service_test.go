package apidoc

// File overview:
// - Responsibility: verifies API documentation generation and transport-facing schema output for service.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down documentation payload shape so model metadata changes do not silently break generated docs or OpenAPI output.

import (
	"context"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestIndexFiltersInaccessibleModels(t *testing.T) {
	svc := NewService(
		WithConfig(Config{Title: "Bean", Version: "test", BasePath: "/api"}),
		WithModelSource(func() []orm.ModelMetadata {
			return []orm.ModelMetadata{
				{
					Name:  "tbl.partner",
					Label: "Contact",
					Fields: []orm.FieldMetadata{
						{Name: "id", Label: "ID", Kind: orm.FieldKindID, Type: "int64", Stored: true},
						{Name: "name", Label: "Name", Kind: orm.FieldKindText, Type: "string", Stored: true},
					},
					RuntimeMethods: []orm.RuntimeMethodMetadata{
						{Name: "read", Public: true, ReadOnly: true, UsesPathID: true},
						{Name: "search", Public: true, ModelOnly: true, ReadOnly: true},
					},
				},
				{
					Name:  "secret.audit",
					Label: "Secret Audit",
					RuntimeMethods: []orm.RuntimeMethodMetadata{
						{Name: "read", Public: true, ReadOnly: true, UsesPathID: true},
					},
				},
			}
		}),
		WithAccessChecker(func(_ context.Context, model orm.ModelMetadata) error {
			if model.Name == "secret.audit" {
				return context.Canceled
			}
			return nil
		}),
	)

	index, err := svc.Index(context.Background())
	if err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	if len(index.Models) != 1 {
		t.Fatalf("expected 1 visible model, got %d", len(index.Models))
	}
	if index.Models[0].Model != "tbl.partner" {
		t.Fatalf("expected tbl.partner, got %s", index.Models[0].Model)
	}
	if _, ok := index.Models[0].Fields["name"]; !ok {
		t.Fatalf("expected indexed field metadata for name")
	}
	if len(index.Models[0].Methods) != 2 {
		t.Fatalf("expected public method names, got %v", index.Models[0].Methods)
	}
}

func TestModelBuildsMethodAndFieldDocs(t *testing.T) {
	svc := NewService(
		WithConfig(Config{Title: "Bean", Version: "test", BasePath: "/api"}),
		WithModelSource(func() []orm.ModelMetadata {
			return []orm.ModelMetadata{
				{
					Name:        "tbl.partner",
					Label:       "Contact",
					Description: "Contact records used by the runtime.",
					Fields: []orm.FieldMetadata{
						{Name: "id", Label: "ID", Kind: orm.FieldKindID, Type: "int64", Stored: true},
						{Name: "name", Label: "Name", Kind: orm.FieldKindText, Type: "string", Stored: true, Required: true, Help: "Display name."},
						{Name: "tag_ids", Label: "Tags", Kind: orm.FieldKindManyToMany, Type: "[]int64", Stored: true},
					},
					RuntimeMethods: []orm.RuntimeMethodMetadata{
						{Name: "read", Public: true, ReadOnly: true, UsesPathID: true},
						{Name: "search_read", Public: true, ModelOnly: true, ReadOnly: true},
						{Name: "internal_only", Public: false},
					},
				},
			}
		}),
	)

	doc, err := svc.Model(context.Background(), "tbl.partner")
	if err != nil {
		t.Fatalf("Model() error = %v", err)
	}
	if doc.Doc == nil || *doc.Doc != "Contact records used by the runtime." {
		t.Fatalf("expected model description, got %#v", doc.Doc)
	}
	if doc.Fields["name"].String != "Name" {
		t.Fatalf("expected field label, got %#v", doc.Fields["name"])
	}
	if doc.Fields["tag_ids"].Searchable {
		t.Fatalf("expected many2many fields to be non-searchable")
	}
	if _, ok := doc.Methods["internal_only"]; ok {
		t.Fatalf("expected private methods to stay hidden")
	}
	readMethod, ok := doc.Methods["read"]
	if !ok {
		t.Fatalf("expected read method to be documented")
	}
	if readMethod.Path != "/api/rpc/tbl.partner/read/{id}" {
		t.Fatalf("unexpected read path: %s", readMethod.Path)
	}
	if readMethod.Signature == "" || readMethod.Parameters["ids"].Annotation == "" {
		t.Fatalf("expected default method docs for read, got %#v", readMethod)
	}
	createMethod := methodDocument(orm.ModelMetadata{
		Name: "tbl.partner",
		Fields: []orm.FieldMetadata{
			{Name: "id", Kind: orm.FieldKindID, PrimaryKey: true, Stored: true},
			{Name: "name", Kind: orm.FieldKindText, Stored: true, Required: true},
			{Name: "active", Kind: orm.FieldKindBoolean, Stored: true},
			{Name: "category_id", Kind: orm.FieldKindManyToOne, Stored: true},
			{Name: "computed_name", Kind: orm.FieldKindText, Stored: true, ReadOnly: true, Compute: "compute_name"},
		},
	}, orm.RuntimeMethodMetadata{Name: "create", Public: true}, "/api", "")
	requestExample, ok := createMethod.RequestExample.(map[string]any)
	if !ok {
		t.Fatalf("expected create request example, got %#v", createMethod.RequestExample)
	}
	args, ok := requestExample["args"].([]any)
	if !ok || len(args) != 1 {
		t.Fatalf("expected one args item, got %#v", requestExample["args"])
	}
	values, ok := args[0].(map[string]any)
	if !ok {
		t.Fatalf("expected create values map, got %#v", args[0])
	}
	if _, ok := values["name"]; !ok {
		t.Fatalf("expected create example to include writable name field: %#v", values)
	}
	if _, ok := values["active"]; !ok {
		t.Fatalf("expected create example to include writable active field: %#v", values)
	}
	if _, ok := values["category_id"]; !ok {
		t.Fatalf("expected create example to include writable relation field: %#v", values)
	}
	if _, ok := values["id"]; ok {
		t.Fatalf("did not expect create example to include id field: %#v", values)
	}
	if _, ok := values["computed_name"]; ok {
		t.Fatalf("did not expect create example to include readonly compute field: %#v", values)
	}
}

func TestOpenAPIIncludesRPCPaths(t *testing.T) {
	svc := NewService(
		WithConfig(Config{Title: "Bean", Version: "test", BasePath: "/api"}),
		WithModelSource(func() []orm.ModelMetadata {
			return []orm.ModelMetadata{
				{
					Name:  "tbl.partner",
					Label: "Contact",
					Fields: []orm.FieldMetadata{
						{Name: "id", Label: "ID", Kind: orm.FieldKindID, Type: "int64", Stored: true, Required: true},
						{Name: "active", Label: "Active", Kind: orm.FieldKindBoolean, Type: "bool", Stored: true},
					},
					RuntimeMethods: []orm.RuntimeMethodMetadata{
						{Name: "search_read", Public: true, ModelOnly: true, ReadOnly: true},
						{Name: "write", Public: true, UsesPathID: true},
					},
				},
			}
		}),
	)

	spec, err := svc.OpenAPI(context.Background())
	if err != nil {
		t.Fatalf("OpenAPI() error = %v", err)
	}
	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatalf("expected OpenAPI paths map, got %T", spec["paths"])
	}
	if _, ok := paths["/api/rpc/tbl.partner/search_read"]; !ok {
		t.Fatalf("expected search_read path in spec")
	}
	if _, ok := paths["/api/rpc/tbl.partner/write/{id}"]; !ok {
		t.Fatalf("expected write path with id in spec")
	}
	writeOperation := paths["/api/rpc/tbl.partner/write/{id}"].(map[string]any)["post"].(map[string]any)
	requestBody := writeOperation["requestBody"].(map[string]any)
	content := requestBody["content"].(map[string]any)
	jsonContent := content["application/json"].(map[string]any)
	if _, ok := jsonContent["example"]; !ok {
		t.Fatalf("expected write request example in OpenAPI content")
	}
}
