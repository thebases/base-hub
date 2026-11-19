package mcp

// File overview:
// - Responsibility: dispatches one website.* MCP tool call (CRUD, SEO
//   upsert, page design authoring, publish/unpublish).
// - Key symbols: Dispatch.
// - Moved verbatim out of modules/base-mcp/routes/mcp_tools_website.go, per
//   the mcp-module-scaffold plan's Phase 3 -- every helper now calls
//   internal/mcpkit directly (RecordDispatch/ExecuteRuntimeMethod/
//   LookupRuntimeMethod/GetIdempotencyStore/etc.) instead of this package's
//   former private helpers of the same name, so ACL/record-rule/company-scope
//   enforcement and idempotency-store behavior stay byte-for-byte identical.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/mcpkit"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// Dispatch routes one website.* MCP tool call. Registered as this
// provider's Dispatch func (see provider.go) -- base-mcp's provider cache
// already confirmed the website module is installed for the caller's tenant
// DB before ever reaching here.
func Dispatch(ctx context.Context, identity *orm.RBACIdentity, toolName string, arguments map[string]any) (any, error) {
	switch toolName {
	case "website.record.search_read":
		return websiteRecordDispatch(ctx, identity, arguments, "search_read", mcpkit.RecordModeSearch)
	case "website.record.read":
		return websiteRecordDispatch(ctx, identity, arguments, "read", mcpkit.RecordModeRead)
	case "website.record.create":
		return websiteRecordDispatch(ctx, identity, arguments, "create", mcpkit.RecordModeCreate)
	case "website.record.write":
		return websiteRecordDispatch(ctx, identity, arguments, "write", mcpkit.RecordModeWrite)
	case "website.record.unlink":
		return websiteRecordDispatch(ctx, identity, arguments, "unlink", mcpkit.RecordModeUnlink)
	case "website.seo.upsert":
		return mcpWebsiteSEOUpsert(ctx, identity, arguments)
	case "website.page.design_upsert":
		return mcpWebsitePageDesignUpsert(ctx, identity, arguments)
	case "website.snippet.list":
		return mcpWebsiteSnippetList(ctx, identity, arguments)
	case "website.publish":
		return mcpWebsitePublish(ctx, identity, arguments, "action_publish")
	case "website.unpublish":
		return mcpWebsitePublish(ctx, identity, arguments, "action_unpublish")
	default:
		return nil, mcpkit.NewToolError(404, "tool_not_found", fmt.Sprintf("unknown mcp tool %q", toolName))
	}
}

// websiteRecordDispatch backs website.record.*: validates arguments.model is
// one of the website.* models, then delegates to the exact same
// mcpkit.RecordDispatch base.record.* already uses, so ACL/record-rule/company
// scope enforcement is identical.
func websiteRecordDispatch(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any, methodName string, mode mcpkit.RecordMode) (any, error) {
	if err := requireWebsiteAllowedModel(arguments); err != nil {
		return nil, err
	}
	return mcpkit.RecordDispatch(ctx, identity, arguments, methodName, mode)
}

func requireWebsiteAllowedModel(arguments map[string]any) error {
	modelName := strings.TrimSpace(mcpkit.StringFromAny(arguments["model"]))
	if modelName == "" {
		return mcpkit.NewToolError(422, "invalid_params", "arguments.model is required")
	}
	if !websiteAllowedModels[modelName] {
		return mcpkit.NewToolError(422, "invalid_params", fmt.Sprintf(
			"model %q is not one of the website.* models this tool group accepts (%s)", modelName, websiteAllowedModelNames(websiteAllowedModels),
		))
	}
	return nil
}

