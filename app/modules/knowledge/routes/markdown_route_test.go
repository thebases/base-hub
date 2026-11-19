package routes

// File overview:
// - Responsibility: verifies HTTP and RPC transport behavior for markdown route.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down request parsing and auth/response contracts so frontend and integration clients keep seeing stable transport behavior.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestEnsureMarkdownMenuSnapshotWritesMenuJSON(t *testing.T) {
	root := t.TempDir()

	mustWriteMarkdownFile(t, root, "bank/get_list.md", "# Bank list\n")
	mustWriteMarkdownFile(t, root, "api.md", "# API\n")

	payload, err := ensureMarkdownMenuSnapshot(root)
	if err != nil {
		t.Fatalf("ensureMarkdownMenuSnapshot returned error: %v", err)
	}

	if len(payload.Items) != 2 {
		t.Fatalf("expected 2 menu items, got %d", len(payload.Items))
	}

	menuPath := filepath.Join(root, markdownMenuFileName)
	raw, err := os.ReadFile(menuPath)
	if err != nil {
		t.Fatalf("expected generated menu snapshot at %s: %v", menuPath, err)
	}

	var persisted markdownMenuResponse
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatalf("expected valid menu json: %v", err)
	}

	if len(persisted.Items) != 2 {
		t.Fatalf("expected persisted menu to contain 2 items, got %d", len(persisted.Items))
	}

	if persisted.Items[0].RelativePath != "api.md" {
		t.Fatalf("expected sorted menu snapshot, first item=%q", persisted.Items[0].RelativePath)
	}

	if persisted.Items[1].RelativePath != "bank/get_list.md" {
		t.Fatalf("unexpected second item path %q", persisted.Items[1].RelativePath)
	}
}

func TestEnsureMarkdownMenuSnapshotHandlesMissingRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "documents")

	payload, err := ensureMarkdownMenuSnapshot(root)
	if err != nil {
		t.Fatalf("ensureMarkdownMenuSnapshot returned error: %v", err)
	}

	if len(payload.Items) != 0 {
		t.Fatalf("expected empty menu for missing root, got %d items", len(payload.Items))
	}

	if _, err := os.Stat(filepath.Join(root, markdownMenuFileName)); err != nil {
		t.Fatalf("expected empty menu snapshot file to be created: %v", err)
	}
}

func TestEnsureMarkdownMenuSnapshotPreservesCustomLabels(t *testing.T) {
	root := t.TempDir()

	mustWriteMarkdownFile(t, root, "docs/api.md", "# API\n")

	initial := markdownMenuResponse{
		Items: []markdownMenuItem{
			{
				Name:         "Custom API Label",
				RelativePath: "docs/api.md",
			},
		},
	}
	if err := writeMarkdownMenuSnapshot(root, initial); err != nil {
		t.Fatalf("writeMarkdownMenuSnapshot returned error: %v", err)
	}

	payload, err := ensureMarkdownMenuSnapshot(root)
	if err != nil {
		t.Fatalf("ensureMarkdownMenuSnapshot returned error: %v", err)
	}

	if len(payload.Items) != 1 {
		t.Fatalf("expected 1 menu item, got %d", len(payload.Items))
	}
	if payload.Items[0].Name != "Custom API Label" {
		t.Fatalf("expected preserved custom label, got %q", payload.Items[0].Name)
	}
}

func TestApplyMarkdownMenuLabelUpdatesGeneratedSnapshot(t *testing.T) {
	root := t.TempDir()

	mustWriteMarkdownFile(t, root, "docs/new-file.md", "# Title\n")

	payload, err := ensureMarkdownMenuSnapshot(root)
	if err != nil {
		t.Fatalf("ensureMarkdownMenuSnapshot returned error: %v", err)
	}

	if err := applyMarkdownMenuLabel(root, payload, "docs/new-file.md", "Shown In Menu"); err != nil {
		t.Fatalf("applyMarkdownMenuLabel returned error: %v", err)
	}

	persisted, err := readMarkdownMenuSnapshot(root)
	if err != nil {
		t.Fatalf("readMarkdownMenuSnapshot returned error: %v", err)
	}

	if len(persisted.Items) != 1 {
		t.Fatalf("expected 1 persisted item, got %d", len(persisted.Items))
	}
	if persisted.Items[0].Name != "Shown In Menu" {
		t.Fatalf("expected custom label to persist, got %q", persisted.Items[0].Name)
	}
}

