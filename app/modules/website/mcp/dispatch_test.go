package mcp

// File overview:
// - Responsibility: argument-validation coverage for the website.* MCP tool
//   group, moved verbatim (dispatchMCPTool(...) calls become direct
//   Dispatch(...) calls, since the tool group itself now lives in this
//   package rather than being reached through base-mcp's generic dispatcher)
//   out of modules/base-mcp/routes/mcp_tools_website_test.go, per the
//   mcp-module-scaffold plan's Phase 3.

import (
	"strings"
	"testing"
)

// TestWebsiteRecordDispatchRejectsUnknownModel covers requireWebsiteAllowedModel:
// website.record.* must refuse any model outside the fixed website.* set,
// even one that would otherwise be a valid base.record.* target.
func TestWebsiteRecordDispatchRejectsUnknownModel(t *testing.T) {
	t.Parallel()

	for _, tool := range []string{
		"website.record.search_read", "website.record.read",
		"website.record.create", "website.record.write", "website.record.unlink",
	} {
		if _, err := Dispatch(t.Context(), nil, tool, map[string]any{"model": "tbl.partner"}); err == nil {
			t.Fatalf("%s: expected an error for a non-website model", tool)
		} else if !strings.Contains(err.Error(), "is not one of the website.* models") {
			t.Fatalf("%s: unexpected error: %v", tool, err)
		}
	}

	if _, err := Dispatch(t.Context(), nil, "website.record.search_read", map[string]any{}); err == nil {
		t.Fatal("expected an error when arguments.model is missing")
	} else if !strings.Contains(err.Error(), "arguments.model is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestWebsiteRecordDispatchRejectsRemovedVersionModel proves website.page.version
// (the removed checkpoint/version-history model) is no longer a valid
// website.record.* target now that pages only have a draft (the live row)
// and a published state (deploy_path) -- no separate version table.
func TestWebsiteRecordDispatchRejectsRemovedVersionModel(t *testing.T) {
	t.Parallel()

	if _, err := Dispatch(t.Context(), nil, "website.record.search_read", map[string]any{"model": "website.page.version"}); err == nil {
		t.Fatal("expected an error for the removed website.page.version model")
	} else if !strings.Contains(err.Error(), "is not one of the website.* models") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestWebsiteSEOUpsertValidation covers mcpWebsiteSEOUpsert's target
// dispatch and required-argument checks, all reachable before any DB call.
func TestWebsiteSEOUpsertValidation(t *testing.T) {
	t.Parallel()

	if _, err := Dispatch(t.Context(), nil, "website.seo.upsert", map[string]any{}); err == nil {
		t.Fatal("expected an error when arguments.target is missing")
	} else if !strings.Contains(err.Error(), "arguments.target is required") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := Dispatch(t.Context(), nil, "website.seo.upsert", map[string]any{"target": "sitemap"}); err == nil {
		t.Fatal("expected an error for an unrecognized target")
	} else if !strings.Contains(err.Error(), `must be "site" or "page"`) {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := Dispatch(t.Context(), nil, "website.seo.upsert", map[string]any{"target": "site"}); err == nil {
		t.Fatal("expected an error when target=site is missing site_id")
	} else if !strings.Contains(err.Error(), "arguments.site_id is required") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := Dispatch(t.Context(), nil, "website.seo.upsert", map[string]any{"target": "site", "site_id": float64(1)}); err == nil {
		t.Fatal("expected an error when no SEO field is provided")
	} else if !strings.Contains(err.Error(), "at least one SEO field is required") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := Dispatch(t.Context(), nil, "website.seo.upsert", map[string]any{"target": "page"}); err == nil {
		t.Fatal("expected an error when target=page is missing page_id")
	} else if !strings.Contains(err.Error(), "arguments.page_id is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestWebsitePageDesignUpsertValidation covers mcpWebsitePageDesignUpsert's
// required-argument checks for both the create and update branches.
func TestWebsitePageDesignUpsertValidation(t *testing.T) {
	t.Parallel()

	if _, err := Dispatch(t.Context(), nil, "website.page.design_upsert", map[string]any{}); err == nil {
		t.Fatal("expected an error when arguments.html is missing")
	} else if !strings.Contains(err.Error(), "arguments.html is required") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := Dispatch(t.Context(), nil, "website.page.design_upsert", map[string]any{
		"html": "<p>hi</p>",
	}); err == nil {
		t.Fatal("expected an error when creating a page without site_id")
	} else if !strings.Contains(err.Error(), "arguments.site_id is required") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := Dispatch(t.Context(), nil, "website.page.design_upsert", map[string]any{
		"html": "<p>hi</p>", "site_id": float64(1),
	}); err == nil {
		t.Fatal("expected an error when creating a page without name")
	} else if !strings.Contains(err.Error(), "arguments.name is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestWebsitePageDesignUpsertChunkArgValidation covers mcpParseHTMLChunkArgs'
// argument checks for the multi-chunk html upload mode, all reachable
// (Dispatch -> mcpWebsitePageDesignUpsert -> mcpParseHTMLChunkArgs) before
// any DB call, same DB-free posture as the rest of this file.
func TestWebsitePageDesignUpsertChunkArgValidation(t *testing.T) {
	t.Parallel()

	if _, err := Dispatch(t.Context(), nil, "website.page.design_upsert", map[string]any{
		"upload_id": "u1",
	}); err == nil {
		t.Fatal("expected an error when upload_id is set but html_chunk is missing")
	} else if !strings.Contains(err.Error(), "arguments.html_chunk is required") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := Dispatch(t.Context(), nil, "website.page.design_upsert", map[string]any{
		"upload_id": "u1", "html_chunk": float64(1),
	}); err == nil {
		t.Fatal("expected an error when html_chunk is not a string")
	} else if !strings.Contains(err.Error(), "arguments.html_chunk must be a string") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := Dispatch(t.Context(), nil, "website.page.design_upsert", map[string]any{
		"upload_id": "u1", "html_chunk": "<p>",
	}); err == nil {
		t.Fatal("expected an error when chunk_total is missing")
	} else if !strings.Contains(err.Error(), "arguments.chunk_total is required") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := Dispatch(t.Context(), nil, "website.page.design_upsert", map[string]any{
		"upload_id": "u1", "html_chunk": "<p>", "chunk_total": float64(0),
	}); err == nil {
		t.Fatal("expected an error when chunk_total is not positive")
	} else if !strings.Contains(err.Error(), "arguments.chunk_total is required") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := Dispatch(t.Context(), nil, "website.page.design_upsert", map[string]any{
		"upload_id": "u1", "html_chunk": "<p>", "chunk_total": float64(3),
	}); err == nil {
		t.Fatal("expected an error when chunk_index is missing")
	} else if !strings.Contains(err.Error(), "arguments.chunk_index is required") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := Dispatch(t.Context(), nil, "website.page.design_upsert", map[string]any{
		"upload_id": "u1", "html_chunk": "<p>", "chunk_total": float64(3), "chunk_index": float64(3),
	}); err == nil {
		t.Fatal("expected an error when chunk_index is out of range")
	} else if !strings.Contains(err.Error(), "arguments.chunk_index is required and must be between 0 and chunk_total-1") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestWebsitePublishValidation covers mcpWebsitePublish's model allowlist and
// required-ids checks, shared by website.publish and website.unpublish.
func TestWebsitePublishValidation(t *testing.T) {
	t.Parallel()

	for _, tool := range []string{"website.publish", "website.unpublish"} {
		if _, err := Dispatch(t.Context(), nil, tool, map[string]any{"model": "tbl.website_domain", "ids": []any{float64(1)}}); err == nil {
			t.Fatalf("%s: expected an error for a model without action_publish/action_unpublish", tool)
		} else if !strings.Contains(err.Error(), "arguments.model must be") {
			t.Fatalf("%s: unexpected error: %v", tool, err)
		}

		if _, err := Dispatch(t.Context(), nil, tool, map[string]any{"model": websitePageModelName}); err == nil {
			t.Fatalf("%s: expected an error when arguments.ids is missing", tool)
		} else if !strings.Contains(err.Error(), "arguments.ids is required") {
			t.Fatalf("%s: unexpected error: %v", tool, err)
		}
	}
}

// TestWebsiteToolsUnknownName covers Dispatch's default case.
func TestWebsiteToolsUnknownName(t *testing.T) {
	t.Parallel()

	if _, err := Dispatch(t.Context(), nil, "website.does_not_exist", map[string]any{}); err == nil {
		t.Fatal("expected an error for an unknown website.* tool name")
	} else if !strings.Contains(err.Error(), "unknown mcp tool") {
		t.Fatalf("unexpected error: %v", err)
	}
}
