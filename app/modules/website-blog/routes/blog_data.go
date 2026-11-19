package routes

// File overview:
// - Responsibility: tenant-scoped reads for the public blog — blogs, visible posts, tags — plus
//   the pure filtering/pagination helpers over them.
// - Key symbols: `loadBlogCatalog`, `visiblePosts`, `postQuery`, `blogPost`, `pageBounds`.

import (
	"context"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/modules/website-blog/models"
)

type blogInfo struct {
	ID              int64
	Name            string
	Subtitle        string
	Slug            string
	Description     string
	CoverURL        string
	MetaTitle       string
	MetaDescription string
}

type tagInfo struct {
	ID       int64
	Name     string
	Slug     string
	Category string
	Count    int
}

type blogPost struct {
	ID              int64
	Title           string
	Subtitle        string
	Slug            string
	BlogID          int64
	BlogSlug        string
	BlogName        string
	Author          string
	Teaser          string
	Content         string
	CoverURL        string
	PublishedAt     time.Time
	TagIDs          []int64
	Tags            []tagInfo
	Featured        bool
	ReadMinutes     int
	MetaTitle       string
	MetaDescription string
	MetaKeywords    string
	CanonicalURL    string
	NoIndex         bool
	UpdatedAt       time.Time
}

type blogCatalog struct {
	Blogs []blogInfo
	Tags  map[int64]tagInfo
}

func rowString(row orm.RecordMap, key string) string {
	if v, ok := row[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func rowInt64(raw any) int64 {
	switch typed := raw.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case int32:
		return int64(typed)
	case float64:
		return int64(typed)
	case string:
		v, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return v
	case []any:
		if len(typed) > 0 {
			return rowInt64(typed[0])
		}
	case [2]any:
		return rowInt64(typed[0])
	default:
		// Other slice/array shapes the runtime produces ([]int64, [][]any ...): first element.
		if rv := reflect.ValueOf(raw); rv.IsValid() && (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) && rv.Len() > 0 {
			return rowInt64(rv.Index(0).Interface())
		}
	}
	return 0
}

// rowLabel returns the display name of a many2one value (`[id, name]`), "" otherwise.
func rowLabel(raw any) string {
	switch typed := raw.(type) {
	case []any:
		if len(typed) > 1 {
			if s, ok := typed[1].(string); ok {
				return s
			}
		}
	case [2]any:
		if s, ok := typed[1].(string); ok {
			return s
		}
	}
	return ""
}

// rowIDs flattens a many2many read value (ids or `[id, name]` tuples, in any slice type) to ids.
func rowIDs(raw any) []int64 {
	rv := reflect.ValueOf(raw)
	if !rv.IsValid() || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) {
		return nil
	}
	out := make([]int64, 0, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		if id := rowInt64(rv.Index(i).Interface()); id > 0 {
			out = append(out, id)
		}
	}
	return out
}

func rowTime(raw any) time.Time {
	switch typed := raw.(type) {
	case time.Time:
		return typed
	case *time.Time:
		if typed != nil {
			return *typed
		}
	case string:
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
			if t, err := time.Parse(layout, strings.TrimSpace(typed)); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}

func searchRead(ctx context.Context, model string, filter orm.RecordMap, fields []string) ([]orm.RecordMap, error) {
	return orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(model, nil), filter, fields)
}

// loadBlogCatalog reads the blogs a site may show (site_id empty or equal to siteID) and every tag
// with its group name.
func loadBlogCatalog(ctx context.Context, siteID int64) (*blogCatalog, error) {
	blogRows, err := searchRead(ctx, models.BlogModelName, orm.RecordMap{"__order": "sequence asc, id asc"}, nil)
	if err != nil {
		return nil, err
	}
	catalog := &blogCatalog{Tags: map[int64]tagInfo{}}
	for _, row := range blogRows {
		if bound := rowInt64(row["site_id"]); bound > 0 && siteID > 0 && bound != siteID {
			continue
		}
		catalog.Blogs = append(catalog.Blogs, blogInfo{
			ID: rowInt64(row["id"]), Name: rowString(row, "name"), Subtitle: rowString(row, "subtitle"),
			Slug: rowString(row, "slug"), Description: rowString(row, "description"), CoverURL: rowString(row, "cover_url"),
			MetaTitle: rowString(row, "meta_title"), MetaDescription: rowString(row, "meta_description"),
		})
	}
	tagRows, err := searchRead(ctx, models.BlogTagModelName, orm.RecordMap{"__order": "name asc"}, nil)
	if err != nil {
		return nil, err
	}
	for _, row := range tagRows {
		id := rowInt64(row["id"])
		catalog.Tags[id] = tagInfo{ID: id, Name: rowString(row, "name"), Slug: rowString(row, "slug"), Category: rowLabel(row["category_id"])}
	}
	return catalog, nil
}

