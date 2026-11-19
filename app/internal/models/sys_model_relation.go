package models

// File overview:
// - Responsibility: defines built-in runtime model metadata and server-side behavior for sys model relation inside the kernel model catalog.
// - Key symbols: `init`, `createSysModelRelation`, `SysModelRelationRecord`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Uses Bun-facing model or repository patterns so schema mapping stays explicit and transaction-aware inside the backend data layer.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"github.com/uptrace/bun"
)

// SysModelRelationRecord stores one runtime many2many field declaration so the
// system can audit which field owns which relation table and columns.
type SysModelRelationRecord struct {
	bun.BaseModel `bun:"table:sys_model_relation,alias:sys_model_relation"`
	orm.MetadataBaseModel
	BID            string `bun:"bid" json:"bid" meta:"index,label:BID,help:Stores the universal global unique record identifier for this record."`
	Name           string `bun:"name" json:"name" meta:"index,label:Name,help:Stores the human readable name for this record."`
	ModelName      string `bun:"model_name" json:"model_name" meta:"index,label:Model Name,help:Stores the model name value for this record."`
	FieldName      string `bun:"field_name" json:"field_name" meta:"index,label:Field Name,help:Stores the field name value for this record."`
	RelationModel  string `bun:"relation_model" json:"relation_model" meta:"index,label:Relation Model,help:Stores the relation model value for this record."`
	RelationTable  string `bun:"relation_table" json:"relation_table" meta:"index,label:Relation Table,help:Stores the relation table value for this record."`
	RelationColumn string `bun:"relation_column" json:"relation_column" meta:"label:Relation Column,help:Stores the relation column value for this record."`
	RelationKey    string `bun:"relation_key" json:"relation_key" meta:"label:Relation Key,help:Stores the relation key value for this record."`
	Module         string `bun:"module" json:"module" meta:"index,label:Module,help:Stores the module value for this record."`
}

var SysModelRelationModelName = "sys.model.relation"

// SysModelRelationModel should extend relation behavior with composition-style
// helpers so graph invariants remain explicit and locally testable.
var SysModelRelationModel = orm.Define[SysModelRelationRecord](&Models, 60, orm.ModelSpec{
	Name:        SysModelRelationModelName,
	Label:       "Model Relations",
	Description: "Many2many relation-table metadata reflected from the runtime model registry; rows synced from Go code (no bid) are read-only, custom relations (with a bid) stay editable",
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "create", Public: true, ModelOnly: true, Call: createSysModelRelation},
		{Name: "write", Public: true, UsesPathID: true, Call: writeSysModelRelation},
		{Name: "unlink", Public: true, UsesPathID: true, Call: unlinkSysModelRelation},
	},
})

// createSysModelRelation creates the sys model relation handled by this
// package. Only rows carrying a caller-supplied bid (a custom relation,
// not one reflected from Go code) may be created here — registry sync never
// sets bid, and it re-derives its rows by name on every sync, so an
// bid-less row created out-of-band here would either collide with or be
// silently dropped by the next sync.
func createSysModelRelation(ctx context.Context, _ *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	valuesList, err := recordMapListFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	for _, values := range valuesList {
		if strings.TrimSpace(stringValue(values["bid"])) == "" {
			return nil, fmt.Errorf("sys.model.relation rows synced from Go code are read-only; only custom relations (with a bid) can be created here")
		}
	}
	return createLoggedMetadataRecords(ctx, SysModelRelationModelName, args, kwargs)
}

// writeSysModelRelation handles the sys model relation handled by this
// package. Only rows already carrying a non-empty bid (custom relations)
// may be edited; registry-synced rows (no bid) are read-only.
func writeSysModelRelation(ctx context.Context, _ *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	if err := guardSysModelRelationRowsCustom(ctx, ids, "edited"); err != nil {
		return nil, err
	}
	return writeLoggedMetadataRecords(ctx, SysModelRelationModelName, ids, args, kwargs)
}

