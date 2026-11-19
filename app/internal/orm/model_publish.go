package orm

// File overview:
// - Responsibility: promotes a metadata-only custom model (a sys_model row
//   with its sys_model_field rows, created e.g. through base.model.create_custom
//   or sys.model.create) into a live, DB-backed, queryable runtime model —
//   the pipeline base.model.create_custom's own doc comment says does not
//   exist in this codebase. It closes that gap without a code deploy by
//   synthesizing a Go struct type at runtime via reflect.StructOf.
// - Key symbols: `PublishModel`, `buildDynamicStructType`, `dynamicFieldKinds`.
// - Pattern: Reuses the exact same struct-tag-driven definition/registration
//   pipeline static Go-coded models go through (ModelDefinitionFromRecord,
//   ModelRegistry.Register, AutoMigrateRegisteredModels) instead of
//   duplicating field-mapping or DDL logic for the dynamic case.

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode"

	"git.thebase.vn/base_hub/internal/services/log"
)

// DropCustomModelTable permanently drops the physical Postgres table backing
// a custom model. This is the one place in the codebase that issues a real
// DROP TABLE — every other unlink/unpublish path deliberately leaves data
// untouched (see PublishModel/UnpublishModel). Callers must have already
// proven the model is unpublished (not in the live ModelRegistry) before
// calling this; it does not re-check the registry itself, so calling it
// against a still-live model's table is a caller bug, not something this
// function guards against.
func DropCustomModelTable(ctx context.Context, storageName string) error {
	storageName = strings.TrimSpace(storageName)
	if storageName == "" {
		return fmt.Errorf("orm: storage name is required to drop a table")
	}

	db := BunDBFromContext(ctx)
	if db == nil {
		log.Errorf("BunDB is not initialized")
		return fmt.Errorf("BunDB is not initialized")
	}

	if _, err := db.NewDropTable().Table(storageName).IfExists().Exec(ctx); err != nil {
		return fmt.Errorf("orm: drop table %q: %w", storageName, err)
	}
	return nil
}

// dynamicFieldKind maps one sys.model.field data_type string onto the
// concrete Go type and storage hints needed to build a real column.
type dynamicFieldKind struct {
	goType  reflect.Type
	bunType string
	kind    FieldKind
}

// dynamicFieldKinds is deliberately a closed set: publishing must fail fast
// on an unrecognized data_type rather than silently guessing a column shape
// for a value nothing validated at sys.model.field create time.
var dynamicFieldKinds = map[string]dynamicFieldKind{
	"text":     {goType: reflect.TypeOf(""), kind: FieldKindText},
	"string":   {goType: reflect.TypeOf(""), kind: FieldKindText},
	"integer":  {goType: reflect.TypeOf(int64(0)), kind: FieldKindInteger},
	"int64":    {goType: reflect.TypeOf(int64(0)), kind: FieldKindInteger},
	"float":    {goType: reflect.TypeOf(float64(0)), kind: FieldKindFloat},
	"float64":  {goType: reflect.TypeOf(float64(0)), kind: FieldKindFloat},
	"boolean":  {goType: reflect.TypeOf(false), kind: FieldKindBoolean},
	"bool":     {goType: reflect.TypeOf(false), kind: FieldKindBoolean},
	"date":     {goType: reflect.TypeOf(time.Time{}), bunType: "date", kind: FieldKindDate},
	"datetime": {goType: reflect.TypeOf(time.Time{}), kind: FieldKindDateTime},
	"json":     {goType: reflect.TypeOf(map[string]any{}), bunType: "jsonb", kind: FieldKindJSON},
	// many2one/one2one are the only relation kinds publish can provision today:
	// both are a single nullable FK column on the owning table. one2many/many2many
	// need a second table (inverse column or join table) publish does not
	// provision, so those data_type values are rejected explicitly below instead
	// of silently guessing a shape.
	"many2one": {goType: reflect.PointerTo(reflect.TypeOf(int64(0))), kind: FieldKindManyToOne},
	"one2one":  {goType: reflect.PointerTo(reflect.TypeOf(int64(0))), kind: FieldKindOneToOne},
}

