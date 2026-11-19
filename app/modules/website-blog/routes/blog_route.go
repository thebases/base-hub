package routes

// File overview:
// - Responsibility: public (unauthenticated) blog transport — /blog index, per-blog index, post
//   page, RSS feed and sitemap. Server-rendered so crawlers and link previews see full HTML.
// - Key symbols: `RegisterRoutes`, `handleBlogIndex`, `handleBlogPost`, `handleBlogFeed`.

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"git.thebase.vn/base_hub/modules/website-blog/models"
	websitemodels "git.thebase.vn/base_hub/modules/website/models"
	websiteroutes "git.thebase.vn/base_hub/modules/website/routes"
	"github.com/gofiber/fiber/v3"
)

const (
	postsPerPage = 10
	// feedItemLimit matches renderFeed's cap.
	feedItemLimit = 30
)

// RegisterRoutes mounts the public blog. It reserves "/blog" in the website runtime first, because
// that catch-all is mounted earlier (module-name order) and would otherwise answer with the
// site's SPA shell.
func RegisterRoutes(router fiber.Router) {
	if router == nil {
		return
	}
	websiteroutes.RegisterReservedRuntimePrefix("/blog")
	router.Get("/blog", handleBlogIndex)
	router.Get("/blog/feed.xml", handleBlogFeed)
	router.Get("/blog/sitemap.xml", handleBlogSitemap)
	router.Get("/blog/:blog", handleBlogIndex)
	router.Get("/blog/:blog/feed.xml", handleBlogFeed)
	router.Get("/blog/:blog/:post", handleBlogPost)
}

// blogRequest bundles what every public handler resolves first.
type blogRequest struct {
	ctx     context.Context
	dbName  string
	chrome  pageChrome
	catalog *blogCatalog
}

// resolveBlogRequest resolves tenant context, the owning site and the blog catalog. ok=false means
// the response was already written (404 when website is not installed or no site exists).
func resolveBlogRequest(c fiber.Ctx) (*blogRequest, bool, error) {
	siteRow, ctx, enabled, err := websiteroutes.ResolveSiteForRequest(c)
	if err != nil {
		return nil, false, err
	}
	if !enabled || siteRow == nil {
		return nil, false, c.SendStatus(fiber.StatusNotFound)
	}
	siteID := rowInt64(siteRow["id"])
	chrome := pageChrome{
		SiteName:     rowString(siteRow, "name"),
		BootstrapURL: "/website/static/bs/css/bootstrap.min.css",
		BaseURL:      siteBaseURL(c, siteRow),
	}
	dbName := ""
	if env := orm.EnvironmentFromContext(ctx); env != nil {
		dbName = strings.TrimSpace(env.DBName())
	}
	// Only trust a deploy folder confined under this tenant's website public root, so a tampered
	// deploy_path cannot turn these checks into a file-exists probe elsewhere on disk.
	if deploy, ok := websitemodels.ConfinedWebsiteDeployPath(dbName, rowString(siteRow, "deploy_path")); ok {
		// A published site serves its own bootstrap/styles from the deploy folder via the website runtime under its /_site mount (root /vendor and /styles.css belong to the core SPA).
		if fileExists(filepath.Join(deploy, "vendor", "bootstrap.min.css")) {
			chrome.BootstrapURL = "/_site/vendor/bootstrap.min.css"
		}
		if fileExists(filepath.Join(deploy, "styles.css")) {
			chrome.StylesURL = "/_site/styles.css"
		}
	}
	catalog, err := loadBlogCatalog(ctx, siteID)
	if err != nil {
		return nil, false, err
	}
	return &blogRequest{ctx: ctx, dbName: dbName, chrome: chrome, catalog: catalog}, true, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// siteBaseURL prefers the site's configured canonical base, else the request origin.
func siteBaseURL(c fiber.Ctx, siteRow orm.RecordMap) string {
	if base := strings.TrimRight(rowString(siteRow, "default_canonical_base"), "/"); base != "" {
		return base
	}
	return c.Scheme() + "://" + c.Hostname()
}

func sendHTML(c fiber.Ctx, body []byte, err error) error {
	if err != nil {
		log.Errorf("website-blog render: %v", err)
		return fiber.NewError(fiber.StatusInternalServerError, "blog render failed")
	}
	c.Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)
	return c.Send(body)
}

