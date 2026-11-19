package models

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestUIViewTypeSeedMatchesFrontendViewTypes guards the catalog contract: the seeded view-mode
// rows (is_view_mode) must be exactly the frontend VIEW_TYPES ModelView can render, so a view
// type added on one side without the other fails CI instead of silently never rendering.
func TestUIViewTypeSeedMatchesFrontendViewTypes(t *testing.T) {
	registry, err := os.ReadFile("../../frontend/modules/base/components/views/view-registry.js")
	if err != nil {
		t.Fatalf("read view-registry.js: %v", err)
	}
	match := regexp.MustCompile(`export const VIEW_TYPES = \[([^\]]*)\]`).FindSubmatch(registry)
	if match == nil {
		t.Fatal("VIEW_TYPES declaration not found in view-registry.js")
	}
	var frontend []string
	for _, item := range strings.Split(string(match[1]), ",") {
		if name := strings.Trim(strings.TrimSpace(item), `"'`); name != "" {
			frontend = append(frontend, name)
		}
	}

	raw, err := os.ReadFile("../../modules/base/data/12_ui_view_type.json")
	if err != nil {
		t.Fatalf("read ui.view.type seed: %v", err)
	}
	var seed []struct {
		Model string `json:"model"`
		Data  struct {
			Name       string `json:"name"`
			IsViewMode bool   `json:"is_view_mode"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &seed); err != nil {
		t.Fatalf("parse ui.view.type seed: %v", err)
	}
	var backend []string
	for _, row := range seed {
		if row.Model == UIViewTypeModelName && row.Data.IsViewMode {
			backend = append(backend, row.Data.Name)
		}
	}

	sort.Strings(frontend)
	sort.Strings(backend)
	if strings.Join(frontend, ",") != strings.Join(backend, ",") {
		t.Fatalf("view modes drifted: frontend VIEW_TYPES=%v, ui.view.type seed=%v", frontend, backend)
	}
}
