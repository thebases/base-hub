package orm

// File overview:
// - Responsibility: legacy column and model-name rename steps that run before schema reconcile.
// - Key symbols: `legacyColumnRenames`, `ensureLegacyColumnRenames`, `renameColumnIfNeeded`, `columnExists`.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

// legacyColumnRenames maps the 2026-08-01 identifier rename (tbl_id ->
// model_id, xml_id -> bid, and the xml_id-derived compound columns) to their
// physical table + old/new column name. bid is the universal, globally
// unique record identifier (format
// <model_name>-<simplified-record-name>-<8 random alphanumeric chars>, nil
// allowed for rows without one). This list must run before the per-model
// loop reaches ensureModelTable/reconcileModelSchema: the schema
// reconciler's rename-detection heuristic (renameCandidateScore) scores
// xml_id->bid at zero token overlap and tbl_id->model_id only weakly, so
// left to itself it would ADD COLUMN the new name and DROP COLUMN the old
// one, destroying every existing value. Renaming the column here first
// means the reconciler sees the new name already in place and treats it as
// a match.
var legacyColumnRenames = []struct {
	table  string
	oldCol string
	newCol string
}{
	{"sys_model_data", "tbl_id", "model_id"},
	{"sys_attachment", "tbl_id", "model_id"},
	{"sys_model", "xml_id", "bid"},
	{"sys_model_field", "xml_id", "bid"},
	{"sys_model_relation", "xml_id", "bid"},
	{"sys_model_constraint", "xml_id", "bid"},
	{"sys_rule", "xml_id", "bid"},
	{"ui_view", "xml_id", "bid"},
	{"ui_menu", "xml_id", "bid"},
	{"tbl_groups", "xml_id", "bid"},
	{"sys_rule", "group_xml_ids", "group_bids"},
	{"tbl_groups", "category_xml_id", "category_bid"},
}

// ensureLegacyColumnRenames ensures the legacy column renames handled by this
// package. It uses renameColumnIfNeeded to prepare inputs, resolve runtime
// dependencies, and delegate the main work.
func ensureLegacyColumnRenames(ctx context.Context, db *bun.DB) error {
	if db == nil {
		return nil
	}
	for _, rename := range legacyColumnRenames {
		if err := renameColumnIfNeeded(ctx, db, rename.table, rename.oldCol, rename.newCol); err != nil {
			return err
		}
	}
	return nil
}

func renameColumnIfNeeded(ctx context.Context, db *bun.DB, table, oldCol, newCol string) error {
	table = strings.TrimSpace(table)
	oldCol = strings.TrimSpace(oldCol)
	newCol = strings.TrimSpace(newCol)
	if table == "" || oldCol == "" || newCol == "" || oldCol == newCol {
		return nil
	}

	tableExists, err := relationTableExists(ctx, db, table)
	if err != nil {
		return err
	}
	if !tableExists {
		return nil
	}

	newExists, err := columnExists(ctx, db, table, newCol)
	if err != nil {
		return err
	}
	if newExists {
		return nil
	}

	oldExists, err := columnExists(ctx, db, table, oldCol)
	if err != nil {
		return err
	}
	if !oldExists {
		return nil
	}

	if _, err := db.NewRaw(
		"ALTER TABLE ? RENAME COLUMN ? TO ?",
		bun.Ident(table),
		bun.Ident(oldCol),
		bun.Ident(newCol),
	).Exec(ctx); err != nil {
		log.Errorf("orm: rename column %s.%s to %s: %v", table, oldCol, newCol, err)
		return fmt.Errorf("orm: rename column %s.%s to %s: %w", table, oldCol, newCol, err)
	}
	return nil
}

func columnExists(ctx context.Context, db *bun.DB, table, column string) (bool, error) {
	if db == nil {
		return false, nil
	}

	type columnInfo struct {
		ColumnName string `bun:"column_name"`
	}

	var info columnInfo
	err := db.NewSelect().
		TableExpr("information_schema.columns").
		Column("column_name").
		Where("table_schema = current_schema()").
		Where("table_name = ?", strings.TrimSpace(table)).
		Where("column_name = ?", strings.TrimSpace(column)).
		Limit(1).
		Scan(ctx, &info)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		log.Errorf("orm: inspect column %s.%s: %v", table, column, err)
		return false, fmt.Errorf("orm: inspect column %s.%s: %w", table, column, err)
	}
	return true, nil
}

// legacyModelNameRenames maps the 2026-07-14 logical model rename res.* ->
// tbl.*. Every base identity/catalog model moved together with its tbl_*
// physical table, so metadata rows written under the old dotted names must be
// rewritten in place or existing databases lose ACLs, record rules, views,
// menus, and external-id resolution for these models.
var legacyModelNameRenames = []struct {
	oldName string
	newName string
}{
	{"res.groups", "tbl.groups"},
	{"res.lang", "tbl.lang"},
	{"res.country", "tbl.country"},
	{"res.country.group", "tbl.country.group"},
	{"res.country.state", "tbl.country.state"},
	{"res.country.ward", "tbl.country.ward"},
	{"res.currency", "tbl.currency"},
	{"res.currency.rate", "tbl.currency.rate"},
	{"res.company", "tbl.company"},
	{"res.partner", "tbl.partner"},
	{"res.partner.bank", "tbl.partner.bank"},
}

