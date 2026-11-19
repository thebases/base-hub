package models

// File overview:
// - Responsibility: defines `tbl.website_blog_post` — one article with its editorial workflow
//   (draft -> published -> archived), SEO fields, tags and derived read time.
// - Key symbols: `BlogPostRecord`, `BlogPostModel`, `BlogPostStatePublished`.

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
)

const BlogPostModelName = "tbl.website_blog_post"

const (
	BlogPostStateDraft     = "draft"
	BlogPostStatePublished = "published"
	BlogPostStateArchived  = "archived"
)

// BlogPostRecord is one article. A post is publicly visible only when state=published AND
// published_at <= now, so a future published_at is how a post is scheduled (no cron needed).
type BlogPostRecord struct {
	orm.BaseModel
	Name            string     `bun:"name,notnull" json:"name" meta:"required,index,label:Title,help:Stores the article title."`
	Subtitle        string     `bun:"subtitle" json:"subtitle" meta:"label:Subtitle,help:Stores the short line shown under the title."`
	Slug            string     `bun:"slug,notnull" json:"slug" meta:"index,label:Slug,help:Stores the URL-safe key used by /blog/<blog>/<slug>; generated from the title when empty and unique within the blog."`
	BlogID          *int64     `bun:"blog_id" json:"blog_id" meta:"kind:many2one,model:tbl.website_blog_blog,ondelete:CASCADE,index,required,label:Blog,help:Links the post to the blog that publishes it."`
	CompanyID       *int64     `bun:"company_id" json:"company_id" meta:"kind:many2one,model:tbl.company,ondelete:SET NULL,index,label:Company,help:Links this post to one company. Empty means shared."`
	State           string     `bun:"state,notnull,default:'draft'" json:"state" meta:"required,index,label:Status,selection:draft|Draft;published|Published;archived|Archived,help:Stores the editorial state; only published posts whose date has passed are public."`
	PublishedAt     *time.Time `bun:"published_at" json:"published_at" meta:"kind:datetime,index,label:Publish Date,help:Stores when the post goes live; a future date schedules it."`
	AuthorID        *int64     `bun:"author_id" json:"author_id" meta:"kind:many2one,model:base.users,ondelete:SET NULL,index,label:Author,help:Links the post to its author account."`
	AuthorName      string     `bun:"author_name" json:"author_name" meta:"label:Author Display Name,help:Overrides the author name shown publicly; falls back to the author account."`
	Teaser          string     `bun:"teaser" json:"teaser" meta:"kind:text,label:Teaser,help:Stores the excerpt shown in lists and feeds; derived from the body when empty."`
	Content         string     `bun:"content" json:"content" meta:"kind:text,label:Content,help:Stores the article body as HTML; it is sanitized to an editorial allowlist on save."`
	CoverURL        string     `bun:"cover_url" json:"cover_url" meta:"label:Cover URL,help:Stores the header image of the post."`
	TagIDs          []int64    `bun:"tag_ids,type:jsonb,nullzero" json:"tag_ids" meta:"kind:many2many,model:tbl.website_blog_tag,relation_table:tbl_website_blog_post_tag_rel,relation_column:post_id,relation_key:tag_id,label:Tags,help:Labels used for filtering and related posts."`
	IsFeatured      bool       `bun:"is_featured,notnull,default:false" json:"is_featured" meta:"label:Featured,help:Pins the post to the top of blog lists."`
	ReadMinutes     int        `bun:"read_minutes,notnull,default:0" json:"read_minutes" meta:"readonly,label:Read Time (min),help:Estimated reading time computed from the body on save."`
	Visits          int64      `bun:"visits,notnull,default:0" json:"visits" meta:"readonly,label:Visits,help:Counts public page views of the post."`
	MetaTitle       string     `bun:"meta_title" json:"meta_title" meta:"label:SEO Title,help:Overrides the HTML title; defaults to the post title."`
	MetaDescription string     `bun:"meta_description" json:"meta_description" meta:"label:SEO Description,help:Overrides the meta description; defaults to the teaser."`
	MetaKeywords    string     `bun:"meta_keywords" json:"meta_keywords" meta:"label:SEO Keywords,help:Stores comma separated meta keywords."`
	CanonicalURL    string     `bun:"canonical_url" json:"canonical_url" meta:"label:Canonical URL,help:Overrides the canonical link when the content originates elsewhere."`
	NoIndex         bool       `bun:"no_index,notnull,default:false" json:"no_index" meta:"label:Hide From Search Engines,help:Emits noindex and omits the post from the sitemap."`
}

