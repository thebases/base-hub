package models

// File overview:
// - Responsibility: defines `tbl.website_blog_blog` — a blog (channel) that owns posts, optionally bound to one website site.
// - Key symbols: `BlogRecord`, `BlogModel`.

import (
	"context"

	"git.thebase.vn/base_hub/internal/orm"
)

const BlogModelName = "tbl.website_blog_blog"

// BlogRecord is one blog, e.g. "News" or "Engineering". Posts live under it at /blog/<slug>/<post>.
type BlogRecord struct {
	orm.BaseModel
	Name            string `bun:"name,notnull" json:"name" meta:"required,index,label:Name,help:Stores the blog title shown on its index page."`
	Subtitle        string `bun:"subtitle" json:"subtitle" meta:"label:Subtitle,help:Stores the short tagline shown under the blog title."`
	Slug            string `bun:"slug,notnull" json:"slug" meta:"index,label:Slug,help:Stores the URL-safe key used by /blog/<slug>; generated from the name when empty."`
	Description     string `bun:"description" json:"description" meta:"kind:text,label:Description,help:Stores the blog introduction used on its index page and as the default meta description."`
	SiteID          *int64 `bun:"site_id" json:"site_id" meta:"kind:many2one,model:tbl.website_site,ondelete:SET NULL,index,label:Website,help:Restricts this blog to one website; empty publishes it on every site."`
	CompanyID       *int64 `bun:"company_id" json:"company_id" meta:"kind:many2one,model:tbl.company,ondelete:SET NULL,index,label:Company,help:Links this blog to one company. Empty means shared."`
	Sequence        int    `bun:"sequence,notnull,default:10" json:"sequence" meta:"label:Sequence,help:Orders blogs in navigation and on /blog."`
	CoverURL        string `bun:"cover_url" json:"cover_url" meta:"label:Cover URL,help:Stores the banner image shown on the blog index page."`
	MetaTitle       string `bun:"meta_title" json:"meta_title" meta:"label:SEO Title,help:Overrides the page title of the blog index."`
	MetaDescription string `bun:"meta_description" json:"meta_description" meta:"label:SEO Description,help:Overrides the meta description of the blog index."`
}

var BlogModel = orm.Define[BlogRecord](&Models, 30, orm.ModelSpec{
	Name:        BlogModelName,
	Label:       "Blogs",
	Description: "Blog channels that group posts and carry index-page SEO",
	Prepare:     prepareBlog,
})

func prepareBlog(ctx context.Context, op orm.WriteOp) error {
	return prepareSluggedRecord(ctx, op, BlogModelName, "blog")
}
