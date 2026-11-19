package orm

// File overview:
// - Responsibility: plans and applies per-table schema operations (add/alter/drop, rename candidates, unique/PK diffs).
// - Key symbols: `maxSafeColumns`, `guardProjectedColumnCount`, `guardNewTableColumnCount`, `planTableSchemaOperations`.

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
	"github.com/uptrace/bun/migrate/sqlschema"
)

// guardProjectedColumnCount estimates the table's attnum slot count after
// applying the planned operations and refuses to proceed if it would breach
// a safety margin under Postgres's hard 1600-column-per-table limit.
// baseline must be the table's true attnum slot count (live plus dropped
// columns), not just a live-column count — see attnumSlotCount.
// maxSafeColumns is the safety margin under Postgres's hard 1600-column-
// per-table limit; shared by both the ALTER-path guard (guardProjectedColumnCount)
// and the CREATE-path guard (guardNewTableColumnCount) below.
const maxSafeColumns = 1500

func guardProjectedColumnCount(modelName, tableName string, baseline int, operations []migrate.Operation) error {
	projected := baseline
	// Only additions grow the projection: Postgres never reclaims a dropped
	// column's attnum slot short of a full table rewrite, so a DropColumnOp
	// must not be treated as freeing room for another column.
	for _, op := range operations {
		if _, ok := op.(*migrate.AddColumnOp); ok {
			projected++
		}
	}
	if projected > maxSafeColumns {
		log.Errorf("orm: refusing to migrate %s (table %s): projected column count %d exceeds safety margin %d (Postgres hard limit 1600)", modelName, tableName, projected, maxSafeColumns)
		return fmt.Errorf("orm: refusing to migrate %s (table %s): projected column count %d exceeds safety margin %d (Postgres hard limit 1600) — inspect information_schema.columns for %s and drop stale/unmanaged columns before retrying", modelName, tableName, projected, maxSafeColumns, tableName)
	}
	return nil
}

// guardNewTableColumnCount refuses to CREATE TABLE for a model whose Go
// struct field count alone would already breach the safety margin, naming
// the offending model/table instead of letting Postgres's bare SQLSTATE
// 54011 abort boot. Only meaningful for a table that does not exist yet —
// see the schemaCache-membership check at its call site in ensureModelTable.
func guardNewTableColumnCount(modelName, tableName string, goType reflect.Type) error {
	if goType == nil {
		return nil
	}
	fieldCount := goType.NumField()
	if fieldCount > maxSafeColumns {
		log.Errorf("orm: refusing to create table for %s (table %s): %d fields exceeds safety margin %d (Postgres hard limit 1600)", modelName, tableName, fieldCount, maxSafeColumns)
		return fmt.Errorf("orm: refusing to create table for %s (table %s): %d fields exceeds safety margin %d (Postgres hard limit 1600) — remove unused fields from the model before retrying", modelName, tableName, fieldCount, maxSafeColumns)
	}
	return nil
}

func planTableSchemaOperations(db *bun.DB, metadata *ModelMetadata, current, target sqlschema.Table) ([]migrate.Operation, error) {
	if db == nil || metadata == nil || current == nil || target == nil {
		return nil, nil
	}

	currentColumns := columnsByName(current.GetColumns())
	targetColumns := columnsByName(target.GetColumns())
	managedColumns := managedStoredModelColumns(metadata)

	renames := make([]migrate.Operation, 0)
	adds := make([]migrate.Operation, 0)
	changes := make([]migrate.Operation, 0)
	drops := make([]migrate.Operation, 0)

	availableCurrent := cloneColumnMap(currentColumns)
	usedCurrent := make(map[string]struct{})

	// Walk each getcolumns() entry in order so validation and side effects stay scoped to the current element.
	for _, targetColumn := range target.GetColumns() {
		targetName := strings.TrimSpace(targetColumn.GetName())
		if _, ok := managedColumns[targetName]; !ok {
			continue
		}

		if currentColumn, ok := currentColumns[targetName]; ok {
			usedCurrent[targetName] = struct{}{}
			// Translated columns are intentionally jsonb while the Go struct
			// declares a string (SRS-F-021): the type divergence is owned by
			// ensureTranslatedColumns, so the generic differ must never plan a
			// change that would flip the column back to varchar and destroy the
			// language envelope.
			if _, isTranslated := translatedColumnSet(metadata)[targetName]; isTranslated {
				continue
			}
			if !equalSchemaColumns(db, currentColumn, targetColumn) {
				changes = append(changes, &migrate.ChangeColumnTypeOp{
					TableName: target.GetName(),
					Column:    targetName,
					From:      currentColumn,
					To:        sqlReadyColumnDefault(targetColumnForChange(db, currentColumn, targetColumn)),
				})
			}
			continue
		}

		if bestCurrentName, bestCurrentColumn := bestRenameCandidate(db, current, availableCurrent, usedCurrent, targetColumns, targetColumn); bestCurrentName != "" {
			renames = append(renames, &migrate.RenameColumnOp{
				TableName: target.GetName(),
				OldName:   bestCurrentName,
				NewName:   targetName,
			})
			usedCurrent[bestCurrentName] = struct{}{}
			delete(availableCurrent, bestCurrentName)
			currentColumns[targetName] = bestCurrentColumn
			delete(currentColumns, bestCurrentName)
			replacePrimaryKeyColumn(current, bestCurrentName, targetName)
			continue
		}

		adds = append(adds, &migrate.AddColumnOp{
			TableName:  target.GetName(),
			ColumnName: targetName,
			Column:     sqlReadyColumnDefault(targetColumn),
		})
	}

	for currentName, currentColumn := range currentColumns {
		if _, keep := targetColumns[currentName]; keep {
			continue
		}
		drops = append(drops, &migrate.DropColumnOp{
			TableName:  target.GetName(),
			ColumnName: currentName,
			Column:     currentColumn,
		})
	}

	dropUniques, addUniques := diffUniqueConstraints(current, target)
	pkOps := diffPrimaryKey(current, target)

	operations := make([]migrate.Operation, 0, len(renames)+len(adds)+len(changes)+len(dropUniques)+len(addUniques)+len(pkOps)+len(drops))
	operations = append(operations, renames...)
	operations = append(operations, adds...)
	operations = append(operations, changes...)
	operations = append(operations, dropUniques...)
	operations = append(operations, pkOps...)
	operations = append(operations, addUniques...)
	operations = append(operations, drops...)
	return operations, nil
}

