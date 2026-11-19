package mcp

// File overview:
// - Responsibility: the website_blog.* MCP tool catalog — record CRUD limited to the blog models
//   plus the post workflow actions.
// - Key symbols: Tools, blogAllowedModels.

import kernelmodules "git.thebase.vn/base_hub/internal/modules"

// Model names are literals on purpose (stable wire identifiers resolved through the runtime
// registry), matching the website module's own MCP layer.
const (
	blogModelName     = "tbl.website_blog_blog"
	postModelName     = "tbl.website_blog_post"
	tagModelName      = "tbl.website_blog_tag"
	categoryModelName = "tbl.website_blog_category"
)

var blogAllowedModels = map[string]bool{
	blogModelName: true, postModelName: true, tagModelName: true, categoryModelName: true,
}

// postActionTools maps a tool name to the post RuntimeMethod it runs.
var postActionTools = map[string]string{
	"website_blog.post.publish":   "action_publish",
	"website_blog.post.unpublish": "action_unpublish",
	"website_blog.post.archive":   "action_archive",
	"website_blog.post.duplicate": "action_duplicate",
}

// Tools returns the catalog appended to tools/list when website-blog is installed.
func Tools() []kernelmodules.MCPToolDescriptor {
	model := map[string]any{"type": "string", "enum": []string{blogModelName, postModelName, tagModelName, categoryModelName}}
	idsOnly := map[string]any{
		"type":       "object",
		"properties": map[string]any{"ids": map[string]any{"type": "array", "description": "Exactly one post id."}},
		"required":   []string{"ids"},
	}
	return []kernelmodules.MCPToolDescriptor{
		{
			Name:        "website_blog.record.search_read",
			Description: "Search and read blog records (tbl.website_blog_blog, _post, _tag, _category), subject to ACL/record rules. Same behavior as base.record.search_read, restricted to the blog models. Post filters: state (draft|published|archived), blog_id, published_at.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model": model, "domain": map[string]any{"type": "array"}, "fields": map[string]any{"type": "array"},
					"limit": map[string]any{"type": "integer", "default": 20}, "offset": map[string]any{"type": "integer"}, "order": map[string]any{"type": "string"},
				},
				"required": []string{"model"},
			},
		},
		{
			Name:        "website_blog.record.read",
			Description: "Read specific blog records by id. Same behavior as base.record.read, restricted to the blog models.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"model": model, "ids": map[string]any{"type": "array"}, "fields": map[string]any{"type": "array"}},
				"required":   []string{"model", "ids"},
			},
		},
		{
			Name:        "website_blog.record.create",
			Description: "Create one blog record (blog, post, tag or tag group). A post needs name and blog_id; content is HTML and is sanitized; slug is generated when omitted; state defaults to draft (use website_blog.post.publish to go live). idempotency_key is required so a retried call replays the original result instead of creating a duplicate.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model": model, "values": map[string]any{"type": "object"},
					"idempotency_key": map[string]any{"type": "string", "description": "Required. A stable caller-chosen string that dedupes retried calls."},
				},
				"required": []string{"model", "values", "idempotency_key"},
			},
		},
		{
			Name:        "website_blog.record.write",
			Description: "Update specific blog records by id. Same behavior as base.record.write, restricted to the blog models. Changing slug, name, state or blog_id requires exactly one id.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"model": model, "ids": map[string]any{"type": "array"}, "values": map[string]any{"type": "object"}},
				"required":   []string{"model", "ids", "values"},
			},
		},
		{
			Name:        "website_blog.record.unlink",
			Description: "Delete specific blog records by id. Deleting a blog deletes its posts (cascade). Prefer website_blog.post.archive for posts.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"model": model, "ids": map[string]any{"type": "array"}},
				"required":   []string{"model", "ids"},
			},
		},
		{Name: "website_blog.post.publish", Description: "Publish one post now. A post whose publish date is in the future stays hidden until then (scheduling).", InputSchema: idsOnly},
		{Name: "website_blog.post.unpublish", Description: "Move one post back to draft; it disappears from /blog.", InputSchema: idsOnly},
		{Name: "website_blog.post.archive", Description: "Archive one post and remove it from the public blog.", InputSchema: idsOnly},
		{Name: "website_blog.post.duplicate", Description: "Create a draft copy of one post and return the new record.", InputSchema: idsOnly},
	}
}