func handleBlogIndex(c fiber.Ctx) error {
	req, ok, err := resolveBlogRequest(c)
	if !ok {
		return err
	}
	blogs := req.catalog.Blogs
	var active *blogInfo
	if slug := c.Params("blog"); slug != "" {
		if active = req.catalog.blogBySlug(slug); active == nil {
			return c.SendStatus(fiber.StatusNotFound)
		}
		blogs = []blogInfo{*active}
	}
	tagSlug := strings.TrimSpace(c.Query("tag"))
	query := strings.TrimSpace(c.Query("q"))
	now := time.Now()
	// The tag cloud counts tags over every visible post, so it reads ids and tags only; the filtered
	// id list gives the page count, and only the posts of the requested page are read in full.
	tagged, err := visiblePosts(req.ctx, req.catalog, postQuery{blogs: blogs, fields: []string{"id", "tag_ids"}}, now)
	if err != nil {
		return err
	}
	var filters []orm.DomainNode
	if tagSlug != "" {
		tag := req.catalog.tagBySlug(tagSlug)
		if tag == nil {
			return c.SendStatus(fiber.StatusNotFound)
		}
		filters = append(filters, orm.Where("id", "in", postIDsWithTag(tagged, tag.ID)))
	}
	filters = append(filters, postSearchFilter(query)...)
	total := len(tagged)
	if len(filters) > 0 {
		matching, err := visiblePosts(req.ctx, req.catalog, postQuery{blogs: blogs, filters: filters, fields: []string{"id"}}, now)
		if err != nil {
			return err
		}
		total = len(matching)
	}
	requested, _ := strconv.Atoi(c.Query("page"))
	page, pages, offset := pageBounds(total, requested, postsPerPage)
	posts, err := visiblePosts(req.ctx, req.catalog, postQuery{blogs: blogs, filters: filters, limit: postsPerPage, offset: offset}, now)
	if err != nil {
		return err
	}

	base := "/blog"
	data := listPageData{
		Chrome: req.chrome, Heading: "Blog", Title: "Blog – " + req.chrome.SiteName, Canonical: req.chrome.BaseURL + "/blog",
		Description: "Latest articles from " + req.chrome.SiteName, Blogs: req.catalog.Blogs, Posts: posts, TagCloud: buildTagCloud(req.catalog, tagged),
		ActiveTag: tagSlug, Query: query, Page: page, Pages: pages, FeedURL: "/blog/feed.xml",
	}
	if active != nil {
		base = "/blog/" + url.PathEscape(active.Slug)
		data.ActiveBlog = active.Slug
		data.Heading, data.Subheading, data.CoverURL = active.Name, active.Subtitle, active.CoverURL
		data.Title = firstNonEmpty(active.MetaTitle, active.Name+" – "+req.chrome.SiteName)
		data.Description = firstNonEmpty(active.MetaDescription, active.Description, data.Description)
		data.Canonical = req.chrome.BaseURL + base
		data.FeedURL = base + "/feed.xml"
	}
	if page > 1 {
		data.PrevURL = pageURL(base, page-1, tagSlug, query)
	}
	if page < pages {
		data.NextURL = pageURL(base, page+1, tagSlug, query)
	}
	body, err := renderListPage(data)
	return sendHTML(c, body, err)
}

