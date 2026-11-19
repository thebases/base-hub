package orm

// File overview:
// - Responsibility: verifies ORM runtime semantics, repository behavior, and registry coordination for model registry migrate.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down runtime invariants so refactors preserve transaction, RBAC, registry, and storage behavior.

// These tests verify that registry-backed migration planning emits deterministic
// schema changes before any live database write happens.

import (
	"reflect"
	"testing"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/migrate"
	"github.com/uptrace/bun/migrate/sqlschema"
)

type migrationSysDocument struct {
	BaseModel
	Name string `bun:"name" json:"name"`
}

type migrationBaseDocument struct {
	BaseModel
	Name string `bun:"name" json:"name"`
}

type migrationOtherDocument struct {
	BaseModel
	Name string `bun:"name" json:"name"`
}

type migrationJSONDefaultsDocument struct {
	BaseModel
	Settings map[string]any `bun:"settings,type:jsonb,nullzero" json:"settings" meta:"kind:json"`
	Tags     []string       `bun:"tags,type:jsonb,nullzero" json:"tags" meta:"kind:json"`
}

func TestCanonicalModelTableNameUsesSingleUnderscore(t *testing.T) {
	if got := canonicalModelTableName("sys.actions.server"); got != "sys_actions_server" {
		t.Fatalf("expected sys.actions.server to normalize to sys_actions_server, got %q", got)
	}
}

func TestModelSpecResolveStorageNameDefaultsToDefinitionTable(t *testing.T) {
	spec := ModelSpec{
		Name:       "sys.actions.server",
		Definition: ModelDefinition("sys.actions.server", (*migrationSysDocument)(nil)),
	}

	if got := spec.ResolveStorageName(); got != "sys_actions_server" {
		t.Fatalf("expected definition-backed storage name fallback, got %q", got)
	}
}

func TestModelRegistryMigrationPlansOrderSysThenBaseThenOther(t *testing.T) {
	registry := NewModelRegistry()

	for _, item := range []struct {
		name string
		typ  any
	}{
		{name: "bank.account", typ: (*migrationOtherDocument)(nil)},
		{name: "tbl.partner", typ: (*migrationBaseDocument)(nil)},
		{name: "sys.model", typ: (*migrationSysDocument)(nil)},
	} {
		if _, err := registry.Register(modelRecordTypeMust(item.typ), ModelSpec{
			Name:       item.name,
			Label:      item.name,
			Definition: ModelDefinition(item.name, item.typ),
		}); err != nil {
			t.Fatalf("register %s: %v", item.name, err)
		}
	}

	plans := registry.migrationPlans()
	if len(plans) != 3 {
		t.Fatalf("expected 3 migration plans, got %d", len(plans))
	}

	got := []string{plans[0].metadata.Name, plans[1].metadata.Name, plans[2].metadata.Name}
	want := []string{"sys.model", "tbl.partner", "bank.account"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected migration order: got %v want %v", got, want)
		}
	}
}

func TestInspectModelTableAddsImplicitObjectJSONBDefault(t *testing.T) {
	db := bun.NewDB(nil, pgdialect.New())
	meta, err := RegisterModelType[migrationJSONDefaultsDocument](ModelSpec{
		Name:       "test.migration_json_defaults",
		Definition: ModelDefinition("test.migration_json_defaults", (*migrationJSONDefaultsDocument)(nil)),
	})
	if err != nil {
		t.Fatalf("RegisterModelType() error = %v", err)
	}

	table, err := inspectModelTable(db, meta)
	if err != nil {
		t.Fatalf("inspectModelTable() error = %v", err)
	}
	if table == nil {
		t.Fatalf("inspectModelTable() returned nil table")
	}

	columns := columnsByName(table.GetColumns())
	settings := columns["settings"]
	if settings == nil {
		t.Fatalf("expected settings column")
	}
	if settings.GetDefaultValue() != "'{}'::jsonb" {
		t.Fatalf("expected settings jsonb default, got %q", settings.GetDefaultValue())
	}

	tags := columns["tags"]
	if tags == nil {
		t.Fatalf("expected tags column")
	}
	if tags.GetDefaultValue() != "'[]'::jsonb" {
		t.Fatalf("expected slice-backed jsonb default to be [] jsonb, got %q", tags.GetDefaultValue())
	}
}

func TestShouldWidenLegacyLongTextType(t *testing.T) {
	for _, tc := range []struct {
		name     string
		dataType string
		want     bool
	}{
		{name: "varchar", dataType: "character varying", want: true},
		{name: "char", dataType: "character", want: true},
		{name: "text", dataType: "text", want: false},
		{name: "empty", dataType: "", want: false},
		{name: "json", dataType: "jsonb", want: false},
	} {
		if got := shouldWidenLegacyLongTextType(tc.dataType); got != tc.want {
			t.Fatalf("%s: shouldWidenLegacyLongTextType(%q) = %v, want %v", tc.name, tc.dataType, got, tc.want)
		}
	}
}

