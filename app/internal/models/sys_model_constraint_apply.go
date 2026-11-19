package models

// File overview:
// - Responsibility: reconciles sys.model.constraint rows against live PostgreSQL objects (SRS-F-004/F-005) and
//   drives the enable/disable/apply object-method actions. Split from sys_model_constraint.go (atomic split per
//   repo convention) because the DDL diff/apply logic is sizable on its own.
// - Key symbols: `ReconcileModelConstraint`, `ReconcileModelConstraints`, `actionApplySysModelConstraint`.
// - Caveat: check/unique_index comparison against pg_get_constraintdef/pg_get_indexdef is a best-effort
//   normalized-text match, not a semantic SQL comparison — Postgres may reformat/cast the declared expression,
//   which can make an otherwise-identical constraint look "changed" on the first re-check after apply.

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"sort"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

// ReconcileModelConstraints iterates every state='active' row and reconciles
// it against PostgreSQL, logging (not failing) per-row errors so one bad
// constraint never blocks boot (SRS-F-005). dbName identifies the connected
// database for the constraint-message cache (see file header caveat on
// sys_model_constraint.go's constraintMessageCache) — boot has no request
// Environment to read it from, so the caller (db_bootstrap.go) passes it
// explicitly.
func ReconcileModelConstraints(ctx context.Context, db *bun.DB, dbName string) error {
	if db == nil {
		return nil
	}
	var records []SysModelConstraintRecord
	if err := db.NewSelect().
		Model(&records).
		Where("state = ?", "active").
		OrderExpr("model_name ASC, name ASC").
		Scan(ctx); err != nil {
		return err
	}
	for _, record := range records {
		row := constraintRecordToMap(record)
		if strings.EqualFold(record.ConstraintType, sysModelConstraintTypeBusinessRule) {
			refreshBusinessRuleCacheRow(dbName, row)
			ensureBusinessRuleHandlerRegistered(record.ModelName)
			continue
		}
		if err := ReconcileModelConstraint(ctx, db, dbName, row); err != nil {
			log.Errorf("%s: boot reconcile of %q failed, row flipped to error: %v", SysModelConstraintModelName, record.Name, err)
		}
	}
	return nil
}

func constraintRecordToMap(record SysModelConstraintRecord) orm.RecordMap {
	return orm.RecordMap{
		"id":              record.ID,
		"name":            record.Name,
		"model_name":      record.ModelName,
		"constraint_type": record.ConstraintType,
		"sql_definition":  record.SQLDefinition,
		"invalid_domain":  record.InvalidDomain,
		"trigger_fields":  record.TriggerFields,
		"message":         record.Message,
		"state":           record.State,
		"last_applied_at": record.LastAppliedAt,
	}
}

