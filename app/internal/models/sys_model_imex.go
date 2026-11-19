package models

// File overview:
// - Responsibility: defines the `sys.model.imex` kernel model — saved per-model import/export
//   column templates used by the list view's Actions > Import/Export dialogs — plus the generic
//   `import_records` method that upserts spreadsheet rows into any registered model.
// - Key symbols: `SysModelImexRecord`, `importSysModelImexRecords`, `coerceImexCell`.
// - Pattern: every row is written through the target model's own `create`/`write` runtime method
//   (ACL, record rules, company scope, StandardHooks all apply) inside ONE transaction, so a bad
//   row rolls back the whole batch instead of leaving a half-imported file behind.

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

// SysModelImexRecord is one named column template for a model. `field_names` keeps the user's
// column order, which is also the exported/template header order.
type SysModelImexRecord struct {
	bun.BaseModel `bun:"table:sys_model_imex,alias:sys_model_imex"`
	orm.MetadataBaseModel
	Name      string `bun:"name,notnull" json:"name" meta:"index,label:Name,help:Stores the template name shown in the import/export dialogs."`
	ModelName string `bun:"model_name,notnull" json:"model_name" meta:"index,label:Model,help:Stores the technical name of the model this template exports or imports."`
	// Kind is false for an export template and true for an import template.
	Kind       bool     `bun:"kind,default:false" json:"kind" default:"false" meta:"index,label:Import Template,help:False for an export template, true for an import template."`
	FieldNames []string `bun:"field_names,type:jsonb,nullzero" json:"field_names" meta:"kind:json,label:Fields,help:Stores the ordered list of field names the template exports or imports."`
}

const SysModelImexModelName = "sys.model.imex"

// sysModelImexMaxRows bounds one import call so a single transaction cannot hold row locks
// for an unbounded batch; larger files must be split by the caller.
const sysModelImexMaxRows = 5000

var SysModelImexModel = orm.Define[SysModelImexRecord](&Models, 32, orm.ModelSpec{
	Name:        SysModelImexModelName,
	Label:       "Model Import/Export Template",
	Description: "Saved per-model column templates for the list view import/export dialogs",
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "import_records", Public: true, ModelOnly: true, Call: importSysModelImexRecords},
	},
})

// importSysModelImexRecords upserts `rows` into `model`.
// kwargs: model (string), rows ([]{field: cell}). A non-empty `id` cell updates that record,
// otherwise a new record is created. Blank cells are skipped (never clear a stored value), so
// re-importing an exported file with some columns emptied is safe. Returns
// {created, updated, total}; any row error aborts the whole batch with its 1-based row number.
func importSysModelImexRecords(ctx context.Context, _ *orm.ModelMetadata, _ []string, _ []any, kwargs map[string]any) (any, error) {
	modelName := stringValue(mapValue(kwargs, "model"))
	target, ok := orm.LookupModel(modelName)
	if !ok {
		return nil, fmt.Errorf("sys.model.imex import_records: model %q is not registered", modelName)
	}
	rows, _ := mapValue(kwargs, "rows").([]any)
	if len(rows) == 0 {
		return nil, fmt.Errorf("sys.model.imex import_records: rows are required")
	}
	if len(rows) > sysModelImexMaxRows {
		return nil, fmt.Errorf("sys.model.imex import_records: at most %d rows per import (got %d)", sysModelImexMaxRows, len(rows))
	}
	fields := imexFieldIndex(target)

	created, updated := 0, 0
	err := withSysModelImexTx(ctx, func(txCtx context.Context) error {
		for index, raw := range rows {
			// Row 1 is the header line in the user's file, so data row N is file row N+1.
			rowLabel := index + 2
			cells, _ := raw.(map[string]any)
			id, values, err := imexRowValues(txCtx, fields, cells)
			if err != nil {
				return fmt.Errorf("row %d: %w", rowLabel, err)
			}
			if len(values) == 0 {
				continue
			}
			if id != "" {
				if _, err := orm.ExecuteRuntimeMethod(txCtx, target.Name, "write", []string{id}, []any{values}, nil); err != nil {
					return fmt.Errorf("row %d: update id %s: %w", rowLabel, id, err)
				}
				updated++
				continue
			}
			if _, err := orm.ExecuteRuntimeMethod(txCtx, target.Name, "create", nil, []any{values}, nil); err != nil {
				return fmt.Errorf("row %d: create: %w", rowLabel, err)
			}
			created++
		}
		return nil
	})
	if err != nil {
		log.Errorf("sys.model.imex import_records %s: %v", target.Name, err)
		return nil, err
	}
	return orm.RecordMap{"created": created, "updated": updated, "total": created + updated}, nil
}

// imexFieldIndex maps field name -> metadata for O(1) per-cell lookup.
func imexFieldIndex(model *orm.ModelMetadata) map[string]orm.FieldMetadata {
	index := make(map[string]orm.FieldMetadata, len(model.Fields))
	for _, field := range model.Fields {
		index[field.Name] = field
	}
	return index
}

