package routes

// File overview:
// - Responsibility: adapts this package's fiber-free document service
//   (knowledge_service.go) to internal/modules.KnowledgeDocumentProvider so
//   other modules (base-mcp) can consume it without importing this package
//   directly.
// - Key symbols: KernelDocumentProvider, NewKernelDocumentProvider.
// - Pattern: thin translation layer only -- no document/readonly-path logic
//   is reimplemented here, it all still lives in knowledge_service.go and
//   markdown_route.go.

import (
	"context"

	kernelmodules "git.thebase.vn/base_hub/internal/modules"
)

// KernelDocumentProvider implements kernelmodules.KnowledgeDocumentProvider
// over a fixed MarkdownRouteOptions, so other modules can reach this
// package's document service through the kernel hook instead of importing
// modules/knowledge/routes.
type KernelDocumentProvider struct {
	options MarkdownRouteOptions
}

// NewKernelDocumentProvider builds the kernel-facing adapter for options, the
// same MarkdownRouteOptions the HTTP /md routes were registered with.
func NewKernelDocumentProvider(options MarkdownRouteOptions) KernelDocumentProvider {
	return KernelDocumentProvider{options: options}
}

func toKernelDocuments(items []markdownMenuItem) []kernelmodules.KnowledgeDocument {
	documents := make([]kernelmodules.KnowledgeDocument, 0, len(items))
	for _, item := range items {
		documents = append(documents, kernelmodules.KnowledgeDocument{
			Name:         item.Name,
			RelativePath: item.RelativePath,
			Source:       item.Source,
			ReadOnly:     item.ReadOnly,
		})
	}
	return documents
}

func (p KernelDocumentProvider) ListDocuments(dbName string) ([]kernelmodules.KnowledgeDocument, error) {
	menu, err := ListDocuments(p.options, dbName)
	if err != nil {
		return nil, err
	}
	return toKernelDocuments(menu.Items), nil
}

func (p KernelDocumentProvider) FindDocuments(dbName, query string) ([]kernelmodules.KnowledgeDocument, error) {
	items, err := FindDocuments(p.options, dbName, query)
	if err != nil {
		return nil, err
	}
	return toKernelDocuments(items), nil
}

func (p KernelDocumentProvider) SearchDocuments(ctx context.Context, dbName, query string) ([]kernelmodules.KnowledgeDocument, error) {
	items, err := SearchDocuments(ctx, p.options, dbName, query)
	if err != nil {
		return nil, err
	}
	return toKernelDocuments(items), nil
}

func (p KernelDocumentProvider) ReadDocument(dbName, path string) ([]byte, error) {
	return ReadDocument(p.options, dbName, path)
}

func (p KernelDocumentProvider) CreateDocument(ctx context.Context, dbName, path, name, content string) (string, error) {
	return CreateDocument(ctx, p.options, dbName, path, name, content)
}

func (p KernelDocumentProvider) UpdateDocument(ctx context.Context, dbName, path, content string) (string, error) {
	return UpdateDocument(ctx, p.options, dbName, path, content)
}

func (p KernelDocumentProvider) RenameDocument(ctx context.Context, dbName, fromPath, toPath string) (string, error) {
	return RenameDocument(ctx, p.options, dbName, fromPath, toPath)
}

func (p KernelDocumentProvider) DeleteDocument(ctx context.Context, dbName, path string) (string, error) {
	return DeleteDocument(ctx, p.options, dbName, path)
}
