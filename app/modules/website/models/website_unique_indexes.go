package models

import (
	"context"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

// websitePageRouteIndex backs the Go route check in validateWebsitePageValues: the check reads
// before the write, so two concurrent writers can both pass it; the index makes the second fail.
const websitePageRouteIndex = "tbl_website_page_site_route_uniq"

func init() {
	orm.RegisterLegacyDataMigration("website: unique route per site", ensureWebsitePageRouteIndex)
	orm.RegisterLegacyDataMigration("website: seed builtin theme presets", seedBuiltinWebsiteThemes)
}

func ensureWebsitePageRouteIndex(ctx context.Context, tx bun.IDB) error {
	return EnsureWebsiteUniqueIndex(ctx, tx, UniqueIndexSpec{
		Table: "tbl_website_page", Index: websitePageRouteIndex,
		Columns: []string{"site_id", "route_path"}, Where: "site_id IS NOT NULL AND route_path <> ''",
	})
}

// UniqueIndexSpec describes a partial unique index created by EnsureWebsiteUniqueIndex. Columns and
// Where are trusted constants, never user input.
type UniqueIndexSpec struct {
	Table   string
	Index   string
	Columns []string
	Where   string
}

// EnsureWebsiteUniqueIndex creates spec's partial unique index when the table has no duplicate rows.
// With duplicates it logs them (ids included) and returns nil: boot continues, no row is changed or
// deleted, the application checks still guard new writes, and the index is retried on the next
// migration run once an operator has resolved the duplicates. A missing table (module never
// installed on this database) is a no-op. Runs inside the data-migration transaction.
func EnsureWebsiteUniqueIndex(ctx context.Context, tx bun.IDB, spec UniqueIndexSpec) error {
	var tableExists, indexExists bool
	if err := tx.NewRaw("SELECT to_regclass(?) IS NOT NULL, to_regclass(?) IS NOT NULL", spec.Table, spec.Index).
		Scan(ctx, &tableExists, &indexExists); err != nil {
		return fmt.Errorf("check %s: %w", spec.Index, err)
	}
	if !tableExists || indexExists {
		return nil
	}
	columns := strings.Join(spec.Columns, ", ")
	var duplicates []string
	if err := tx.NewRaw(fmt.Sprintf(`SELECT concat_ws(' / ', %s) || ': ids ' || string_agg(id::text, ',' ORDER BY id)
		FROM %s WHERE %s GROUP BY %s HAVING COUNT(*) > 1 ORDER BY 1 LIMIT 50`,
		columns, spec.Table, spec.Where, columns)).Scan(ctx, &duplicates); err != nil {
		return fmt.Errorf("find duplicates for %s: %w", spec.Index, err)
	}
	if len(duplicates) > 0 {
		log.Warnf("website: unique index %s not created: duplicate (%s) rows in %s (resolve them; the index is retried on the next boot): %s",
			spec.Index, columns, spec.Table, strings.Join(duplicates, "; "))
		return nil
	}
	if _, err := tx.NewRaw(fmt.Sprintf("CREATE UNIQUE INDEX IF NOT EXISTS %s ON %s (%s) WHERE %s",
		spec.Index, spec.Table, columns, spec.Where)).Exec(ctx); err != nil {
		return fmt.Errorf("create %s: %w", spec.Index, err)
	}
	log.Infof("website: created unique index %s on %s (%s)", spec.Index, spec.Table, columns)
	return nil
}
