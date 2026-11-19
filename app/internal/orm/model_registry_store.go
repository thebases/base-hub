package orm

// File overview:
// - Responsibility: implements the metadata-driven ORM/runtime kernel for model registry store, where storage, runtime dispatch, and security rules stay centralized.
// - Key symbols: `SyncModelRegistry`, `SyncModelRegistryToDB`, `systemModelRow`, `systemModelFieldRow`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Uses Bun-facing model or repository patterns so schema mapping stays explicit and transaction-aware inside the backend data layer.

import (
	"context"
	"fmt"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
	"time"
)

// sysModelStatusPublished mirrors models.SysModelStatusPublished (orm cannot
// import internal/models — that package imports orm — so this lifecycle
// status string is duplicated as a literal on both sides of the boundary).
const sysModelStatusPublished = "PUBLISHED"

type systemModelRow struct {
	bun.BaseModel  `bun:"table:sys_model"`
	ID             int64          `bun:"id,pk,autoincrement"`
	CreatedAt      time.Time      `bun:"created_at,nullzero"`
	UpdatedAt      time.Time      `bun:"updated_at,nullzero"`
	BID            string         `bun:"bid"`
	Name           string         `bun:"name"`
	Label          string         `bun:"label"`
	Description    string         `bun:"description"`
	StorageBackend string         `bun:"storage_backend"`
	StorageName    string         `bun:"storage_name"`
	HelperData     string         `bun:"helper_data"`
	JsonDTO        map[string]any `bun:"json_dto,type:jsonb,nullzero"`
	RegisteredAt   time.Time      `bun:"registered_at"`
	SyncedAt       time.Time      `bun:"synced_at"`
	Status         string         `bun:"status"`
	HasUi          bool           `bun:"has_ui"`
}

type systemModelFieldRow struct {
	bun.BaseModel `bun:"table:sys_model_field"`
	ID            int64     `bun:"id,pk,autoincrement"`
	CreatedAt     time.Time `bun:"created_at,nullzero"`
	UpdatedAt     time.Time `bun:"updated_at,nullzero"`
	BID           string    `bun:"bid"`
	ModelName     string    `bun:"model_name"`
	FieldName     string    `bun:"field_name"`
	GoName        string    `bun:"go_name"`
	Label         string    `bun:"label"`
	DataType      string    `bun:"data_type"`
	// RelationModel is only meaningful when DataType is a relation kind
	// (many2one/one2one/one2many/many2many): the model name the field points
	// to. Registry-synced rows populate it from FieldMetadata.Relation;
	// custom-model rows populate it from base.model.add_field's relation
	// argument aliases (see mcpCreateCustomField).
	RelationModel string `bun:"relation_model"`
	BSONName      string `bun:"bson_name"`
	JSONName      string `bun:"json_name"`
	BunColumn     string `bun:"bun_column"`
	IsPrimaryKey  bool   `bun:"is_primary_key"`
	IsRequired    bool   `bun:"is_required"`
	FieldIndex    int    `bun:"field_index"`
	Status        string `bun:"status"`
}

// SyncModelRegistry synchronizes the model registry handled by this package.
func SyncModelRegistry(ctx context.Context) error {
	// Reuse the shared Bun connection because registry sync is system metadata,
	// not model-specific runtime data.
	db := GetBunDB()
	if db == nil {
		log.Errorf("BunDB is not initialized")
		return fmt.Errorf("BunDB is not initialized")
	}
	return SyncModelRegistryToDB(ctx, db, RegisteredModels())
}

