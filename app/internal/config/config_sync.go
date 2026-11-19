package config

// File overview:
// - Responsibility: syncs runtime config with the system config table and the config file (load/persist/flatten/expand).
// - Key symbols: `SyncRuntimeConfig`, `InitialMasterDataImportEnabled`, `SetInitialMasterDataImportEnabled`, `PersistRuntimeConfigToFile`.

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	servicelog "git.thebase.vn/base_hub/internal/services/log"
	"github.com/spf13/viper"
	"github.com/uptrace/bun"
	"gopkg.in/yaml.v3"
)

// SyncRuntimeConfig ensures sys.config_param is seeded from the loaded file config
// and then refreshes the in-memory Config from the database-backed source.
func SyncRuntimeConfig(ctx context.Context, db *bun.DB, forceFileOverride bool) error {
	if db == nil {
		servicelog.Errorf("bun db is not initialized")
		return fmt.Errorf("bun db is not initialized")
	}

	if err := ensureSystemConfigTable(ctx, db); err != nil {
		return err
	}

	// File-only secrets (config_file_only.go) must not survive in the DB from older builds.
	if _, err := PurgeFileOnlyConfigRows(ctx, db); err != nil {
		return err
	}

	fileSettings := cloneConfigMap(loadedConfigSettings)
	fileRows := flattenConfigMapToRows(fileSettings)

	rowCount, err := db.NewSelect().Model((*systemConfigRow)(nil)).Count(ctx)
	if err != nil {
		servicelog.Errorf("count sys.config_param rows: %v", err)
		return fmt.Errorf("count sys.config_param rows: %w", err)
	}

	switch {
	case rowCount == 0:
		if err := upsertSystemConfigRows(ctx, db, fileRows); err != nil {
			return err
		}
		return decodeConfigMap(fileSettings)
	case forceFileOverride:
		if err := upsertSystemConfigRows(ctx, db, fileRows); err != nil {
			return err
		}
		return decodeConfigMap(fileSettings)
	default:
		dbRows, err := listSystemConfigRows(ctx, db)
		if err != nil {
			return err
		}

		// Backfill newly added file keys without overriding the existing DB truth.
		missingRows := missingSystemConfigRows(fileRows, dbRows)
		if len(missingRows) > 0 {
			if err := upsertSystemConfigRows(ctx, db, missingRows); err != nil {
				return err
			}
			dbRows = append(dbRows, missingRows...)
		}

		configMap, err := expandSystemConfigRows(dbRows)
		if err != nil {
			return err
		}
		// DB rows never carry file-only secrets; take them from the loaded YAML.
		if err := overlayFileOnlyConfig(configMap); err != nil {
			return err
		}
		return decodeConfigMap(configMap)
	}
}

// InitialMasterDataImportEnabled reads the startup gate for the one-time
// master-data bootstrap path from sys.config_param.
func InitialMasterDataImportEnabled(ctx context.Context, db *bun.DB) (bool, error) {
	if db == nil {
		servicelog.Errorf("bun db is not initialized")
		return false, fmt.Errorf("bun db is not initialized")
	}

	// Keep the config table available here too because startup asks for this
	// flag before the runtime starts serving requests and before operators have
	// a chance to repair missing tables manually.
	if err := ensureSystemConfigTable(ctx, db); err != nil {
		return false, err
	}

	rows, err := listSystemConfigRows(ctx, db)
	if err != nil {
		return false, err
	}
	var fallback *systemConfigRow
	for _, row := range rows {
		canonicalName := canonicalSystemConfigKey(row.Name)
		if canonicalName != initialImportConfigKey {
			continue
		}
		if row.Name == initialImportConfigKey {
			return configParamBoolValue(row.Value), nil
		}
		if fallback == nil {
			rowCopy := row
			fallback = &rowCopy
		}
	}
	if fallback == nil {
		return false, nil
	}
	return configParamBoolValue(fallback.Value), nil
}

// SetInitialMasterDataImportEnabled stores the startup gate that decides
// whether the backend should run the bootstrap master-data import path.
func SetInitialMasterDataImportEnabled(ctx context.Context, db *bun.DB, enabled bool) error {
	if db == nil {
		servicelog.Errorf("bun db is not initialized")
		return fmt.Errorf("bun db is not initialized")
	}

	// Keep writes behind the shared upsert helper so startup uses the same
	// conflict handling and table bootstrapping rules as the rest of config sync.
	if err := ensureSystemConfigTable(ctx, db); err != nil {
		return err
	}
	if err := upsertSystemConfigRows(ctx, db, []systemConfigRow{{
		Name:  initialImportConfigKey,
		Value: enabled,
	}}); err != nil {
		return err
	}
	return nil
}

