package orm

// File overview:
// - Responsibility: implements the metadata-driven ORM/runtime kernel for model registry migrate, where storage, runtime dispatch, and security rules stay centralized.
// - Key symbols: `modelMigrationPlan`, `AutoMigrateRegisteredModels`, `AutoMigrateRegisteredModelsToDB`, `ensureSystemMetadataTables`.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate/sqlschema"
)

type modelMigrationPlan struct {
	metadata *ModelMetadata
}

// AutoMigrateRegisteredModels creates metadata tables first, then creates
// registered model tables in a deterministic dependency-friendly order, and
// finally syncs model metadata into the sys_model catalog tables.
func AutoMigrateRegisteredModels(ctx context.Context) error {
	db := BunDBFromContext(ctx)
	if db == nil {
		log.Errorf("BunDB is not initialized")
		return fmt.Errorf("BunDB is not initialized")
	}
	return AutoMigrateRegisteredModelsToDB(ctx, db, defaultModelRegistry)
}

// migratePrereconcilePhases run in order before any table is reconciled.
// Module-declared renames/NULL backfills must land before reconcile for the
// same reason legacy ones do.
var migratePrereconcilePhases = []func(ctx context.Context, db *bun.DB) error{
	ensureSystemMetadataTables,
	ensureLegacyModelNameRenames,
	ensureLegacyColumnRenames,
	ensureModuleLegacyColumnMigrations,
}

// AutoMigrateRegisteredModelsToDB executes the registered-model migration
// sequence against the provided Bun database: pre-reconcile phases, one
// table plus relation tables per model, then the sys_model catalog sync.
func AutoMigrateRegisteredModelsToDB(ctx context.Context, db *bun.DB, registry *ModelRegistry) error {
	if db == nil {
		log.Errorf("bun db is nil")
		return fmt.Errorf("bun db is nil")
	}
	if registry == nil {
		log.Errorf("model registry is nil")
		return fmt.Errorf("model registry is nil")
	}

	plans := registry.migrationPlans()
	// Order is load-bearing: metadata tables first, then every rename/backfill,
	// so reconcile below always sees the final column and model names.
	for _, phase := range migratePrereconcilePhases {
		if err := phase(ctx, db); err != nil {
			return err
		}
	}

	// Inspect the live schema once up front instead of per model: the naive
	// per-model path ran a full pg_catalog scan (all tables/columns/indexes)
	// inside reconcileModelSchema for every one of the ~100+ registered
	// models, turning a single-digit-second migration into 30+ seconds on a
	// fresh boot. schemaCache serves that one snapshot to every plan below
	// and is refreshed only for the handful of tables mutated in-place by
	// legacy widen/backfill steps inside ensureModelTable.
	schemaCache, err := newDatabaseSchemaCache(ctx, db)
	if err != nil {
		return err
	}

	for _, plan := range plans {
		if err := ensureModelTable(ctx, db, plan, schemaCache); err != nil {
			return err
		}
		if err := ensureModelRelationTables(ctx, db, plan); err != nil {
			return err
		}
	}
	// Tables now match the registered models, so module data fixes can address them.
	runModuleDataMigrations(ctx, db)
	return SyncModelRegistryToDB(ctx, db, registry.List())
}

