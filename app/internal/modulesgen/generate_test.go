package modulesgen

// File overview:
// - Responsibility: verifies generated module-import scaffolding for generate.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down generator behavior so discovered modules keep producing stable bootstrap import output.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGenerateModuleImports(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "modules")
	for _, moduleName := range []string{"base", "base-sales"} {
		moduleRoot := filepath.Join(root, moduleName)
		if err := os.MkdirAll(moduleRoot, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", moduleName, err)
		}
		if err := os.WriteFile(filepath.Join(moduleRoot, moduleName+".go"), []byte("package "+moduleName+"\n"), 0o644); err != nil {
			t.Fatalf("write %s bootstrap: %v", moduleName, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "assets_only"), 0o755); err != nil {
		t.Fatalf("mkdir assets_only: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "all"), 0o755); err != nil {
		t.Fatalf("mkdir all: %v", err)
	}
	// A non-`base`/`base-*` folder with real bootstrap Go code must still be
	// discovered -- compile-time discovery covers every first-party module
	// (e.g. database, knowledge, website), not just base/base-*. Only
	// auto-install *eligibility* is restricted to base/base-*, and that is
	// enforced independently elsewhere (IsCoreModuleName, DefaultEnabledModules,
	// singleTenantHomeModuleSelection), never by this discovery step.
	nonBaseRoot := filepath.Join(root, "sales")
	if err := os.MkdirAll(nonBaseRoot, 0o755); err != nil {
		t.Fatalf("mkdir sales: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nonBaseRoot, "sales.go"), []byte("package sales\n"), 0o644); err != nil {
		t.Fatalf("write sales bootstrap: %v", err)
	}

	outputPath := filepath.Join(t.TempDir(), "modules_gen.go")
	if err := GenerateModuleImports(GenerateOptions{
		ModulesRoot: root,
		ImportBase:  "git.thebase.vn/base_hub/modules",
		OutputPath:  outputPath,
	}); err != nil {
		t.Fatalf("GenerateModuleImports() error = %v", err)
	}

	raw, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	source := string(raw)
	if !strings.Contains(source, `_ "git.thebase.vn/base_hub/modules/base"`) {
		t.Fatalf("generated file missing base import: %s", source)
	}
	if !strings.Contains(source, `_ "git.thebase.vn/base_hub/modules/base-sales"`) {
		t.Fatalf("generated file missing base-sales import: %s", source)
	}
	if strings.Contains(source, "assets_only") {
		t.Fatalf("generated file should ignore asset-only module: %s", source)
	}
	if !strings.Contains(source, `_ "git.thebase.vn/base_hub/modules/sales"`) {
		t.Fatalf("generated file should still import non-base-prefixed first-party modules: %s", source)
	}
}

func TestGenerateModuleImportsSkipsRewriteWhenOutputMatches(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "modules")
	moduleRoot := filepath.Join(root, "base")
	if err := os.MkdirAll(moduleRoot, 0o755); err != nil {
		t.Fatalf("mkdir base: %v", err)
	}
	if err := os.WriteFile(filepath.Join(moduleRoot, "base.go"), []byte("package base\n"), 0o644); err != nil {
		t.Fatalf("write base bootstrap: %v", err)
	}

	outputPath := filepath.Join(t.TempDir(), "modules_gen.go")
	opts := GenerateOptions{
		ModulesRoot: root,
		ImportBase:  "git.thebase.vn/base_hub/modules",
		OutputPath:  outputPath,
	}
	if err := GenerateModuleImports(opts); err != nil {
		t.Fatalf("first GenerateModuleImports() error = %v", err)
	}
	infoBefore, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("stat generated file before rewrite check: %v", err)
	}

	time.Sleep(1100 * time.Millisecond)

	if err := GenerateModuleImports(opts); err != nil {
		t.Fatalf("second GenerateModuleImports() error = %v", err)
	}
	infoAfter, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("stat generated file after rewrite check: %v", err)
	}
	if !infoAfter.ModTime().Equal(infoBefore.ModTime()) {
		t.Fatalf("expected unchanged output to keep modtime, before=%v after=%v", infoBefore.ModTime(), infoAfter.ModTime())
	}
}