// websiteAllowedModelNames renders a model-name set as a sorted, comma-joined
// string for deterministic error messages.
func websiteAllowedModelNames(allowed map[string]bool) string {
	names := make([]string, 0, len(allowed))
	for name := range allowed {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// pickPresentKeys copies only the keys that are actually present in
// arguments (including explicit false/nil values, which are legitimate
// updates), so a website.seo.upsert call never resets fields the caller
// didn't mention.
func pickPresentKeys(arguments map[string]any, keys []string) map[string]any {
	out := map[string]any{}
	for _, key := range keys {
		if v, ok := arguments[key]; ok {
			out[key] = v
		}
	}
	return out
}

// mcpWebsiteSEOUpsert backs website.seo.upsert: dispatches on arguments.target
// since site-level and page-level SEO live on different models.
func mcpWebsiteSEOUpsert(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any) (any, error) {
	target := strings.ToLower(strings.TrimSpace(mcpkit.StringFromAny(arguments["target"])))
	switch target {
	case "site":
		return mcpWebsiteSiteSEOUpsert(ctx, identity, arguments)
	case "page":
		return mcpWebsitePageSEOUpsert(ctx, identity, arguments)
	case "":
		return nil, mcpkit.NewToolError(422, "invalid_params", `arguments.target is required ("site" or "page")`)
	default:
		return nil, mcpkit.NewToolError(422, "invalid_params", fmt.Sprintf(`arguments.target %q must be "site" or "page"`, target))
	}
}

// mcpWebsiteSiteSEOUpsert writes the provided site-level SEO fields directly
// onto tbl.website_site — these fields already live on the site record.
func mcpWebsiteSiteSEOUpsert(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any) (any, error) {
	siteID, ok := mcpkit.Int64FromArgument(arguments["site_id"])
	if !ok || siteID <= 0 {
		return nil, mcpkit.NewToolError(422, "invalid_params", `arguments.site_id is required for target "site"`)
	}
	values := pickPresentKeys(arguments, websiteSiteSEOFields)
	if len(values) == 0 {
		return nil, mcpkit.NewToolError(422, "invalid_params", "at least one SEO field is required")
	}
	meta, method, err := mcpkit.LookupRuntimeMethod(websiteSiteModelName, "write")
	if err != nil {
		return nil, err
	}
	return mcpkit.ExecuteRuntimeMethod(ctx, identity, meta, method, []string{strconv.FormatInt(siteID, 10)}, []any{values}, nil)
}

// mcpWebsitePageSEOUpsert merges the provided flat SEO fields into the page's
// nested tbl.website_page.Seo jsonb column and writes it back. tbl.website_page is
// the single content entity now (no separate draft-version row), so there is
// nothing to resolve/create before writing.
func mcpWebsitePageSEOUpsert(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any) (any, error) {
	pageID, ok := mcpkit.Int64FromArgument(arguments["page_id"])
	if !ok || pageID <= 0 {
		return nil, mcpkit.NewToolError(422, "invalid_params", `arguments.page_id is required for target "page"`)
	}
	flatValues := pickPresentKeys(arguments, websitePageSEOFields)
	if len(flatValues) == 0 {
		return nil, mcpkit.NewToolError(422, "invalid_params", "at least one SEO field is required")
	}
	mergedSEO, err := mcpMergeWebsitePageSEO(ctx, pageID, mcpNestedSEOFromFlatFields(flatValues))
	if err != nil {
		return nil, err
	}
	meta, method, err := mcpkit.LookupRuntimeMethod(websitePageModelName, "write")
	if err != nil {
		return nil, err
	}
	return mcpkit.ExecuteRuntimeMethod(ctx, identity, meta, method, []string{strconv.FormatInt(pageID, 10)}, []any{map[string]any{"seo": mergedSEO}}, nil)
}

// mcpNestedSEOFromFlatFields translates website.seo.upsert's flat MCP-facing
// field names into the legacy editor's nested SEO shape
// ({title,description,canonicalUrl,robots:{index,follow},openGraph:{...},
// structuredData,includeInSitemap}).
func mcpNestedSEOFromFlatFields(flat map[string]any) map[string]any {
	nested := map[string]any{}
	if v, ok := flat["seo_title"]; ok {
		nested["title"] = v
	}
	if v, ok := flat["seo_description"]; ok {
		nested["description"] = v
	}
	if v, ok := flat["canonical_path"]; ok {
		nested["canonicalUrl"] = v
	}
	if v, ok := flat["include_in_sitemap"]; ok {
		nested["includeInSitemap"] = v
	}
	if v, ok := flat["structured_data_json"]; ok {
		nested["structuredData"] = v
	}
	openGraph := map[string]any{}
	if v, ok := flat["og_title"]; ok {
		openGraph["title"] = v
	}
	if v, ok := flat["og_description"]; ok {
		openGraph["description"] = v
	}
	if v, ok := flat["og_image_url"]; ok {
		openGraph["imageAssetId"] = v
	}
	if len(openGraph) > 0 {
		nested["openGraph"] = openGraph
	}
	if v, ok := flat["robots_directives"]; ok {
		directives := strings.ToLower(mcpkit.StringFromAny(v))
		nested["robots"] = map[string]any{
			"index":  !strings.Contains(directives, "noindex"),
			"follow": !strings.Contains(directives, "nofollow"),
		}
	}
	return nested
}

// mcpMergeWebsitePageSEO reads the page's current Seo jsonb and shallow-merges
// patch on top (top-level keys in patch replace, everything else is kept),
// so an SEO upsert that only sends e.g. seo_title never blanks unrelated
// fields like openGraph or robots.
func mcpMergeWebsitePageSEO(ctx context.Context, pageID int64, patch map[string]any) (map[string]any, error) {
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(websitePageModelName, nil), []string{strconv.FormatInt(pageID, 10)}, []string{"seo"})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, mcpkit.NewToolError(404, "not_found", fmt.Sprintf("tbl.website_page %d not found", pageID))
	}
	existing, _ := rows[0]["seo"].(map[string]any)
	merged := map[string]any{}
	for key, value := range existing {
		merged[key] = value
	}
	for key, value := range patch {
		merged[key] = value
	}
	return merged, nil
}