func (c *blogCatalog) blogBySlug(slug string) *blogInfo {
	for i := range c.Blogs {
		if c.Blogs[i].Slug == slug {
			return &c.Blogs[i]
		}
	}
	return nil
}

func (c *blogCatalog) blogByID(id int64) *blogInfo {
	for i := range c.Blogs {
		if c.Blogs[i].ID == id {
			return &c.Blogs[i]
		}
	}
	return nil
}

func (c *blogCatalog) tagBySlug(slug string) *tagInfo {
	for _, tag := range c.Tags {
		if tag.Slug == slug {
			copy := tag
			return &copy
		}
	}
	return nil
}

// postQuery narrows visiblePosts in SQL. The zero value lists every visible post of the blogs,
// featured first.
type postQuery struct {
	blogs         []blogInfo
	filters       []orm.DomainNode // extra conditions ANDed to the visibility rules
	fields        []string         // nil = postListFields (+ content when withContent)
	withContent   bool
	chronological bool // true: newest first without pinning featured posts (feeds)
	limit, offset int  // 0 = no limit / no offset
}

// postListFields are the columns list pages render; content is left out (it is the heavy column).
var postListFields = []string{"id", "name", "subtitle", "slug", "blog_id", "author_id", "author_name", "teaser", "cover_url", "published_at",
	"tag_ids", "is_featured", "read_minutes", "meta_title", "meta_description", "meta_keywords", "canonical_url", "no_index", "updated_at"}

// postSearchFilter matches title, subtitle or teaser case-insensitively; nil for an empty needle.
func postSearchFilter(needle string) []orm.DomainNode {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return nil
	}
	return []orm.DomainNode{orm.Or(
		orm.Where("name", "ilike", needle),
		orm.Where("subtitle", "ilike", needle),
		orm.Where("teaser", "ilike", needle),
	)}
}

// postIDsWithTag returns the ids of posts carrying tagID. It filters the (id, tag_ids) rows the
// caller already read for the tag cloud: the ORM's many2many "any" domain cannot be used here (its
// SQL references the joined tag table by name after aliasing it, see the T76 report).
func postIDsWithTag(posts []blogPost, tagID int64) []int64 {
	ids := make([]int64, 0)
	for _, p := range posts {
		if slices.Contains(p.TagIDs, tagID) {
			ids = append(ids, p.ID)
		}
	}
	return ids
}

// postIDs lists the ids of posts in order.
func postIDs(posts []blogPost) []int64 {
	ids := make([]int64, 0, len(posts))
	for _, p := range posts {
		ids = append(ids, p.ID)
	}
	return ids
}

// orderPostsByIDs returns posts in the order of ids, dropping posts not listed.
func orderPostsByIDs(posts []blogPost, ids []int64) []blogPost {
	byID := make(map[int64]blogPost, len(posts))
	for _, p := range posts {
		byID[p.ID] = p
	}
	out := make([]blogPost, 0, len(ids))
	for _, id := range ids {
		if p, ok := byID[id]; ok {
			out = append(out, p)
		}
	}
	return out
}

