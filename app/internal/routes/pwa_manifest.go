package routes

// File overview:
// - Responsibility: serves GET /manifest.webmanifest, built on every request from the `pwa.*`
//   rows of the home DB sys.config_param (seeded by modules/base/data/19_pwa_data.json).
// - Key symbols: `RegisterPWAManifestRoute`, `buildPWAManifest`.
// - Pattern: Home DB only (the manifest is a control-plane concern, like the filestore root);
//   missing or malformed rows fall back to built-in defaults so the install prompt never breaks.

import (
	"context"
	"encoding/json"
	"strings"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/gofiber/fiber/v3"
)

const pwaConfigPrefix = "pwa."

// pwaStringKeys maps each string manifest member to its sys.config_param key suffix and fallback.
var pwaStringKeys = []struct{ member, fallback string }{
	{"name", "Base HUB"},
	{"short_name", "Base HUB"},
	{"description", "Base HUB platform"},
	{"id", "/"},
	{"start_url", "/"},
	{"scope", "/"},
	{"display", "standalone"},
	{"orientation", "any"},
	{"theme_color", "#000000"},
	{"background_color", "#ffffff"},
}

// RegisterPWAManifestRoute mounts the dynamic web app manifest; call it before the static handlers
// so it wins over any file of the same name.
func RegisterPWAManifestRoute(app *fiber.App) {
	app.Get("/manifest.webmanifest", handlePWAManifest)
}

func handlePWAManifest(c fiber.Ctx) error {
	manifest := buildPWAManifest(c.Context(), loadPWAParams(c.Context()))
	body, err := json.Marshal(manifest)
	if err != nil {
		log.Errorf("pwa manifest: marshal: %v", err)
		return c.SendStatus(fiber.StatusInternalServerError)
	}
	c.Set(fiber.HeaderContentType, "application/manifest+json; charset=utf-8")
	// Short cache so edited config params show up quickly without hammering the DB.
	c.Set(fiber.HeaderCacheControl, "public, max-age=300")
	return c.Send(body)
}

// PWAThemeColor returns the configured pwa.theme_color (or the default) so the SPA shell's
// <meta name="theme-color"> matches the manifest.
func PWAThemeColor(ctx context.Context) string {
	return buildPWAManifest(ctx, loadPWAParams(ctx))["theme_color"].(string)
}

// loadPWAParams reads every pwa.* row from the home DB, keyed without the prefix.
func loadPWAParams(ctx context.Context) map[string]json.RawMessage {
	params := map[string]json.RawMessage{}
	homeDB := orm.ResolveRegisteredDatabase(strings.TrimSpace(config.HomeDBName()))
	if homeDB == nil || homeDB.DB == nil {
		return params
	}
	var rows []struct {
		Name  string          `bun:"name"`
		Value json.RawMessage `bun:"value"`
	}
	if err := homeDB.DB.NewRaw(
		`SELECT name, value FROM sys_config_param WHERE name LIKE ?`, pwaConfigPrefix+"%",
	).Scan(ctx, &rows); err != nil {
		log.Errorf("pwa manifest: load sys_config_param: %v", err)
		return params
	}
	for _, row := range rows {
		params[strings.TrimPrefix(row.Name, pwaConfigPrefix)] = row.Value
	}
	return params
}

// buildPWAManifest overlays configured values on the defaults; blank strings and non-array icons
// are ignored so a bad row degrades to the default instead of an invalid manifest.
func buildPWAManifest(_ context.Context, params map[string]json.RawMessage) map[string]any {
	manifest := make(map[string]any, len(pwaStringKeys)+1)
	for _, key := range pwaStringKeys {
		value := key.fallback
		var configured string
		if raw, ok := params[key.member]; ok && json.Unmarshal(raw, &configured) == nil && strings.TrimSpace(configured) != "" {
			value = strings.TrimSpace(configured)
		}
		manifest[key.member] = value
	}
	var icons []map[string]any
	if raw, ok := params["icons"]; ok && json.Unmarshal(raw, &icons) == nil && len(icons) > 0 {
		manifest["icons"] = icons
	} else {
		manifest["icons"] = []map[string]any{}
	}
	return manifest
}