// ReconcileModelConstraint applies (or no-ops) one check/unique/unique_index
// row against PostgreSQL. business_rule rows short-circuit into the cache
// registration path instead of DDL. dbName scopes the constraint-message
// cache entry (see constraintMessageCache in sys_model_constraint.go).
func ReconcileModelConstraint(ctx context.Context, db bun.IDB, dbName string, row orm.RecordMap) error {
	if db == nil {
		return fmt.Errorf("%s: database is not initialized", SysModelConstraintModelName)
	}
	id := stringValue(row["id"])
	modelName := strings.TrimSpace(stringValue(row["model_name"]))
	meta, ok := orm.LookupModel(modelName)
	if !ok || meta == nil {
		return markConstraintRowError(ctx, db, id, fmt.Errorf("unknown_model: %q is not a registered model", modelName))
	}
	table := meta.StorageName
	name := strings.TrimSpace(stringValue(row["name"]))
	objectName := table + "_" + name
	constraintType := strings.TrimSpace(stringValue(row["constraint_type"]))
	sqlDefinition := strings.TrimSpace(stringValue(row["sql_definition"]))
	message := strings.TrimSpace(stringValue(row["message"]))

	if constraintType == sysModelConstraintTypeBusinessRule {
		// The cache keeps only state=active rows, and the row read by the caller
		// is still draft/disabled: refresh only after the DB row is active, with
		// the state set on a copy (callers log the original row as "before").
		if err := markConstraintRowActive(ctx, db, id); err != nil {
			return err
		}
		active := maps.Clone(row)
		active["state"] = "active"
		// Enforce only once the activation commits: a rolled-back batch must not
		// leave a rule active in this process's cache.
		orm.AfterCommit(ctx, func(context.Context) {
			refreshBusinessRuleCacheRow(dbName, active)
			ensureBusinessRuleHandlerRegistered(modelName)
			invalidateBusinessRuleSync(dbName)
		})
		return nil
	}

	liveDef, exists, err := readLiveConstraintDefinition(ctx, db, table, objectName, constraintType)
	if err != nil {
		return markConstraintRowError(ctx, db, id, err)
	}
	if exists && constraintDefinitionMatches(constraintType, liveDef, sqlDefinition) {
		// No-op path: already reconciled, no DDL needed.
		setConstraintMessage(dbName, objectName, message)
		return markConstraintRowActive(ctx, db, id)
	}
	lastAppliedAt, _ := row["last_applied_at"].(time.Time)
	if exists && lastAppliedAt.IsZero() {
		// An object with this name already exists and we have never applied it
		// ourselves — this is not ours to overwrite (e.g. a struct-tag unique
		// constraint coincidentally sharing the derived object name).
		err := fmt.Errorf("constraint_name_conflict: a PostgreSQL object named %q already exists on %q and was not created by this row", objectName, table)
		return markConstraintRowError(ctx, db, id, err)
	}

	if err := applyLiveConstraintDDL(ctx, db, table, objectName, constraintType, sqlDefinition); err != nil {
		return markConstraintRowError(ctx, db, id, err)
	}
	setConstraintMessage(dbName, objectName, message)
	return markConstraintRowActive(ctx, db, id)
}

// readLiveConstraintDefinition looks up the live pg_constraint/pg_indexes
// definition for objectName on table, per constraintType.
func readLiveConstraintDefinition(ctx context.Context, db bun.IDB, table, objectName, constraintType string) (string, bool, error) {
	switch constraintType {
	case sysModelConstraintTypeCheck, sysModelConstraintTypeUnique:
		var info struct {
			Definition string `bun:"definition"`
		}
		err := db.NewSelect().
			ColumnExpr("pg_get_constraintdef(con.oid) AS definition").
			TableExpr("pg_constraint AS con").
			Join("JOIN pg_class AS rel ON rel.oid = con.conrelid").
			Where("rel.relname = ?", table).
			Where("con.conname = ?", objectName).
			Limit(1).
			Scan(ctx, &info)
		if err != nil {
			if isNoRowsErr(err) {
				return "", false, nil
			}
			return "", false, err
		}
		return info.Definition, true, nil
	case sysModelConstraintTypeUniqueIndex:
		var info struct {
			IndexDef string `bun:"indexdef"`
		}
		err := db.NewSelect().
			TableExpr("pg_indexes").
			Column("indexdef").
			Where("schemaname = current_schema()").
			Where("tablename = ?", table).
			Where("indexname = ?", objectName).
			Limit(1).
			Scan(ctx, &info)
		if err != nil {
			if isNoRowsErr(err) {
				return "", false, nil
			}
			return "", false, err
		}
		return info.IndexDef, true, nil
	default:
		return "", false, fmt.Errorf("unsupported constraint_type %q", constraintType)
	}
}

func isNoRowsErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no rows")
}

