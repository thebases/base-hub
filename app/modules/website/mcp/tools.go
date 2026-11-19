package mcp

// File overview:
// - Responsibility: the website.* MCP tool catalog (record CRUD restricted to
//   website.* models, SEO upsert, page design authoring, publish/unpublish).
// - Key symbols: Tools, websiteAllowedModels, websitePublishAllowedModels,
//   website*ModelName constants.
// - Moved verbatim (only the descriptor type changed, to
//   kernelmodules.MCPToolDescriptor) out of
//   modules/base-mcp/routes/mcp_tools_website.go, per the mcp-module-scaffold
//   plan's Phase 3. This is a pure relocation -- these model names were
//   already kept as local const literals rather than importing
//   modules/website/models (see the original file's header comment), so this
//   move needed no new decoupling work, unlike base-workflow's tool file.

import (
	kernelmodules "git.thebase.vn/base_hub/internal/modules"
)

// Runtime model names owned by modules/website. Kept as local literals
// instead of importing modules/website/models -- these are stable
// wire-level model identifiers resolved dynamically through the runtime
// model registry, not Go types, so a hook interface would add indirection
// for no benefit. (This package lives inside modules/website itself now, so
// importing modules/website/models directly would be fine too, but keeping
// these as literals avoids introducing a new coupling for zero benefit.)
const (
	websiteSiteModelName     = "tbl.website_site"
	websitePageModelName     = "tbl.website_page"
	websiteDomainModelName   = "tbl.website_domain"
	websiteAssetModelName    = "tbl.website_asset"
	websiteThemeModelName    = "tbl.website_theme"
	websiteSnippetModelName  = "tbl.website_snippet"
	websiteMenuModelName     = "tbl.website_menu"
	websiteRedirectModelName = "tbl.website_redirect"

	websiteTranslationModelName    = "tbl.website_page_translation"
	websiteFormSubmissionModelName = "tbl.website_form_submission"
)

// websiteAllowedModels is the fixed set of runtime models the website.*
// record/publish tools may target. An explicit allowlist (rather than a
// "website." name-prefix check) keeps the tool group precise even if an
// unrelated model is ever named similarly.
var websiteAllowedModels = map[string]bool{
	websiteSiteModelName:     true,
	websitePageModelName:     true,
	websiteDomainModelName:   true,
	websiteAssetModelName:    true,
	websiteThemeModelName:    true,
	websiteSnippetModelName:  true,
	websiteMenuModelName:     true,
	websiteRedirectModelName: true,

	websiteTranslationModelName:    true,
	websiteFormSubmissionModelName: true,
}

// websitePublishAllowedModels restricts website.publish/website.unpublish to
// the two models that actually declare action_publish/action_unpublish.
var websitePublishAllowedModels = map[string]bool{
	websiteSiteModelName: true,
	websitePageModelName: true,
}

// websiteSiteSEOFields and websitePageSEOFields are the only fields
// website.seo.upsert ever writes, one set per target. Site-level SEO lives
// directly on tbl.website_site (flat columns). Page-level SEO lives in
// tbl.website_page.Seo, a nested jsonb column matching the legacy editor's schema
// -- these flat MCP field names are the external tool contract and are
// translated to/from the nested shape by
// mcpNestedSEOFromFlatFields/mcpMergeWebsitePageSEO below.
var websiteSiteSEOFields = []string{
	"default_seo_name", "default_seo_desc", "default_canonical_base", "default_robots", "robots_txt", "sitemap_enabled",
}

var websitePageSEOFields = []string{
	"seo_title", "seo_description", "canonical_path", "robots_directives",
	"og_title", "og_description", "og_image_url", "structured_data_json", "include_in_sitemap",
}

