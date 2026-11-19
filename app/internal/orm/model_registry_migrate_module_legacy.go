package orm

// File overview:
// - Responsibility: lets a module declare its own pre-reconcile legacy schema steps (explicit
//   table renames, column renames, NULL backfills ahead of a nullable -> NOT NULL change) without
//   adding its table names to the kernel's legacyColumnRenames list.
// - Key symbols: `RegisterLegacyTableRename`, `RegisterLegacyColumnRename`,
//   `RegisterLegacyNullBackfill`, `ensureModuleLegacyColumnMigrations`.
// - Why: the schema reconciler only detects a rename when the names share a token or substring
//   (renameCandidateScore); `customer_id` -> `customerid` scores zero, so left alone it would ADD
//   the new column and DROP the old one, destroying data. Likewise a ChangeColumnTypeOp that adds
//   NOT NULL fails the whole boot migration if any existing row is NULL. A changed storage name is
//   worse still: ensureModelTable would CREATE the new empty table and orphan every existing row
//   in the old one, so the physical table must be renamed first.
// - Pattern: registered from a module models package's var/init, executed once per migration run
//   before the per-model loop, idempotent (table rename skips when the new table already exists or
//   the old one is absent; column rename skips when the new column already exists or the
//   table/old column is absent; backfill only touches NULL rows of an existing column).

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

type moduleLegacyTableRename struct {
	oldTable string
	newTable string
}

type moduleLegacyColumnRename struct {
	table  string
	oldCol string
	newCol string
}

type moduleLegacyNullBackfill struct {
	table  string
	column string
	value  any
}

var moduleLegacyMigrations = struct {
	mu        sync.RWMutex
	tables    []moduleLegacyTableRename
	renames   []moduleLegacyColumnRename
	backfills []moduleLegacyNullBackfill
}{}

// RegisterLegacyTableRename declares that physical table oldTable is now newTable, so the migration
// renames it in place (keeping rows, sequences, indexes and constraints) before any column step or
// CREATE TABLE IF NOT EXISTS runs against the new name.
func RegisterLegacyTableRename(oldTable, newTable string) {
	moduleLegacyMigrations.mu.Lock()
	defer moduleLegacyMigrations.mu.Unlock()
	moduleLegacyMigrations.tables = append(moduleLegacyMigrations.tables, moduleLegacyTableRename{
		oldTable: strings.TrimSpace(oldTable), newTable: strings.TrimSpace(newTable),
	})
}

// RegisterLegacyColumnRename declares that table.oldCol was renamed to table.newCol, so the
// migration renames it in place (keeping its data) before the schema reconciler runs.
func RegisterLegacyColumnRename(table, oldCol, newCol string) {
	moduleLegacyMigrations.mu.Lock()
	defer moduleLegacyMigrations.mu.Unlock()
	moduleLegacyMigrations.renames = append(moduleLegacyMigrations.renames, moduleLegacyColumnRename{
		table: strings.TrimSpace(table), oldCol: strings.TrimSpace(oldCol), newCol: strings.TrimSpace(newCol),
	})
}

// RegisterLegacyNullBackfill declares that NULLs in table.column must be replaced with value
// before the reconciler tightens the column to NOT NULL. value is bound as a query parameter.
func RegisterLegacyNullBackfill(table, column string, value any) {
	moduleLegacyMigrations.mu.Lock()
	defer moduleLegacyMigrations.mu.Unlock()
	moduleLegacyMigrations.backfills = append(moduleLegacyMigrations.backfills, moduleLegacyNullBackfill{
		table: strings.TrimSpace(table), column: strings.TrimSpace(column), value: value,
	})
}

// ensureModuleLegacyColumnMigrations applies every registered table rename, then every column
// rename, then every backfill (column steps address post-rename table names, backfills address
// post-rename column names).
func ensureModuleLegacyColumnMigrations(ctx context.Context, db *bun.DB) error {
	if db == nil {
		return nil
	}
	moduleLegacyMigrations.mu.RLock()
	tables := append([]moduleLegacyTableRename(nil), moduleLegacyMigrations.tables...)
	renames := append([]moduleLegacyColumnRename(nil), moduleLegacyMigrations.renames...)
	backfills := append([]moduleLegacyNullBackfill(nil), moduleLegacyMigrations.backfills...)
	moduleLegacyMigrations.mu.RUnlock()

	for _, table := range tables {
		if err := renameTableIfNeeded(ctx, db, table.oldTable, table.newTable); err != nil {
			return err
		}
	}
	for _, rename := range renames {
		if err := renameColumnIfNeeded(ctx, db, rename.table, rename.oldCol, rename.newCol); err != nil {
			return err
		}
	}
	for _, backfill := range backfills {
		if err := backfillNullColumnIfPresent(ctx, db, backfill); err != nil {
			return err
		}
	}
	return nil
}

// renameTableIfNeeded renames oldTable to newTable. It is a no-op when the old table is absent
// (fresh database, module not installed) or already renamed. When both exist it refuses to guess
// which holds the real data and fails the migration instead of silently orphaning rows.
func renameTableIfNeeded(ctx context.Context, db *bun.DB, oldTable, newTable string) error {
	if oldTable == "" || newTable == "" || oldTable == newTable {
		return nil
	}
	oldExists, err := relationTableExists(ctx, db, oldTable)
	if err != nil || !oldExists {
		return err
	}
	newExists, err := relationTableExists(ctx, db, newTable)
	if err != nil {
		return err
	}
	if newExists {
		log.Errorf("orm: rename table %s to %s: both tables exist, resolve manually", oldTable, newTable)
		return fmt.Errorf("orm: rename table %s to %s: both tables exist, resolve manually", oldTable, newTable)
	}
	if _, err := db.NewRaw("ALTER TABLE ? RENAME TO ?", bun.Ident(oldTable), bun.Ident(newTable)).Exec(ctx); err != nil {
		log.Errorf("orm: rename table %s to %s: %v", oldTable, newTable, err)
		return fmt.Errorf("orm: rename table %s to %s: %w", oldTable, newTable, err)
	}
	return nil
}

// backfillNullColumnIfPresent replaces NULLs in one column; a missing table or column (module not
// installed on this database yet, or column not created yet) is a no-op.
func backfillNullColumnIfPresent(ctx context.Context, db *bun.DB, backfill moduleLegacyNullBackfill) error {
	if backfill.table == "" || backfill.column == "" {
		return nil
	}
	exists, err := columnExists(ctx, db, backfill.table, backfill.column)
	if err != nil || !exists {
		return err
	}
	if _, err := db.NewRaw(
		"UPDATE ? SET ? = ? WHERE ? IS NULL",
		bun.Ident(backfill.table),
		bun.Ident(backfill.column),
		backfill.value,
		bun.Ident(backfill.column),
	).Exec(ctx); err != nil {
		log.Errorf("orm: backfill NULL %s.%s: %v", backfill.table, backfill.column, err)
		return fmt.Errorf("orm: backfill NULL %s.%s: %w", backfill.table, backfill.column, err)
	}
	return nil
}