// mcpWebsitePageDesignUpsert backs website.page.design_upsert: create a page
// (or target an existing one), write the designed html directly onto it,
// then immediately publish that page — one MCP call takes an AI caller from
// "here is the html" to "it is live" with no follow-up website.publish call
// required. The html value is written to tbl.website_page.body_html, where the
// page write hook sanitizes it (scripts, event handlers, <style>, iframes and
// svg are removed) -- the page is the single content entity, holding both its
// draft (body_html/seo) and its published state (deploy_path) directly.
//
// html may also arrive in pieces across several calls (upload_id/html_chunk/
// chunk_index/chunk_total) instead of one arguments.html -- see
// mcpParseHTMLChunkArgs/mcpAccumulateHTMLChunk for the buffering; every call
// before the last chunk returns a small "complete": false ack instead of
// touching the page at all, so a chunked upload never writes a half-finished
// document.
func mcpWebsitePageDesignUpsert(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any) (any, error) {
	chunk, isChunked, err := mcpParseHTMLChunkArgs(arguments)
	if err != nil {
		return nil, err
	}

	var html string
	if isChunked {
		assembled, complete, received, accErr := mcpAccumulateHTMLChunk(identity, chunk)
		if accErr != nil {
			return nil, accErr
		}
		if !complete {
			return map[string]any{
				"upload_id":       chunk.uploadID,
				"chunk_index":     chunk.index,
				"chunk_total":     chunk.total,
				"chunks_received": received,
				"complete":        false,
			}, nil
		}
		html = assembled
	} else {
		html = strings.TrimSpace(mcpkit.StringFromAny(arguments["html"]))
		if html == "" {
			return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.html is required (or send it in pieces via upload_id/html_chunk/chunk_index/chunk_total)")
		}
	}

	var pageID int64
	if existingID, ok := mcpkit.Int64FromArgument(arguments["page_id"]); ok && existingID > 0 {
		pageID = existingID
	} else {
		created, err := mcpCreateWebsitePageForDesign(ctx, identity, arguments)
		if err != nil {
			return nil, err
		}
		createdID, ok := mcpkit.Int64FromArgument(created["id"])
		if !ok || createdID <= 0 {
			return nil, mcpkit.NewToolError(500, "internal_error", "tbl.website_page.create did not return a valid id")
		}
		pageID = createdID
	}

	if _, err := mcpWriteWebsitePageDraftHTML(ctx, identity, pageID, html); err != nil {
		return nil, err
	}

	pageRecord, err := mcpPublishWebsitePageByID(ctx, identity, pageID)
	if err != nil {
		return nil, wrapMCPToolError(err, "draft was saved but publish failed")
	}

	// pageRecord already carries body_html/seo (the page is the
	// single content entity now, no separate version row to also return).
	// base-mcp's toolCallResult runs mcpkit.TrimLargeResponseFields generically
	// over every tool result, so large fields still get "<field>_bytes" treatment.
	return map[string]any{"page_id": pageID, "page": pageRecord}, nil
}

