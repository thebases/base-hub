package models

import (
	"context"
	"encoding/json"
	"fmt"

	"git.thebase.vn/base_hub/internal/orm"
	"github.com/uptrace/bun"
)

const WebsiteThemeModelName = "tbl.website_theme"

// WebsiteThemeRecord is a catalog of installable theme presets (colors,
// typography, buttons, inputs, links, layout, advanced CSS variables), backing
// the legacy editor's tbl.website_theme/list|install|update|uninstall capabilities.
// "install" copies Definition onto the target tbl.website_site.Theme jsonb column;
// this catalog row itself is never mutated by a project's own customizations.
type WebsiteThemeRecord struct {
	orm.BaseModel
	Name       string         `bun:"name" json:"name" meta:"index,required,label:Name,help:Stores the theme preset's display name."`
	Slug       string         `bun:"slug" json:"slug" meta:"index,required,label:Slug,help:Stores the stable preset identifier."`
	Definition map[string]any `bun:"definition,type:jsonb,nullzero" json:"definition" meta:"kind:json,label:Definition,help:Stores the theme definition (colors, typography, buttons, inputs, links, layout, advanced CSS variables)."`
	IsBuiltin  bool           `bun:"is_builtin,notnull,default:false" json:"is_builtin" meta:"readonly,label:Built-in,help:Marks presets seeded by the module rather than created by an admin."`
}

var WebsiteThemeModel = orm.Define[WebsiteThemeRecord](&Models, 460, orm.ModelSpec{
	Name:        WebsiteThemeModelName,
	Label:       "Website Themes",
	Description: "Catalog of installable theme presets for website projects",
})

// builtinWebsiteThemes are the presets every database with the website tables gets.
var builtinWebsiteThemes = []struct {
	name, slug string
	definition map[string]any
}{
	{"Default", "default", map[string]any{
		"colors":     map[string]any{"main": "#0f172a", "light": "#f8fafc", "dark": "#020617"},
		"typography": map[string]any{"fontFamily": "system-ui, -apple-system, \"Segoe UI\", sans-serif"},
	}},
	{"Slate", "slate", map[string]any{
		"colors":     map[string]any{"main": "#334155", "light": "#f1f5f9", "dark": "#0f172a"},
		"typography": map[string]any{"fontFamily": "Inter, system-ui, sans-serif"},
	}},
}

// seedBuiltinWebsiteThemes inserts the builtin presets missing (by slug) from this database. It is a
// data migration (website_unique_indexes.go), so it runs on every database the ORM migrates, tenant
// databases included, inside that database's own transaction; it used to run once at boot with
// context.Background(), which only ever reached the default database. Idempotent, and a missing table
// (website never installed here) is a no-op.
func seedBuiltinWebsiteThemes(ctx context.Context, tx bun.IDB) error {
	table := "tbl_website_theme" // literal: the spec var no longer exists (canonical name of tbl.website_theme)
	var exists bool
	if err := tx.NewRaw("SELECT to_regclass(?) IS NOT NULL", table).Scan(ctx, &exists); err != nil {
		return fmt.Errorf("check %s table: %w", table, err)
	}
	if !exists {
		return nil
	}
	for _, preset := range builtinWebsiteThemes {
		definition, err := json.Marshal(preset.definition)
		if err != nil {
			return err
		}
		if _, err := tx.NewRaw(`INSERT INTO ? (name, slug, is_builtin, definition, active, created_at, updated_at)
			SELECT ?, ?, true, ?::jsonb, true, now(), now()
			WHERE NOT EXISTS (SELECT 1 FROM ? WHERE slug = ?)`,
			bun.Ident(table), preset.name, preset.slug, string(definition), bun.Ident(table), preset.slug).Exec(ctx); err != nil {
			return fmt.Errorf("seed theme %s: %w", preset.slug, err)
		}
	}
	return nil
}
