package routes

// File overview:
// - Responsibility: coverage for the exported service API in knowledge_service.go, the
//   non-Fiber entrypoints the base-mcp knowledge.* MCP tool group calls.
// - Key symbols: focused Test cases exercising ListDocuments/CreateDocument/UpdateDocument/
//   RenameDocument/DeleteDocument directly against a temp doc root.

import (
	"strings"
	"testing"
)

func testMarkdownOptions(t *testing.T) MarkdownRouteOptions {
	t.Helper()
	root := t.TempDir()
	return MarkdownRouteOptions{
		HomeDBName:      "home",
		PublicRootForDB: func(string) string { return root },
	}
}

func TestCreateReadUpdateRenameDeleteDocument(t *testing.T) {
	opts := testMarkdownOptions(t)
	const dbName = "tenant-a"

	createdPath, err := CreateDocument(t.Context(), opts, dbName, "notes/todo.md", "Todo", "# Todo\n")
	if err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}
	if createdPath != "public/notes/todo.md" {
		t.Fatalf("CreateDocument path = %q, want %q", createdPath, "public/notes/todo.md")
	}

	if _, err := CreateDocument(t.Context(), opts, dbName, "notes/todo.md", "Todo", "# Todo\n"); err == nil {
		t.Fatal("expected an error creating a document that already exists")
	}

	content, err := ReadDocument(opts, dbName, createdPath)
	if err != nil {
		t.Fatalf("ReadDocument: %v", err)
	}
	if string(content) != "# Todo\n" {
		t.Fatalf("ReadDocument content = %q, want %q", content, "# Todo\n")
	}

	updatedPath, err := UpdateDocument(t.Context(), opts, dbName, createdPath, "# Todo\n\n- [ ] item\n")
	if err != nil {
		t.Fatalf("UpdateDocument: %v", err)
	}
	content, err = ReadDocument(opts, dbName, updatedPath)
	if err != nil {
		t.Fatalf("ReadDocument after update: %v", err)
	}
	if !strings.Contains(string(content), "- [ ] item") {
		t.Fatalf("ReadDocument content after update = %q, missing expected line", content)
	}

	renamedPath, err := RenameDocument(t.Context(), opts, dbName, updatedPath, "notes/done.md")
	if err != nil {
		t.Fatalf("RenameDocument: %v", err)
	}
	if renamedPath != "public/notes/done.md" {
		t.Fatalf("RenameDocument path = %q, want %q", renamedPath, "public/notes/done.md")
	}
	if _, err := ReadDocument(opts, dbName, updatedPath); err == nil {
		t.Fatal("expected the old path to no longer exist after rename")
	}

	menu, err := ListDocuments(opts, dbName)
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	if len(menu.Items) != 1 || menu.Items[0].RelativePath != "public/notes/done.md" {
		t.Fatalf("ListDocuments items = %+v, want exactly user/notes/done.md", menu.Items)
	}

	deletedPath, err := DeleteDocument(t.Context(), opts, dbName, renamedPath)
	if err != nil {
		t.Fatalf("DeleteDocument: %v", err)
	}
	if deletedPath != renamedPath {
		t.Fatalf("DeleteDocument path = %q, want %q", deletedPath, renamedPath)
	}
	if _, err := ReadDocument(opts, dbName, renamedPath); err == nil {
		t.Fatal("expected the document to no longer exist after delete")
	}
}

func TestFindDocumentsMatchesNameOrKeywordCaseInsensitively(t *testing.T) {
	opts := testMarkdownOptions(t)
	const dbName = "tenant-a"

	if _, err := CreateDocument(t.Context(), opts, dbName, "guides/base-mcp-user-guide.md", "Base Mcp User Guide", "# Base MCP\n"); err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}
	if _, err := CreateDocument(t.Context(), opts, dbName, "guides/frontend-coding-guideline.md", "Frontend Coding Guideline", "# Frontend\n"); err != nil {
		t.Fatalf("CreateDocument: %v", err)
	}

	// Full display name, case-insensitive.
	byFullName, err := FindDocuments(opts, dbName, "base mcp user guide")
	if err != nil {
		t.Fatalf("FindDocuments: %v", err)
	}
	if len(byFullName) != 1 || byFullName[0].RelativePath != "public/guides/base-mcp-user-guide.md" {
		t.Fatalf("FindDocuments(full name) = %+v, want exactly the mcp guide", byFullName)
	}

	// Bare keyword substring, mixed case.
	byKeyword, err := FindDocuments(opts, dbName, "MCP")
	if err != nil {
		t.Fatalf("FindDocuments: %v", err)
	}
	if len(byKeyword) != 1 || byKeyword[0].RelativePath != "public/guides/base-mcp-user-guide.md" {
		t.Fatalf("FindDocuments(keyword) = %+v, want exactly the mcp guide", byKeyword)
	}

	// Keyword shared by both documents.
	byShared, err := FindDocuments(opts, dbName, "guide")
	if err != nil {
		t.Fatalf("FindDocuments: %v", err)
	}
	if len(byShared) != 2 {
		t.Fatalf("FindDocuments(shared keyword) = %+v, want both documents", byShared)
	}

	// No match.
	noMatch, err := FindDocuments(opts, dbName, "does-not-exist")
	if err != nil {
		t.Fatalf("FindDocuments: %v", err)
	}
	if len(noMatch) != 0 {
		t.Fatalf("FindDocuments(no match) = %+v, want empty", noMatch)
	}

	if _, err := FindDocuments(opts, dbName, "   "); err == nil {
		t.Fatal("expected an error for a blank query")
	}
}

func TestCreateDocumentRejectsReadOnlyNamespace(t *testing.T) {
	opts := testMarkdownOptions(t)

	if _, err := CreateDocument(t.Context(), opts, "tenant-a", "base/anything.md", "", "content"); err == nil {
		t.Fatal("expected an error creating a document under the read-only base namespace")
	}
	if _, err := CreateDocument(t.Context(), opts, "tenant-a", "user_guide/anything.md", "", "content"); err == nil {
		t.Fatal("expected an error creating a document under the read-only user_guide namespace")
	}
}