// bestRenameCandidate picks the existing column that a new target column most
// plausibly replaces, or "" when none qualifies. A candidate must score above
// zero (real token overlap, a substring relationship, or being the current
// primary key). Matching purely on SQL type/nullable/default is not enough
// evidence: two unrelated columns of the same shape (e.g. two nullable TEXT
// fields with no default) would otherwise tie at score 0 and the old,
// unrelated column gets silently repurposed under the new field's name instead
// of the new column being added and the true stale column being dropped.
// Candidates are visited in sorted order (not Go map order) so the same input
// schema always produces the same migration plan.
func bestRenameCandidate(db *bun.DB, current sqlschema.Table, availableCurrent map[string]sqlschema.Column, usedCurrent map[string]struct{}, targetColumns map[string]sqlschema.Column, targetColumn sqlschema.Column) (string, sqlschema.Column) {
	targetName := strings.TrimSpace(targetColumn.GetName())
	candidateNames := make([]string, 0, len(availableCurrent))
	for currentName := range availableCurrent {
		candidateNames = append(candidateNames, currentName)
	}
	sort.Strings(candidateNames)

	bestScore := 0
	bestName := ""
	var bestColumn sqlschema.Column
	for _, currentName := range candidateNames {
		currentColumn := availableCurrent[currentName]
		if _, alreadyUsed := usedCurrent[currentName]; alreadyUsed {
			continue
		}
		if _, existsInTarget := targetColumns[currentName]; existsInTarget {
			continue
		}
		if !equalSchemaColumns(db, currentColumn, targetColumn) {
			continue
		}
		if score := renameCandidateScore(current, currentName, targetName); score > bestScore {
			bestScore = score
			bestName = currentName
			bestColumn = currentColumn
		}
	}
	return bestName, bestColumn
}

// applyTableSchemaOperations applies the table schema operations handled by this package.
func applyTableSchemaOperations(ctx context.Context, db *bun.DB, tableName string, operations []migrate.Operation) error {
	if db == nil || len(operations) == 0 {
		return nil
	}
	migrator, err := sqlschema.NewMigrator(db, db.Dialect().DefaultSchema())
	if err != nil {
		log.Errorf("orm: create schema migrator for %s: %v", tableName, err)
		return fmt.Errorf("orm: create schema migrator for %s: %w", tableName, err)
	}

	for _, op := range operations {
		// Own the "column becomes serial" sequence step before bun sees the op (see prepareSerialColumnChange).
		if change, ok := op.(*migrate.ChangeColumnTypeOp); ok {
			if err := prepareSerialColumnChange(ctx, db, change); err != nil {
				log.Errorf("orm: create serial sequence for %s.%s: %v", tableName, change.Column, err)
				return fmt.Errorf("orm: create serial sequence for %s.%s: %w", tableName, change.Column, err)
			}
		}
		queryBytes, err := migrator.AppendSQL(nil, op)
		if err != nil {
			log.Errorf("orm: build schema operation for %s: %v", tableName, err)
			return fmt.Errorf("orm: build schema operation for %s: %w", tableName, err)
		}
		query := strings.TrimSpace(string(queryBytes))
		if query == "" {
			continue
		}
		if _, err := db.NewRaw(query).Exec(ctx); err != nil {
			log.Errorf("orm: apply schema operation on %s: %v", tableName, err)
			return fmt.Errorf("orm: apply schema operation on %s: %w", tableName, err)
		}
		if add, ok := op.(*migrate.AddColumnOp); ok {
			if err := backfillAddedColumn(ctx, db, tableName, add.Column); err != nil {
				return err
			}
		}
	}
	return nil
}