// PersistRuntimeConfigToFile writes the current sys.config_param contents back to YAML.
func PersistRuntimeConfigToFile(ctx context.Context, db *bun.DB, path string) error {
	if db == nil {
		servicelog.Errorf("bun db is not initialized")
		return fmt.Errorf("bun db is not initialized")
	}

	if path == "" {
		path = loadedConfigPath
	}
	if strings.TrimSpace(path) == "" {
		servicelog.Errorf("config path is empty")
		return fmt.Errorf("config path is empty")
	}

	rows, err := listSystemConfigRows(ctx, db)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		servicelog.Errorf("sys.config_param is empty")
		return fmt.Errorf("sys.config_param is empty")
	}

	configMap, err := expandSystemConfigRows(rows)
	if err != nil {
		return err
	}
	// Rewriting the YAML from DB rows alone would erase file-only secrets from the file.
	if err := overlayFileOnlyConfig(configMap); err != nil {
		return err
	}

	data, err := yaml.Marshal(camelizeConfigMapKeys(normalizeConfigStructure(configMap)))
	if err != nil {
		servicelog.Errorf("marshal sys.config_param to yaml: %v", err)
		return fmt.Errorf("marshal sys.config_param to yaml: %w", err)
	}

	if err := writeConfigFile(path, data); err != nil {
		servicelog.Errorf("write yaml config file: %v", err)
		return fmt.Errorf("write yaml config file: %w", err)
	}
	return nil
}

// PersistRuntimeConfig writes sys.config_param to the last loaded config file path.
func PersistRuntimeConfig(ctx context.Context, db *bun.DB) error {
	return PersistRuntimeConfigToFile(ctx, db, loadedConfigPath)
}

// ensureSystemConfigTable ensures the system config table handled by this package.
func ensureSystemConfigTable(ctx context.Context, db *bun.DB) error {
	if _, err := db.NewCreateTable().Model((*systemConfigRow)(nil)).IfNotExists().Exec(ctx); err != nil {
		servicelog.Errorf("create sys.config_param table: %v", err)
		return fmt.Errorf("create sys.config_param table: %w", err)
	}
	if _, err := db.ExecContext(
		ctx,
		fmt.Sprintf(
			`INSERT INTO %s (name, value)
			 SELECT legacy.name, legacy.value
			 FROM %s AS legacy
			 ON CONFLICT (name) DO NOTHING`,
			systemConfigTable,
			legacySystemConfigTable,
		),
	); err != nil && !strings.Contains(strings.ToLower(err.Error()), "does not exist") {
		servicelog.Errorf("backfill sys.config_param from legacy sys.config: %v", err)
		return fmt.Errorf("backfill sys.config_param from legacy sys.config: %w", err)
	}
	return nil
}

// listSystemConfigRows lists the system config rows handled by this package.
func listSystemConfigRows(ctx context.Context, db *bun.DB) ([]systemConfigRow, error) {
	var rows []systemConfigRow
	if err := db.NewSelect().
		Model(&rows).
		OrderExpr("? ASC", bun.Ident("name")).
		Scan(ctx); err != nil {
		servicelog.Errorf("load sys.config_param rows: %v", err)
		return nil, fmt.Errorf("load sys.config_param rows: %w", err)
	}
	return rows, nil
}

