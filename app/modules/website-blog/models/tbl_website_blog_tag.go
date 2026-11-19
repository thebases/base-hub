package models

// File overview:
// - Responsibility: defines `tbl.website_blog_tag` — a label attached to posts for filtering and the tag cloud.
// - Key symbols: `BlogTagRecord`, `BlogTagModel`.

import (
	"context"

	"git.thebase.vn/base_hub/internal/orm"
)

const BlogTagModelName = "tbl.website_blog_tag"

// BlogTagRecord is one post label, optionally grouped by a category.
type BlogTagRecord struct {
	orm.BaseModel
	Name       string `bun:"name,notnull" json:"name" meta:"required,index,label:Name,help:Stores the label shown on posts."`
	Slug       string `bun:"slug,notnull" json:"slug" meta:"index,label:Slug,help:Stores the URL-safe key used by /blog/tag/<slug>; generated from the name when empty."`
	CategoryID *int64 `bun:"category_id" json:"category_id" meta:"kind:many2one,model:tbl.website_blog_category,ondelete:SET NULL,index,label:Group,help:Groups this tag in the public tag cloud."`
}

var BlogTagModel = orm.Define[BlogTagRecord](&Models, 20, orm.ModelSpec{
	Name:        BlogTagModelName,
	Label:       "Blog Tags",
	Description: "Labels attached to blog posts",
	Prepare:     prepareBlogTag,
})

func prepareBlogTag(ctx context.Context, op orm.WriteOp) error {
	return prepareSluggedRecord(ctx, op, BlogTagModelName, "tag")
}