// prepareSerialColumnChange creates the backing sequence for a column that turns serial
// (e.g. a surrogate `id` added to a former join table) and rewrites the op into a plain
// SET DEFAULT nextval(...) change. bun v1.2.16's own path scans MAX(col) into an int, which
// fails on an empty table (NULL), drops that error and leaves op.To nil, so changeColumnType
// panics in CompareType. The sequence name matches bun's inspector serial check
// (`<table>_<col>_seq`), so the next boot reads the column as autoincrement and plans no change.
func prepareSerialColumnChange(ctx context.Context, db *bun.DB, op *migrate.ChangeColumnTypeOp) error {
	if op == nil || op.From == nil || op.To == nil || op.From.GetIsAutoIncrement() || !op.To.GetIsAutoIncrement() {
		return nil
	}
	seq := op.TableName + "_" + op.Column + "_seq"
	if _, err := db.NewRaw("CREATE SEQUENCE IF NOT EXISTS ? OWNED BY ?.?", bun.Ident(seq), bun.Ident(op.TableName), bun.Ident(op.Column)).Exec(ctx); err != nil {
		return err
	}
	// Start after the current max so existing rows never collide; COALESCE covers the empty table.
	if _, err := db.NewRaw("SELECT setval(?, COALESCE((SELECT MAX(?) FROM ?), 0) + 1, false)", seq, bun.Ident(op.Column), bun.Ident(op.TableName)).Exec(ctx); err != nil {
		return err
	}
	// IsAutoIncrement=false keeps bun off its createDefaultSequence branch; type/nullability/default
	// are still diffed by changeColumnType against op.From.
	op.To = &sqlschema.BaseColumn{
		Name:         op.To.GetName(),
		SQLType:      op.To.GetSQLType(),
		VarcharLen:   op.To.GetVarcharLen(),
		DefaultValue: fmt.Sprintf("nextval('%s'::regclass)", seq),
		IsNullable:   op.To.GetIsNullable(),
		IsIdentity:   op.To.GetIsIdentity(),
	}
	return nil
}

func renameCandidateScore(table sqlschema.Table, currentName, targetName string) int {
	score := tokenOverlapScore(currentName, targetName)
	if strings.Contains(targetName, currentName) || strings.Contains(currentName, targetName) {
		score += 100
	}
	if pk := table.GetPrimaryKey(); pk != nil && pk.Columns.Contains(currentName) {
		score += 50
	}
	return score
}

func tokenOverlapScore(left, right string) int {
	leftTokens := strings.Split(strings.TrimSpace(left), "_")
	rightTokens := strings.Split(strings.TrimSpace(right), "_")
	score := 0
	for _, leftToken := range leftTokens {
		leftToken = strings.TrimSpace(leftToken)
		if leftToken == "" {
			continue
		}
		for _, rightToken := range rightTokens {
			rightToken = strings.TrimSpace(rightToken)
			if rightToken == "" {
				continue
			}
			if leftToken == rightToken {
				score += 10
			}
		}
	}
	return score
}

func diffUniqueConstraints(current, target sqlschema.Table) ([]migrate.Operation, []migrate.Operation) {
	if current == nil || target == nil {
		return nil, nil
	}
	currentUniques := current.GetUniqueConstraints()
	targetUniques := target.GetUniqueConstraints()

	drops := make([]migrate.Operation, 0)
	adds := make([]migrate.Operation, 0)

	for _, currentUnique := range currentUniques {
		if !hasEquivalentUnique(targetUniques, currentUnique) {
			drops = append(drops, &migrate.DropUniqueConstraintOp{
				TableName: target.GetName(),
				Unique:    currentUnique,
			})
		}
	}
	for _, targetUnique := range targetUniques {
		if !hasEquivalentUnique(currentUniques, targetUnique) {
			adds = append(adds, &migrate.AddUniqueConstraintOp{
				TableName: target.GetName(),
				Unique:    targetUnique,
			})
		}
	}
	return drops, adds
}

// hasEquivalentUnique checks the equivalent unique handled by this package.
func hasEquivalentUnique(candidates []sqlschema.Unique, target sqlschema.Unique) bool {
	for _, candidate := range candidates {
		if candidate.Equals(target) {
			return true
		}
	}
	return false
}

func diffPrimaryKey(current, target sqlschema.Table) []migrate.Operation {
	if current == nil || target == nil {
		return nil
	}
	currentPK := current.GetPrimaryKey()
	targetPK := target.GetPrimaryKey()

	switch {
	case currentPK == nil && targetPK == nil:
		return nil
	case currentPK == nil && targetPK != nil:
		return []migrate.Operation{&migrate.AddPrimaryKeyOp{
			TableName:  target.GetName(),
			PrimaryKey: *targetPK,
		}}
	case currentPK != nil && targetPK == nil:
		return []migrate.Operation{&migrate.DropPrimaryKeyOp{
			TableName:  target.GetName(),
			PrimaryKey: *currentPK,
		}}
	case currentPK.Columns != targetPK.Columns:
		return []migrate.Operation{&migrate.ChangePrimaryKeyOp{
			TableName: target.GetName(),
			Old:       *currentPK,
			New:       *targetPK,
		}}
	default:
		return nil
	}
}