// applyLiveConstraintDDL performs the DROP-then-ADD in one transaction.
func applyLiveConstraintDDL(ctx context.Context, db bun.IDB, table, objectName, constraintType, sqlDefinition string) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		switch constraintType {
		case sysModelConstraintTypeCheck:
			_, err := tx.NewRaw(
				"ALTER TABLE ? DROP CONSTRAINT IF EXISTS ?, ADD CONSTRAINT ? CHECK ("+sqlDefinition+")",
				bun.Ident(table), bun.Ident(objectName), bun.Ident(objectName),
			).Exec(ctx)
			return err
		case sysModelConstraintTypeUnique:
			// Built from identifiers, never the raw text (plan T21); also covers rows stored
			// before validation existed, which now fail here and flip to state=error.
			columns, err := parseUniqueConstraintColumns(sqlDefinition)
			if err != nil {
				return err
			}
			placeholders := make([]string, len(columns))
			queryArgs := []any{bun.Ident(table), bun.Ident(objectName), bun.Ident(objectName)}
			for i, column := range columns {
				placeholders[i] = "?"
				queryArgs = append(queryArgs, bun.Ident(column))
			}
			_, err = tx.NewRaw(
				"ALTER TABLE ? DROP CONSTRAINT IF EXISTS ?, ADD CONSTRAINT ? UNIQUE ("+strings.Join(placeholders, ", ")+")",
				queryArgs...,
			).Exec(ctx)
			return err
		case sysModelConstraintTypeUniqueIndex:
			if _, err := tx.NewRaw("DROP INDEX IF EXISTS ?", bun.Ident(objectName)).Exec(ctx); err != nil {
				return err
			}
			// Deliberately no CONCURRENTLY (SRS 4.1's deferred Should) — plain
			// CREATE UNIQUE INDEX runs fine inside an explicit transaction.
			_, err := tx.NewRaw(
				"CREATE UNIQUE INDEX ? ON ? "+sqlDefinition,
				bun.Ident(objectName), bun.Ident(table),
			).Exec(ctx)
			return err
		default:
			return fmt.Errorf("unsupported constraint_type %q", constraintType)
		}
	})
}

// dropLiveConstraintObjectBestEffort is used by unlink and action_disable:
// best-effort DROP, swallowing "table doesn't exist" as a logged warning
// rather than a hard failure (the target table may itself be gone). Also
// clears the constraint-message cache so a dropped object never leaves a
// stale friendly-message mapping behind.
func dropLiveConstraintObjectBestEffort(ctx context.Context, db bun.IDB, dbName string, row orm.RecordMap) error {
	constraintType := strings.TrimSpace(stringValue(row["constraint_type"]))
	if constraintType == sysModelConstraintTypeBusinessRule {
		return nil
	}
	modelName := strings.TrimSpace(stringValue(row["model_name"]))
	meta, ok := orm.LookupModel(modelName)
	if !ok || meta == nil {
		log.Errorf("%s: drop skipped, model %q no longer registered", SysModelConstraintModelName, modelName)
		return nil
	}
	table := meta.StorageName
	objectName := table + "_" + strings.TrimSpace(stringValue(row["name"]))
	clearConstraintMessage(dbName, objectName)

	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var err error
		switch constraintType {
		case sysModelConstraintTypeUniqueIndex:
			_, err = tx.NewRaw("DROP INDEX IF EXISTS ?", bun.Ident(objectName)).Exec(ctx)
		default:
			_, err = tx.NewRaw("ALTER TABLE ? DROP CONSTRAINT IF EXISTS ?", bun.Ident(table), bun.Ident(objectName)).Exec(ctx)
		}
		if err != nil && strings.Contains(strings.ToLower(err.Error()), "does not exist") {
			log.Errorf("%s: table %q missing while dropping %q, treating as already-gone: %v", SysModelConstraintModelName, table, objectName, err)
			return nil
		}
		return err
	})
}

// markConstraintRowActive commits state=active, last_error=NULL,
// last_applied_at=now as a statement separate from the DDL transaction.
func markConstraintRowActive(ctx context.Context, db bun.IDB, id string) error {
	_, err := db.NewUpdate().
		Model((*SysModelConstraintRecord)(nil)).
		Set("state = ?", "active").
		Set("last_error = ?", "").
		Set("last_applied_at = ?", time.Now().UTC()).
		Set("updated_at = ?", time.Now().UTC()).
		Where("id = ?", id).
		Exec(ctx)
	return err
}

// markConstraintRowError commits state=error, last_error=<diagnostic> as its
// own statement/transaction (independent of the failed DDL tx which already
// rolled back), then returns the original error to the caller.
func markConstraintRowError(ctx context.Context, db bun.IDB, id string, cause error) error {
	if _, updateErr := db.NewUpdate().
		Model((*SysModelConstraintRecord)(nil)).
		Set("state = ?", "error").
		Set("last_error = ?", cause.Error()).
		Set("updated_at = ?", time.Now().UTC()).
		Where("id = ?", id).
		Exec(ctx); updateErr != nil {
		log.Errorf("%s: failed to persist error state for row %s: %v (original error: %v)", SysModelConstraintModelName, id, updateErr, cause)
	}
	return cause
}