func TestListEmbeddedMarkdownMenuItemsUsesEmbeddedLabelsAndReadonlyNamespace(t *testing.T) {
	baseFS := fstest.MapFS{
		"guide.md":      &fstest.MapFile{Data: []byte("# Guide\n")},
		"nested/api.md": &fstest.MapFile{Data: []byte("# API\n")},
		"menu.json":     &fstest.MapFile{Data: []byte(`{"items":[{"name":"API Guide","relative_path":"nested/api.md"}]}`)},
	}

	items, err := listEmbeddedMarkdownMenuItems(baseFS, markdownBaseNamespace)
	if err != nil {
		t.Fatalf("listEmbeddedMarkdownMenuItems returned error: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	if items[0].RelativePath != "base/guide.md" {
		t.Fatalf("unexpected first logical path %q", items[0].RelativePath)
	}
	if items[0].Source != markdownBaseNamespace || !items[0].ReadOnly {
		t.Fatalf("expected embedded item metadata to mark base readonly, got source=%q readonly=%v", items[0].Source, items[0].ReadOnly)
	}

	if items[1].RelativePath != "base/nested/api.md" {
		t.Fatalf("unexpected second logical path %q", items[1].RelativePath)
	}
	if items[1].Name != "API Guide" {
		t.Fatalf("expected embedded menu label override, got %q", items[1].Name)
	}
}

func TestBuildMarkdownMenuCombinesBaseUserGuideAndPublicItems(t *testing.T) {
	baseFS := fstest.MapFS{
		"guide.md": &fstest.MapFile{Data: []byte("# Guide\n")},
	}
	userGuideFS := fstest.MapFS{
		"onboarding.md": &fstest.MapFile{Data: []byte("# Onboarding\n")},
	}
	publicRoot := t.TempDir()
	mustWriteMarkdownFile(t, publicRoot, "notes/today.md", "# Today\n")

	payload, err := buildMarkdownMenu(baseFS, userGuideFS, publicRoot, true)
	if err != nil {
		t.Fatalf("buildMarkdownMenu returned error: %v", err)
	}

	if len(payload.Items) != 3 {
		t.Fatalf("expected 3 combined items, got %d", len(payload.Items))
	}

	if payload.Items[0].RelativePath != "base/guide.md" {
		t.Fatalf("unexpected first combined path %q", payload.Items[0].RelativePath)
	}
	if payload.Items[1].RelativePath != "public/notes/today.md" {
		t.Fatalf("unexpected second combined path %q", payload.Items[1].RelativePath)
	}
	if payload.Items[1].Source != markdownPublicNamespace || payload.Items[1].ReadOnly {
		t.Fatalf("expected public item metadata to be editable, got source=%q readonly=%v", payload.Items[1].Source, payload.Items[1].ReadOnly)
	}
	if payload.Items[2].RelativePath != "user_guide/onboarding.md" {
		t.Fatalf("unexpected third combined path %q", payload.Items[2].RelativePath)
	}
	if payload.Items[2].Source != markdownUserGuideNamespace || !payload.Items[2].ReadOnly {
		t.Fatalf("expected user_guide item metadata to be readonly, got source=%q readonly=%v", payload.Items[2].Source, payload.Items[2].ReadOnly)
	}
}

func TestBuildMarkdownMenuHidesBaseForNonHomeTenant(t *testing.T) {
	baseFS := fstest.MapFS{
		"guide.md": &fstest.MapFile{Data: []byte("# Guide\n")},
	}
	userGuideFS := fstest.MapFS{
		"onboarding.md": &fstest.MapFile{Data: []byte("# Onboarding\n")},
	}
	publicRoot := t.TempDir()

	payload, err := buildMarkdownMenu(baseFS, userGuideFS, publicRoot, false)
	if err != nil {
		t.Fatalf("buildMarkdownMenu returned error: %v", err)
	}

	if len(payload.Items) != 1 {
		t.Fatalf("expected only the shared user_guide item for a non-Home tenant, got %d items", len(payload.Items))
	}
	if payload.Items[0].RelativePath != "user_guide/onboarding.md" {
		t.Fatalf("unexpected item path %q", payload.Items[0].RelativePath)
	}
}

func TestResolvePublicMarkdownPathRejectsBaseNamespace(t *testing.T) {
	root := t.TempDir()

	if _, _, err := resolvePublicMarkdownPath(root, "base/guide.md"); err == nil {
		t.Fatal("expected base namespace to be rejected for public mutations")
	}

	absolutePath, relativePath, err := resolvePublicMarkdownPath(root, "notes/guide.md")
	if err != nil {
		t.Fatalf("resolvePublicMarkdownPath returned error: %v", err)
	}
	if relativePath != "notes/guide.md" {
		t.Fatalf("unexpected relative path %q", relativePath)
	}
	expectedAbsolutePath := filepath.Join(root, "notes", "guide.md")
	if absolutePath != expectedAbsolutePath {
		t.Fatalf("unexpected absolute path %q", absolutePath)
	}
}

func TestReadMarkdownDocumentFallsBackFromPublicToBase(t *testing.T) {
	publicRoot := t.TempDir()
	baseFS := fstest.MapFS{
		"shared.md": &fstest.MapFile{Data: []byte("# Base\n")},
	}

	content, err := readMarkdownDocument(baseFS, nil, publicRoot, "shared.md", true)
	if err != nil {
		t.Fatalf("readMarkdownDocument returned error: %v", err)
	}
	if string(content) != "# Base\n" {
		t.Fatalf("unexpected base fallback content %q", string(content))
	}

	mustWriteMarkdownFile(t, publicRoot, "shared.md", "# Public\n")
	content, err = readMarkdownDocument(baseFS, nil, publicRoot, "shared.md", true)
	if err != nil {
		t.Fatalf("readMarkdownDocument returned error after public write: %v", err)
	}
	if string(content) != "# Public\n" {
		t.Fatalf("expected public document to override base fallback, got %q", string(content))
	}
}

func TestReadMarkdownDocumentHidesBaseForNonHomeTenant(t *testing.T) {
	publicRoot := t.TempDir()
	baseFS := fstest.MapFS{
		"shared.md": &fstest.MapFile{Data: []byte("# Base\n")},
	}

	if _, err := readMarkdownDocument(baseFS, nil, publicRoot, "base/shared.md", false); err == nil {
		t.Fatal("expected base namespace to be hidden for a non-Home tenant")
	}
}

func TestReadMarkdownDocumentServesUserGuideForEveryTenant(t *testing.T) {
	publicRoot := t.TempDir()
	userGuideFS := fstest.MapFS{
		"onboarding.md": &fstest.MapFile{Data: []byte("# Onboarding\n")},
	}

	content, err := readMarkdownDocument(nil, userGuideFS, publicRoot, "user_guide/onboarding.md", false)
	if err != nil {
		t.Fatalf("readMarkdownDocument returned error: %v", err)
	}
	if string(content) != "# Onboarding\n" {
		t.Fatalf("unexpected user_guide content %q", string(content))
	}
}

func TestResolvePublicMarkdownPathRejectsUserGuideNamespace(t *testing.T) {
	root := t.TempDir()

	if _, _, err := resolvePublicMarkdownPath(root, "user_guide/onboarding.md"); err == nil {
		t.Fatal("expected user_guide namespace to be rejected for public mutations")
	}
}

func mustWriteMarkdownFile(t *testing.T, root, relativePath, content string) {
	t.Helper()

	absolutePath := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(absolutePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}
}