func upsertSystemConfigRows(ctx context.Context, db *bun.DB, rows []systemConfigRow) error {
	if len(rows) == 0 {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		servicelog.Errorf("begin sys.config_param transaction: %v", err)
		return fmt.Errorf("begin sys.config_param transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var existingRows []systemConfigRow
	if err = tx.NewSelect().
		Model(&existingRows).
		Scan(ctx); err != nil {
		servicelog.Errorf("load existing sys.config_param rows: %v", err)
		return fmt.Errorf("load existing sys.config_param rows: %w", err)
	}

	aliasNamesByCanonical := make(map[string][]string)
	for _, existing := range existingRows {
		canonicalName := canonicalSystemConfigKey(existing.Name)
		if canonicalName == "" || existing.Name == canonicalName {
			continue
		}
		aliasNamesByCanonical[canonicalName] = append(aliasNamesByCanonical[canonicalName], existing.Name)
	}

	for _, row := range rows {
		rowCopy := row
		rowCopy.Name = canonicalSystemConfigKey(rowCopy.Name)
		// Drop legacy aliases before the canonical upsert so sys.config_param
		// converges on one snake_case row name for each logical config key.
		if aliasNames := uniqueStrings(aliasNamesByCanonical[rowCopy.Name]); len(aliasNames) > 0 {
			if _, err = tx.NewDelete().
				Model((*systemConfigRow)(nil)).
				Where("name IN (?)", bun.In(aliasNames)).
				Exec(ctx); err != nil {
				servicelog.Errorf("dedupe sys.config_param aliases for %q: %v", rowCopy.Name, err)
				return fmt.Errorf("dedupe sys.config_param aliases for %q: %w", rowCopy.Name, err)
			}
			delete(aliasNamesByCanonical, rowCopy.Name)
		}
		if _, err = tx.NewInsert().
			Model(&rowCopy).
			On("CONFLICT (name) DO UPDATE").
			Set("value = EXCLUDED.value").
			Exec(ctx); err != nil {
			servicelog.Errorf("upsert sys.config_param name %q: %v", row.Name, err)
			return fmt.Errorf("upsert sys.config_param name %q: %w", row.Name, err)
		}
	}

	if err = tx.Commit(); err != nil {
		servicelog.Errorf("commit sys.config_param transaction: %v", err)
		return fmt.Errorf("commit sys.config_param transaction: %w", err)
	}
	return nil
}

// flattenConfigMapToRows flattens the config map to rows handled by this package.
func flattenConfigMapToRows(configMap map[string]any) []systemConfigRow {
	flatMap := make(map[string]any)
	flattenConfigMap("", configMap, flatMap)

	keys := make([]string, 0, len(flatMap))
	for key := range flatMap {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	rows := make([]systemConfigRow, 0, len(keys))
	for _, key := range keys {
		// File-only secrets are never seeded into sys.config_param.
		if IsFileOnlyConfigKey(key) {
			continue
		}
		rows = append(rows, systemConfigRow{
			Name:  canonicalSystemConfigKey(key),
			Value: flatMap[key],
		})
	}
	return rows
}

// configParamBoolValue coerces persisted JSON-backed config values into the
// boolean meaning the startup gate expects.
func configParamBoolValue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		parsed, err := strconv.ParseBool(strings.ToLower(strings.TrimSpace(typed)))
		return err == nil && parsed
	case fmt.Stringer:
		parsed, err := strconv.ParseBool(strings.ToLower(strings.TrimSpace(typed.String())))
		return err == nil && parsed
	case int:
		return typed != 0
	case int8:
		return typed != 0
	case int16:
		return typed != 0
	case int32:
		return typed != 0
	case int64:
		return typed != 0
	case uint:
		return typed != 0
	case uint8:
		return typed != 0
	case uint16:
		return typed != 0
	case uint32:
		return typed != 0
	case uint64:
		return typed != 0
	case float32:
		return typed != 0
	case float64:
		return typed != 0
	default:
		return false
	}
}

// flattenConfigMap flattens the config map handled by this package.
func flattenConfigMap(prefix string, value any, output map[string]any) {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			nextPrefix := key
			if prefix != "" {
				nextPrefix = prefix + "." + key
			}
			flattenConfigMap(nextPrefix, typed[key], output)
		}
	default:
		if prefix == "" {
			return
		}
		output[prefix] = normalizeConfigValue(typed)
	}
}

func expandSystemConfigRows(rows []systemConfigRow) (map[string]any, error) {
	result := make(map[string]any)
	for _, row := range rows {
		canonicalName := canonicalSystemConfigKey(row.Name)
		// A stray DB copy of a file-only secret is never read back (the file is the only source).
		if canonicalName == "" || IsFileOnlyConfigKey(canonicalName) {
			continue
		}
		if err := setNestedValue(result, canonicalName, normalizeConfigValue(row.Value)); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// setNestedValue stores the nested value handled by this package.
func setNestedValue(target map[string]any, path string, value any) error {
	parts := strings.Split(path, ".")
	current := target

	for index, part := range parts {
		if part == "" {
			servicelog.Errorf("invalid empty config key in path %q", path)
			return fmt.Errorf("invalid empty config key in path %q", path)
		}

		if index == len(parts)-1 {
			current[part] = value
			return nil
		}

		nextValue, exists := current[part]
		if !exists {
			nextMap := make(map[string]any)
			current[part] = nextMap
			current = nextMap
			continue
		}

		nextMap, ok := nextValue.(map[string]any)
		if !ok {
			servicelog.Errorf("config path collision at %q", path)
			return fmt.Errorf("config path collision at %q", path)
		}
		current = nextMap
	}
	return nil
}

// decodeConfigMap decodes the config map handled by this package.
func decodeConfigMap(configMap map[string]any) error {
	configMap = normalizeConfigStructure(canonicalizeConfigMapKeys(configMap).(map[string]any))

	instance := viper.New()
	if err := instance.MergeConfigMap(configMap); err != nil {
		servicelog.Errorf("merge runtime config map: %v", err)
		return fmt.Errorf("merge runtime config map: %w", err)
	}

	var nextConfig AppConfig
	if err := instance.Unmarshal(&nextConfig); err != nil {
		servicelog.Errorf("decode runtime config map: %v", err)
		return fmt.Errorf("decode runtime config map: %w", err)
	}

	Set(nextConfig)
	return nil
}