// visiblePosts returns published posts (published_at already passed) of q.blogs. Filtering, order
// (featured pinned first unless q.chronological) and paging run in SQL, so a view reads only the
// rows it renders instead of every post of the blog.
func visiblePosts(ctx context.Context, catalog *blogCatalog, q postQuery, now time.Time) ([]blogPost, error) {
	if len(q.blogs) == 0 {
		return nil, nil
	}
	blogIDs := make([]int64, 0, len(q.blogs))
	for _, b := range q.blogs {
		blogIDs = append(blogIDs, b.ID)
	}
	fields := q.fields
	if fields == nil {
		fields = postListFields
		if q.withContent {
			fields = append(append([]string(nil), postListFields...), "content")
		}
	}
	conditions := append([]orm.DomainNode{
		orm.Where("state", "=", models.BlogPostStatePublished),
		orm.Where("published_at", "<=", now),
		orm.Where("blog_id", "in", blogIDs),
	}, q.filters...)
	order := "is_featured desc, published_at desc, id desc"
	if q.chronological {
		order = "published_at desc, id desc"
	}
	filter := orm.RecordMap{"__domain": orm.And(conditions...), "__order": order}
	if q.limit > 0 {
		filter["__limit"] = q.limit
	}
	if q.offset > 0 {
		filter["__offset"] = q.offset
	}
	rows, err := searchRead(ctx, models.BlogPostModelName, filter, fields)
	if err != nil {
		return nil, err
	}
	posts := make([]blogPost, 0, len(rows))
	for _, row := range rows {
		posts = append(posts, blogPostFromRow(catalog, row))
	}
	return posts, nil
}

func blogPostFromRow(catalog *blogCatalog, row orm.RecordMap) blogPost {
	post := blogPost{
		ID: rowInt64(row["id"]), Title: rowString(row, "name"), Subtitle: rowString(row, "subtitle"), Slug: rowString(row, "slug"),
		BlogID: rowInt64(row["blog_id"]), Author: rowString(row, "author_name"), Teaser: rowString(row, "teaser"),
		Content: rowString(row, "content"), CoverURL: rowString(row, "cover_url"), PublishedAt: rowTime(row["published_at"]),
		TagIDs: rowIDs(row["tag_ids"]), Featured: row["is_featured"] == true, ReadMinutes: int(rowInt64(row["read_minutes"])),
		MetaTitle: rowString(row, "meta_title"), MetaDescription: rowString(row, "meta_description"),
		MetaKeywords: rowString(row, "meta_keywords"), CanonicalURL: rowString(row, "canonical_url"),
		NoIndex: row["no_index"] == true, UpdatedAt: rowTime(row["updated_at"]),
	}
	if post.Author == "" {
		post.Author = rowLabel(row["author_id"])
	}
	if blog := catalog.blogByID(post.BlogID); blog != nil {
		post.BlogSlug, post.BlogName = blog.Slug, blog.Name
	}
	for _, id := range post.TagIDs {
		if tag, ok := catalog.Tags[id]; ok {
			post.Tags = append(post.Tags, tag)
		}
	}
	return post
}

// pageBounds clamps a 1-based page against total items and returns it with the page count (>=1)
// and the SQL offset.
func pageBounds(total, page, perPage int) (current, pages, offset int) {
	pages = (total + perPage - 1) / perPage
	if pages < 1 {
		pages = 1
	}
	current = min(max(page, 1), pages)
	return current, pages, (current - 1) * perPage
}

// relatedPosts ranks other posts by shared-tag count (ties: newest), capped at limit.
func relatedPosts(all []blogPost, current blogPost, limit int) []blogPost {
	type scored struct {
		post  blogPost
		score int
	}
	have := map[int64]bool{}
	for _, id := range current.TagIDs {
		have[id] = true
	}
	var ranked []scored
	for _, p := range all {
		if p.ID == current.ID {
			continue
		}
		score := 0
		for _, id := range p.TagIDs {
			if have[id] {
				score++
			}
		}
		if score > 0 {
			ranked = append(ranked, scored{p, score})
		}
	}
	for i := 1; i < len(ranked); i++ { // stable insertion sort keeps date order among equal scores
		for j := i; j > 0 && ranked[j].score > ranked[j-1].score; j-- {
			ranked[j], ranked[j-1] = ranked[j-1], ranked[j]
		}
	}
	out := make([]blogPost, 0, limit)
	for _, r := range ranked {
		if len(out) == limit {
			break
		}
		out = append(out, r.post)
	}
	return out
}
