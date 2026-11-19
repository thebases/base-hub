package orm

// File overview:
// - Responsibility: database schema introspection and the per-database schema cache.
// - Key symbols: `databaseSchemaCache`, `newDatabaseSchemaCache`, `loadTrueAttnumSlotCounts`.

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate/sqlschema"
	"github.com/uptrace/bun/schema"
)

// databaseSchemaCache holds one full-database sqlschema.Inspect() snapshot so
// the per-model migration loop in AutoMigrateRegisteredModelsToDB can look up
// each table's live column/index state from memory instead of re-scanning
// every table/column/index in the database (pg_catalog) once per model. The
// naive per-call inspect turned a ~100-model migration into ~100 full-schema
// scans, which is what made a fresh boot's db_provision step take 30+ seconds.
type databaseSchemaCache struct {
	tables map[string]sqlschema.Table
	// attnumSlots holds, per table, the true number of attnum slots Postgres
	// has ever allocated (live columns plus dropped-but-not-reclaimed ones).
	// sqlschema.Table/information_schema.columns only report live columns, so
	// a table that has had columns added and dropped repeatedly (e.g. a
	// metadata-driven custom model whose fields get edited/removed a lot)
	// can be sitting right at Postgres's hard 1600-attnum-per-table ceiling
	// while every "column count" derived from tables map still looks small.
	// Without this, guardProjectedColumnCount undercounts and Postgres's
	// bare SQLSTATE 54011 surfaces instead of the guard's clearer refusal.
	attnumSlots map[string]int
}

// newDatabaseSchemaCache takes the one full-schema snapshot the cache serves
// until a caller explicitly asks for a refresh via refresh().
func newDatabaseSchemaCache(ctx context.Context, db *bun.DB) (*databaseSchemaCache, error) {
	cache := &databaseSchemaCache{tables: make(map[string]sqlschema.Table), attnumSlots: make(map[string]int)}
	if err := cache.reload(ctx, db); err != nil {
		return nil, err
	}
	return cache, nil
}

// reload re-runs the full-database inspection and replaces the cached table
// set. Only called at cache construction and by refresh(), never once per
// model — callers needing a single table's fresh state should use refresh.
func (c *databaseSchemaCache) reload(ctx context.Context, db *bun.DB) error {
	if db == nil {
		return nil
	}
	inspector, err := sqlschema.NewInspector(db, sqlschema.WithSchemaName(db.Dialect().DefaultSchema()))
	if err != nil {
		log.Errorf("orm: inspect database schema: %v", err)
		return fmt.Errorf("orm: inspect database schema: %w", err)
	}
	state, err := inspector.Inspect(ctx)
	if err != nil {
		log.Errorf("orm: inspect database schema: %v", err)
		return fmt.Errorf("orm: inspect database schema: %w", err)
	}
	tables := make(map[string]sqlschema.Table, len(state.GetTables()))
	for _, table := range state.GetTables() {
		tables[strings.TrimSpace(table.GetName())] = table
	}
	c.tables = tables

	slots, err := loadTrueAttnumSlotCounts(ctx, db)
	if err != nil {
		log.Errorf("orm: inspect attnum slot counts: %v", err)
		return fmt.Errorf("orm: inspect attnum slot counts: %w", err)
	}
	c.attnumSlots = slots
	return nil
}

// loadTrueAttnumSlotCounts counts every attnum slot pg_attribute has ever
// handed out for each table in the current schema, including dropped
// columns (attisdropped=true rows stay in pg_attribute forever — Postgres
// never reuses their attnum without a full table rewrite). This is the
// number that Postgres actually checks against its 1600-column hard limit,
// unlike a live/visible column count.
func loadTrueAttnumSlotCounts(ctx context.Context, db *bun.DB) (map[string]int, error) {
	slots := make(map[string]int)
	rows, err := db.QueryContext(ctx, `
		SELECT c.relname, count(*)
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE a.attnum > 0 AND c.relkind = 'r' AND n.nspname = current_schema()
		GROUP BY c.relname
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var count int
		if err := rows.Scan(&name, &count); err != nil {
			return nil, err
		}
		slots[strings.TrimSpace(name)] = count
	}
	return slots, rows.Err()
}

// attnumSlotCount returns the true Postgres attnum slot count for tableName
// (live plus dropped columns), 0 when the table has no cached entry yet.
func (c *databaseSchemaCache) attnumSlotCount(tableName string) int {
	tableName = strings.TrimSpace(tableName)
	if c == nil || tableName == "" {
		return 0
	}
	return c.attnumSlots[tableName]
}

// refresh re-inspects the whole database and updates the cache; used only
// for the small number of tables mutated in place mid-migration by the
// legacy widen/backfill steps (ensureLegacyLongTextColumns/
// ensureLegacyAddedColumns) so their lookup reflects current state instead
// of the stale pre-loop snapshot. Rare by construction, so paying for one
// more full scan here is negligible next to eliminating the
// N-scans-per-migration baseline. tableName is accepted for call-site
// clarity even though a reload always refreshes every table.
func (c *databaseSchemaCache) refresh(ctx context.Context, db *bun.DB, tableName string) error {
	if c == nil {
		return nil
	}
	return c.reload(ctx, db)
}

// table returns the cached snapshot's entry for tableName, nil when the
// table does not exist in that snapshot (mirrors the old per-call
// inspectDatabaseTable's not-found return).
func (c *databaseSchemaCache) table(tableName string) sqlschema.Table {
	tableName = strings.TrimSpace(tableName)
	if c == nil || tableName == "" {
		return nil
	}
	return c.tables[tableName]
}

func inspectModelTable(db *bun.DB, metadata *ModelMetadata) (sqlschema.Table, error) {
	if db == nil || metadata == nil || metadata.goReflectType == nil {
		return nil, nil
	}
	tables := schema.NewTables(db.Dialect())
	tables.Register(reflect.New(metadata.goReflectType).Interface())
	inspector := sqlschema.NewBunModelInspector(tables, sqlschema.WithSchemaName(db.Dialect().DefaultSchema()))
	state, err := inspector.Inspect(context.Background())
	if err != nil {
		log.Errorf("orm: inspect model schema for %s: %v", metadata.Name, err)
		return nil, fmt.Errorf("orm: inspect model schema for %s: %w", metadata.Name, err)
	}
	tablesInState := state.GetTables()
	if len(tablesInState) == 0 {
		return nil, nil
	}

	table := tablesInState[0]
	storageName := strings.TrimSpace(metadata.StorageName)
	if storageName == "" || storageName == strings.TrimSpace(table.GetName()) {
		return table, nil
	}

	// Registered runtime models can intentionally point a Go struct at a legacy
	// storage table name, so schema operations must target the registry-backed
	// table instead of Bun's default struct-derived table name.
	return applyImplicitJSONBDefaultsToTable(metadata, &sqlschema.BaseTable{
		Schema:            table.GetSchema(),
		Name:              storageName,
		Columns:           cloneSchemaColumns(table.GetColumns()),
		PrimaryKey:        table.GetPrimaryKey(),
		UniqueConstraints: table.GetUniqueConstraints(),
	}), nil
}