func TestManagedModelColumnsUsesRegisteredBunColumns(t *testing.T) {
	meta := &ModelMetadata{
		Name: "test.columns",
		Fields: []FieldMetadata{
			{Name: "id", BunColumn: "id"},
			{Name: "name", BunColumn: "name"},
			{Name: "child_ids", BunColumn: "child_ids", Kind: FieldKindOneToMany, Stored: false},
			{Name: "runtime_only"},
		},
	}

	managed := managedModelColumns(meta)
	for _, column := range []string{"id", "name", "child_ids"} {
		if _, ok := managed[column]; !ok {
			t.Fatalf("expected managed columns to include %q", column)
		}
	}
	if _, ok := managed["runtime_only"]; ok {
		t.Fatalf("expected fields without bun columns to stay unmanaged")
	}
}

func TestStaleModelColumnsReturnsRemovedColumnsInStableOrder(t *testing.T) {
	meta := &ModelMetadata{
		Name: "test.columns",
		Fields: []FieldMetadata{
			{Name: "id", BunColumn: "id"},
			{Name: "name", BunColumn: "name"},
			{Name: "created_at", BunColumn: "created_at"},
		},
	}

	got := staleModelColumns(meta, []string{"legacy_code", "name", "id", "removed_flag", "created_at"})
	want := []string{"legacy_code", "removed_flag"}
	if len(got) != len(want) {
		t.Fatalf("unexpected stale columns length: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected stale columns: got %v want %v", got, want)
		}
	}
}

func TestManagedStoredModelColumnsSkipsRuntimeOnlyFields(t *testing.T) {
	meta := &ModelMetadata{
		Name: "test.columns",
		Fields: []FieldMetadata{
			{Name: "id", BunColumn: "id", Stored: true},
			{Name: "created_at", BunColumn: "created_at", Stored: true},
			{Name: "child_ids", BunColumn: "child_ids", Stored: false},
			{Name: "computed_name", BunColumn: "computed_name", Stored: false},
		},
	}

	managed := managedStoredModelColumns(meta)
	if _, ok := managed["id"]; !ok {
		t.Fatalf("expected id to be managed")
	}
	if _, ok := managed["created_at"]; !ok {
		t.Fatalf("expected created_at to be managed")
	}
	if _, ok := managed["child_ids"]; ok {
		t.Fatalf("expected runtime-only child_ids to stay unmanaged")
	}
	if _, ok := managed["computed_name"]; ok {
		t.Fatalf("expected computed field to stay unmanaged")
	}
}

func TestColumnDefinitionSQLSupportsIdentityAndDefault(t *testing.T) {
	identity := &sqlschema.BaseColumn{
		Name:            "id",
		SQLType:         "bigint",
		IsAutoIncrement: true,
	}
	if got := columnDefinitionSQL(identity); got != "bigint GENERATED BY DEFAULT AS IDENTITY" {
		t.Fatalf("unexpected identity column SQL: %q", got)
	}

	withDefault := &sqlschema.BaseColumn{
		Name:         "active",
		SQLType:      "boolean",
		DefaultValue: "true",
	}
	if got := columnDefinitionSQL(withDefault); got != "boolean DEFAULT true" {
		t.Fatalf("unexpected default column SQL: %q", got)
	}
}

// TestSQLReadyColumnDefaultQuotesBareTextualDefault reproduces the
// SQLSTATE 0A000 "cannot use column reference in DEFAULT expression"
// bootstrap failure on base_workflow: bun's BunModelInspector strips the
// quotes off a `default:'linear'` struct tag before it reaches our target
// schema, but pgdialect's AddColumnOp/ChangeColumnTypeOp migrator writes
// that value verbatim after "DEFAULT ", so the bare token "linear" gets
// parsed by Postgres as a column reference instead of a text literal.
func TestSQLReadyColumnDefaultQuotesBareTextualDefault(t *testing.T) {
	textual := &sqlschema.BaseColumn{
		Name:         "execution_mode",
		SQLType:      "varchar",
		DefaultValue: "linear",
	}
	got := sqlReadyColumnDefault(textual)
	if got.GetDefaultValue() != "'linear'" {
		t.Fatalf("expected bare textual default to be quoted, got %q", got.GetDefaultValue())
	}

	alreadyQuoted := &sqlschema.BaseColumn{
		Name:         "dag_failure_policy",
		SQLType:      "varchar",
		DefaultValue: "'fail_branch'",
	}
	if got := sqlReadyColumnDefault(alreadyQuoted); got.GetDefaultValue() != "'fail_branch'" {
		t.Fatalf("expected already-quoted default to stay unchanged, got %q", got.GetDefaultValue())
	}

	numeric := &sqlschema.BaseColumn{
		Name:         "max_parallel_steps",
		SQLType:      "integer",
		DefaultValue: "4",
	}
	if got := sqlReadyColumnDefault(numeric); got.GetDefaultValue() != "4" {
		t.Fatalf("expected numeric default to stay bare, got %q", got.GetDefaultValue())
	}

	boolean := &sqlschema.BaseColumn{
		Name:         "is_active",
		SQLType:      "boolean",
		DefaultValue: "true",
	}
	if got := sqlReadyColumnDefault(boolean); got.GetDefaultValue() != "true" {
		t.Fatalf("expected boolean default to stay bare, got %q", got.GetDefaultValue())
	}

	functionCall := &sqlschema.BaseColumn{
		Name:         "external_id",
		SQLType:      "uuid",
		DefaultValue: "gen_random_uuid()",
	}
	if got := sqlReadyColumnDefault(functionCall); got.GetDefaultValue() != "gen_random_uuid()" {
		t.Fatalf("expected function-call default to stay bare, got %q", got.GetDefaultValue())
	}
}