// SyncModelRegistryToDB synchronizes the model registry to db handled by this package.
func SyncModelRegistryToDB(ctx context.Context, db *bun.DB, models []ModelMetadata) error {
	if db == nil {
		log.Errorf("bun db is nil")
		return fmt.Errorf("bun db is nil")
	}
	// Ensure the metadata tables exist before attempting the upsert cycle.
	if err := ensureSystemMetadataTables(ctx, db); err != nil {
		return err
	}
	if len(models) == 0 {
		return nil
	}

	// Sync all model and field rows in one transaction so readers never observe
	// a partially refreshed registry snapshot.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	syncedAt := time.Now().UTC()
	for _, meta := range models {
		existingFieldRows, err := loadSystemModelFieldRows(ctx, tx, meta.Name)
		if err != nil {
			return err
		}
		existingFieldMap := indexSystemModelFieldRows(existingFieldRows)

		// Upsert the parent model row first so field rows always reference a
		// fresh model-level snapshot.
		modelRow := newSystemModelRow(meta, syncedAt)
		if _, err = tx.NewInsert().
			Model(&modelRow).
			On("CONFLICT (name) DO UPDATE").
			Set("label = EXCLUDED.label").
			Set("description = EXCLUDED.description").
			Set("storage_backend = EXCLUDED.storage_backend").
			Set("storage_name = EXCLUDED.storage_name").
			Set("helper_data = EXCLUDED.helper_data").
			Set("json_dto = EXCLUDED.json_dto").
			Set("registered_at = EXCLUDED.registered_at").
			Set("synced_at = EXCLUDED.synced_at").
			// A row only reaches this sync loop by being in the live
			// registry.List() snapshot, i.e. it is by definition currently
			// published. This stamps a Go-coded model PUBLISHED at boot and
			// a just-registered custom model PUBLISHED as part of
			// orm.PublishModel's own AutoMigrateRegisteredModels call, with
			// no separate write needed on the publish success path.
			Set("status = EXCLUDED.status").
			Exec(ctx); err != nil {
			return err
		}

		fieldRows := newSystemModelFieldRows(meta, syncedAt)
		for _, fieldRow := range fieldRows {
			beforeRow, hadBefore := existingFieldMap[fieldRow.FieldName]
			// Upsert runtime-owned snapshot rows individually so custom frontend
			// field rows with `x-` bids survive registry syncs.
			if _, err = tx.NewInsert().
				Model(&fieldRow).
				On("CONFLICT (model_name, field_name) DO UPDATE").
				Set("go_name = EXCLUDED.go_name").
				Set("label = EXCLUDED.label").
				Set("data_type = EXCLUDED.data_type").
				Set("relation_model = EXCLUDED.relation_model").
				Set("bson_name = EXCLUDED.bson_name").
				Set("json_name = EXCLUDED.json_name").
				Set("bun_column = EXCLUDED.bun_column").
				Set("is_primary_key = EXCLUDED.is_primary_key").
				Set("is_required = EXCLUDED.is_required").
				Set("field_index = EXCLUDED.field_index").
				Set("updated_at = EXCLUDED.updated_at").
				// See the sibling model-row upsert above: a field only
				// reaches this loop as part of a currently-registered
				// model's live field set, so it is PUBLISHED too.
				Set("status = EXCLUDED.status").
				Exec(ctx); err != nil {
				return err
			}
			if err = logSystemModelFieldUpsert(ctx, tx, hadBefore, beforeRow, fieldRow); err != nil {
				return err
			}
		}
		if err = deleteStaleSystemModelFields(ctx, tx, meta, fieldRows); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// newSystemModelRow constructs and returns the system model row handled by this package.
func newSystemModelRow(meta ModelMetadata, syncedAt time.Time) systemModelRow {
	return systemModelRow{
		// Copy the runtime registry payload into the persisted system-model shape
		// without any additional transformation.
		Name:           meta.Name,
		Label:          meta.Label,
		Description:    meta.Description,
		StorageBackend: string(meta.StorageBackend),
		StorageName:    meta.StorageName,
		HelperData:     BuildModelHelperHTML(meta),
		JsonDTO:        BuildModelJSONDTO(meta),
		RegisteredAt:   meta.RegisteredAt,
		SyncedAt:       syncedAt,
		Status:         sysModelStatusPublished,
		// Only takes effect on first insert (see the ON CONFLICT clause in
		// SyncModelRegistryToDB, which deliberately omits has_ui from its
		// Set() list). A row only reaches a first insert here for a Go-coded
		// model with no bid — a custom model's sys.model row always already
		// exists (created via sys.model.create) before it can ever be
		// registered/synced, so this path never fires for one. HasUi=true is
		// therefore correct and permanent for every Go-coded model, while a
		// custom model's has_ui stays whatever CreateUIForModel/models.
		// markSysModelHasUI last set it to.
		HasUi: true,
	}
}

// newSystemModelFieldRows constructs and returns the system model field rows handled by this package.
func newSystemModelFieldRows(meta ModelMetadata, syncedAt time.Time) []systemModelFieldRow {
	rows := make([]systemModelFieldRow, 0, len(meta.Fields))
	for i, field := range meta.Fields {
		// Preserve field slice order so the system table reflects registry order
		// for debugging and metadata export.
		rows = append(rows, systemModelFieldRow{
			ModelName:     meta.Name,
			FieldName:     field.Name,
			GoName:        field.GoName,
			Label:         field.Label,
			DataType:      field.Type,
			RelationModel: field.Relation,
			BSONName:      field.BSONName,
			JSONName:      field.JSONName,
			BunColumn:     field.BunColumn,
			IsPrimaryKey:  field.PrimaryKey,
			IsRequired:    field.Required,
			FieldIndex:    i,
			CreatedAt:     syncedAt,
			UpdatedAt:     syncedAt,
			Status:        sysModelStatusPublished,
		})
	}
	return rows
}

func deleteStaleSystemModelFields(ctx context.Context, tx bun.IDB, meta ModelMetadata, syncedRows []systemModelFieldRow) error {
	if tx == nil {
		return nil
	}
	fieldNames := make([]string, 0, len(syncedRows))
	for _, row := range syncedRows {
		fieldNames = append(fieldNames, row.FieldName)
	}

	query := tx.NewDelete().
		Model((*systemModelFieldRow)(nil)).
		Where("model_name = ?", meta.Name).
		Where("(bid IS NULL OR bid = '')")
	if len(fieldNames) > 0 {
		query = query.Where("field_name NOT IN (?)", bun.In(fieldNames))
	}
	staleRows, err := loadStaleSystemModelFieldRows(ctx, tx, meta.Name, fieldNames)
	if err != nil {
		return err
	}
	if _, err := query.Exec(ctx); err != nil {
		return err
	}
	for _, row := range staleRows {
		entry := ModelChangeLogEntry{
			ChangeType:    "field",
			Action:        "unlink",
			TargetModel:   "sys.model.field",
			TargetRecord:  fieldRowRecordID(row),
			SubjectModel:  row.ModelName,
			SubjectName:   row.FieldName,
			Source:        "registry_sync",
			ChangedFields: nil,
			BeforeData:    systemModelFieldRowMap(row),
		}
		if err := LogModelChange(ctx, tx, entry); err != nil {
			return err
		}
	}
	return nil
}

// loadSystemModelFieldRows loads the system model field rows handled by this package.
// Scoped to bid-less rows (Go-registered snapshot fields) because its only
// caller, SyncModelRegistryToDB, must never see or touch custom-model field
// rows (which always carry a bid, see createSysModel/mcpCreateCustomField) —
// those are owned by the base.model.add_field pipeline, not registry sync.
// Publish uses loadCustomSystemModelFieldRows instead, see its doc comment.
func loadSystemModelFieldRows(ctx context.Context, db bun.IDB, modelName string) ([]systemModelFieldRow, error) {
	if db == nil {
		return nil, nil
	}
	rows := make([]systemModelFieldRow, 0)
	if err := db.NewSelect().
		Model(&rows).
		Where("model_name = ?", modelName).
		Where("(bid IS NULL OR bid = '')").
		OrderExpr("field_index ASC, field_name ASC").
		Scan(ctx); err != nil {
		return nil, err
	}
	return rows, nil
}

// loadCustomSystemModelFieldRows loads the sys_model_field rows for one
// custom (bid-owned) model. PublishModel needs this instead of
// loadSystemModelFieldRows: every field row created through
// base.model.add_field / base.model.create_custom / the admin "Model
// Fields" form carries a non-empty bid (mirroring sys_model's own bid
// requirement, see createSysModel), so the bid-less filter in
// loadSystemModelFieldRows would always return zero rows here and publish
// would build a struct type with no fields.
//
// Filters on model_name + non-empty bid only — NOT on the field row's bid
// matching the owning model's own bid. Nothing in this codebase ever sets a
// field's bid to its model's bid: mcpCreateCustomField server-generates an
// independent random bid per field (models.GenerateBID is non-deterministic,
// see its doc comment), and the admin "Model Fields" form (bid field on
// view_sys_model_field_form) has the caller type an arbitrary bid with no
// hint it should match the model. Requiring exact equality here made every
// field row publish-invisible in practice, which is what produced "orm:
// model has no publishable fields..." even when fields clearly existed in
// the UI. model_name is already globally unique on sys.model (meta:"unique"
// on SysModelRecord.Name), so model_name + "has a bid at all" is sufficient
// to scope to this model's custom fields.
func loadCustomSystemModelFieldRows(ctx context.Context, db bun.IDB, modelName string) ([]systemModelFieldRow, error) {
	if db == nil {
		return nil, nil
	}
	rows := make([]systemModelFieldRow, 0)
	if err := db.NewSelect().
		Model(&rows).
		Where("model_name = ?", modelName).
		Where("bid IS NOT NULL AND bid != ''").
		OrderExpr("field_index ASC, field_name ASC").
		Scan(ctx); err != nil {
		return nil, err
	}
	return rows, nil
}

// loadStaleSystemModelFieldRows loads the stale system model field rows handled by this package.
func loadStaleSystemModelFieldRows(ctx context.Context, db bun.IDB, modelName string, currentFieldNames []string) ([]systemModelFieldRow, error) {
	if db == nil {
		return nil, nil
	}
	rows := make([]systemModelFieldRow, 0)
	query := db.NewSelect().
		Model(&rows).
		Where("model_name = ?", modelName).
		Where("(bid IS NULL OR bid = '')")
	if len(currentFieldNames) > 0 {
		query = query.Where("field_name NOT IN (?)", bun.In(currentFieldNames))
	}
	if err := query.OrderExpr("field_index ASC, field_name ASC").Scan(ctx); err != nil {
		return nil, err
	}
	return rows, nil
}

func indexSystemModelFieldRows(rows []systemModelFieldRow) map[string]systemModelFieldRow {
	indexed := make(map[string]systemModelFieldRow, len(rows))
	for _, row := range rows {
		indexed[row.FieldName] = row
	}
	return indexed
}

// logSystemModelFieldUpsert logs the system model field upsert handled by this package.
func logSystemModelFieldUpsert(ctx context.Context, db bun.IDB, hadBefore bool, before, after systemModelFieldRow) error {
	entry := ModelChangeLogEntry{
		ChangeType:    "field",
		TargetModel:   "sys.model.field",
		TargetRecord:  fieldRowRecordID(after),
		SubjectModel:  after.ModelName,
		SubjectName:   after.FieldName,
		Source:        "registry_sync",
		AfterData:     systemModelFieldRowMap(after),
		ChangedFields: changedSystemModelFieldColumns(before, after),
	}
	if !hadBefore {
		entry.Action = "create"
		return LogModelChange(ctx, db, entry)
	}
	entry.Action = "write"
	entry.BeforeData = systemModelFieldRowMap(before)
	if len(entry.ChangedFields) == 0 {
		return nil
	}
	return LogModelChange(ctx, db, entry)
}

func changedSystemModelFieldColumns(before, after systemModelFieldRow) []string {
	changed := make([]string, 0, 7)
	if before.ModelName != after.ModelName {
		changed = append(changed, "model_name")
	}
	if before.FieldName != after.FieldName {
		changed = append(changed, "field_name")
	}
	if before.GoName != after.GoName {
		changed = append(changed, "go_name")
	}
	if before.Label != after.Label {
		changed = append(changed, "label")
	}
	if before.DataType != after.DataType {
		changed = append(changed, "data_type")
	}
	if before.RelationModel != after.RelationModel {
		changed = append(changed, "relation_model")
	}
	if before.BSONName != after.BSONName {
		changed = append(changed, "bson_name")
	}
	if before.JSONName != after.JSONName {
		changed = append(changed, "json_name")
	}
	if before.BunColumn != after.BunColumn {
		changed = append(changed, "bun_column")
	}
	if before.IsPrimaryKey != after.IsPrimaryKey {
		changed = append(changed, "is_primary_key")
	}
	if before.IsRequired != after.IsRequired {
		changed = append(changed, "is_required")
	}
	if before.FieldIndex != after.FieldIndex {
		changed = append(changed, "field_index")
	}
	return changed
}

func systemModelFieldRowMap(row systemModelFieldRow) RecordMap {
	return RecordMap{
		"id":             row.ID,
		"model_name":     row.ModelName,
		"field_name":     row.FieldName,
		"go_name":        row.GoName,
		"label":          row.Label,
		"data_type":      row.DataType,
		"relation_model": row.RelationModel,
		"bson_name":      row.BSONName,
		"json_name":      row.JSONName,
		"bun_column":     row.BunColumn,
		"is_primary_key": row.IsPrimaryKey,
		"is_required":    row.IsRequired,
		"field_index":    row.FieldIndex,
	}
}

func fieldRowRecordID(row systemModelFieldRow) string {
	if row.ID == 0 {
		return ""
	}
	return fmt.Sprintf("%d", row.ID)
}
