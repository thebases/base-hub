package mcp

import (
	"strings"
	"testing"
)

func TestRecordDispatchRejectsForeignModel(t *testing.T) {
	t.Parallel()
	for _, tool := range []string{"search_read", "read", "create", "write", "unlink"} {
		_, err := Dispatch(t.Context(), nil, "website_blog.record."+tool, map[string]any{"model": "tbl.website_page"})
		if err == nil || !strings.Contains(err.Error(), "not one of the website_blog models") {
			t.Fatalf("%s: unexpected error %v", tool, err)
		}
	}
	if _, err := Dispatch(t.Context(), nil, "website_blog.record.read", map[string]any{}); err == nil || !strings.Contains(err.Error(), "arguments.model is required") {
		t.Fatalf("missing model error = %v", err)
	}
}

func TestPostActionRequiresExactlyOneID(t *testing.T) {
	t.Parallel()
	for tool := range postActionTools {
		if _, err := Dispatch(t.Context(), nil, tool, map[string]any{"ids": []any{1, 2}}); err == nil || !strings.Contains(err.Error(), "exactly one") {
			t.Fatalf("%s: unexpected error %v", tool, err)
		}
	}
}

func TestUnknownToolAndCatalogMatchesDispatch(t *testing.T) {
	t.Parallel()
	if _, err := Dispatch(t.Context(), nil, "website_blog.nope", nil); err == nil {
		t.Fatal("expected tool_not_found")
	}
	for _, tool := range Tools() {
		if _, err := Dispatch(t.Context(), nil, tool.Name, map[string]any{}); err != nil && strings.Contains(err.Error(), "unknown mcp tool") {
			t.Fatalf("catalog tool %s has no dispatch case", tool.Name)
		}
	}
}
