package models

// File overview:
// - Responsibility: defines built-in runtime model metadata and server-side behavior for sys model field inside the kernel model catalog.
// - Key symbols: `init`, `createSysModelField`, `SysModelFieldRecord`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"context"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"github.com/uptrace/bun"
)

// SysModelFieldRecord stores editable field metadata rows for custom models
// without overwriting the runtime-synced system field snapshot.
type SysModelFieldRecord struct {
	bun.BaseModel `bun:"table:sys_model_field,alias:sys_model_field"`
	orm.MetadataBaseModel
	BID           string `bun:"bid" json:"bid" meta:"index,label:BID,help:Stores the universal global unique record identifier for this record."`
	ModelName     string `bun:"model_name" json:"model_name" meta:"index,label:Model Name,help:Stores the model name value for this record."`
	FieldName     string `bun:"field_name" json:"field_name" meta:"index,label:Field Name,help:Stores the field name value for this record."`
	GoName        string `bun:"go_name" json:"go_name" meta:"label:Go Name,help:Stores the go name value for this record."`
	Label         string `bun:"label" json:"label" meta:"label:Label,help:Stores the human readable label shown to users."`
	DataType      string `bun:"data_type" json:"data_type" meta:"label:Data Type,help:Stores the data type value for this record. Relation kinds (many2one, one2one; one2many/many2many are metadata-only, not yet publishable) require relation_model to also be set."`
	RelationModel string `bun:"relation_model" json:"relation_model" meta:"label:Relation Model,help:Target model name for a relation-kind field (data_type many2one/one2one/one2many/many2many)."`
	BSONName      string `bun:"bson_name" json:"bson_name" meta:"label:BSON Name,help:Stores the bson name value for this record."`
	JSONName      string `bun:"json_name" json:"json_name" meta:"label:JSON Name,help:Stores the json name value for this record."`
	BunColumn     string `bun:"bun_column" json:"bun_column" meta:"label:Bun Column,help:Stores the bun column value for this record."`
	IsPrimaryKey  bool   `bun:"is_primary_key" json:"is_primary_key" meta:"label:Is Primary Key,help:Controls whether is primary key is enabled for this record."`
	IsRequired    bool   `bun:"is_required" json:"is_required" meta:"label:Is Required,help:Controls whether is required is enabled for this record."`
	FieldIndex    int    `bun:"field_index" json:"field_index" meta:"label:Field Index,help:Stores the numeric value for field index."`
	Status        string `bun:"status" json:"status" meta:"readonly,label:Status,help:Stores the publish lifecycle status of this field (INIT, PUBLISHING, PUBLISHED, UNPUBLISHING, UNPUBLISHED, DELETING, DELETED), mirrored from the owning model's publish state."`
}

const SysModelFieldModelName = "sys.model.field"

var SysModelFieldModel = orm.Define[SysModelFieldRecord](&Models, 40, orm.ModelSpec{
	Name:        SysModelFieldModelName,
	Label:       "Model Fields",
	Description: "System model field catalog exposed to the frontend",
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "create", Public: true, ModelOnly: true, Call: createSysModelField},
		{Name: "write", Public: true, UsesPathID: true, Call: writeSysModelField},
		{Name: "unlink", Public: true, UsesPathID: true, Call: unlinkSysModelField},
	},
})

func init() {
	registerFrontendBIDConstraint(SysModelFieldModelName)
}

// createSysModelField creates the sys model field handled by this package.
// Rows without a bid are the registry-synced snapshot of a Go-coded
// model's fields and are read-only; only custom fields on custom/dynamic
// models — identified by a caller-supplied bid — may be created here.
func createSysModelField(ctx context.Context, _ *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	valuesList, err := recordMapListFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	for _, values := range valuesList {
		if strings.TrimSpace(stringValue(values["bid"])) == "" {
			return nil, fmt.Errorf("sys.model.field rows synced from code are read-only; only custom fields (with a bid) can be created here")
		}
		if err := guardSysModelFieldModelNotPublished(stringValue(values["model_name"]), "created"); err != nil {
			return nil, err
		}
		if err := guardSysModelFieldModelNotPublishing(ctx, stringValue(values["model_name"]), "created"); err != nil {
			return nil, err
		}
		if strings.TrimSpace(stringValue(values["status"])) == "" {
			values["status"] = SysModelStatusInit
		}
	}
	return createLoggedMetadataRecords(ctx, SysModelFieldModelName, args, kwargs)
}

