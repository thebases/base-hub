package mcp

// File overview:
// - Responsibility: coverage for the knowledge.* MCP tool group's dispatch,
//   moved verbatim (mcp_route_fallback_test.go's `t.Context()` idiom, the
//   fake in-memory provider, and the argument-validation table) out of
//   modules/base-mcp/routes/mcp_tools_knowledge_test.go, per the
//   mcp-module-scaffold plan's Phase 2 -- proves the moved Dispatch/Tools
//   functions behave identically to the pre-move dispatchKnowledgeMCPTool/
//   knowledgeTools.

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/mcpkit"
	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/orm"
)

// testKnowledgeWriter passes the write gate (sudo) so the table below
// exercises argument validation and the provider round trip.
var testKnowledgeWriter = &orm.RBACIdentity{UserID: 1, Login: "admin", AuthMode: "oauth", IsSuperadmin: true}

// TestKnowledgeWriteToolsNeedWriter: write tools refuse anonymous callers
// (401) and authenticated callers outside base.group_system (403) before the
// provider is reached; read tools stay open to any caller.
func TestKnowledgeWriteToolsNeedWriter(t *testing.T) {
	provider := &fakeKnowledgeDocumentProvider{docs: map[string]string{"public/a.md": "# A\n"}}
	kernelmodules.RegisterKnowledgeDocumentProvider(provider)
	t.Cleanup(func() { kernelmodules.RegisterKnowledgeDocumentProvider(nil) })

	plainUser := &orm.RBACIdentity{UserID: 7, Login: "user", AuthMode: "oauth"}
	writes := []struct {
		tool      string
		arguments map[string]any
	}{
		{"knowledge.doc.create", map[string]any{"path": "notes/x.md", "content": "x"}},
		{"knowledge.doc.update", map[string]any{"path": "public/a.md", "content": "x"}},
		{"knowledge.doc.rename", map[string]any{"from_path": "public/a.md", "to_path": "public/b.md"}},
		{"knowledge.doc.delete", map[string]any{"path": "public/a.md"}},
	}
	for _, tc := range writes {
		for _, caller := range []struct {
			name     string
			identity *orm.RBACIdentity
			status   int
		}{{"anonymous", nil, 401}, {"plain user", plainUser, 403}} {
			_, err := Dispatch(t.Context(), caller.identity, tc.tool, tc.arguments)
			var toolErr *mcpkit.ToolError
			if !errors.As(err, &toolErr) || toolErr.Status != caller.status {
				t.Fatalf("%s as %s: err = %v, want tool error status %d", tc.tool, caller.name, err, caller.status)
			}
		}
	}
	if got := provider.docs["public/a.md"]; got != "# A\n" {
		t.Fatalf("document changed by a refused write: %q", got)
	}
	if _, err := Dispatch(t.Context(), plainUser, "knowledge.doc.read", map[string]any{"path": "public/a.md"}); err != nil {
		t.Fatalf("knowledge.doc.read as plain user: %v", err)
	}
}

// fakeKnowledgeDocumentProvider is a minimal in-memory
// kernelmodules.KnowledgeDocumentProvider so this package's tests have no
// dependency on modules/knowledge/routes' real filesystem-backed service.
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
		return nil, &fs.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
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
		return "", &fs.PathError{Op: "open", Path: fromPath, Err: fs.ErrNotExist}
	}
	delete(p.docs, fromLogical)
	p.docs[toLogical] = content
	return toLogical, nil
}

func (p *fakeKnowledgeDocumentProvider) DeleteDocument(_ context.Context, _ string, path string) (string, error) {
	logicalPath := "user/" + path
	if _, ok := p.docs[logicalPath]; !ok {
		return "", &fs.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
	}
	delete(p.docs, logicalPath)
	return logicalPath, nil
}

// TestKnowledgeToolsArgumentValidation exercises each knowledge.* tool's
// required-argument checks directly against Dispatch.
func TestKnowledgeToolsArgumentValidation(t *testing.T) {
	kernelmodules.RegisterKnowledgeDocumentProvider(&fakeKnowledgeDocumentProvider{docs: map[string]string{}})
	t.Cleanup(func() { kernelmodules.RegisterKnowledgeDocumentProvider(nil) })

	cases := []struct {
		tool      string
		arguments map[string]any
		wantErr   string
	}{
		{"knowledge.doc.find", map[string]any{}, "arguments.query is required"},
		{"knowledge.doc.read", map[string]any{}, "arguments.path is required"},
		{"knowledge.doc.create", map[string]any{}, "arguments.path is required"},
		{"knowledge.doc.update", map[string]any{}, "arguments.path is required"},
		{"knowledge.doc.delete", map[string]any{}, "arguments.path is required"},
		{"knowledge.doc.rename", map[string]any{"from_path": "user/a.md"}, "arguments.from_path and arguments.to_path are required"},
		{"knowledge.does_not_exist", map[string]any{}, "unknown mcp tool"},
	}
	for _, tc := range cases {
		if _, err := Dispatch(t.Context(), testKnowledgeWriter, tc.tool, tc.arguments); err == nil {
			t.Fatalf("%s: expected an error", tc.tool)
		} else if !strings.Contains(err.Error(), tc.wantErr) {
			t.Fatalf("%s: unexpected error: %v", tc.tool, err)
		}
	}

	created, err := Dispatch(t.Context(), testKnowledgeWriter, "knowledge.doc.create", map[string]any{
		"path": "notes/todo.md", "content": "# Todo\n",
	})
	if err != nil {
		t.Fatalf("knowledge.doc.create: %v", err)
	}
	createdMap, ok := created.(map[string]any)
	if !ok {
		t.Fatalf("knowledge.doc.create result type = %T, want map[string]any", created)
	}
	if createdMap["path"] != "user/notes/todo.md" {
		t.Fatalf("knowledge.doc.create path = %v, want user/notes/todo.md", createdMap["path"])
	}

	if _, err := Dispatch(t.Context(), testKnowledgeWriter, "knowledge.doc.read", map[string]any{"path": "user/notes/todo.md"}); err != nil {
		t.Fatalf("knowledge.doc.read: %v", err)
	}

	found, err := Dispatch(t.Context(), testKnowledgeWriter, "knowledge.doc.find", map[string]any{"query": "todo"})
	if err != nil {
		t.Fatalf("knowledge.doc.find: %v", err)
	}
	if paths := findResultPaths(t, found); len(paths) != 1 || paths[0] != "user/notes/todo.md" {
		t.Fatalf("knowledge.doc.find items = %v, want exactly user/notes/todo.md", paths)
	}

	noMatch, err := Dispatch(t.Context(), testKnowledgeWriter, "knowledge.doc.find", map[string]any{"query": "does-not-exist"})
	if err != nil {
		t.Fatalf("knowledge.doc.find (no match): %v", err)
	}
	if paths := findResultPaths(t, noMatch); len(paths) != 0 {
		t.Fatalf("knowledge.doc.find (no match) items = %v, want empty", paths)
	}
}

// findResultPaths extracts each item's RelativePath from a knowledge.doc.find
// result.
func findResultPaths(t *testing.T, result any) []string {
	t.Helper()
	foundMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("knowledge.doc.find result type = %T, want map[string]any", result)
	}
	items, ok := foundMap["items"].([]kernelmodules.KnowledgeDocument)
	if !ok {
		t.Fatalf("knowledge.doc.find items type = %T, want []kernelmodules.KnowledgeDocument", foundMap["items"])
	}
	paths := make([]string, len(items))
	for i, item := range items {
		paths[i] = item.RelativePath
	}
	return paths
}
