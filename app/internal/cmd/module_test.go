package cmd

// File overview:
// - Responsibility: verifies CLI bootstrap, module loading, and server startup wiring for module.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down startup expectations so changes to command bootstrap do not break operational flows or initialization order.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScaffoldModuleCreatesExpectedFiles(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "modules")
	moduleRoot, err := scaffoldModule(root, "demo_sales")
	if err != nil {
		t.Fatalf("scaffold module: %v", err)
	}

	expectedFiles := []string{
		filepath.Join(moduleRoot, "demo_sales.go"),
		filepath.Join(moduleRoot, "manifest.md"),
		filepath.Join(moduleRoot, "data", ".gitkeep"),
		filepath.Join(moduleRoot, "i18n", ".gitkeep"),
		filepath.Join(moduleRoot, "models", "runtime_bootstrap.go"),
		filepath.Join(moduleRoot, "security", "sys_model_access.csv"),
		filepath.Join(moduleRoot, "static", ".gitkeep"),
		// scaffoldModule seeds a dashboard so views/ is never an empty placeholder.
		filepath.Join(moduleRoot, "views", "dashboard-demo_sales.xml"),
	}

	for _, path := range expectedFiles {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected scaffold file %s: %v", path, err)
		}
	}

	manifestBytes, err := os.ReadFile(filepath.Join(moduleRoot, "manifest.md"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	manifest := string(manifestBytes)
	if !strings.Contains(manifest, "name: demo_sales") {
		t.Fatalf("manifest missing module name: %s", manifest)
	}
	if !strings.Contains(manifest, "depends: [base]") {
		t.Fatalf("manifest missing base dependency: %s", manifest)
	}
	if !strings.Contains(manifest, "frontend_widgets: {}") {
		t.Fatalf("manifest missing frontend widget catalog placeholder: %s", manifest)
	}

	bootstrapBytes, err := os.ReadFile(filepath.Join(moduleRoot, "models", "runtime_bootstrap.go"))
	if err != nil {
		t.Fatalf("read runtime bootstrap: %v", err)
	}
	if !strings.Contains(string(bootstrapBytes), "var Models orm.ModelSet") {
		t.Fatalf("runtime bootstrap is not the ModelSet form: %s", bootstrapBytes)
	}

	accessBytes, err := os.ReadFile(filepath.Join(moduleRoot, "security", "sys_model_access.csv"))
	if err != nil {
		t.Fatalf("read access csv: %v", err)
	}
	if string(accessBytes) != scaffoldAccessCSVHeader {
		t.Fatalf("unexpected access csv header: %q", string(accessBytes))
	}
}

func TestNormalizeModuleNameRejectsInvalidNames(t *testing.T) {
	t.Parallel()

	normalized, err := normalizeModuleName("Sales")
	if err != nil {
		t.Fatalf("normalize mixed-case name: %v", err)
	}
	if normalized != "sales" {
		t.Fatalf("expected mixed-case name to normalize to sales, got %q", normalized)
	}

	invalidNames := []string{"", "sales-order", "1sales", "sales.order"}
	for _, name := range invalidNames {
		if _, err := normalizeModuleName(name); err == nil {
			t.Fatalf("expected invalid name %q to fail", name)
		}
	}
}
