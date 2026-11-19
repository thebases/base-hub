package queries

// File overview:
// - Responsibility: holds read-oriented query logic for module catalog, separating optimized fetch paths from mutation-heavy runtime code.
// - Key symbols: `SyncModuleCatalog`, `EnsureModuleCatalogNameConflictTarget`, `ModuleCatalogRow`.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ModuleCatalogRow struct {
	Name             string
	Version          string
	Category         string
	Installable      bool
	AutoInstall      bool
	State            string
	FrontendWidgets  json.RawMessage
	StaticJSAssets   json.RawMessage
	StaticCSSAssets  json.RawMessage
	StaticHTMLAssets json.RawMessage
}

// SyncModuleCatalog synchronizes the module catalog handled by this package.
func SyncModuleCatalog(ctx context.Context, pool *pgxpool.Pool, rows []ModuleCatalogRow) error {
	if pool == nil {
		log.Errorf("query: module catalog pgx pool is not initialized")
		return fmt.Errorf("query: module catalog pgx pool is not initialized")
	}
	if len(rows) == 0 {
		return nil
	}

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return logModuleCatalogError("query: begin module catalog tx", err)
	}
	committed := false
	defer rollbackModuleCatalogTx(ctx, tx, &committed)

	if err := EnsureModuleCatalogNameConflictTarget(ctx, tx); err != nil {
		return err
	}
	if err := upsertModuleCatalogRows(ctx, tx, rows); err != nil {
		return err
	}
	if err := pruneMissingModuleCatalogRows(ctx, tx, rows); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return logModuleCatalogError("query: commit module catalog tx", err)
	}
	committed = true
	return nil
}

