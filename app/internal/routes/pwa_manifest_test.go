package routes

import (
	"context"
	"encoding/json"
	"testing"
)

func TestBuildPWAManifestOverlaysConfigOnDefaults(t *testing.T) {
	params := map[string]json.RawMessage{
		"name":  json.RawMessage(`"Acme"`),
		"scope": json.RawMessage(`"  "`),
		"icons": json.RawMessage(`[{"src":"/i.png","sizes":"48x48"}]`),
	}
	m := buildPWAManifest(context.Background(), params)
	if m["name"] != "Acme" {
		t.Fatalf("name = %v", m["name"])
	}
	if m["scope"] != "/" {
		t.Fatalf("blank scope must fall back, got %v", m["scope"])
	}
	if icons, _ := m["icons"].([]map[string]any); len(icons) != 1 {
		t.Fatalf("icons = %v", m["icons"])
	}
}
