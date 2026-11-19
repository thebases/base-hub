package modules

import "testing"

func TestResolveSelectedManifestsExpandsDependenciesInDiscoveryOrder(t *testing.T) {
	manifests := []Manifest{
		{Name: "base"},
		{Name: "database", Depends: []string{"base"}},
		{Name: "base-workflow", Depends: []string{"base"}},
		{Name: "base-mcp", Depends: []string{"base"}},
	}

	selected, err := ResolveSelectedManifests(manifests, []string{"base-workflow", "base-mcp"})
	if err != nil {
		t.Fatalf("ResolveSelectedManifests() error = %v", err)
	}
	if len(selected) != 3 {
		t.Fatalf("ResolveSelectedManifests() len = %d, want 3", len(selected))
	}
	if selected[0].Name != "base" || selected[1].Name != "base-workflow" || selected[2].Name != "base-mcp" {
		t.Fatalf("ResolveSelectedManifests() order = %#v", selected)
	}
}

func TestResolveSelectedManifestsRejectsUnknownModules(t *testing.T) {
	_, err := ResolveSelectedManifests([]Manifest{{Name: "base"}}, []string{"missing"})
	if err == nil {
		t.Fatalf("ResolveSelectedManifests() expected unknown-module error")
	}
}

func TestFilterImportRootsByModuleNamesKeepsOnlySelectedModules(t *testing.T) {
	roots := []string{
		"modules/base/data",
		"modules/database/views",
		"modules/base-workflow/security",
	}

	filtered := FilterImportRootsByModuleNames(roots, []string{"base", "base-workflow"})
	if len(filtered) != 2 {
		t.Fatalf("FilterImportRootsByModuleNames() len = %d, want 2", len(filtered))
	}
	if filtered[0] != "modules/base/data" || filtered[1] != "modules/base-workflow/security" {
		t.Fatalf("FilterImportRootsByModuleNames() = %#v", filtered)
	}
}