// mcpHTMLChunkSessionTTL bounds how long a partial multi-chunk html upload
// stays buffered server-side waiting for its remaining chunks. Long enough
// for an AI generation loop spread across many slow tool calls, short enough
// that an abandoned upload doesn't sit in the shared home-DB idempotency
// table forever.
const mcpHTMLChunkSessionTTL = 2 * time.Hour

// mcpHTMLChunkArgs is one call's parsed chunk-mode arguments for
// website.page.design_upsert.
type mcpHTMLChunkArgs struct {
	uploadID string
	index    int
	total    int
	data     string
}

// mcpHTMLChunkSession is the server-side buffer for one upload_id, persisted
// as JSON in the same home-DB PGStorage mcpkit.GetIdempotencyStore already
// binds for idempotency-key replay -- reused rather than standing up a second
// storage mechanism, just under a different key prefix/TTL. Chunks is keyed
// by index rather than appended in arrival order, so a retried, out-of-order,
// or duplicated chunk delivery is naturally idempotent: the same index just
// overwrites the same slot instead of producing a corrupt concatenation.
type mcpHTMLChunkSession struct {
	Total  int            `json:"total"`
	Chunks map[int]string `json:"chunks"`
}

// mcpParseHTMLChunkArgs detects and validates website.page.design_upsert's
// chunked-upload arguments. isChunked is false with a nil error when
// upload_id is simply absent (the plain arguments.html path), so callers can
// tell "not chunked" apart from "chunked but malformed."
func mcpParseHTMLChunkArgs(arguments map[string]any) (chunk mcpHTMLChunkArgs, isChunked bool, err error) {
	uploadID := strings.TrimSpace(mcpkit.StringFromAny(arguments["upload_id"]))
	if uploadID == "" {
		return mcpHTMLChunkArgs{}, false, nil
	}
	rawChunk, hasChunk := arguments["html_chunk"]
	if !hasChunk {
		return mcpHTMLChunkArgs{}, true, mcpkit.NewToolError(422, "invalid_params", "arguments.html_chunk is required when arguments.upload_id is set")
	}
	data, ok := rawChunk.(string)
	if !ok {
		return mcpHTMLChunkArgs{}, true, mcpkit.NewToolError(422, "invalid_params", "arguments.html_chunk must be a string")
	}
	total, ok := mcpkit.Int64FromArgument(arguments["chunk_total"])
	if !ok || total <= 0 {
		return mcpHTMLChunkArgs{}, true, mcpkit.NewToolError(422, "invalid_params", "arguments.chunk_total is required and must be a positive integer when arguments.upload_id is set")
	}
	index, ok := mcpkit.Int64FromArgument(arguments["chunk_index"])
	if !ok || index < 0 || index >= total {
		return mcpHTMLChunkArgs{}, true, mcpkit.NewToolError(422, "invalid_params", fmt.Sprintf("arguments.chunk_index is required and must be between 0 and chunk_total-1 (%d)", total-1))
	}
	return mcpHTMLChunkArgs{uploadID: uploadID, index: int(index), total: int(total), data: data}, true, nil
}