// constraintDefinitionMatches is a best-effort normalized-text comparator —
// see file header caveat.
func constraintDefinitionMatches(constraintType, liveDef, declared string) bool {
	switch constraintType {
	case sysModelConstraintTypeUnique:
		return columnListsEqual(extractParenColumns(liveDef), declared)
	default:
		return normalizeSQLSnippet(liveDef) == normalizeSQLSnippet(wrapForComparison(constraintType, declared))
	}
}

func wrapForComparison(constraintType, declared string) string {
	switch constraintType {
	case sysModelConstraintTypeCheck:
		return "CHECK (" + declared + ")"
	case sysModelConstraintTypeUniqueIndex:
		return declared
	default:
		return declared
	}
}

var sqlCastPattern = regexp.MustCompile(`::[a-zA-Z_][a-zA-Z0-9_]*`)
var sqlWhitespacePattern = regexp.MustCompile(`\s+`)

// normalizeSQLSnippet lowercases, strips type casts, and collapses
// whitespace so trivially-equivalent Postgres-reformatted SQL compares equal
// to the originally-declared text in the common case.
func normalizeSQLSnippet(s string) string {
	s = sqlCastPattern.ReplaceAllString(s, "")
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "(", " ( ")
	s = strings.ReplaceAll(s, ")", " ) ")
	s = sqlWhitespacePattern.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

var parenColumnsPattern = regexp.MustCompile(`\(([^()]*)\)`)

// extractParenColumns pulls the first parenthesized column list out of a
// pg_get_constraintdef "UNIQUE (a, b)" style definition.
func extractParenColumns(def string) string {
	match := parenColumnsPattern.FindStringSubmatch(def)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

func columnListsEqual(a, b string) bool {
	return sortedColumnList(a) == sortedColumnList(b)
}

func sortedColumnList(raw string) string {
	parts := strings.Split(raw, ",")
	cols := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.ToLower(strings.TrimSpace(part))
		if trimmed != "" {
			cols = append(cols, trimmed)
		}
	}
	sort.Strings(cols)
	return strings.Join(cols, ",")
}

// actionApplySysModelConstraint is the manual "Apply" button (SRS-F-004).
func actionApplySysModelConstraint(ctx context.Context, _ *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	db := sysModelConstraintDB(ctx)
	dbName := sysModelConstraintDBName(ctx)
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(SysModelConstraintModelName, nil), ids, nil)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		before := row
		applyErr := ReconcileModelConstraint(ctx, db, dbName, row)
		after, readErr := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(SysModelConstraintModelName, nil), []string{stringValue(row["id"])}, nil)
		if readErr == nil && len(after) > 0 {
			_ = logMetadataChange(ctx, SysModelConstraintModelName, "action_apply", "runtime_action", before, after[0])
		}
		if applyErr != nil {
			return nil, applyErr
		}
	}
	return true, nil
}

// applyOrEnableConstraintRows backs action_enable (SRS-F-007): resumes as if
// from draft — business_rule rows just re-enter the cache, DDL rows re-run
// the normal apply/reconcile pipeline.
func applyOrEnableConstraintRows(ctx context.Context, ids []string, _ string) (any, error) {
	db := sysModelConstraintDB(ctx)
	dbName := sysModelConstraintDBName(ctx)
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(SysModelConstraintModelName, nil), ids, nil)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		before := row
		applyErr := ReconcileModelConstraint(ctx, db, dbName, row)
		after, readErr := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(SysModelConstraintModelName, nil), []string{stringValue(row["id"])}, nil)
		if readErr == nil && len(after) > 0 {
			_ = logMetadataChange(ctx, SysModelConstraintModelName, "action_enable", "runtime_action", before, after[0])
		}
		if applyErr != nil {
			return nil, applyErr
		}
	}
	return true, nil
}
