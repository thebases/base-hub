package routes

// File overview:
// - Responsibility: coverage for SearchDocuments' filesystem fallback path (no *bun.DB is
//   registered in these tests, so orm.ResolveRegisteredDatabase always returns nil and every
//   call exercises knowledge_search_fallback.go, not Postgres FTS).
// - Key symbols: focused Test cases proving content-based ranking beats the old
//   filename-substring-only behavior FindDocuments still has.

import (
	"strings"
	"testing"
)

func TestSearchDocumentsMatchesContentNotJustName(t *testing.T) {
	opts := testMarkdownOptions(t)
	const dbName = "tenant-content-search"

	if _, err := CreateDocument(t.Context(), opts, dbName, "notes/alpha.md", "Alpha", "# Alpha\nThis document explains the payout reconciliation workflow.\n"); err != nil {
		t.Fatalf("CreateDocument(alpha) error: %v", err)
	}
	if _, err := CreateDocument(t.Context(), opts, dbName, "notes/beta.md", "Beta", "# Beta\nUnrelated content about frontend theming.\n"); err != nil {
		t.Fatalf("CreateDocument(beta) error: %v", err)
	}

	// "reconciliation" appears only in alpha's content, not in either
	// document's name/filename -- FindDocuments (name-only) would miss it.
	found, err := FindDocuments(opts, dbName, "reconciliation")
	if err != nil {
		t.Fatalf("FindDocuments error: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("FindDocuments(%q) = %d matches, want 0 (name-only search should miss a content-only term)", "reconciliation", len(found))
	}

	results, err := SearchDocuments(t.Context(), opts, dbName, "reconciliation")
	if err != nil {
		t.Fatalf("SearchDocuments error: %v", err)
	}
	if len(results) != 1 || !strings.HasSuffix(results[0].RelativePath, "alpha.md") {
		t.Fatalf("SearchDocuments(%q) = %+v, want exactly alpha.md", "reconciliation", results)
	}
}

func TestSearchDocumentsRanksNameMatchAboveContentOnlyMatch(t *testing.T) {
	opts := testMarkdownOptions(t)
	const dbName = "tenant-content-search-rank"

	if _, err := CreateDocument(t.Context(), opts, dbName, "notes/other.md", "Other", "Mentions payout once, in passing.\n"); err != nil {
		t.Fatalf("CreateDocument(other) error: %v", err)
	}
	if _, err := CreateDocument(t.Context(), opts, dbName, "notes/payout.md", "Payout Guide", "The main guide.\n"); err != nil {
		t.Fatalf("CreateDocument(payout) error: %v", err)
	}

	results, err := SearchDocuments(t.Context(), opts, dbName, "payout")
	if err != nil {
		t.Fatalf("SearchDocuments error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("SearchDocuments(%q) = %d matches, want 2", "payout", len(results))
	}
	if !strings.HasSuffix(results[0].RelativePath, "payout.md") {
		t.Fatalf("SearchDocuments(%q)[0] = %q, want the name-matching document ranked first", "payout", results[0].RelativePath)
	}
}

func TestSearchDocumentsRequiresQuery(t *testing.T) {
	opts := testMarkdownOptions(t)
	if _, err := SearchDocuments(t.Context(), opts, "tenant-empty-query", "   "); err == nil {
		t.Fatal("SearchDocuments(blank query) error = nil, want an error")
	}
}

func TestSearchDocumentsIndexUpdatesAfterDeleteAndRename(t *testing.T) {
	opts := testMarkdownOptions(t)
	const dbName = "tenant-content-search-mutations"

	createdPath, err := CreateDocument(t.Context(), opts, dbName, "notes/gamma.md", "Gamma", "Discusses idempotency keys at length.\n")
	if err != nil {
		t.Fatalf("CreateDocument(gamma) error: %v", err)
	}

	if results, err := SearchDocuments(t.Context(), opts, dbName, "idempotency"); err != nil || len(results) != 1 {
		t.Fatalf("SearchDocuments after create = (%v, %v), want 1 match", results, err)
	}

	renamedPath, err := RenameDocument(t.Context(), opts, dbName, createdPath, "notes/gamma-renamed.md")
	if err != nil {
		t.Fatalf("RenameDocument error: %v", err)
	}
	results, err := SearchDocuments(t.Context(), opts, dbName, "idempotency")
	if err != nil || len(results) != 1 || results[0].RelativePath != renamedPath {
		t.Fatalf("SearchDocuments after rename = (%+v, %v), want exactly %q", results, err, renamedPath)
	}

	if _, err := DeleteDocument(t.Context(), opts, dbName, renamedPath); err != nil {
		t.Fatalf("DeleteDocument error: %v", err)
	}
	results, err = SearchDocuments(t.Context(), opts, dbName, "idempotency")
	if err != nil {
		t.Fatalf("SearchDocuments after delete error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("SearchDocuments after delete = %+v, want no matches", results)
	}
}
