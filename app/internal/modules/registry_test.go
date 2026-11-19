package modules

// File overview:
// - Responsibility: verifies module discovery, catalog sync, and bootstrap registration for registry.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down module bootstrap behavior so discovery order, manifest parsing, and catalog persistence stay deterministic.

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/fstest"
)

func TestDiscoverImportRootsFromDiskPreservesStageOrder(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "modules")
	moduleRoot := filepath.Join(root, "base")
	for _, rel := range []string{"security", "data", "views"} {
		if err := os.MkdirAll(filepath.Join(moduleRoot, rel), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	if err := os.WriteFile(filepath.Join(moduleRoot, "manifest.md"), []byte("---\nname: base\n---\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	roots, err := DiscoverImportRootsFromDisk(root)
	if err != nil {
		t.Fatalf("DiscoverImportRootsFromDisk() error = %v", err)
	}

	want := []string{
		filepath.Join(moduleRoot, "security"),
		filepath.Join(moduleRoot, "data"),
		filepath.Join(moduleRoot, "views"),
	}
	if len(roots) != len(want) {
		t.Fatalf("root count = %d, want %d (%v)", len(roots), len(want), roots)
	}
	for i := range want {
		if roots[i] != want[i] {
			t.Fatalf("roots[%d] = %q, want %q", i, roots[i], want[i])
		}
	}
}

func TestReadModuleResourceReadsDiskAndEmbeddedAssets(t *testing.T) {
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	tempDir := t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir tempdir: %v", err)
	}
	defer func() {
		_ = os.Chdir(oldWD)
	}()

	SetEmbeddedFS(nil)
	defer SetEmbeddedFS(nil)

	diskPath := filepath.Join(tempDir, "modules", "base", "views")
	if err := os.MkdirAll(diskPath, 0o755); err != nil {
		t.Fatalf("mkdir disk views: %v", err)
	}
	if err := os.WriteFile(filepath.Join(diskPath, "disk.xml"), []byte("<form/>"), 0o644); err != nil {
		t.Fatalf("write disk view: %v", err)
	}

	raw, err := ReadModuleResource("base/views/disk.xml")
	if err != nil {
		t.Fatalf("ReadModuleResource(disk) error = %v", err)
	}
	if string(raw) != "<form/>" {
		t.Fatalf("disk resource = %q, want <form/>", string(raw))
	}

	SetEmbeddedFS(fstest.MapFS{
		"base/static/description/icon.png": &fstest.MapFile{Data: []byte("png")},
	})
	raw, err = ReadModuleResource("base/static/description/icon.png")
	if err != nil {
		t.Fatalf("ReadModuleResource(embedded) error = %v", err)
	}
	if string(raw) != "png" {
		t.Fatalf("embedded resource = %q, want png", string(raw))
	}

	if _, err := ReadModuleResource("../etc/passwd"); err == nil {
		t.Fatalf("expected invalid resource path to fail")
	}
}

func TestListModuleResourceDirListsDiskAndEmbeddedFiles(t *testing.T) {
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	tempDir := t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir tempdir: %v", err)
	}
	defer func() {
		_ = os.Chdir(oldWD)
	}()

	SetEmbeddedFS(nil)
	defer SetEmbeddedFS(nil)

	diskPath := filepath.Join(tempDir, "modules", "website", "static", "js")
	if err := os.MkdirAll(diskPath, 0o755); err != nil {
		t.Fatalf("mkdir disk js dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(diskPath, "b.js"), []byte("b"), 0o644); err != nil {
		t.Fatalf("write disk js file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(diskPath, "a.js"), []byte("a"), 0o644); err != nil {
		t.Fatalf("write disk js file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(diskPath, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir nested dir: %v", err)
	}

	names, err := ListModuleResourceDir("website/static/js")
	if err != nil {
		t.Fatalf("ListModuleResourceDir(disk) error = %v", err)
	}
	if want := []string{"a.js", "b.js"}; !slices.Equal(names, want) {
		t.Fatalf("ListModuleResourceDir(disk) = %v, want %v (nested dirs must be excluded, names sorted)", names, want)
	}

	SetEmbeddedFS(fstest.MapFS{
		"other-module/static/js/embedded.js": &fstest.MapFile{Data: []byte("e")},
	})
	names, err = ListModuleResourceDir("other-module/static/js")
	if err != nil {
		t.Fatalf("ListModuleResourceDir(embedded) error = %v", err)
	}
	if want := []string{"embedded.js"}; !slices.Equal(names, want) {
		t.Fatalf("ListModuleResourceDir(embedded) = %v, want %v", names, want)
	}

	names, err = ListModuleResourceDir("website/static/js")
	if err != nil {
		t.Fatalf("ListModuleResourceDir(disk-precedence) error = %v", err)
	}
	if want := []string{"a.js", "b.js"}; !slices.Equal(names, want) {
		t.Fatalf("ListModuleResourceDir() with both disk and embedded present = %v, want disk-only %v", names, want)
	}
}

func TestDiscoverImportRootsFromFSPreservesStageOrder(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"base/manifest.md":       &fstest.MapFile{Data: []byte("---\nname: base\n---\n")},
		"base/security/.keep":    &fstest.MapFile{Data: []byte{}},
		"base/data/.keep":        &fstest.MapFile{Data: []byte{}},
		"base/views/.keep":       &fstest.MapFile{Data: []byte{}},
		"ignored/data/demo.json": &fstest.MapFile{Data: []byte("{}")},
	}

	roots, err := DiscoverImportRootsFromFS(fs.FS(fsys), ".")
	if err != nil {
		t.Fatalf("DiscoverImportRootsFromFS() error = %v", err)
	}

	want := []string{"base/security", "base/data", "base/views"}
	if len(roots) != len(want) {
		t.Fatalf("root count = %d, want %d (%v)", len(roots), len(want), roots)
	}
	for i := range want {
		if roots[i] != want[i] {
			t.Fatalf("roots[%d] = %q, want %q", i, roots[i], want[i])
		}
	}
}

func TestDiscoverManifestsFromFSSkipsGeneratedAllDirectory(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"all/modules_gen.go":     &fstest.MapFile{Data: []byte("package all")},
		"base/manifest.md":       &fstest.MapFile{Data: []byte("---\nname: base\n---\n")},
		"ignored/data/demo.json": &fstest.MapFile{Data: []byte("{}")},
	}

	manifests, err := DiscoverManifestsFromFS(fs.FS(fsys), ".")
	if err != nil {
		t.Fatalf("DiscoverManifestsFromFS() error = %v", err)
	}
	if len(manifests) != 1 {
		t.Fatalf("manifest count = %d, want 1 (%v)", len(manifests), manifests)
	}
	if manifests[0].Name != "base" {
		t.Fatalf("manifest name = %q, want base", manifests[0].Name)
	}
}

func TestParseManifestFromMarkdownNormalizesFrontendWidgets(t *testing.T) {
	t.Parallel()

	manifest, err := ParseManifestFromMarkdown(`---
name: demo_widget
version: 0.1.0
category: Hidden
installable: true
depends: [base]
auto_install: false
frontend_widgets:
  demo.signature_pad:
    js: static/widgets/signature-pad.js
    css:
      - static/widgets/signature-pad.css
    html: static/widgets/signature-pad.html
---`)
	if err != nil {
		t.Fatalf("ParseManifestFromMarkdown() error = %v", err)
	}

	widget, ok := manifest.FrontendWidgets["demo.signature_pad"]
	if !ok {
		t.Fatalf("expected frontend widget metadata to be parsed")
	}
	if widget.JS != "static/widgets/signature-pad.js" {
		t.Fatalf("widget.JS = %q", widget.JS)
	}
	if len(widget.CSS) != 1 || widget.CSS[0] != "static/widgets/signature-pad.css" {
		t.Fatalf("widget.CSS = %#v", widget.CSS)
	}
	if widget.HTML != "static/widgets/signature-pad.html" {
		t.Fatalf("widget.HTML = %q", widget.HTML)
	}
}