// dynamicUnsupportedRelationDataTypes are relation data_type values a catalog
// field row may legitimately carry (e.g. authored for a future join-table
// publish pipeline) but that today's single-table PublishModel cannot
// provision. Kept distinct from an actually-unrecognized data_type so the
// error message can point at the right gap instead of "unsupported type".
var dynamicUnsupportedRelationDataTypes = map[string]struct{}{
	"one2many":  {},
	"many2many": {},
}

// dynamicReservedColumns are already provided by the embedded orm.BaseModel
// fragment (id, active, created_at, updated_at); a catalog field row using
// one of these names is skipped rather than duplicated.
var dynamicReservedColumns = map[string]struct{}{
	"id": {}, "active": {}, "created_at": {}, "updated_at": {},
}

// PublishModel promotes modelName from a metadata-only sys_model/sys_model_field
// catalog entry into a registered runtime model with a live Postgres table.
// The caller must have already created the model (base.model.create_custom /
// sys.model.create) and at least one field (base.model.add_field /
// sys.model.field create); PublishModel only reads that catalog, it never
// writes it directly — SyncModelRegistryToDB (called via
// AutoMigrateRegisteredModels below) is the single place that upserts
// sys_model/sys_model_field snapshot columns.
func PublishModel(ctx context.Context, modelName string) (*ModelMetadata, error) {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		log.Errorf("orm: model name is required")
		return nil, fmt.Errorf("orm: model name is required")
	}
	if _, exists := LookupModel(modelName); exists {
		return nil, fmt.Errorf("orm: model %q is already a registered runtime model", modelName)
	}

	db := BunDBFromContext(ctx)
	if db == nil {
		log.Errorf("BunDB is not initialized")
		return nil, fmt.Errorf("BunDB is not initialized")
	}

	var catalogRow systemModelRow
	if err := db.NewSelect().Model(&catalogRow).Where("name = ?", modelName).Scan(ctx); err != nil {
		return nil, fmt.Errorf("orm: model %q is not registered in the sys.model catalog: %w", modelName, err)
	}
	if strings.TrimSpace(catalogRow.BID) == "" {
		return nil, fmt.Errorf("orm: model %q is a Go-registered model snapshot and cannot be published from metadata", modelName)
	}

	fieldRows, err := loadCustomSystemModelFieldRows(ctx, db, modelName)
	if err != nil {
		return nil, err
	}

	typ, err := buildDynamicStructType(fieldRows)
	if err != nil {
		return nil, err
	}

	definition, err := ModelDefinitionFromRecord(modelName, reflect.New(typ).Interface())
	if err != nil {
		return nil, fmt.Errorf("orm: publish %q: %w", modelName, err)
	}

	meta, err := DefaultModelRegistry().Register(typ, ModelSpec{
		Name:        modelName,
		Label:       firstNonEmpty(catalogRow.Label, modelName),
		Description: catalogRow.Description,
		Definition:  definition,
	})
	if err != nil {
		return nil, err
	}

	// Provisioning the physical table reuses the exact boot-time migration
	// sweep (idempotent for every already-existing table via IfNotExists /
	// diff-based reconciliation) instead of a bespoke single-model path, so
	// a freshly published model gets the same schema-safety guards (column
	// count cap, rename detection, translated-column handling) as any
	// statically registered one.
	if err := AutoMigrateRegisteredModels(ctx); err != nil {
		// Roll back the in-memory registration so a failed publish never
		// leaves a model queryable with no backing table.
		DefaultModelRegistry().Unregister(modelName)
		return nil, fmt.Errorf("orm: publish %q: provision physical table: %w", modelName, err)
	}

	// Registering metadata and provisioning the table is not enough: runtime
	// CRUD (search/read/create/write/unlink) dispatches through a separate
	// registry (runtimeExecutorRegistry, see runtime_service.go) that static
	// Go models populate at boot via Handler[T].Init. A published model has
	// no compile-time Go type to build a Handler[T] from, so it needs its own
	// executor registered here -- otherwise every request against it 400s
	// with "orm: no runtime executor registered for model ...".
	RegisterRuntimeExecutor(modelName, newDynamicRuntimeExecutor(meta, meta.StorageName))

	return meta, nil
}