// legacyModelNameColumns lists every persisted column that stores a logical
// model name verbatim. base_workflow_action is a module table referenced by
// its literal storage name because internal/ must not import modules/*
// (same precedent as the autovacuum cron handler).
var legacyModelNameColumns = []struct {
	table  string
	column string
}{
	{"sys_model", "name"},
	{"sys_model_field", "model_name"},
	{"sys_model_data", "model"},
	{"sys_model_access", "model_name"},
	{"sys_rule", "model_name"},
	{"ui_view", "model"},
	{"ui_actions_window", "sys_model"},
	{"ui_actions_client", "sys_model"},
	{"sys_attachment", "sys_model"},
	{"sys_report", "model"},
	{"sys_model_log", "target_model"},
	{"sys_model_log", "subject_model"},
	{"base_workflow_action", "model_name"},
}

// ensureLegacyModelNameRenames rewrites metadata rows that still reference
// the pre-rename res.* logical model names. It must run before
// SyncModelRegistryToDB: the sync upserts sys_model/sys_model_field by the
// new names, so stale res.* rows left behind would linger as dead catalog
// entries (sys_model.name is unique) instead of being refreshed in place.
// Every statement is idempotent — after the first successful run no res.*
// values remain, so subsequent boots are no-ops.
func ensureLegacyModelNameRenames(ctx context.Context, db *bun.DB) error {
	if db == nil {
		return nil
	}

	// Cache table existence once per table so fresh databases (where most of
	// these tables do not exist yet) and databases without the workflow module
	// skip cleanly instead of failing the whole migration.
	tableExists := make(map[string]bool)
	// ui_menu holds derived route paths rather than a plain model-name column,
	// so it must be probed explicitly alongside the exact-column tables.
	tablesToProbe := []string{"ui_menu"}
	for _, target := range legacyModelNameColumns {
		tablesToProbe = append(tablesToProbe, target.table)
	}
	for _, table := range tablesToProbe {
		if _, checked := tableExists[table]; checked {
			continue
		}
		exists, err := relationTableExists(ctx, db, table)
		if err != nil {
			return err
		}
		tableExists[table] = exists
	}

	for _, rename := range legacyModelNameRenames {
		for _, target := range legacyModelNameColumns {
			if !tableExists[target.table] {
				continue
			}
			// sys_model.name and sys_model_field.(model_name, field_name) carry
			// unique constraints: if a row for the new name somehow already
			// exists (for example a restored dump mixed with a fresh sync), the
			// stale old-name row must be dropped instead of renamed or the
			// UPDATE would abort startup on a unique violation.
			if target.table == "sys_model" {
				if _, err := db.NewRaw(
					"DELETE FROM ? WHERE ? = ? AND EXISTS (SELECT 1 FROM ? WHERE ? = ?)",
					bun.Ident(target.table), bun.Ident(target.column), rename.oldName,
					bun.Ident(target.table), bun.Ident(target.column), rename.newName,
				).Exec(ctx); err != nil {
					log.Errorf("orm: drop stale legacy model row %s in %s: %v", rename.oldName, target.table, err)
					return fmt.Errorf("orm: drop stale legacy model row %s in %s: %w", rename.oldName, target.table, err)
				}
			}
			// Same unique-violation guard for the per-field catalog rows.
			if target.table == "sys_model_field" {
				if _, err := db.NewRaw(
					"DELETE FROM ? AS stale WHERE stale.? = ? AND EXISTS (SELECT 1 FROM ? AS fresh WHERE fresh.? = ? AND fresh.field_name = stale.field_name)",
					bun.Ident(target.table), bun.Ident(target.column), rename.oldName,
					bun.Ident(target.table), bun.Ident(target.column), rename.newName,
				).Exec(ctx); err != nil {
					log.Errorf("orm: drop stale legacy field rows for %s: %v", rename.oldName, err)
					return fmt.Errorf("orm: drop stale legacy field rows for %s: %w", rename.oldName, err)
				}
			}
			if _, err := db.NewRaw(
				"UPDATE ? SET ? = ? WHERE ? = ?",
				bun.Ident(target.table), bun.Ident(target.column), rename.newName,
				bun.Ident(target.column), rename.oldName,
			).Exec(ctx); err != nil {
				log.Errorf("orm: rename legacy model %s to %s in %s.%s: %v", rename.oldName, rename.newName, target.table, target.column, err)
				return fmt.Errorf("orm: rename legacy model %s to %s in %s.%s: %w", rename.oldName, rename.newName, target.table, target.column, err)
			}
		}

		// View names and keys are derived from the model name with a view-type
		// suffix (res.partner.form, res.partner.list, ...), so rewrite the
		// dotted prefix while preserving the suffix.
		if tableExists["ui_view"] {
			for _, column := range []string{"name", "key"} {
				if _, err := db.NewRaw(
					"UPDATE ? SET ? = ? || substr(?, ?) WHERE ? = ? OR ? LIKE ?",
					bun.Ident("ui_view"), bun.Ident(column), rename.newName,
					bun.Ident(column), len(rename.oldName)+1,
					bun.Ident(column), rename.oldName,
					bun.Ident(column), rename.oldName+".%",
				).Exec(ctx); err != nil {
					log.Errorf("orm: rename legacy view %s prefix %s: %v", column, rename.oldName, err)
					return fmt.Errorf("orm: rename legacy view %s prefix %s: %w", column, rename.oldName, err)
				}
			}
		}

		// Menu route paths embed the model name after the /web/ prefix, and the
		// SPA resolves the target model from that path segment.
		if tableExists["ui_menu"] {
			if _, err := db.NewRaw(
				"UPDATE ? SET route_path = ? WHERE route_path = ?",
				bun.Ident("ui_menu"), "/web/"+rename.newName, "/web/"+rename.oldName,
			).Exec(ctx); err != nil {
				log.Errorf("orm: rename legacy menu route for %s: %v", rename.oldName, err)
				return fmt.Errorf("orm: rename legacy menu route for %s: %w", rename.oldName, err)
			}
		}
	}
	return nil
}