// Tools is the website.* MCPToolDescriptor catalog, registered with
// base-mcp via Register (provider.go) so it's appended to tools/list only
// when the website module is installed for the caller's tenant DB.
func Tools() []kernelmodules.MCPToolDescriptor {
	websiteModelEnum := []string{
		websiteSiteModelName, websitePageModelName,
		websiteDomainModelName,
		websiteAssetModelName, websiteThemeModelName,
		websiteSnippetModelName,
		websiteMenuModelName, websiteRedirectModelName,
		websiteTranslationModelName, websiteFormSubmissionModelName,
	}
	recordModelProperty := map[string]any{"type": "string", "enum": websiteModelEnum}

	return []kernelmodules.MCPToolDescriptor{
		{
			Name:        "website.record.search_read",
			Description: "Search and read website module records (tbl.website_site, tbl.website_page, tbl.website_domain, tbl.website_asset, tbl.website_theme, tbl.website_snippet, tbl.website_menu, tbl.website_redirect), subject to ACL/record-rule/company scope. Same behavior as base.record.search_read, restricted to website.* models.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model":  recordModelProperty,
					"domain": map[string]any{"type": "array"},
					"fields": map[string]any{"type": "array"},
					"limit":  map[string]any{"type": "integer", "default": 20},
					"offset": map[string]any{"type": "integer"},
					"order":  map[string]any{"type": "string"},
				},
				"required": []string{"model"},
			},
		},
		{
			Name:        "website.record.read",
			Description: "Read specific website module records by id. Same behavior as base.record.read, restricted to website.* models.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model":  recordModelProperty,
					"ids":    map[string]any{"type": "array"},
					"fields": map[string]any{"type": "array"},
				},
				"required": []string{"model", "ids"},
			},
		},
		{
			Name:        "website.record.create",
			Description: "Create one website module record. Same behavior as base.record.create, restricted to website.* models. For creating a page with initial rendered HTML in one call, prefer website.page.design_upsert. idempotency_key is required: a stable caller-chosen string so a retried call after a timeout replays the original result instead of creating a duplicate record.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model":           recordModelProperty,
					"values":          map[string]any{"type": "object"},
					"idempotency_key": map[string]any{"type": "string", "description": "Required. A stable caller-chosen string that dedupes retried calls."},
				},
				"required": []string{"model", "values", "idempotency_key"},
			},
		},
		{
			Name:        "website.record.write",
			Description: "Update specific website module records by id. Same behavior as base.record.write (including the per-id batch values array form), restricted to website.* models. For SEO fields specifically, prefer website.seo.upsert (page-level SEO is a nested JSON object on tbl.website_page.seo, not flat columns).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model": recordModelProperty,
					"ids":   map[string]any{"type": "array"},
					"values": map[string]any{
						"description": "One object applied to every id, or an array of objects (one per id, same order) for a per-record batch write.",
						"oneOf": []map[string]any{
							{"type": "object"},
							{"type": "array", "items": map[string]any{"type": "object"}},
						},
					},
				},
				"required": []string{"model", "ids", "values"},
			},
		},
		{
			Name:        "website.record.unlink",
			Description: "Delete specific website module records by id. Same behavior as base.record.unlink, restricted to website.* models.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model": recordModelProperty,
					"ids":   map[string]any{"type": "array"},
				},
				"required": []string{"model", "ids"},
			},
		},
		{
			Name:        "website.seo.upsert",
			Description: "Upsert SEO fields for a tbl.website_site or a tbl.website_page. target=\"site\" writes directly onto the tbl.website_site record (site_id required): default_seo_name, default_seo_desc, default_canonical_base, default_robots, robots_txt, sitemap_enabled. target=\"page\" (page_id required) merges the given fields into tbl.website_page's nested seo JSON: seo_title, seo_description, canonical_path, robots_directives, og_title, og_description, og_image_url, structured_data_json, include_in_sitemap. Only fields you pass are changed.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target":                 map[string]any{"type": "string", "enum": []string{"site", "page"}},
					"site_id":                map[string]any{"type": "integer", "description": "Required when target is \"site\"."},
					"page_id":                map[string]any{"type": "integer", "description": "Required when target is \"page\"."},
					"default_seo_name":       map[string]any{"type": "string", "description": "target=site only."},
					"default_seo_desc":       map[string]any{"type": "string", "description": "target=site only."},
					"default_canonical_base": map[string]any{"type": "string", "description": "target=site only."},
					"default_robots":         map[string]any{"type": "string", "description": "target=site only."},
					"robots_txt":             map[string]any{"type": "string", "description": "target=site only."},
					"sitemap_enabled":        map[string]any{"type": "boolean", "description": "target=site only."},
					"seo_title":              map[string]any{"type": "string", "description": "target=page only."},
					"seo_description":        map[string]any{"type": "string", "description": "target=page only."},
					"canonical_path":         map[string]any{"type": "string", "description": "target=page only."},
					"robots_directives":      map[string]any{"type": "string", "description": "target=page only."},
					"og_title":               map[string]any{"type": "string", "description": "target=page only."},
					"og_description":         map[string]any{"type": "string", "description": "target=page only."},
					"og_image_url":           map[string]any{"type": "string", "description": "target=page only."},
					"structured_data_json":   map[string]any{"type": "string", "description": "target=page only."},
					"include_in_sitemap":     map[string]any{"type": "boolean", "description": "target=page only."},
				},
				"required": []string{"target"},
			},
		},
		{
			Name:        "website.page.design_upsert",
			Description: "One call to go from html to a live page: create a new page (omit page_id: requires site_id, name, and idempotency_key; route_path/is_homepage optional) or target an existing one (pass page_id, no idempotency_key needed since an update replaying is harmless), write html directly onto it, and immediately publish that page — no follow-up website.publish call needed. html is stored in body_html and sanitized on write: scripts, event handlers, <style> blocks, iframes, svg and unsafe URLs are removed, so style with classes (Bootstrap/BaseUI) and inline style attributes. Forms are kept without action/method (use data-bh-form). The returned page object reflects the publish (deploy_path set) and omits rendered_html/document, reporting their sizes instead as rendered_html_bytes/document_bytes, to keep the response small — call website.record.read on tbl.website_page if you need the content back. If publish fails after the draft was saved, the error says so explicitly — the draft write is not rolled back; retry with website.publish once the underlying issue is fixed. To take a page offline, use website.unpublish.\n\nLong AI-generated pages: instead of one giant html string, split it into pieces and send one piece per call using upload_id/html_chunk/chunk_index/chunk_total (0-based chunk_index, same chunk_total on every call for that upload_id). Every call before the last chunk (chunk_index < chunk_total-1) only buffers that piece server-side and returns {upload_id, chunk_index, chunk_total, chunks_received, complete: false} — it does not touch the page, so a chunked upload never publishes half-finished html. The call for the final chunk (chunk_index == chunk_total-1) assembles all chunks in order, then behaves exactly like a normal single-call html write/publish (page_id/site_id/name/route_path/is_homepage/idempotency_key all apply to that final call, not the earlier chunk calls). Chunks may arrive out of order or be retried — the same chunk_index just overwrites its slot. An upload_id not completed within a couple hours expires and must be restarted from chunk_index 0. Do not mix upload_id/html_chunk with a plain html argument in the same call.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"page_id":         map[string]any{"type": "integer", "description": "Omit to create a new page."},
					"site_id":         map[string]any{"type": "integer", "description": "Required when page_id is omitted."},
					"name":            map[string]any{"type": "string", "description": "Required when page_id is omitted."},
					"route_path":      map[string]any{"type": "string", "description": "Optional; derived from name when creating if omitted."},
					"is_homepage":     map[string]any{"type": "boolean"},
					"html":            map[string]any{"type": "string", "description": "The full designed page HTML, when sending it in one call; sanitized into body_html and, on publish, reparsed into the page's deployed Owl component scaffold. Omit this and use upload_id/html_chunk/chunk_index/chunk_total instead for a long page sent in pieces."},
					"idempotency_key": map[string]any{"type": "string", "description": "Required only when page_id is omitted (creating a new page) on the call that actually writes/publishes — not required on intermediate chunk calls. A stable caller-chosen string that dedupes retried calls so a client retry after a timeout cannot create a duplicate page."},
					"upload_id":       map[string]any{"type": "string", "description": "Chunked mode only: a stable id you choose for this one page-html upload session, the same value on every chunk call."},
					"html_chunk":      map[string]any{"type": "string", "description": "Chunked mode only: this call's piece of the html, in order."},
					"chunk_index":     map[string]any{"type": "integer", "description": "Chunked mode only: 0-based index of this piece."},
					"chunk_total":     map[string]any{"type": "integer", "description": "Chunked mode only: total number of pieces for this upload_id; same value on every call."},
				},
			},
		},
		{
			Name:        "website.snippet.list",
			Description: "List the builder blocks (snippets) available to the website builder: key, name, group, keywords, class and sanitized html for each. Optional group (exact) and search (name/group/keyword substring) filters. Read-only.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"group":  map[string]any{"type": "string", "description": "Exact group, e.g. intro, content, columns, text, images, people, catalog, social, contact."},
					"search": map[string]any{"type": "string"},
				},
			},
		},
		{
			Name:        "website.publish",
			Description: "Publish a tbl.website_site (all its pages) or a single tbl.website_page, building rendered content into a deployed static file. Equivalent to base.record.call_method with method=\"action_publish\", restricted to these two models.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model": map[string]any{"type": "string", "enum": []string{websiteSiteModelName, websitePageModelName}},
					"ids":   map[string]any{"type": "array"},
				},
				"required": []string{"model", "ids"},
			},
		},
		{
			Name:        "website.unpublish",
			Description: "Unpublish a tbl.website_site (all its pages) or a single tbl.website_page, removing its deployed static file. Equivalent to base.record.call_method with method=\"action_unpublish\", restricted to these two models.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model": map[string]any{"type": "string", "enum": []string{websiteSiteModelName, websitePageModelName}},
					"ids":   map[string]any{"type": "array"},
				},
				"required": []string{"model", "ids"},
			},
		},
	}
}
