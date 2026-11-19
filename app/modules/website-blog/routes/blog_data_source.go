package routes

// File overview:
// - Responsibility: publishes the latest blog posts as the `blog-posts` website data source, so a
//   `data-bh-source="blog-posts"` block on any page lists them.
// - Key symbols: `RegisterDataSources`.

import (
	"context"
	"net/url"
	"time"

	websitemodels "git.thebase.vn/base_hub/modules/website/models"
)

// BlogPostsSource is the data source key.
const BlogPostsSource = "blog-posts"

// RegisterDataSources registers the blog's data sources with the website module.
func RegisterDataSources() error {
	return websitemodels.RegisterWebsiteDataSource(BlogPostsSource, listBlogPostsForSource)
}

// listBlogPostsForSource returns the newest published posts of the blogs the site may show. It reuses
// the public blog's own visibility rules (state, publish date, site binding), so a draft or a future
// post never appears.
func listBlogPostsForSource(ctx context.Context, siteID int64, limit int) ([]websitemodels.WebsiteDataItem, error) {
	catalog, err := loadBlogCatalog(ctx, siteID)
	if err != nil {
		return nil, err
	}
	posts, err := visiblePosts(ctx, catalog, postQuery{blogs: catalog.Blogs, limit: limit}, time.Now())
	if err != nil {
		return nil, err
	}
	items := make([]websitemodels.WebsiteDataItem, 0, len(posts))
	for _, p := range posts {
		item := websitemodels.WebsiteDataItem{
			Title: p.Title, Summary: p.Teaser, Image: p.CoverURL,
			URL: "/blog/" + url.PathEscape(p.BlogSlug) + "/" + url.PathEscape(p.Slug),
		}
		if !p.PublishedAt.IsZero() {
			item.Date = p.PublishedAt.UTC().Format(time.RFC3339)
		}
		items = append(items, item)
	}
	return items, nil
}
