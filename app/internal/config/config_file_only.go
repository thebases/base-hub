package config

// File overview:
// - Responsibility: settings that live ONLY in the YAML config file — never seeded into, read
//   from, or written back through sys.config_param (decision 2026-10-03). A database copy of a
//   secret key would sit next to the ciphertext it protects, be cloned into every service DB,
//   and be readable through the sys.config_param runtime model.
// - Key symbols: `IsFileOnlyConfigKey`, `PurgeFileOnlyConfigRows`, `overlayFileOnlyConfig`.
// - Invariant: every DB->Config or DB->YAML path overlays these values from the loaded file, so
//   SyncRuntimeConfig never blanks them and PersistRuntimeConfigToFile never erases them.

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

// fileOnlyConfigKeys holds canonical (snake_case, dotted) names.
var fileOnlyConfigKeys = map[string]struct{}{
	"app.encrypt_key": {},
}

// IsFileOnlyConfigKey reports whether name (any alias spelling) must never be stored in a DB.
func IsFileOnlyConfigKey(name string) bool {
	_, ok := fileOnlyConfigKeys[canonicalSystemConfigKey(name)]
	return ok
}

// overlayFileOnlyConfig copies every file-only value from the loaded YAML onto configMap, so a
// map rebuilt from sys.config_param rows keeps the file's value.
func overlayFileOnlyConfig(configMap map[string]any) error {
	flat := make(map[string]any)
	flattenConfigMap("", cloneConfigMap(loadedConfigSettings), flat)
	for key, value := range flat {
		if !IsFileOnlyConfigKey(key) {
			continue
		}
		if err := setNestedValue(configMap, canonicalSystemConfigKey(key), value); err != nil {
			return err
		}
	}
	return nil
}

// PurgeFileOnlyConfigRows deletes stored copies of file-only keys (every alias spelling) from db's
// sys.config_param and returns how many rows were removed. Used at home-DB sync and by the
// rotate-encrypt-key CLI for service DBs, which carry cloned rows.
func PurgeFileOnlyConfigRows(ctx context.Context, db bun.IDB) (int, error) {
	var names []string
	if err := db.NewSelect().TableExpr(systemConfigTable).Column("name").Scan(ctx, &names); err != nil {
		return 0, fmt.Errorf("list sys.config_param names: %w", err)
	}
	doomed := make([]string, 0, 1)
	for _, name := range names {
		if IsFileOnlyConfigKey(name) {
			doomed = append(doomed, name)
		}
	}
	if len(doomed) == 0 {
		return 0, nil
	}
	if _, err := db.NewDelete().TableExpr(systemConfigTable).Where("name IN (?)", bun.In(doomed)).Exec(ctx); err != nil {
		return 0, fmt.Errorf("purge file-only sys.config_param rows: %w", err)
	}
	return len(doomed), nil
}