var BlogPostModel = orm.Define[BlogPostRecord](&Models, 40, orm.ModelSpec{
	Name:        BlogPostModelName,
	Label:       "Blog Posts",
	Description: "Articles with editorial workflow, scheduling and SEO",
	StandardHooks: orm.StandardRuntimeHooks{
		Create: createBlogPost,
		Write:  writeBlogPost,
	},
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "action_publish", Public: true, UsesPathID: true, Call: actionPublishBlogPost, Description: "Publishes the post now (keeps a future publish date, which schedules it)."},
		{Name: "action_unpublish", Public: true, UsesPathID: true, Call: actionUnpublishBlogPost, Description: "Moves the post back to draft."},
		{Name: "action_archive", Public: true, UsesPathID: true, Call: actionArchiveBlogPost, Description: "Archives the post and removes it from public pages."},
		{Name: "action_duplicate", Public: true, UsesPathID: true, Call: actionDuplicateBlogPost, Description: "Creates a draft copy of the post and returns its id."},
	},
})

func init() {
}

func createBlogPost(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		return nil, orm.InvalidParamsf("%s.create requires values", BlogPostModelName)
	}
	working := cloneRecordMap(values)
	if err := normalizeBlogPostValues(ctx, "", working, true); err != nil {
		return nil, err
	}
	created, err := orm.CreateRecord(ctx, model, working)
	if err != nil {
		return nil, blogPostSlugConflict(err, working)
	}
	return created, nil
}

func writeBlogPost(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		return nil, orm.InvalidParamsf("%s.write requires values", BlogPostModelName)
	}
	if len(ids) != 1 {
		// Slug uniqueness and state transitions are per row; bulk writes of those fields would
		// race, so only single-record writes may touch them.
		for _, key := range []string{"slug", "name", "state", "blog_id"} {
			if hasRecordMapKey(values, key) {
				return nil, orm.InvalidParamsf("%s.write of %q requires exactly one record", BlogPostModelName, key)
			}
		}
		working := cloneRecordMap(values)
		sanitizeBlogPostBody(working)
		return orm.WriteRecords(ctx, model, ids, working)
	}
	working := cloneRecordMap(values)
	if err := normalizeBlogPostValues(ctx, ids[0], working, false); err != nil {
		return nil, err
	}
	written, err := orm.WriteRecords(ctx, model, ids, working)
	if err != nil {
		return nil, blogPostSlugConflict(err, working)
	}
	return written, nil
}

// sanitizeBlogPostBody cleans `content` and refreshes the derived read time whenever the body is
// part of the payload.
func sanitizeBlogPostBody(working orm.RecordMap) {
	if !hasRecordMapKey(working, "content") {
		return
	}
	cleaned := SanitizeBlogHTML(stringValue(working["content"]))
	working["content"] = cleaned
	working["read_minutes"] = blogReadMinutes(cleaned)
}

// normalizeBlogPostValues enforces the post invariants on create and on single-record writes:
// title/blog present, state known, slug unique per blog, body sanitized, and published_at set
// the moment a post becomes published.
func normalizeBlogPostValues(ctx context.Context, currentID string, working orm.RecordMap, isCreate bool) error {
	var current orm.RecordMap
	if !isCreate {
		rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(BlogPostModelName, nil), []string{currentID}, []string{"name", "slug", "blog_id", "state", "published_at"})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return orm.NotFoundf("%s %s not found", BlogPostModelName, currentID)
		}
		current = rows[0]
	}
	pick := func(key string) any {
		if hasRecordMapKey(working, key) {
			return working[key]
		}
		return current[key]
	}

	name := stringValue(pick("name"))
	if name == "" {
		return orm.InvalidParamsf("%s title is required", BlogPostModelName)
	}
	blogID, ok := int64FromAny(pick("blog_id"))
	if !ok || blogID <= 0 {
		return orm.InvalidParamsf("%s blog is required", BlogPostModelName)
	}

	state := stringValue(pick("state"))
	if state == "" {
		state = BlogPostStateDraft
	}
	switch state {
	case BlogPostStateDraft, BlogPostStatePublished, BlogPostStateArchived:
	default:
		return orm.InvalidParamsf("%s state %q is not draft, published or archived", BlogPostModelName, state)
	}
	if isCreate || hasRecordMapKey(working, "state") {
		working["state"] = state
	}
	if state == BlogPostStatePublished && blogPostPublishedAtEmpty(pick("published_at")) {
		working["published_at"] = time.Now().UTC().Format(time.RFC3339)
	}

	// Re-slug when the caller sets one, on create, or when the post moves to another blog (the old
	// slug may collide there). A plain title edit keeps the slug so published URLs stay stable.
	blogChanged := !isCreate && hasRecordMapKey(working, "blog_id")
	if isCreate || hasRecordMapKey(working, "slug") || blogChanged {
		base := slugify(stringValue(working["slug"]), "")
		if base == "" && !isCreate && !hasRecordMapKey(working, "slug") {
			base = slugify(stringValue(current["slug"]), "")
		}
		if base == "" {
			base = slugify(name, "post")
		}
		slug, err := uniqueBlogSlug(ctx, BlogPostModelName, base, orm.RecordMap{"blog_id": blogID}, currentID)
		if err != nil {
			return err
		}
		working["slug"] = slug
	}

	if isCreate && !hasRecordMapKey(working, "content") {
		working["content"] = ""
	}
	sanitizeBlogPostBody(working)
	if hasRecordMapKey(working, "teaser") {
		working["teaser"] = strings.TrimSpace(stringValue(working["teaser"]))
	}
	return nil
}