// EnsureModuleCatalogNameConflictTarget ensures the module catalog name conflict target handled by this package.
func EnsureModuleCatalogNameConflictTarget(ctx context.Context, tx pgx.Tx) error {
	var blankNames int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM sys_module_module
		WHERE name IS NULL OR btrim(name) = ''
	`).Scan(&blankNames); err != nil {
		return logModuleCatalogError("modules: check sys_module_module blank names", err)
	}
	if blankNames > 0 {
		log.Errorf("modules: sys_module_module contains %d rows with blank names; cannot enforce module name key", blankNames)
		return fmt.Errorf("modules: sys_module_module contains %d rows with blank names; cannot enforce module name key", blankNames)
	}

	rows, err := tx.Query(ctx, `
		SELECT name, COUNT(*)
		FROM sys_module_module
		GROUP BY name
		HAVING COUNT(*) > 1
		ORDER BY name
		LIMIT 5
	`)
	if err != nil {
		return logModuleCatalogError("modules: check sys_module_module duplicate names", err)
	}
	defer rows.Close()

	duplicates := make([]string, 0, 5)
	for rows.Next() {
		var name string
		var count int
		if err := rows.Scan(&name, &count); err != nil {
			return logModuleCatalogError("modules: scan sys_module_module duplicate names", err)
		}
		duplicates = append(duplicates, fmt.Sprintf("%s (%d rows)", name, count))
	}
	if err := rows.Err(); err != nil {
		return logModuleCatalogError("modules: iterate sys_module_module duplicate names", err)
	}
	if len(duplicates) > 0 {
		log.Errorf("modules: sys_module_module has duplicate names; cannot enforce module name key until cleaned up: %s", strings.Join(duplicates, ", "))
		return fmt.Errorf("modules: sys_module_module has duplicate names; cannot enforce module name key until cleaned up: %s", strings.Join(duplicates, ", "))
	}

	var hasUniqueNameKey bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM (
				SELECT string_agg(attr.attname, ',' ORDER BY keycols.ordinality) AS columns_csv
				FROM pg_class tbl
				JOIN pg_namespace ns ON ns.oid = tbl.relnamespace
				JOIN pg_index idx ON idx.indrelid = tbl.oid
				JOIN unnest(idx.indkey) WITH ORDINALITY AS keycols(attnum, ordinality) ON keycols.attnum > 0
				JOIN pg_attribute attr ON attr.attrelid = tbl.oid AND attr.attnum = keycols.attnum
				WHERE ns.nspname = current_schema()
				  AND tbl.relname = 'sys_module_module'
				  AND idx.indisunique
				  AND idx.indimmediate
				  AND idx.indpred IS NULL
				  AND idx.indexprs IS NULL
				  AND idx.indnkeyatts = 1
				  AND idx.indnatts = 1
				GROUP BY idx.indexrelid
			) unique_indexes
			WHERE columns_csv = 'name'
		)
	`).Scan(&hasUniqueNameKey); err != nil {
		return logModuleCatalogError("modules: inspect sys_module_module name key", err)
	}
	if hasUniqueNameKey {
		return nil
	}

	// The catalog sync depends on PostgreSQL being able to resolve
	// `ON CONFLICT (name)` immediately inside the same SQL-owned transaction.
	if _, err := tx.Exec(ctx, `ALTER TABLE sys_module_module ALTER COLUMN name SET NOT NULL`); err != nil {
		return logModuleCatalogError("modules: enforce sys_module_module.name not null", err)
	}
	if _, err := tx.Exec(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_sys_module_module_name_conflict_key ON sys_module_module (name)`); err != nil {
		return logModuleCatalogError("modules: enforce sys_module_module.name unique key", err)
	}
	return nil
}

// ExistingModuleNames returns which of the given candidate names already have
// a sys_module_module row, so a manual scan can report only genuinely new
// modules instead of every discovered manifest.
func ExistingModuleNames(ctx context.Context, pool *pgxpool.Pool, candidates []string) (map[string]struct{}, error) {
	existing := make(map[string]struct{}, len(candidates))
	if pool == nil || len(candidates) == 0 {
		return existing, nil
	}

	rows, err := pool.Query(ctx, `
		SELECT name
		FROM sys_module_module
		WHERE name = ANY($1)
	`, candidates)
	if err != nil {
		return nil, logModuleCatalogError("query: fetch existing sys_module_module names", err)
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, logModuleCatalogError("query: scan existing sys_module_module name", err)
		}
		existing[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, logModuleCatalogError("query: iterate existing sys_module_module names", err)
	}
	return existing, nil
}

// InstalledModuleNames returns the names of every module currently marked
// installed, used by install/uninstall dependency checks.
func InstalledModuleNames(ctx context.Context, pool *pgxpool.Pool) (map[string]struct{}, error) {
	installed := make(map[string]struct{})
	if pool == nil {
		return installed, nil
	}

	rows, err := pool.Query(ctx, `
		SELECT name
		FROM sys_module_module
		WHERE state = 'installed'
	`)
	if err != nil {
		return nil, logModuleCatalogError("query: fetch installed sys_module_module names", err)
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, logModuleCatalogError("query: scan installed sys_module_module name", err)
		}
		installed[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, logModuleCatalogError("query: iterate installed sys_module_module names", err)
	}
	return installed, nil
}

// SetModuleState updates the lifecycle state for the given module names in
// one statement, used after install/uninstall completes. manualUninstall
// records whether this transition was an explicit admin action rather than
// automatic reconciliation: true on an explicit uninstall (so the optional
// enable-only reconciliation in ApplyServiceDatabaseModuleSelection stops
// forcing the module back to installed), false whenever the module lands on
// state="installed" again (install, or the auto-install cascade), so the
// override doesn't linger past the state it was protecting. manual_install
// mirrors state="installed": it records that this row currently reflects an
// explicit install rather than a selection-driven one, so
// ApplyServiceDatabaseModuleSelection's disable branch (provision.go) knows
// not to auto-uninstall it on the next boot just because it is absent from
// the default/base module selection.
func SetModuleState(ctx context.Context, pool *pgxpool.Pool, names []string, state string, manualUninstall bool) error {
	if pool == nil {
		return fmt.Errorf("query: module catalog pgx pool is not initialized")
	}
	if len(names) == 0 {
		return nil
	}

	if _, err := pool.Exec(ctx, `
		UPDATE sys_module_module
		SET state = $1, manual_uninstall = $3, manual_install = ($1 = 'installed')
		WHERE name = ANY($2)
	`, state, names, manualUninstall); err != nil {
		return logModuleCatalogError("query: set sys_module_module state", err)
	}
	return nil
}

// AutoReconcilableInstalledModuleNames returns which of names are currently
// state='installed' AND manual_install=false — i.e. eligible for automatic
// uninstall by ApplyServiceDatabaseModuleSelection's disable branch because
// they were never explicitly installed by an admin through sys.module.module's
// own Install row action (only pulled in by selection/auto-install logic).
func AutoReconcilableInstalledModuleNames(ctx context.Context, pool *pgxpool.Pool, names []string) (map[string]struct{}, error) {
	result := make(map[string]struct{})
	if pool == nil || len(names) == 0 {
		return result, nil
	}

	rows, err := pool.Query(ctx, `
		SELECT name
		FROM sys_module_module
		WHERE name = ANY($1) AND state = 'installed' AND manual_install = false
	`, names)
	if err != nil {
		return nil, logModuleCatalogError("query: fetch auto-reconcilable sys_module_module names", err)
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, logModuleCatalogError("query: scan auto-reconcilable sys_module_module name", err)
		}
		result[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, logModuleCatalogError("query: iterate auto-reconcilable sys_module_module names", err)
	}
	return result, nil
}

// ModuleInstalled reports whether name's sys_module_module row is currently
// state='installed'. A missing pool or missing row both fail open (true) so
// early-boot callers (no catalog synced yet) are not blocked by a module
// lookup that simply hasn't happened yet.
func ModuleInstalled(ctx context.Context, pool *pgxpool.Pool, name string) (bool, error) {
	if pool == nil {
		return true, nil
	}

	var state string
	if err := pool.QueryRow(ctx, `
		SELECT state
		FROM sys_module_module
		WHERE name = $1
		LIMIT 1
	`, name).Scan(&state); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return true, nil
		}
		return false, err
	}
	return state == "installed", nil
}

func upsertModuleCatalogRows(ctx context.Context, tx pgx.Tx, rows []ModuleCatalogRow) error {
	batch := &pgx.Batch{}
	for _, row := range rows {
		batch.Queue(`
			INSERT INTO sys_module_module (
				name,
				version,
				category,
				installable,
				auto_install,
				state,
				frontend_widgets,
				static_js_assets,
				static_css_assets,
				static_html_assets
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8::jsonb, $9::jsonb, $10::jsonb)
			ON CONFLICT (name) DO UPDATE
			SET version = EXCLUDED.version,
				category = EXCLUDED.category,
				installable = EXCLUDED.installable,
				auto_install = EXCLUDED.auto_install,
				state = COALESCE(sys_module_module.state, EXCLUDED.state),
				frontend_widgets = EXCLUDED.frontend_widgets,
				static_js_assets = EXCLUDED.static_js_assets,
				static_css_assets = EXCLUDED.static_css_assets,
				static_html_assets = EXCLUDED.static_html_assets
		`,
			row.Name,
			row.Version,
			row.Category,
			row.Installable,
			row.AutoInstall,
			row.State,
			string(row.FrontendWidgets),
			string(row.StaticJSAssets),
			string(row.StaticCSSAssets),
			string(row.StaticHTMLAssets),
		)
	}

	results := tx.SendBatch(ctx, batch)
	for range rows {
		if _, err := results.Exec(); err != nil {
			_ = results.Close()
			return logModuleCatalogError("modules: upsert catalog rows", err)
		}
	}
	if err := results.Close(); err != nil {
		return logModuleCatalogError("modules: close module catalog batch", err)
	}
	return nil
}

// rollbackModuleCatalogTx keeps rollback ownership in one helper so catalog
// sync preserves the existing transaction boundary without repeating cleanup logic.
func rollbackModuleCatalogTx(ctx context.Context, tx pgx.Tx, committed *bool) {
	if *committed {
		return
	}
	_ = tx.Rollback(ctx)
}

func logModuleCatalogError(message string, err error) error {
	log.Errorf("%s: %v", message, err)
	return fmt.Errorf("%s: %w", message, err)
}

// pruneMissingModuleCatalogRows drops not_installed catalog rows whose manifest
// is no longer discovered (module folder removed from app/modules). Installed
// modules are left untouched: removing their row would orphan seeded data, so
// they are only reported and must be uninstalled explicitly.
func pruneMissingModuleCatalogRows(ctx context.Context, tx pgx.Tx, rows []ModuleCatalogRow) error {
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, row.Name)
	}
	tag, err := tx.Exec(ctx, `
		DELETE FROM sys_module_module
		WHERE state = 'not_installed' AND NOT (name = ANY($1))
	`, names)
	if err != nil {
		return logModuleCatalogError("query: prune removed modules from catalog", err)
	}
	if n := tag.RowsAffected(); n > 0 {
		log.Infof("modules: pruned %d not_installed catalog row(s) whose manifest no longer exists", n)
	}
	var stale []string
	srows, err := tx.Query(ctx, `SELECT name FROM sys_module_module WHERE state <> 'not_installed' AND NOT (name = ANY($1))`, names)
	if err != nil {
		return logModuleCatalogError("query: list installed modules missing from disk", err)
	}
	defer srows.Close()
	for srows.Next() {
		var name string
		if err := srows.Scan(&name); err != nil {
			return logModuleCatalogError("query: scan installed module missing from disk", err)
		}
		stale = append(stale, name)
	}
	if err := srows.Err(); err != nil {
		return logModuleCatalogError("query: iterate installed modules missing from disk", err)
	}
	if len(stale) > 0 {
		log.Warnf("modules: installed module(s) have no manifest on disk: %s", strings.Join(stale, ", "))
	}
	return nil
}