// imexRowValues splits one row into the update id (if any) and coerced write values. Cells are
// visited in sorted key order so the first reported error is deterministic.
func imexRowValues(ctx context.Context, fields map[string]orm.FieldMetadata, cells map[string]any) (string, orm.RecordMap, error) {
	keys := make([]string, 0, len(cells))
	for key := range cells {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	id := ""
	values := orm.RecordMap{}
	for _, key := range keys {
		text := strings.TrimSpace(fmt.Sprint(cells[key]))
		if cells[key] == nil || text == "" {
			continue
		}
		if key == "id" {
			parsed, err := strconv.ParseInt(text, 10, 64)
			if err != nil || parsed <= 0 {
				return "", nil, fmt.Errorf("id %q is not a valid record id", text)
			}
			id = strconv.FormatInt(parsed, 10)
			continue
		}
		field, ok := fields[key]
		if !ok {
			return "", nil, fmt.Errorf("unknown field %q", key)
		}
		if field.ReadOnly || field.Compute != "" {
			return "", nil, fmt.Errorf("field %q is read-only", key)
		}
		value, err := coerceImexCell(ctx, field, text)
		if err != nil {
			return "", nil, fmt.Errorf("%s: %w", imexFieldLabel(field), err)
		}
		values[key] = value
	}
	// A row carrying only an id has nothing to write.
	if len(values) == 0 {
		return id, nil, nil
	}
	return id, values, nil
}

// coerceImexCell turns one spreadsheet cell into the typed value the target field expects.
func coerceImexCell(ctx context.Context, field orm.FieldMetadata, text string) (any, error) {
	switch field.Kind {
	case orm.FieldKindBoolean:
		return parseImexBool(text)
	case orm.FieldKindInteger:
		parsed, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not an integer", text)
		}
		return parsed, nil
	case orm.FieldKindFloat:
		parsed, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", text)
		}
		return parsed, nil
	case orm.FieldKindSelection:
		return matchImexSelection(field, text)
	case orm.FieldKindManyToOne, orm.FieldKindOneToOne:
		return resolveImexRelation(ctx, field, text)
	case orm.FieldKindOneToMany, orm.FieldKindManyToMany:
		return nil, fmt.Errorf("relation lists cannot be imported")
	default:
		return text, nil
	}
}

// parseImexBool accepts the same words as the EDC device import (TRUE/YES/1/ACTIVE ...).
func parseImexBool(text string) (bool, error) {
	switch strings.ToLower(text) {
	case "true", "1", "yes", "y", "active":
		return true, nil
	case "false", "0", "no", "n", "inactive":
		return false, nil
	}
	return false, fmt.Errorf("%q must be TRUE/FALSE, YES/NO or 1/0", text)
}

// matchImexSelection accepts either the stored value or its label (case-insensitive), because an
// exported file carries labels while hand-written files often carry values.
func matchImexSelection(field orm.FieldMetadata, text string) (any, error) {
	for _, option := range field.Selection {
		if strings.EqualFold(option.Value, text) || strings.EqualFold(option.Label, text) {
			return option.Value, nil
		}
	}
	return nil, fmt.Errorf("%q is not an allowed value", text)
}

// resolveImexRelation accepts a numeric id or the related record's exact display name. An
// ambiguous name is an error rather than "first match wins" — silently linking the wrong
// partner/company is worse than asking the user to use the id.
func resolveImexRelation(ctx context.Context, field orm.FieldMetadata, text string) (any, error) {
	if parsed, err := strconv.ParseInt(text, 10, 64); err == nil && parsed > 0 {
		return parsed, nil
	}
	related, ok := orm.LookupModel(field.Relation)
	if !ok {
		return nil, fmt.Errorf("related model %q is not registered", field.Relation)
	}
	recName := strings.TrimSpace(related.RecName)
	if recName == "" {
		recName = "name"
	}
	// Search goes through the runtime repository, so the caller's record rules scope the match.
	matches, err := orm.RuntimeRepositorySearchRead(ctx, related, orm.RecordMap{recName: text, "__limit": 2}, []string{"id"})
	if err != nil {
		return nil, fmt.Errorf("lookup %q in %s: %w", text, related.Name, err)
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no %s named %q", related.Name, text)
	case 1:
		if id, ok := int64FromAny(matches[0]["id"]); ok {
			return id, nil
		}
		return nil, fmt.Errorf("lookup %q in %s returned no id", text, related.Name)
	default:
		return nil, fmt.Errorf("%q matches several %s records; use the record id", text, related.Name)
	}
}

// imexFieldLabel prefers the human label in error messages, since users see column labels.
func imexFieldLabel(field orm.FieldMetadata) string {
	if label := strings.TrimSpace(field.Label); label != "" {
		return label
	}
	return field.Name
}

// withSysModelImexTx runs fn in one transaction, reusing the caller's transaction when the
// request environment is already bound to one (same binding rule as the ORM's withRuntimeTx), so
// every nested create/write joins the same unit of work.
func withSysModelImexTx(ctx context.Context, fn func(ctx context.Context) error) error {
	env := orm.EnvironmentFromContext(ctx)
	if env != nil {
		switch env.DB().(type) {
		case bun.Tx, *bun.Tx:
			return fn(ctx)
		}
	}
	db := orm.BunDBFromContext(ctx)
	if db == nil {
		return fmt.Errorf("sys.model.imex: database is not initialized")
	}
	if env == nil {
		env = orm.NewEnvironment(db, 0, nil, false)
	}
	// The scope flushes on the Environment's DB, so bind the handle the
	// transaction is opened from. This call owns the transaction and so the
	// after-commit queue: a batch that rolls back at row N fires no record
	// events (workflow triggers) for rows 1..N-1.
	baseCtx := orm.ContextWithEnvironment(ctx, env.WithDB(db))
	return orm.WithAfterCommitScope(baseCtx, func(scopeCtx context.Context) error {
		return db.RunInTx(scopeCtx, nil, func(txCtx context.Context, tx bun.Tx) error {
			return fn(orm.ContextWithEnvironment(txCtx, env.WithDB(tx)))
		})
	})
}