// UnpublishModel is the explicit, safety-checked counterpart to PublishModel:
// it removes modelName from the live runtime registry so it stops being
// queryable via base.record.*, while deliberately leaving its physical table
// and rows untouched — this codebase never destructively drops data on
// unlink/unpublish (see PublishModel's own doc comment for the symmetric
// provisioning side). It is idempotent (a no-op if the model is not
// currently registered) and only ever exposed to callers that need to take a
// published model offline without deleting its sys.model catalog row —
// unlinkSysModel handles the "catalog row deleted" case itself, directly via
// DefaultModelRegistry().Unregister, since it has already proven bid-custody
// through its own guard and the catalog row is gone by the time it would
// call this function.
//
// Refuses to touch a Go-registered (no bid) model: unregistering e.g.
// sys.model or base.users at runtime would break every subsequent request
// against it, and there is no reflect.StructOf-synthesized type to
// re-register it from — only PublishModel-created dynamic models are safe to
// unregister this way.
func UnpublishModel(ctx context.Context, modelName string) error {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		log.Errorf("orm: model name is required")
		return fmt.Errorf("orm: model name is required")
	}
	if _, exists := LookupModel(modelName); !exists {
		// Already unpublished, or never was — idempotent no-op.
		return nil
	}

	db := BunDBFromContext(ctx)
	if db == nil {
		log.Errorf("BunDB is not initialized")
		return fmt.Errorf("BunDB is not initialized")
	}
	var catalogRow systemModelRow
	if err := db.NewSelect().Model(&catalogRow).Where("name = ?", modelName).Scan(ctx); err != nil {
		return fmt.Errorf("orm: model %q is not registered in the sys.model catalog: %w", modelName, err)
	}
	if strings.TrimSpace(catalogRow.BID) == "" {
		return fmt.Errorf("orm: model %q is a Go-registered model and cannot be unpublished at runtime", modelName)
	}

	DefaultModelRegistry().Unregister(modelName)
	// Symmetric with PublishModel's RegisterRuntimeExecutor call: leaving the
	// executor registered after unpublish would let CRUD keep dispatching
	// against a model LookupModel no longer reports as registered, and would
	// go stale (pointing at pre-republish metadata) across an
	// unpublish/republish cycle.
	UnregisterRuntimeExecutor(modelName)

	// Clear the catalog snapshot's storage markers so storage_backend/
	// storage_name — the columns the admin UI's Publish/Unpublish buttons
	// key their visibility off (see view-sys-model.xml) — accurately report
	// "not currently a live runtime model" instead of going stale at
	// whatever the last successful PublishModel/SyncModelRegistryToDB wrote.
	if _, err := db.NewUpdate().
		Model((*systemModelRow)(nil)).
		Set("storage_backend = ?", "").
		Set("storage_name = ?", "").
		Where("name = ?", modelName).
		Exec(ctx); err != nil {
		log.Errorf("orm: unpublish %q: clear storage markers: %v", modelName, err)
		return fmt.Errorf("orm: unpublish %q: clear storage markers: %w", modelName, err)
	}
	return nil
}

// buildDynamicStructType synthesizes a Go struct type at runtime from
// fieldRows via reflect.StructOf: one anonymous orm.BaseModel embed for the
// standard id/active/created_at/updated_at columns, plus one field per
// catalog row. reflect.StructOf can panic on internal invariant violations
// (e.g. a malformed tag), so the whole build is recovered into a plain error
// — a bad catalog row must never crash the calling request goroutine.
func buildDynamicStructType(fieldRows []systemModelFieldRow) (typ reflect.Type, err error) {
	defer func() {
		if r := recover(); r != nil {
			typ = nil
			err = fmt.Errorf("orm: publish: building dynamic struct type: %v", r)
		}
	}()

	structFields := []reflect.StructField{
		{Name: "BaseModel", Type: reflect.TypeOf(BaseModel{}), Anonymous: true},
	}
	usedGoNames := map[string]struct{}{"BaseModel": {}}

	for _, row := range fieldRows {
		column := strings.TrimSpace(row.FieldName)
		if column == "" {
			continue
		}
		if _, reserved := dynamicReservedColumns[column]; reserved {
			continue
		}

		dataType := strings.ToLower(strings.TrimSpace(row.DataType))
		kindInfo, ok := dynamicFieldKinds[dataType]
		if !ok {
			if _, unsupportedRelation := dynamicUnsupportedRelationDataTypes[dataType]; unsupportedRelation {
				return nil, fmt.Errorf(
					"orm: field %q has data_type %q, which base.model.publish cannot provision yet (only many2one/one2one relation columns are supported)",
					column, row.DataType,
				)
			}
			return nil, fmt.Errorf(
				"orm: field %q has unsupported data_type %q; supported types: text, integer, float, boolean, date, datetime, json, many2one, one2one",
				column, row.DataType,
			)
		}

		relationModel := strings.TrimSpace(row.RelationModel)
		if (kindInfo.kind == FieldKindManyToOne || kindInfo.kind == FieldKindOneToOne) && relationModel == "" {
			return nil, fmt.Errorf("orm: field %q has data_type %q but no relation_model", column, row.DataType)
		}

		goName, err := pascalCaseFieldName(column, usedGoNames)
		if err != nil {
			return nil, err
		}

		label := sanitizeTagValue(firstNonEmpty(row.Label, column))
		structFields = append(structFields, reflect.StructField{
			Name: goName,
			Type: kindInfo.goType,
			Tag:  reflect.StructTag(buildDynamicFieldTag(column, label, relationModel, kindInfo, row.IsRequired)),
		})
	}

	if len(structFields) == 1 {
		return nil, fmt.Errorf("orm: model has no publishable fields beyond the standard id/active/created_at/updated_at columns")
	}
	return reflect.StructOf(structFields), nil
}