// unlinkSysModelRelation handles the sys model relation handled by this
// package. Only rows already carrying a non-empty bid (custom relations)
// may be removed; registry-synced rows (no bid) are read-only.
func unlinkSysModelRelation(ctx context.Context, _ *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	if err := guardSysModelRelationRowsCustom(ctx, ids, "deleted"); err != nil {
		return nil, err
	}
	return unlinkLoggedMetadataRecords(ctx, SysModelRelationModelName, ids)
}

// guardSysModelRelationRowsCustom rejects the call when any target row has
// no bid, i.e. it is a registry-synced snapshot rather than a custom
// relation.
func guardSysModelRelationRowsCustom(ctx context.Context, ids []string, verb string) error {
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(SysModelRelationModelName, nil), ids, []string{"id", "bid"})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if strings.TrimSpace(stringValue(row["bid"])) == "" {
			return fmt.Errorf("sys.model.relation rows synced from Go code are read-only; only custom relations (with a bid) can be %s", verb)
		}
	}
	return nil
}

// SyncSysModelRelations rewrites the registry-owned relation metadata
// snapshot (rows with no bid) from the registered runtime models so
// startup leaves one auditable source of truth. Custom relations (bid
// set, created via the UI/API) are never loaded, matched, or deleted here —
// they live entirely outside this sync cycle, mirroring sys.model.field's
// split between registry-synced and custom rows.
func SyncSysModelRelations(ctx context.Context, db *bun.DB, models []orm.ModelMetadata) error {
	if db == nil {
		return nil
	}

	rows := buildSysModelRelationRows(models)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// Load the current registry-owned rows before making decisions that depend on existing data.
	existingRows, err := loadSysModelRelationRows(ctx, tx)
	if err != nil {
		return err
	}
	existingByName := indexSysModelRelationRowsByName(existingRows)

	// Upsert each synced row individually (update in place when a registry row
	// with the same name already exists, otherwise insert) instead of a blind
	// delete-then-reinsert, so row IDs stay stable across syncs.
	syncedNames := make([]string, 0, len(rows))
	for i := range rows {
		syncedNames = append(syncedNames, rows[i].Name)
		if existing, ok := existingByName[rows[i].Name]; ok {
			rows[i].ID = existing.ID
			if _, err = tx.NewUpdate().Model(&rows[i]).WherePK().Exec(ctx); err != nil {
				return err
			}
			continue
		}
		if _, err = tx.NewInsert().Model(&rows[i]).Exec(ctx); err != nil {
			return err
		}
	}

	// Delete registry-owned rows the registry no longer produces; custom rows
	// (bid set) are excluded by the same WHERE clause used to load them.
	deleteQuery := tx.NewDelete().
		Model((*SysModelRelationRecord)(nil)).
		Where("(bid IS NULL OR bid = '')")
	if len(syncedNames) > 0 {
		deleteQuery = deleteQuery.Where("name NOT IN (?)", bun.In(syncedNames))
	}
	if _, err = deleteQuery.Exec(ctx); err != nil {
		return err
	}

	if err = logSysModelRelationSyncChanges(ctx, tx, existingRows, rows); err != nil {
		return err
	}
	return tx.Commit()
}

// buildSysModelRelationRows builds the sys model relation rows handled by this package.
func buildSysModelRelationRows(models []orm.ModelMetadata) []SysModelRelationRecord {
	rows := make([]SysModelRelationRecord, 0)
	for _, model := range models {
		for _, field := range model.Fields {
			if field.Kind != orm.FieldKindManyToMany || strings.TrimSpace(field.RelationTable) == "" {
				continue
			}
			rows = append(rows, SysModelRelationRecord{
				Name:           model.Name + "." + field.Name,
				ModelName:      model.Name,
				FieldName:      field.Name,
				RelationModel:  strings.TrimSpace(field.Relation),
				RelationTable:  strings.TrimSpace(field.RelationTable),
				RelationColumn: strings.TrimSpace(field.RelationColumn),
				RelationKey:    strings.TrimSpace(field.RelationKey),
				Module:         modelModuleName(model.Name),
			})
		}
	}
	return rows
}