func TestPlanTableSchemaOperationsDetectsAddRenameChangeAndDrop(t *testing.T) {
	db := bun.NewDB(nil, pgdialect.New())

	metadata := &ModelMetadata{
		Name:        "sys.config_param",
		StorageName: "sys_config_param",
		Fields: []FieldMetadata{
			{Name: "id", BunColumn: "id", Stored: true},
			{Name: "name", BunColumn: "name", Stored: true},
			{Name: "value", BunColumn: "value", Stored: true},
			{Name: "updated_at", BunColumn: "updated_at", Stored: true},
		},
	}

	current := &sqlschema.BaseTable{
		Name: "sys_config_param",
		Columns: []sqlschema.Column{
			&sqlschema.BaseColumn{Name: "key", SQLType: "text"},
			&sqlschema.BaseColumn{Name: "value", SQLType: "text"},
			&sqlschema.BaseColumn{Name: "obsolete", SQLType: "text"},
		},
		PrimaryKey: &sqlschema.PrimaryKey{Columns: sqlschema.NewColumns("key")},
	}
	target := &sqlschema.BaseTable{
		Name: "sys_config_param",
		Columns: []sqlschema.Column{
			&sqlschema.BaseColumn{Name: "id", SQLType: "bigint", IsAutoIncrement: true},
			&sqlschema.BaseColumn{Name: "name", SQLType: "text"},
			&sqlschema.BaseColumn{Name: "value", SQLType: "jsonb"},
			&sqlschema.BaseColumn{Name: "updated_at", SQLType: "timestamptz"},
		},
		PrimaryKey: &sqlschema.PrimaryKey{Columns: sqlschema.NewColumns("id")},
	}

	ops, err := planTableSchemaOperations(db, metadata, current, target)
	if err != nil {
		t.Fatalf("planTableSchemaOperations() error = %v", err)
	}
	if len(ops) != 6 {
		t.Fatalf("expected 6 operations, got %#v", ops)
	}

	if op, ok := ops[0].(*migrate.RenameColumnOp); !ok || op.OldName != "key" || op.NewName != "name" {
		t.Fatalf("op[0] = %#v, want rename key -> name", ops[0])
	}
	if op, ok := ops[1].(*migrate.AddColumnOp); !ok || op.ColumnName != "id" {
		t.Fatalf("op[1] = %#v, want add id", ops[1])
	}
	if op, ok := ops[2].(*migrate.AddColumnOp); !ok || op.ColumnName != "updated_at" {
		t.Fatalf("op[2] = %#v, want add updated_at", ops[2])
	}
	if op, ok := ops[3].(*migrate.ChangeColumnTypeOp); !ok || op.Column != "value" {
		t.Fatalf("op[3] = %#v, want change type on value", ops[3])
	}
	if op, ok := ops[4].(*migrate.ChangePrimaryKeyOp); !ok || op.New.Columns != sqlschema.NewColumns("id") {
		t.Fatalf("op[4] = %#v, want primary key change to id", ops[4])
	}
	if op, ok := ops[5].(*migrate.DropColumnOp); !ok || op.ColumnName != "obsolete" {
		t.Fatalf("op[5] = %#v, want drop obsolete", ops[5])
	}
}

func TestInspectModelTableUsesRegisteredStorageName(t *testing.T) {
	db := bun.NewDB(nil, pgdialect.New())

	table, err := inspectModelTable(db, &ModelMetadata{
		Name:          "sys.model",
		StorageName:   "sys_model",
		goReflectType: reflect.TypeOf(systemModelRow{}),
	})
	if err != nil {
		t.Fatalf("inspectModelTable() error = %v", err)
	}
	if table == nil {
		t.Fatalf("inspectModelTable() returned nil table")
	}
	if got := table.GetName(); got != "sys_model" {
		t.Fatalf("inspectModelTable() table name = %q, want sys_model", got)
	}
}

func modelRecordTypeMust(model any) reflect.Type {
	modelType, err := modelRecordType(model)
	if err != nil {
		panic(err)
	}
	return modelType
}
