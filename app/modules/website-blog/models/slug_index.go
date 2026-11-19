package models

// File overview:
// - Responsibility: database-level slug uniqueness for posts (`(blog_id, slug)`), backing the Go
//   allocation in `uniqueBlogSlug`, and the mapping of its violation to a Conflict.
// - Key symbols: `blogPostSlugIndex`, `blogPostSlugConflict`.

import (
	"context"
	"errors"

	"git.thebase.vn/base_hub/internal/orm"
	websitemodels "git.thebase.vn/base_hub/modules/website/models"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/uptrace/bun"
)

// blogPostSlugIndex catches two writers that both passed uniqueBlogSlug's read before either wrote.
const blogPostSlugIndex = "tbl_website_blog_post_blog_slug_uniq"

func init() {
	orm.RegisterLegacyDataMigration("website-blog: unique post slug per blog", ensureBlogPostSlugIndex)
}

// ensureBlogPostSlugIndex creates the partial unique index unless existing duplicates are found
// (then they are logged with ids and boot continues; no row is changed).
func ensureBlogPostSlugIndex(ctx context.Context, tx bun.IDB) error {
	return websitemodels.EnsureWebsiteUniqueIndex(ctx, tx, websitemodels.UniqueIndexSpec{
		Table: "tbl_website_blog_post", Index: blogPostSlugIndex,
		Columns: []string{"blog_id", "slug"}, Where: "blog_id IS NOT NULL AND slug <> ''",
	})
}

// blogPostSlugConflict maps a unique violation (SQLSTATE 23505) on blogPostSlugIndex to a -32009
// Conflict; other errors pass through unchanged.
func blogPostSlugConflict(err error, values orm.RecordMap) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == blogPostSlugIndex {
		return orm.Conflictf("%s slug %q is already used in this blog; retry the save", BlogPostModelName, stringValue(values["slug"]))
	}
	return err
}