func blogPostPublishedAtEmpty(raw any) bool {
	switch typed := raw.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	case time.Time:
		return typed.IsZero()
	case *time.Time:
		return typed == nil || typed.IsZero()
	default:
		return false
	}
}

func blogPostIDFromArgs(ids []string) (string, error) {
	if len(ids) != 1 {
		return "", orm.InvalidParamsf("%s action requires exactly one record", BlogPostModelName)
	}
	id := strings.TrimSpace(ids[0])
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		return "", orm.InvalidParamsf("%s id %q is invalid", BlogPostModelName, id)
	}
	return id, nil
}

// setBlogPostState routes a workflow transition through the same normalization as a form write so
// published_at and slug rules stay in one place.
func setBlogPostState(ctx context.Context, model *orm.ModelMetadata, ids []string, state string) (any, error) {
	id, err := blogPostIDFromArgs(ids)
	if err != nil {
		return nil, err
	}
	working := orm.RecordMap{"state": state}
	if err := normalizeBlogPostValues(ctx, id, working, false); err != nil {
		return nil, err
	}
	written, err := orm.WriteRecords(ctx, model, []string{id}, working)
	if err != nil {
		return nil, blogPostSlugConflict(err, working)
	}
	return written, nil
}

func actionPublishBlogPost(ctx context.Context, model *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	return setBlogPostState(ctx, model, ids, BlogPostStatePublished)
}

func actionUnpublishBlogPost(ctx context.Context, model *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	return setBlogPostState(ctx, model, ids, BlogPostStateDraft)
}

func actionArchiveBlogPost(ctx context.Context, model *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	return setBlogPostState(ctx, model, ids, BlogPostStateArchived)
}

// actionDuplicateBlogPost copies editorial content into a fresh draft: counters, publish date and
// slug are reset so the copy never shadows the original.
func actionDuplicateBlogPost(ctx context.Context, model *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	id, err := blogPostIDFromArgs(ids)
	if err != nil {
		return nil, err
	}
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(BlogPostModelName, model), []string{id}, nil)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, orm.NotFoundf("%s %s not found", BlogPostModelName, id)
	}
	source := rows[0]
	copyValues := orm.RecordMap{
		"name":             stringValue(source["name"]) + " (copy)",
		"subtitle":         source["subtitle"],
		"blog_id":          source["blog_id"],
		"author_id":        source["author_id"],
		"author_name":      source["author_name"],
		"teaser":           source["teaser"],
		"content":          source["content"],
		"cover_url":        source["cover_url"],
		"tag_ids":          blogIDList(source["tag_ids"]),
		"meta_title":       source["meta_title"],
		"meta_description": source["meta_description"],
		"meta_keywords":    source["meta_keywords"],
		"state":            BlogPostStateDraft,
	}
	if err := normalizeBlogPostValues(ctx, "", copyValues, true); err != nil {
		return nil, err
	}
	created, err := orm.CreateRecord(ctx, model, copyValues)
	if err != nil {
		return nil, blogPostSlugConflict(err, copyValues)
	}
	return created, nil
}

// blogIDList normalizes a many2many read value (ids or `[id, name]` tuples in any slice type)
// into plain ids for a write payload.
func blogIDList(raw any) []int64 {
	rv := reflect.ValueOf(raw)
	if !rv.IsValid() || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) {
		return nil
	}
	ids := make([]int64, 0, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		if id, ok := int64FromAny(rv.Index(i).Interface()); ok && id > 0 {
			ids = append(ids, id)
		}
	}
	return ids
}