func modelModuleName(modelName string) string {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return "runtime"
	}
	parts := strings.SplitN(modelName, ".", 2)
	if len(parts) == 2 && strings.TrimSpace(parts[0]) != "" {
		return strings.TrimSpace(parts[0])
	}
	return "runtime"
}

// loadSysModelRelationRows loads only registry-owned relation rows (no
// bid) handled by this package, excluding custom relations from the sync
// cycle entirely.
func loadSysModelRelationRows(ctx context.Context, db bun.IDB) ([]SysModelRelationRecord, error) {
	if db == nil {
		return nil, nil
	}
	rows := make([]SysModelRelationRecord, 0)
	if err := db.NewSelect().
		Model(&rows).
		Where("(bid IS NULL OR bid = '')").
		OrderExpr("model_name ASC, field_name ASC").
		Scan(ctx); err != nil {
		return nil, err
	}
	return rows, nil
}

func indexSysModelRelationRowsByName(rows []SysModelRelationRecord) map[string]SysModelRelationRecord {
	indexed := make(map[string]SysModelRelationRecord, len(rows))
	for _, row := range rows {
		indexed[row.Name] = row
	}
	return indexed
}

// logSysModelRelationSyncChanges logs the sys model relation sync changes handled by this package.
func logSysModelRelationSyncChanges(ctx context.Context, db bun.IDB, beforeRows, afterRows []SysModelRelationRecord) error {
	beforeByName := make(map[string]SysModelRelationRecord, len(beforeRows))
	for _, row := range beforeRows {
		beforeByName[row.Name] = row
	}
	afterByName := make(map[string]SysModelRelationRecord, len(afterRows))
	for _, row := range afterRows {
		afterByName[row.Name] = row
	}

	names := make(map[string]struct{}, len(beforeByName)+len(afterByName))
	for name := range beforeByName {
		names[name] = struct{}{}
	}
	for name := range afterByName {
		names[name] = struct{}{}
	}

	orderedNames := make([]string, 0, len(names))
	for name := range names {
		orderedNames = append(orderedNames, name)
	}
	sort.Strings(orderedNames)

	for _, name := range orderedNames {
		before, hadBefore := beforeByName[name]
		after, hadAfter := afterByName[name]
		switch {
		case !hadBefore && hadAfter:
			if err := orm.LogModelChange(ctx, db, buildMetadataChangeLogEntry(ctx, SysModelRelationModelName, "create", "registry_sync", nil, sysModelRelationRecordMap(after))); err != nil {
				return err
			}
		case hadBefore && !hadAfter:
			if err := orm.LogModelChange(ctx, db, buildMetadataChangeLogEntry(ctx, SysModelRelationModelName, "unlink", "registry_sync", sysModelRelationRecordMap(before), nil)); err != nil {
				return err
			}
		case hadBefore && hadAfter:
			entry := buildMetadataChangeLogEntry(ctx, SysModelRelationModelName, "write", "registry_sync", sysModelRelationRecordMap(before), sysModelRelationRecordMap(after))
			if len(entry.ChangedFields) == 0 {
				continue
			}
			if err := orm.LogModelChange(ctx, db, entry); err != nil {
				return err
			}
		}
	}
	return nil
}

func sysModelRelationRecordMap(row SysModelRelationRecord) orm.RecordMap {
	return orm.RecordMap{
		"id":              row.ID,
		"name":            row.Name,
		"model_name":      row.ModelName,
		"field_name":      row.FieldName,
		"relation_model":  row.RelationModel,
		"relation_table":  row.RelationTable,
		"relation_column": row.RelationColumn,
		"relation_key":    row.RelationKey,
		"module":          row.Module,
	}
}
