package models

// File overview:
// - Responsibility: defines `tbl.website_blog_category` — a named group of tags, used to
//   organize the tag cloud on the public blog sidebar.
// - Key symbols: `BlogCategoryRecord`, `BlogCategoryModel`.

import (
	"context"

	"git.thebase.vn/base_hub/internal/orm"
)

const BlogCategoryModelName = "tbl.website_blog_category"

// BlogCategoryRecord is one tag group.
type BlogCategoryRecord struct {
	orm.BaseModel
	Name     string `bun:"name,notnull" json:"name" meta:"required,index,label:Name,help:Stores the group name shown above its tags."`
	Slug     string `bun:"slug,notnull" json:"slug" meta:"index,label:Slug,help:Stores the URL-safe key; generated from the name when empty."`
	Sequence int    `bun:"sequence,notnull,default:10" json:"sequence" meta:"label:Sequence,help:Orders groups in the tag cloud."`
}

var BlogCategoryModel = orm.Define[BlogCategoryRecord](&Models, 10, orm.ModelSpec{
	Name:        BlogCategoryModelName,
	Label:       "Blog Tag Categories",
	Description: "Groups of blog tags",
	Prepare:     prepareBlogCategory,
})

func prepareBlogCategory(ctx context.Context, op orm.WriteOp) error {
	return prepareSluggedRecord(ctx, op, BlogCategoryModelName, "tag-group")
}

// prepareSluggedRecord is the shared create/write rule for the flat name+slug models (category, tag,
// blog): it derives the slug from `name` on create (or when the slug is blanked) and keeps it unique.
func prepareSluggedRecord(ctx context.Context, op orm.WriteOp, modelName, fallback string) error {
	working := op.Values
	isCreate := op.Create
	if isCreate && stringValue(working["name"]) == "" {
		return orm.InvalidParamsf("%s name is required", modelName)
	}
	if len(op.IDs) > 1 && hasRecordMapKey(working, "slug") {
		// One slug per row: a bulk write would give every row the slug allocated for the first one.
		return orm.InvalidParamsf("%s.write of \"slug\" requires exactly one record", modelName)
	}
	excludeID := ""
	if !isCreate {
		excludeID = op.IDs[0]
	}
	if !isCreate && !hasRecordMapKey(working, "slug") {
		return nil
	}
	base := slugify(stringValue(working["slug"]), "")
	if base == "" {
		base = slugify(stringValue(working["name"]), fallback)
	}
	if base == fallback && !isCreate && !hasRecordMapKey(working, "name") {
		// A slug-only blank on an existing row falls back to its stored name.
		rows, readErr := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(modelName, op.Model), op.IDs, []string{"name"})
		if readErr == nil && len(rows) > 0 {
			base = slugify(stringValue(rows[0]["name"]), fallback)
		}
	}
	slug, err := uniqueBlogSlug(ctx, modelName, base, nil, excludeID)
	if err != nil {
		return err
	}
	working["slug"] = slug
	return nil
}