// Only rows already carrying a non-empty bid (custom-model fields) may be
// edited; registry-synced rows (no bid) are read-only.
func writeSysModelField(ctx context.Context, _ *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	if err := guardSysModelFieldRowsCustom(ctx, ids, "edited"); err != nil {
		return nil, err
	}
	return writeLoggedMetadataRecords(ctx, SysModelFieldModelName, ids, args, kwargs)
}

// Only rows already carrying a non-empty bid (custom-model fields) may be
// removed; registry-synced rows (no bid) are read-only.
func unlinkSysModelField(ctx context.Context, _ *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	if err := guardSysModelFieldRowsCustom(ctx, ids, "deleted"); err != nil {
		return nil, err
	}
	return unlinkLoggedMetadataRecords(ctx, SysModelFieldModelName, ids)
}

// guardSysModelFieldRowsCustom rejects the call when any target row has no
// bid, i.e. it is a registry-synced field snapshot rather than a
// custom-model field.
func guardSysModelFieldRowsCustom(ctx context.Context, ids []string, verb string) error {
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(SysModelFieldModelName, nil), ids, []string{"id", "bid", "model_name"})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if strings.TrimSpace(stringValue(row["bid"])) == "" {
			return fmt.Errorf("sys.model.field rows synced from code are read-only; only custom fields (with a bid) can be %s", verb)
		}
		if err := guardSysModelFieldModelNotPublished(stringValue(row["model_name"]), verb); err != nil {
			return err
		}
		if err := guardSysModelFieldModelNotPublishing(ctx, stringValue(row["model_name"]), verb); err != nil {
			return err
		}
	}
	return nil
}

// guardSysModelFieldModelNotPublished rejects a create/write/unlink against
// sys.model.field when the target row's model is already a live, published
// runtime model. PublishModel synthesizes its Go struct once, at publish
// time, from the sys.model.field catalog snapshot; editing that snapshot
// afterward never retroactively touches the already-registered type or its
// physical table columns. This is the single choke point every entry point
// (admin SPA form save, MCP tools, raw /rpc) funnels through — the MCP
// gateway also has its own faster-failing, MCP-shaped copy of this check
// (mcpGuardModelNotPublished) for a nicer error before it even resolves a
// field row, but this one is what actually closes the gap for every caller.
func guardSysModelFieldModelNotPublished(modelName, verb string) error {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return nil
	}
	if _, exists := orm.LookupModel(modelName); exists {
		return fmt.Errorf(
			"cannot have a field %s on %q while it is published; unpublish the model first, make the field change, then publish again to apply it to the database",
			verb, modelName,
		)
	}
	return nil
}

// guardSysModelFieldModelNotPublishing rejects a create/write/unlink against
// sys.model.field while the target row's model has an in-flight async
// publish job (status PUBLISHING). orm.PublishModel only registers the model
// (making guardSysModelFieldModelNotPublished's orm.LookupModel check start
// firing) partway through runSysModelPublishJob — a field edit that lands in
// the PUBLISHING window before that point would race the job's own read of
// the field-row snapshot (loadCustomSystemModelFieldRows), silently
// publishing a model built from a stale or half-edited field set. Checking
// the status column (set synchronously before the job starts, see
// publishSysModel) closes that window; UNPUBLISHING is already covered by
// the model staying registered/orm.LookupModel until the job unregisters it.
func guardSysModelFieldModelNotPublishing(ctx context.Context, modelName, verb string) error {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return nil
	}
	db := sysModelMetadataDB(ctx)
	if db == nil {
		return nil
	}
	var status string
	if err := db.NewSelect().
		Model((*SysModelRecord)(nil)).
		Column("status").
		Where("name = ?", modelName).
		Scan(ctx, &status); err != nil {
		return nil
	}
	if status == SysModelStatusPublishing {
		return fmt.Errorf("cannot have a field %s on %q while a publish is in progress; wait for it to finish", verb, modelName)
	}
	return nil
}
