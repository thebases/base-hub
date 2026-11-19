package models

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestShippedOptionSchemasAreValid(t *testing.T) {
	files, _ := filepath.Glob("../static/options/*.json")
	if len(files) < 4 {
		t.Fatalf("expected at least 4 option schemas, got %d", len(files))
	}
	for _, f := range files {
		raw, _ := os.ReadFile(f)
		var s websiteOptionSchema
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if err := validateWebsiteOptionSchema(&s); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
}

func TestValidateWebsiteOptionSchemaRejectsUnsafe(t *testing.T) {
	mk := func(c map[string]any) *websiteOptionSchema {
		return &websiteOptionSchema{Selector: "[data-snippet]", Groups: []websiteGroup{{Controls: []map[string]any{c}}}}
	}
	bad := []map[string]any{
		{"id": "a", "label": "A", "type": "toggle", "class": `x" onclick="y`},
		{"id": "a", "label": "A", "type": "color", "style": "background:url(x)"},
		{"id": "a", "label": "A", "type": "text", "attr": "onclick"},
		{"id": "a", "label": "A", "type": "range", "style": "padding-top", "unit": "pt;"},
		{"id": "a", "label": "A", "type": "script"},
		{"id": "a", "label": "A", "type": "select"},
		{"id": "a", "label": "A", "type": "toggle"},
	}
	for i, c := range bad {
		if err := validateWebsiteOptionSchema(mk(c)); err == nil {
			t.Errorf("case %d should be rejected: %v", i, c)
		}
	}
}
