package apidoc

import (
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestBuildAliasOpenAPICoversLegacyAndRESTRoutes(t *testing.T) {
	models := []orm.ModelMetadata{{
		Name: "demo.order",
		RuntimeMethods: []orm.RuntimeMethodMetadata{
			{Name: "cancel", Public: true, UsesPathID: true, AliasRoute: "demo/orders/:id/cancel"},
			{Name: "get_by_serial", Public: true, AliasRoute: "GET /base/orders/:serial", AliasRoutes: []string{"DELETE /base/orders/:serial"}, Description: "Fetch one order."},
			{Name: "internal_only", Public: true},
		},
	}}
	spec := BuildAliasOpenAPI(Config{Title: "T", Version: "1"}, models, "en")

	if spec["openapi"] != "3.1.0" {
		t.Fatalf("openapi version = %v", spec["openapi"])
	}
	paths := spec["paths"].(map[string]any)
	if len(paths) != 2 {
		t.Fatalf("paths = %v, want 2 (methods without alias are skipped)", paths)
	}
	legacy := paths["/base/demo/orders/{id}/cancel"].(map[string]any)
	if _, ok := legacy["post"]; !ok {
		t.Fatalf("legacy alias must be POST: %v", legacy)
	}
	rest := paths["/base/orders/{serial}"].(map[string]any)
	get := rest["get"].(map[string]any)
	if _, ok := rest["delete"]; !ok {
		t.Fatalf("extra alias verb missing: %v", rest)
	}
	if tags := get["tags"].([]string); len(tags) != 1 || tags[0] != "demo.order" {
		t.Fatalf("tags = %v, want grouped by model", tags)
	}
	if get["summary"] != "demo.order.get_by_serial" {
		t.Fatalf("summary = %v, want \"<model>.<method>\"", get["summary"])
	}
	if !strings.Contains(get["description"].(string), "Fetch one order.") {
		t.Fatalf("description = %v", get["description"])
	}
}

func TestAliasOpenAPIFileName(t *testing.T) {
	if got := AliasOpenAPIFileName("en"); got != "open_apidoc.json" {
		t.Fatalf("en = %s", got)
	}
	if got := AliasOpenAPIFileName("vi"); got != "open_apidoc.vn.json" {
		t.Fatalf("vi = %s", got)
	}
}

func TestBuildAliasOpenAPIBodyExamples(t *testing.T) {
	models := []orm.ModelMetadata{{
		Name:   "demo.order",
		Fields: []orm.FieldMetadata{{Name: "name", Kind: orm.FieldKindText}},
		RuntimeMethods: []orm.RuntimeMethodMetadata{
			{Name: "create", Public: true, AliasRoute: "POST /base/orders"},
			{Name: "ship", Public: true, AliasRoute: "POST /base/orders/ship", Params: []orm.RuntimeMethodParam{{Name: "qty", Type: "integer"}, {Name: "note"}}},
		},
	}}
	paths := BuildAliasOpenAPI(Config{}, models, "en")["paths"].(map[string]any)
	example := func(path string) map[string]any {
		post := paths[path].(map[string]any)["post"].(map[string]any)
		content := post["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)
		return content["example"].(map[string]any)
	}
	if _, ok := example("/base/orders")["name"]; !ok {
		t.Fatalf("create example must list writable fields: %v", example("/base/orders"))
	}
	ship := example("/base/orders/ship")
	if ship["qty"] != 0 || ship["note"] != "string" {
		t.Fatalf("ship example = %v, want params as typed placeholders", ship)
	}
}
