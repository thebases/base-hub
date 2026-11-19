package routes

// File overview:
// - Responsibility: coverage for the model/method lookup-miss fallback pipeline in
//   mcp_route_fallback.go -- the knowledge-search hint and the debounced catalog reload.
// - Key symbols: focused Test cases exercising mcpResolveModel and
//   mcpTriggerCatalogReload directly, reusing fakeKnowledgeDocumentProvider from
//   mcp_tools_knowledge_test.go.

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"git.thebase.vn/base_hub/internal/mcpkit"
	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/orm"
)

// fakeKnowledgeDocumentProvider is a minimal in-memory
// kernelmodules.KnowledgeDocumentProvider standing in for the real knowledge
// module's KernelDocumentProvider, so these tests don't depend on
// modules/knowledge at all. Kept here (moved from the now-deleted
// mcp_tools_knowledge_test.go, per the mcp-module-scaffold plan's Phase 2)
// since mcpkit.KnowledgeHint's tests still need a fake provider to register
// against kernelmodules.RegisterKnowledgeDocumentProvider.
type fakeKnowledgeDocumentProvider struct {
	docs map[string]string // logical path -> content
}

func (p *fakeKnowledgeDocumentProvider) ListDocuments(string) ([]kernelmodules.KnowledgeDocument, error) {
	items := make([]kernelmodules.KnowledgeDocument, 0, len(p.docs))
	for path := range p.docs {
		items = append(items, kernelmodules.KnowledgeDocument{RelativePath: path})
	}
	return items, nil
}

func (p *fakeKnowledgeDocumentProvider) FindDocuments(_ string, query string) ([]kernelmodules.KnowledgeDocument, error) {
	needle := strings.ToLower(query)
	matches := make([]kernelmodules.KnowledgeDocument, 0)
	for path := range p.docs {
		if strings.Contains(strings.ToLower(path), needle) {
			matches = append(matches, kernelmodules.KnowledgeDocument{RelativePath: path})
		}
	}
	return matches, nil
}

func (p *fakeKnowledgeDocumentProvider) SearchDocuments(_ context.Context, dbName, query string) ([]kernelmodules.KnowledgeDocument, error) {
	return p.FindDocuments(dbName, query)
}

func (p *fakeKnowledgeDocumentProvider) ReadDocument(_ string, path string) ([]byte, error) {
	content, ok := p.docs[path]
	if !ok {
		return nil, errors.New("no such file or directory")
	}
	return []byte(content), nil
}

func (p *fakeKnowledgeDocumentProvider) CreateDocument(_ context.Context, _ string, path, _ string, content string) (string, error) {
	logicalPath := "user/" + path
	if _, exists := p.docs[logicalPath]; exists {
		return "", errors.New("markdown file already exists")
	}
	p.docs[logicalPath] = content
	return logicalPath, nil
}

func (p *fakeKnowledgeDocumentProvider) UpdateDocument(_ context.Context, _ string, path, content string) (string, error) {
	logicalPath := "user/" + path
	p.docs[logicalPath] = content
	return logicalPath, nil
}

func (p *fakeKnowledgeDocumentProvider) RenameDocument(_ context.Context, _ string, fromPath, toPath string) (string, error) {
	fromLogical, toLogical := "user/"+fromPath, "user/"+toPath
	content, ok := p.docs[fromLogical]
	if !ok {
		return "", errors.New("no such file or directory")
	}
	delete(p.docs, fromLogical)
	p.docs[toLogical] = content
	return toLogical, nil
}

func (p *fakeKnowledgeDocumentProvider) DeleteDocument(_ context.Context, _ string, path string) (string, error) {
	logicalPath := "user/" + path
	if _, ok := p.docs[logicalPath]; !ok {
		return "", errors.New("no such file or directory")
	}
	delete(p.docs, logicalPath)
	return logicalPath, nil
}

func TestMcpResolveModelUnknownHasNoHintWithoutKnowledge(t *testing.T) {
	kernelmodules.RegisterKnowledgeDocumentProvider(nil)

	_, err := mcpResolveModel(t.Context(), nil, "tbl.does_not_exist")
	if err == nil {
		t.Fatal("mcpResolveModel(unknown) error = nil, want model_not_found")
	}
	if !strings.Contains(err.Error(), "model_not_found") && !strings.Contains(err.Error(), "is not registered") {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(err.Error(), "see also") {
		t.Fatalf("unexpected knowledge hint with no provider registered: %v", err)
	}
}

func TestMcpResolveModelUnknownAttachesKnowledgeHintWhenAvailable(t *testing.T) {
	kernelmodules.RegisterKnowledgeDocumentProvider(&fakeKnowledgeDocumentProvider{
		docs: map[string]string{"user/widget-guide.md": "# Widget Guide\n"},
	})
	t.Cleanup(func() { kernelmodules.RegisterKnowledgeDocumentProvider(nil) })

	_, err := mcpResolveModel(t.Context(), nil, "widget")
	if err == nil {
		t.Fatal("mcpResolveModel(unknown) error = nil, want model_not_found")
	}
	if !strings.Contains(err.Error(), "see also") || !strings.Contains(err.Error(), "widget-guide.md") {
		t.Fatalf("expected a knowledge hint pointing at widget-guide.md, got: %v", err)
	}
}

func TestMcpTriggerCatalogReloadIsDebounced(t *testing.T) {
	mcpkit.ResetCatalogReloadDebounce()

	var calls int32
	orm.RegisterModuleCatalogSync(func(ctx context.Context) (string, int, error) {
		atomic.AddInt32(&calls, 1)
		return "test", 0, nil
	})
	t.Cleanup(func() { orm.RegisterModuleCatalogSync(nil) })

	mcpkit.TriggerCatalogReload(t.Context())
	mcpkit.TriggerCatalogReload(t.Context())

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("module catalog sync ran %d times for two reload triggers inside the debounce window, want 1", got)
	}
}