func (r *ModelRegistry) migrationPlans() []modelMigrationPlan {
	if r == nil {
		return nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	plans := make([]modelMigrationPlan, 0, len(r.ordered))
	for _, name := range r.ordered {
		meta := r.byName[name]
		if meta == nil {
			continue
		}
		plans = append(plans, modelMigrationPlan{metadata: cloneModelMetadata(meta)})
	}

	sort.Slice(plans, func(i, j int) bool {
		left := plans[i].metadata
		right := plans[j].metadata
		leftRank := modelMigrationRank(left.Name)
		rightRank := modelMigrationRank(right.Name)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		return left.Name < right.Name
	})
	return plans
}

// ensureSystemMetadataTables ensures the system metadata tables handled by this package.
func ensureSystemMetadataTables(ctx context.Context, db *bun.DB) error {
	// Walk each any{(*systemmodelrow)(nil), (*systemmodelfieldrow)(nil) entry in order so validation and side effects stay scoped to the current element.
	for _, model := range []any{(*systemModelRow)(nil), (*systemModelFieldRow)(nil)} {
		if _, err := db.NewCreateTable().Model(model).IfNotExists().Exec(ctx); err != nil {
			return err
		}
	}
	if err := ensureSystemMetadataSchema(ctx, db); err != nil {
		return err
	}
	return nil
}

// ensureSystemMetadataSchema ensures the system metadata schema handled by this package.
func ensureSystemMetadataSchema(ctx context.Context, db *bun.DB) error {
	type statement struct {
		sql  string
		args []any
	}
	statements := []statement{
		{sql: "ALTER TABLE ? ADD COLUMN IF NOT EXISTS id BIGSERIAL", args: []any{bun.Ident("sys_model")}},
		{sql: "ALTER TABLE ? ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ", args: []any{bun.Ident("sys_model")}},
		{sql: "ALTER TABLE ? ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ", args: []any{bun.Ident("sys_model")}},
		{sql: "ALTER TABLE ? ADD COLUMN IF NOT EXISTS bid TEXT", args: []any{bun.Ident("sys_model")}},
		{sql: "ALTER TABLE ? ADD COLUMN IF NOT EXISTS label TEXT", args: []any{bun.Ident("sys_model")}},
		{sql: "ALTER TABLE ? ADD COLUMN IF NOT EXISTS description TEXT", args: []any{bun.Ident("sys_model")}},
		{sql: "ALTER TABLE ? ADD COLUMN IF NOT EXISTS storage_backend TEXT", args: []any{bun.Ident("sys_model")}},
		{sql: "ALTER TABLE ? ADD COLUMN IF NOT EXISTS storage_name TEXT", args: []any{bun.Ident("sys_model")}},
		{sql: "ALTER TABLE ? DROP COLUMN IF EXISTS go_type", args: []any{bun.Ident("sys_model")}},
		{sql: "ALTER TABLE ? ADD COLUMN IF NOT EXISTS helper_data TEXT", args: []any{bun.Ident("sys_model")}},
		{sql: "ALTER TABLE ? ADD COLUMN IF NOT EXISTS json_dto JSONB", args: []any{bun.Ident("sys_model")}},
		{sql: "ALTER TABLE ? ADD COLUMN IF NOT EXISTS registered_at TIMESTAMPTZ", args: []any{bun.Ident("sys_model")}},
		{sql: "ALTER TABLE ? ADD COLUMN IF NOT EXISTS synced_at TIMESTAMPTZ", args: []any{bun.Ident("sys_model")}},
		{sql: "ALTER TABLE ? ADD COLUMN IF NOT EXISTS status TEXT", args: []any{bun.Ident("sys_model")}},
		{sql: "ALTER TABLE ? ADD COLUMN IF NOT EXISTS id BIGSERIAL", args: []any{bun.Ident("sys_model_field")}},
		{sql: "ALTER TABLE ? ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ", args: []any{bun.Ident("sys_model_field")}},
		{sql: "ALTER TABLE ? ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ", args: []any{bun.Ident("sys_model_field")}},
		{sql: "ALTER TABLE ? ADD COLUMN IF NOT EXISTS bid TEXT", args: []any{bun.Ident("sys_model_field")}},
		{sql: "ALTER TABLE ? ADD COLUMN IF NOT EXISTS go_name TEXT", args: []any{bun.Ident("sys_model_field")}},
		{sql: "ALTER TABLE ? ADD COLUMN IF NOT EXISTS label TEXT", args: []any{bun.Ident("sys_model_field")}},
		{sql: "ALTER TABLE ? ADD COLUMN IF NOT EXISTS data_type TEXT", args: []any{bun.Ident("sys_model_field")}},
		{sql: "ALTER TABLE ? ADD COLUMN IF NOT EXISTS bson_name TEXT", args: []any{bun.Ident("sys_model_field")}},
		{sql: "ALTER TABLE ? ADD COLUMN IF NOT EXISTS json_name TEXT", args: []any{bun.Ident("sys_model_field")}},
		{sql: "ALTER TABLE ? ADD COLUMN IF NOT EXISTS bun_column TEXT", args: []any{bun.Ident("sys_model_field")}},
		{sql: "ALTER TABLE ? ADD COLUMN IF NOT EXISTS is_primary_key BOOLEAN", args: []any{bun.Ident("sys_model_field")}},
		{sql: "ALTER TABLE ? ADD COLUMN IF NOT EXISTS is_required BOOLEAN", args: []any{bun.Ident("sys_model_field")}},
		{sql: "ALTER TABLE ? ADD COLUMN IF NOT EXISTS field_index INTEGER", args: []any{bun.Ident("sys_model_field")}},
		{sql: "ALTER TABLE ? ADD COLUMN IF NOT EXISTS status TEXT", args: []any{bun.Ident("sys_model_field")}},
	}
	for _, stmt := range statements {
		if _, err := db.NewRaw(stmt.sql, stmt.args...).Exec(ctx); err != nil {
			return err
		}
	}
	// Walk each ident("sys model field") entry in order so validation and side effects stay scoped to the current element.
	for _, stmt := range []statement{
		{sql: "UPDATE ? SET created_at = NOW(), updated_at = NOW() WHERE created_at IS NULL OR updated_at IS NULL", args: []any{bun.Ident("sys_model")}},
		{sql: "UPDATE ? SET label = COALESCE(NULLIF(label, ''), name), description = COALESCE(description, ''), storage_backend = COALESCE(NULLIF(storage_backend, ''), 'bun'), storage_name = COALESCE(storage_name, ''), registered_at = COALESCE(registered_at, NOW()), synced_at = COALESCE(synced_at, NOW()), status = COALESCE(NULLIF(status, ''), 'INIT')", args: []any{bun.Ident("sys_model")}},
		{sql: "UPDATE ? SET created_at = NOW(), updated_at = NOW() WHERE created_at IS NULL OR updated_at IS NULL", args: []any{bun.Ident("sys_model_field")}},
		{sql: "UPDATE ? SET go_name = COALESCE(go_name, ''), label = COALESCE(NULLIF(label, ''), field_name), data_type = COALESCE(data_type, ''), bson_name = COALESCE(bson_name, ''), json_name = COALESCE(json_name, ''), bun_column = COALESCE(bun_column, ''), is_primary_key = COALESCE(is_primary_key, FALSE), is_required = COALESCE(is_required, FALSE), field_index = COALESCE(field_index, 0), status = COALESCE(NULLIF(status, ''), 'INIT')", args: []any{bun.Ident("sys_model_field")}},
		{sql: "CREATE UNIQUE INDEX IF NOT EXISTS sys_model_id_key ON ? (id)", args: []any{bun.Ident("sys_model")}},
		{sql: "CREATE UNIQUE INDEX IF NOT EXISTS sys_model_name_key ON ? (name)", args: []any{bun.Ident("sys_model")}},
		{sql: "CREATE INDEX IF NOT EXISTS idx_sys_model_storage_backend ON ? (storage_backend)", args: []any{bun.Ident("sys_model")}},
		{sql: "CREATE INDEX IF NOT EXISTS idx_sys_model_storage_name ON ? (storage_name)", args: []any{bun.Ident("sys_model")}},
		{sql: "CREATE UNIQUE INDEX IF NOT EXISTS sys_model_field_id_key ON ? (id)", args: []any{bun.Ident("sys_model_field")}},
		{sql: "CREATE UNIQUE INDEX IF NOT EXISTS sys_model_field_model_name_field_name_key ON ? (model_name, field_name)", args: []any{bun.Ident("sys_model_field")}},
		{sql: "CREATE INDEX IF NOT EXISTS idx_sys_model_field_bun_column ON ? (bun_column)", args: []any{bun.Ident("sys_model_field")}},
	} {
		if _, err := db.NewRaw(stmt.sql, stmt.args...).Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

// ensureModelTable ensures the model table handled by this package.
func ensureModelTable(ctx context.Context, db *bun.DB, plan modelMigrationPlan, schemaCache *databaseSchemaCache) error {
	if plan.metadata == nil {
		return nil
	}
	if plan.metadata.goReflectType == nil {
		log.Errorf("orm: model %q is missing a Go type for auto-migration", plan.metadata.Name)
		return fmt.Errorf("orm: model %q is missing a Go type for auto-migration", plan.metadata.Name)
	}
	if strings.TrimSpace(plan.metadata.StorageName) == "" {
		log.Errorf("orm: model %q is missing a storage table name", plan.metadata.Name)
		return fmt.Errorf("orm: model %q is missing a storage table name", plan.metadata.Name)
	}

	tableName := strings.TrimSpace(plan.metadata.StorageName)

	// A table this database has never seen before (schemaCache has no entry
	// for it) is about to go through a real CREATE TABLE, unlike an existing
	// table where IF NOT EXISTS short-circuits before Postgres validates the
	// column list. Guard the field count here too: without this, a
	// metadata-driven/published model whose live field count alone exceeds
	// the 1600 hard cap surfaces as a bare, table-name-less SQLSTATE 54011
	// straight out of Exec below, aborting boot with no indication of which
	// model caused it — the same failure mode guardProjectedColumnCount
	// already fixed for the ALTER path, but that guard never runs for a
	// table that doesn't exist yet.
	if _, known := schemaCache.tables[tableName]; !known {
		if err := guardNewTableColumnCount(plan.metadata.Name, tableName, plan.metadata.goReflectType); err != nil {
			return err
		}
	}

	modelValue := reflect.New(plan.metadata.goReflectType).Interface()
	// A table created here is absent from the shared pre-loop schemaCache
	// snapshot, but that's fine: reconcileModelSchema treats an unknown table
	// as nil/no-op below, and a table just created from this exact Go struct
	// already matches its target shape, so there is nothing left to reconcile.
	_, err := db.NewCreateTable().
		Model(modelValue).
		ModelTableExpr("?", bun.Ident(plan.metadata.StorageName)).
		IfNotExists().
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("orm: create table for %s (table %s): %w", plan.metadata.Name, tableName, err)
	}
	if err := ensureLegacyLongTextColumns(ctx, db, plan.metadata); err != nil {
		return err
	}
	if err := ensureLegacyAddedColumns(ctx, db, plan.metadata); err != nil {
		return err
	}
	// Legacy widen/backfill above mutate this table's live columns in place;
	// refresh the cached snapshot for just this table so the reconciler below
	// diffs against current state instead of stale pre-loop data.
	if plan.metadata.StorageName == "ui_view" || plan.metadata.StorageName == "tbl_currency" {
		if err := schemaCache.refresh(ctx, db, tableName); err != nil {
			return err
		}
	}
	if err := reconcileModelSchema(ctx, db, plan.metadata, schemaCache); err != nil {
		return err
	}
	return ensureTranslatedColumns(ctx, db, plan.metadata)
}

// ensureModelRelationTables ensures the model relation tables handled by this package.
func ensureModelRelationTables(ctx context.Context, db *bun.DB, plan modelMigrationPlan) error {
	if db == nil || plan.metadata == nil {
		return nil
	}

	// Materialize declared many2many join tables up front so runtime readback
	// and prefetch paths never fail on first import/read of metadata-backed
	// relations such as ui.view.group_ids.
	for _, field := range plan.metadata.Fields {
		if field.Kind != FieldKindManyToMany {
			continue
		}
		if strings.TrimSpace(field.RelationTable) == "" {
			continue
		}
		if strings.TrimSpace(field.RelationColumn) == "" || strings.TrimSpace(field.RelationKey) == "" {
			continue
		}
		if _, err := db.NewRaw(
			"CREATE TABLE IF NOT EXISTS ? (? bigint NOT NULL, ? bigint NOT NULL)",
			bun.Ident(field.RelationTable),
			bun.Ident(field.RelationColumn),
			bun.Ident(field.RelationKey),
		).Exec(ctx); err != nil {
			log.Errorf("orm: ensure relation table %s for %s.%s: %v", field.RelationTable, plan.metadata.Name, field.Name, err)
			return fmt.Errorf("orm: ensure relation table %s for %s.%s: %w", field.RelationTable, plan.metadata.Name, field.Name, err)
		}
	}
	return nil
}

func modelMigrationRank(modelName string) int {
	switch {
	case strings.HasPrefix(modelName, "sys."):
		return 0
	case strings.HasPrefix(modelName, "base."):
		return 1
	case strings.HasPrefix(modelName, "tbl."), strings.HasPrefix(modelName, "res."):
		// Identity/catalog models (tbl.company, tbl.lang, ...) are FK targets
		// for module models, so they must exist before the rest. The legacy
		// res. prefix keeps the same rank so any DB-defined runtime model that
		// still uses it migrates in the old order.
		return 2
	default:
		return 3
	}
}

// ensureLegacyLongTextColumns ensures the legacy long text columns handled by this package.
func ensureLegacyLongTextColumns(ctx context.Context, db *bun.DB, metadata *ModelMetadata) error {
	if metadata == nil {
		return nil
	}
	if strings.TrimSpace(metadata.StorageName) != "ui_view" {
		return nil
	}

	// Walk each string{"arch", "arch base", "arch db", "arch fs", "arch prev" entry in order so validation and side effects stay scoped to the current element.
	for _, column := range []string{"arch", "arch_base", "arch_db", "arch_fs", "arch_prev"} {
		needsWiden, err := legacyColumnNeedsTextWiden(ctx, db, metadata.StorageName, column)
		if err != nil {
			return err
		}
		if !needsWiden {
			continue
		}
		// Widen legacy XML storage columns in place so existing databases can
		// keep full form/list/search view payloads without truncation.
		if _, err := db.NewRaw(
			"ALTER TABLE ? ALTER COLUMN ? TYPE text",
			bun.Ident(metadata.StorageName),
			bun.Ident(column),
		).Exec(ctx); err != nil {
			log.Errorf("orm: widen %s.%s to text: %v", metadata.StorageName, column, err)
			return fmt.Errorf("orm: widen %s.%s to text: %w", metadata.StorageName, column, err)
		}
	}
	return nil
}

// ensureLegacyAddedColumns ensures the legacy added columns handled by this package.
func ensureLegacyAddedColumns(ctx context.Context, db *bun.DB, metadata *ModelMetadata) error {
	if metadata == nil {
		return nil
	}
	switch strings.TrimSpace(metadata.StorageName) {
	case "ui_menu":
		_, err := db.NewRaw(
			"ALTER TABLE ? ADD COLUMN IF NOT EXISTS bid TEXT",
			bun.Ident(metadata.StorageName),
		).Exec(ctx)
		if err != nil {
			log.Errorf("orm: add %s.bid: %v", metadata.StorageName, err)
			return fmt.Errorf("orm: add %s.bid: %w", metadata.StorageName, err)
		}
	case "tbl_currency":
		statements := []string{
			"ALTER TABLE ? ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ",
			"ALTER TABLE ? ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ",
			"ALTER TABLE ? ADD COLUMN IF NOT EXISTS iso_numeric BIGINT",
			"ALTER TABLE ? ADD COLUMN IF NOT EXISTS full_name TEXT",
			"ALTER TABLE ? ADD COLUMN IF NOT EXISTS rounding DOUBLE PRECISION",
			"ALTER TABLE ? ADD COLUMN IF NOT EXISTS decimal_places BIGINT",
			"ALTER TABLE ? ADD COLUMN IF NOT EXISTS active BOOLEAN",
			"ALTER TABLE ? ADD COLUMN IF NOT EXISTS position TEXT",
			"ALTER TABLE ? ADD COLUMN IF NOT EXISTS currency_unit_label TEXT",
			"ALTER TABLE ? ADD COLUMN IF NOT EXISTS currency_subunit_label TEXT",
		}
		for _, stmt := range statements {
			if _, err := db.NewRaw(stmt, bun.Ident(metadata.StorageName)).Exec(ctx); err != nil {
				log.Errorf("orm: extend legacy %s schema: %v", metadata.StorageName, err)
				return fmt.Errorf("orm: extend legacy %s schema: %w", metadata.StorageName, err)
			}
		}
		backfills := []string{
			"UPDATE ? SET created_at = NOW() WHERE created_at IS NULL",
			"UPDATE ? SET updated_at = NOW() WHERE updated_at IS NULL",
			"UPDATE ? SET rounding = 0.01 WHERE rounding IS NULL",
			"UPDATE ? SET decimal_places = 2 WHERE decimal_places IS NULL",
			"UPDATE ? SET active = TRUE WHERE active IS NULL",
			"UPDATE ? SET position = 'after' WHERE position IS NULL OR BTRIM(position) = ''",
		}
		for _, stmt := range backfills {
			if _, err := db.NewRaw(stmt, bun.Ident(metadata.StorageName)).Exec(ctx); err != nil {
				log.Errorf("orm: backfill legacy %s defaults: %v", metadata.StorageName, err)
				return fmt.Errorf("orm: backfill legacy %s defaults: %w", metadata.StorageName, err)
			}
		}
	}
	return nil
}

func reconcileModelSchema(ctx context.Context, db *bun.DB, metadata *ModelMetadata, schemaCache *databaseSchemaCache) error {
	if db == nil || metadata == nil {
		return nil
	}

	currentTable := schemaCache.table(strings.TrimSpace(metadata.StorageName))
	if currentTable == nil {
		return nil
	}

	targetTable, err := inspectModelTable(db, metadata)
	if err != nil {
		return err
	}
	if targetTable == nil {
		return nil
	}

	operations, err := planTableSchemaOperations(db, metadata, currentTable, targetTable)
	if err != nil {
		return err
	}
	if len(operations) == 0 {
		return nil
	}
	// Guard against a runaway migration plan before touching the database:
	// Postgres hard-caps a table at 1600 columns (SQLSTATE 54011), and that
	// failure otherwise surfaces as a bare error with no indication of which
	// table or model caused it, aborting the entire server boot. Fail here
	// instead, naming the offending table/model so it can be fixed directly.
	// The baseline comes from the true attnum slot count (live + dropped
	// columns), not currentTable's live-only column count: a table whose
	// fields get added and removed over time (e.g. an edited custom model)
	// keeps consuming attnum slots on every drop, so a live-column count
	// alone can look safely under the margin while Postgres itself is
	// already at the hard ceiling.
	baseline := schemaCache.attnumSlotCount(strings.TrimSpace(metadata.StorageName))
	if liveColumns := 0; currentTable != nil {
		liveColumns = len(currentTable.GetColumns())
		if liveColumns > baseline {
			baseline = liveColumns
		}
	}
	if err := guardProjectedColumnCount(metadata.Name, strings.TrimSpace(metadata.StorageName), baseline, operations); err != nil {
		return err
	}
	return applyTableSchemaOperations(ctx, db, strings.TrimSpace(metadata.StorageName), operations)
}

func staleModelColumns(metadata *ModelMetadata, existingColumns []string) []string {
	if metadata == nil {
		return nil
	}

	managed := managedModelColumns(metadata)
	stale := make([]string, 0)
	for _, column := range existingColumns {
		column = strings.TrimSpace(column)
		if column == "" {
			continue
		}
		if _, ok := managed[column]; ok {
			continue
		}
		stale = append(stale, column)
	}
	sort.Strings(stale)
	return stale
}

func managedModelColumns(metadata *ModelMetadata) map[string]struct{} {
	managed := make(map[string]struct{})
	if metadata == nil {
		return managed
	}
	for _, field := range metadata.Fields {
		column := strings.TrimSpace(field.BunColumn)
		if column == "" {
			continue
		}
		managed[column] = struct{}{}
	}
	return managed
}

func managedStoredModelColumns(metadata *ModelMetadata) map[string]struct{} {
	managed := make(map[string]struct{})
	if metadata == nil {
		return managed
	}
	for _, field := range metadata.Fields {
		column := strings.TrimSpace(field.BunColumn)
		if column == "" || !field.Stored {
			continue
		}
		managed[column] = struct{}{}
	}
	return managed
}

func columnsByName(columns []sqlschema.Column) map[string]sqlschema.Column {
	indexed := make(map[string]sqlschema.Column, len(columns))
	for _, column := range columns {
		name := strings.TrimSpace(column.GetName())
		if name == "" {
			continue
		}
		indexed[name] = column
	}
	return indexed
}

// cloneColumnMap clones the column map handled by this package.
func cloneColumnMap(columns map[string]sqlschema.Column) map[string]sqlschema.Column {
	cloned := make(map[string]sqlschema.Column, len(columns))
	for name, column := range columns {
		cloned[name] = column
	}
	return cloned
}

func backfillAddedColumn(ctx context.Context, db *bun.DB, tableName string, column sqlschema.Column) error {
	if db == nil || column == nil {
		return nil
	}
	columnName := strings.TrimSpace(column.GetName())
	if columnName == "" {
		return nil
	}
	switch {
	case column.GetIsIdentity() || column.GetIsAutoIncrement():
		if _, err := db.NewRaw(
			"UPDATE ? SET ? = DEFAULT WHERE ? IS NULL",
			bun.Ident(strings.TrimSpace(tableName)),
			bun.Ident(columnName),
			bun.Ident(columnName),
		).Exec(ctx); err != nil {
			log.Errorf("orm: backfill identity column %s.%s: %v", tableName, columnName, err)
			return fmt.Errorf("orm: backfill identity column %s.%s: %w", tableName, columnName, err)
		}
	case columnName == "created_at" || columnName == "updated_at":
		if _, err := db.NewRaw(
			"UPDATE ? SET ? = NOW() WHERE ? IS NULL",
			bun.Ident(strings.TrimSpace(tableName)),
			bun.Ident(columnName),
			bun.Ident(columnName),
		).Exec(ctx); err != nil {
			log.Errorf("orm: backfill timestamp column %s.%s: %v", tableName, columnName, err)
			return fmt.Errorf("orm: backfill timestamp column %s.%s: %w", tableName, columnName, err)
		}
	}
	return nil
}

func legacyColumnNeedsTextWiden(ctx context.Context, db *bun.DB, tableName, columnName string) (bool, error) {
	type columnInfo struct {
		DataType string `bun:"data_type"`
	}

	var info columnInfo
	err := db.NewSelect().
		TableExpr("information_schema.columns").
		Column("data_type").
		Where("table_schema = current_schema()").
		Where("table_name = ?", strings.TrimSpace(tableName)).
		Where("column_name = ?", strings.TrimSpace(columnName)).
		Limit(1).
		Scan(ctx, &info)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		log.Errorf("orm: inspect %s.%s column type: %v", tableName, columnName, err)
		return false, fmt.Errorf("orm: inspect %s.%s column type: %w", tableName, columnName, err)
	}
	return shouldWidenLegacyLongTextType(info.DataType), nil
}

func relationTableExists(ctx context.Context, db *bun.DB, tableName string) (bool, error) {
	if db == nil {
		return false, nil
	}

	type tableInfo struct {
		TableName string `bun:"table_name"`
	}

	var info tableInfo
	err := db.NewSelect().
		TableExpr("information_schema.tables").
		Column("table_name").
		Where("table_schema = current_schema()").
		Where("table_name = ?", strings.TrimSpace(tableName)).
		Limit(1).
		Scan(ctx, &info)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		log.Errorf("orm: inspect relation table %s: %v", tableName, err)
		return false, fmt.Errorf("orm: inspect relation table %s: %w", tableName, err)
	}
	return strings.TrimSpace(info.TableName) != "", nil
}

func shouldWidenLegacyLongTextType(dataType string) bool {
	switch strings.ToLower(strings.TrimSpace(dataType)) {
	case "", "text":
		return false
	case "character varying", "character", "varchar":
		return true
	default:
		return false
	}
}