func handleBlogPost(c fiber.Ctx) error {
	req, ok, err := resolveBlogRequest(c)
	if !ok {
		return err
	}
	blog := req.catalog.blogBySlug(c.Params("blog"))
	if blog == nil {
		return c.SendStatus(fiber.StatusNotFound)
	}
	now := time.Now()
	matches, err := visiblePosts(req.ctx, req.catalog, postQuery{
		blogs: []blogInfo{*blog}, filters: []orm.DomainNode{orm.Where("slug", "=", c.Params("post"))}, withContent: true, limit: 1,
	}, now)
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		return c.SendStatus(fiber.StatusNotFound)
	}
	post := &matches[0]
	// Related posts are ranked on (id, tag_ids) rows of the blog; only the winners are read in full.
	var related []blogPost
	if len(post.TagIDs) > 0 {
		tagged, err := visiblePosts(req.ctx, req.catalog, postQuery{blogs: []blogInfo{*blog}, fields: []string{"id", "tag_ids"}, chronological: true}, now)
		if err != nil {
			return err
		}
		if ids := postIDs(relatedPosts(tagged, *post, 3)); len(ids) > 0 {
			rows, err := visiblePosts(req.ctx, req.catalog, postQuery{blogs: []blogInfo{*blog}, filters: []orm.DomainNode{orm.Where("id", "in", ids)}}, now)
			if err != nil {
				return err
			}
			related = orderPostsByIDs(rows, ids)
		}
	}
	countVisit(c, req, post.ID)

	canonical := post.CanonicalURL
	if canonical == "" {
		canonical = req.chrome.BaseURL + "/blog/" + url.PathEscape(blog.Slug) + "/" + url.PathEscape(post.Slug)
	}
	robots := "index,follow"
	if post.NoIndex {
		robots = "noindex,nofollow"
	}
	desc := firstNonEmpty(post.MetaDescription, post.Teaser, models.BlogPlainText(post.Content))
	if r := []rune(desc); len(r) > 200 {
		desc = string(r[:200]) + "…"
	}
	body, err := renderPostPage(postPageData{
		Chrome: req.chrome, Post: *post, Blog: *blog, Title: firstNonEmpty(post.MetaTitle, post.Title) + " – " + req.chrome.SiteName,
		Desc: desc, Canonical: canonical, Body: postBodyHTML(post.Content), Related: related, Robots: robots,
	})
	return sendHTML(c, body, err)
}

func handleBlogFeed(c fiber.Ctx) error {
	req, ok, err := resolveBlogRequest(c)
	if !ok {
		return err
	}
	blogs := req.catalog.Blogs
	title, self := "Blog – "+req.chrome.SiteName, "/blog"
	if slug := c.Params("blog"); slug != "" {
		blog := req.catalog.blogBySlug(slug)
		if blog == nil {
			return c.SendStatus(fiber.StatusNotFound)
		}
		blogs, title, self = []blogInfo{*blog}, blog.Name+" – "+req.chrome.SiteName, "/blog/"+url.PathEscape(blog.Slug)
	}
	posts, err := visiblePosts(req.ctx, req.catalog, postQuery{blogs: blogs, chronological: true, limit: feedItemLimit}, time.Now())
	if err != nil {
		return err
	}
	body, err := renderFeed(req.chrome, title, "Latest articles from "+req.chrome.SiteName, self, posts)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderContentType, "application/rss+xml; charset=utf-8")
	return c.Send(body)
}

func handleBlogSitemap(c fiber.Ctx) error {
	req, ok, err := resolveBlogRequest(c)
	if !ok {
		return err
	}
	posts, err := visiblePosts(req.ctx, req.catalog, postQuery{blogs: req.catalog.Blogs}, time.Now())
	if err != nil {
		return err
	}
	body, err := renderSitemap(req.chrome, req.catalog.Blogs, posts)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderContentType, "application/xml; charset=utf-8")
	return c.Send(body)
}

// countVisit bumps the post's view counter atomically in SQL (no read-modify-write race, no
// write_date churn). Crawlers and HEAD requests are not counted; failures never fail the page.
func countVisit(c fiber.Ctx, req *blogRequest, postID int64) {
	if c.Method() != fiber.MethodGet || isBot(c.Get(fiber.HeaderUserAgent)) {
		return
	}
	pool := orm.ResolvePGXPool(req.dbName)
	if pool == nil {
		return
	}
	table := "tbl_website_blog_post" // canonical storage name of tbl.website_blog_post
	if _, err := pool.Exec(req.ctx, `UPDATE `+table+` SET visits = visits + 1 WHERE id = $1`, postID); err != nil {
		log.Warnf("website-blog visit counter: %v", err)
	}
}

func isBot(userAgent string) bool {
	ua := strings.ToLower(userAgent)
	for _, marker := range []string{"bot", "crawl", "spider", "slurp", "preview", "facebookexternalhit"} {
		if strings.Contains(ua, marker) {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