// mcpAccumulateHTMLChunk buffers one chunk of a multi-chunk html upload and
// reports whether every chunk (0..total-1) has now been received. Buffering
// happens in the shared home-DB idempotency store (mcpkit.GetIdempotencyStore),
// under a key namespaced by tenant DB + user + upload_id so uploads from
// different tenants/users/sessions never collide. Once complete, the
// assembled html is returned and the session is deleted so a stale upload_id
// can never be silently reused for a second document.
func mcpAccumulateHTMLChunk(identity *orm.RBACIdentity, chunk mcpHTMLChunkArgs) (html string, complete bool, receivedCount int, err error) {
	store, storeErr := mcpkit.GetIdempotencyStore()
	if storeErr != nil {
		return "", false, 0, mcpkit.NewToolError(503, "internal_error", fmt.Sprintf("chunk upload buffer unavailable: %v", storeErr))
	}
	key := mcpkit.IdempotencyKey(identity, "website.page.design_upsert.chunk", chunk.uploadID)

	session := mcpHTMLChunkSession{Chunks: map[int]string{}}
	if raw, getErr := store.Get(key); getErr == nil && len(raw) > 0 {
		if jsonErr := json.Unmarshal(raw, &session); jsonErr != nil {
			return "", false, 0, mcpkit.NewToolError(500, "internal_error", fmt.Sprintf("chunk upload session %q is corrupt: %v", chunk.uploadID, jsonErr))
		}
		if session.Chunks == nil {
			session.Chunks = map[int]string{}
		}
	}
	if session.Total != 0 && session.Total != chunk.total {
		return "", false, 0, mcpkit.NewToolError(422, "invalid_params", fmt.Sprintf(
			"arguments.chunk_total (%d) does not match upload_id %q's earlier chunk_total (%d)", chunk.total, chunk.uploadID, session.Total,
		))
	}
	session.Total = chunk.total
	session.Chunks[chunk.index] = chunk.data

	if len(session.Chunks) < session.Total {
		encoded, marshalErr := json.Marshal(session)
		if marshalErr != nil {
			return "", false, 0, mcpkit.NewToolError(500, "internal_error", fmt.Sprintf("encode chunk upload session: %v", marshalErr))
		}
		if setErr := store.Set(key, encoded, mcpHTMLChunkSessionTTL); setErr != nil {
			return "", false, 0, mcpkit.NewToolError(500, "internal_error", fmt.Sprintf("buffer chunk %d for upload_id %q: %v", chunk.index, chunk.uploadID, setErr))
		}
		return "", false, len(session.Chunks), nil
	}

	var assembled strings.Builder
	for i := 0; i < session.Total; i++ {
		part, ok := session.Chunks[i]
		if !ok {
			// Every index 0..total-1 must be present once len(Chunks) == Total;
			// this can only happen if a caller changed chunk_total partway
			// through in a way the mismatch guard above didn't catch — defensive only.
			return "", false, 0, mcpkit.NewToolError(422, "invalid_params", fmt.Sprintf("chunk_index %d for upload_id %q was never received", i, chunk.uploadID))
		}
		assembled.WriteString(part)
	}
	if delErr := store.Delete(key); delErr != nil {
		log.Errorf("mcp html chunk upload: delete completed session %q: %v", key, delErr)
	}
	return assembled.String(), true, session.Total, nil
}

// mcpWriteWebsitePageDraftHTML writes html to tbl.website_page.body_html (sanitized by the page
// write hook) -- the page is the single content entity, there is no separate
// draft-version row to resolve first. Split out of mcpWebsitePageDesignUpsert so the write
// step can also be reused by any future caller that needs "just save the
// draft" without publish.
func mcpWriteWebsitePageDraftHTML(ctx context.Context, identity *orm.RBACIdentity, pageID int64, html string) (any, error) {
	pageValues := map[string]any{"body_html": html}
	meta, method, err := mcpkit.LookupRuntimeMethod(websitePageModelName, "write")
	if err != nil {
		return nil, err
	}
	return mcpkit.ExecuteRuntimeMethod(ctx, identity, meta, method, []string{strconv.FormatInt(pageID, 10)}, []any{pageValues}, nil)
}