// buildDynamicFieldTag renders the bun/json/meta struct tag for one dynamic
// field, mirroring the hand-written tags on statically defined models (see
// code-pattern.md's Bun Model Field Tags convention).
func buildDynamicFieldTag(column, label, relationModel string, kindInfo dynamicFieldKind, required bool) string {
	bunOpts := column
	if kindInfo.bunType != "" {
		bunOpts += ",type:" + kindInfo.bunType
	}
	if kindInfo.kind == FieldKindJSON {
		bunOpts += ",default:'{}'"
	}
	isRelation := kindInfo.kind == FieldKindManyToOne || kindInfo.kind == FieldKindOneToOne
	switch {
	case required && !isRelation:
		bunOpts += ",notnull"
	case kindInfo.goType == reflect.TypeOf(time.Time{}):
		// Optional date/datetime columns must tolerate an unset Go zero value
		// instead of persisting it as a literal (invalid/misleading) timestamp.
		bunOpts += ",nullzero"
	}

	metaOpts := "label:" + label
	switch {
	case kindInfo.kind == FieldKindDate:
		// time.Time defaults to FieldKindDateTime; date-only columns need an
		// explicit override (see bunFieldKind's kindOverride handling).
		metaOpts = "kind:date," + metaOpts
	case isRelation:
		// A pointer-to-int64 column defaults to plain FieldKindInteger unless
		// explicitly overridden (see bunFieldKind's isIntegerKind branch), and a
		// relation field errors at registration if "model" is set without an
		// explicit "kind" override — both must be present together.
		metaOpts = fmt.Sprintf("kind:%s,model:%s,ondelete:SET NULL,index,%s", kindInfo.kind, sanitizeTagValue(relationModel), metaOpts)
	}

	return fmt.Sprintf(`bun:"%s" json:"%s" meta:"%s"`, bunOpts, column, metaOpts)
}

// pascalCaseFieldName converts one sys.model.field field_name (snake_case,
// caller-supplied) into a valid, unique, exported Go struct field name.
func pascalCaseFieldName(fieldName string, used map[string]struct{}) (string, error) {
	parts := strings.FieldsFunc(fieldName, func(r rune) bool {
		return r == '_' || r == '-' || r == '.' || r == ' '
	})
	var b strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]))
		b.WriteString(part[1:])
	}
	name := b.String()
	if name == "" || !unicode.IsLetter(rune(name[0])) {
		name = "F" + name
	}
	if _, dup := used[name]; dup {
		return "", fmt.Errorf("orm: field_name %q collides with another field after Go identifier normalization", fieldName)
	}
	used[name] = struct{}{}
	return name, nil
}

// sanitizeTagValue strips characters that would break struct tag parsing
// (unescaped quotes, comma/colon option separators) out of caller-supplied
// text destined for a `meta:"label:..."` tag value.
func sanitizeTagValue(value string) string {
	replacer := strings.NewReplacer(`"`, "", ",", " ", ":", " ", "`", "")
	return strings.TrimSpace(replacer.Replace(value))
}
