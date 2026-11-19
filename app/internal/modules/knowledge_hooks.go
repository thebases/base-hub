package modules

import "context"

// File overview:
// - Responsibility: lets the optional knowledge module supply its markdown
//   document service to other modules (base-mcp's knowledge.* MCP tool group)
//   without a compile-time import, so knowledge stays deletable/uninstallable
//   like any other optional module.
// - Key symbols: KnowledgeDocument, KnowledgeDocumentProvider,
//   RegisterKnowledgeDocumentProvider, KnowledgeDocumentProviderHooks.
// - Pattern: Mirrors the DatabaseProvisioning/ModuleDataRunner hook idiom in
//   database_hooks.go/lifecycle_hooks.go.

// KnowledgeDocument mirrors one markdown menu entry (name, logical path,
// namespace/source, and whether the knowledge module treats it as read-only)
// without callers needing knowledge's own unexported menu-item type.
type KnowledgeDocument struct {
	Name         string
	RelativePath string
	Source       string
	ReadOnly     bool
}

// KnowledgeDocumentProvider is implemented by the knowledge module (if
// installed) so other modules can list/read/create/update/rename/delete
// markdown documents without importing modules/knowledge directly.
type KnowledgeDocumentProvider interface {
	ListDocuments(dbName string) ([]KnowledgeDocument, error)
	FindDocuments(dbName, query string) ([]KnowledgeDocument, error)
	// SearchDocuments ranks documents by content relevance to query (Postgres
	// full-text search when the tenant DB supports it, a filesystem index
	// otherwise) -- unlike FindDocuments, which only matches document names.
	SearchDocuments(ctx context.Context, dbName, query string) ([]KnowledgeDocument, error)
	ReadDocument(dbName, path string) ([]byte, error)
	CreateDocument(ctx context.Context, dbName, path, name, content string) (string, error)
	UpdateDocument(ctx context.Context, dbName, path, content string) (string, error)
	RenameDocument(ctx context.Context, dbName, fromPath, toPath string) (string, error)
	DeleteDocument(ctx context.Context, dbName, path string) (string, error)
}

var knowledgeDocumentProviderImpl KnowledgeDocumentProvider

// RegisterKnowledgeDocumentProvider lets the knowledge module's RegisterRoutes
// supply its document-service implementation once it has bootstrapped.
func RegisterKnowledgeDocumentProvider(impl KnowledgeDocumentProvider) {
	knowledgeDocumentProviderImpl = impl
}

// KnowledgeDocumentProviderHooks returns the registered implementation. ok is
// false when the knowledge module hasn't bootstrapped (or isn't compiled in),
// so callers can fail closed instead of panicking.
func KnowledgeDocumentProviderHooks() (KnowledgeDocumentProvider, bool) {
	return knowledgeDocumentProviderImpl, knowledgeDocumentProviderImpl != nil
}