// mcpPublishWebsitePageByID runs tbl.website_page's action_publish for one page
// id, the same runtime method website.publish (mcpWebsitePublish) uses.
func mcpPublishWebsitePageByID(ctx context.Context, identity *orm.RBACIdentity, pageID int64) (any, error) {
	meta, method, err := mcpkit.LookupRuntimeMethod(websitePageModelName, "action_publish")
	if err != nil {
		return nil, err
	}
	return mcpkit.ExecuteRuntimeMethod(ctx, identity, meta, method, []string{strconv.FormatInt(pageID, 10)}, nil, nil)
}

// wrapMCPToolError prefixes an error's message with extra context (e.g. "the
// draft write already succeeded") while preserving its *mcpkit.ToolError
// status/code if it has one, so the caller still gets an accurate HTTP status
// instead of a blanket 500.
func wrapMCPToolError(err error, prefix string) error {
	if toolErr, ok := err.(*mcpkit.ToolError); ok {
		return mcpkit.NewToolError(toolErr.Status, toolErr.Code, fmt.Sprintf("%s: %s", prefix, toolErr.Message))
	}
	return mcpkit.NewToolError(500, "internal_error", fmt.Sprintf("%s: %s", prefix, err.Error()))
}

// mcpCreateWebsitePageForDesign creates a new tbl.website_page for
// website.page.design_upsert when the caller omitted page_id. The caller of
// this helper only needs the returned record's id -- mcpWriteWebsitePageDraftHTML
// writes html directly onto that page next.
func mcpCreateWebsitePageForDesign(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any) (orm.RecordMap, error) {
	siteID, ok := mcpkit.Int64FromArgument(arguments["site_id"])
	if !ok || siteID <= 0 {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.site_id is required when arguments.page_id is not given")
	}
	name := strings.TrimSpace(mcpkit.StringFromAny(arguments["name"]))
	if name == "" {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.name is required when arguments.page_id is not given")
	}
	values := map[string]any{"site_id": siteID, "name": name}
	if routePath := strings.TrimSpace(mcpkit.StringFromAny(arguments["route_path"])); routePath != "" {
		values["route_path"] = routePath
	}
	if isHomepage, ok := arguments["is_homepage"].(bool); ok {
		values["is_homepage"] = isHomepage
	}
	meta, method, err := mcpkit.LookupRuntimeMethod(websitePageModelName, "create")
	if err != nil {
		return nil, err
	}
	result, err := mcpkit.ExecuteRuntimeMethod(ctx, identity, meta, method, nil, []any{values}, nil)
	if err != nil {
		return nil, err
	}
	record, ok := result.(orm.RecordMap)
	if !ok {
		return nil, mcpkit.NewToolError(500, "internal_error", "tbl.website_page.create did not return a record")
	}
	return record, nil
}

// mcpWebsitePublish backs website.publish/website.unpublish: a thin,
// pinned-method wrapper over the exact action_publish/action_unpublish
// runtime methods tbl.website_site and tbl.website_page already declare, so an AI
// caller does not need to already know base.record.call_method's shape.
func mcpWebsitePublish(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any, methodName string) (any, error) {
	modelName := strings.TrimSpace(mcpkit.StringFromAny(arguments["model"]))
	if !websitePublishAllowedModels[modelName] {
		return nil, mcpkit.NewToolError(422, "invalid_params", fmt.Sprintf(
			"arguments.model must be %q or %q", websiteSiteModelName, websitePageModelName,
		))
	}
	ids, err := mcpkit.RequireIDsArg(arguments)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.ids is required")
	}
	meta, method, err := mcpkit.LookupRuntimeMethod(modelName, methodName)
	if err != nil {
		return nil, err
	}
	return mcpkit.ExecuteRuntimeMethod(ctx, identity, meta, method, ids, nil, nil)
}

// mcpWebsiteSnippetList backs website.snippet.list: the builder block catalog
// (static/snippets of installed modules), optionally filtered by group/search.
func mcpWebsiteSnippetList(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any) (any, error) {
	meta, method, err := mcpkit.LookupRuntimeMethod(websiteSnippetModelName, "list_catalog")
	if err != nil {
		return nil, err
	}
	kwargs := pickPresentKeys(arguments, []string{"group", "search"})
	return mcpkit.ExecuteRuntimeMethod(ctx, identity, meta, method, nil, nil, kwargs)
}
