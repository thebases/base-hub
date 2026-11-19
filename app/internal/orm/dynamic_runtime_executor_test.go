package orm

// These integration tests prove the fix for the reported bug: publishing a
// custom model (base.model.publish / PublishModel) must leave it with a
// working runtime executor, not just registry metadata and a physical table
// -- otherwise every request against it 400s with "orm: no runtime executor
// registered for model ...". They reuse the same live-Postgres fixture
// pattern as runtime_graph_integration_test.go.

import (
	"context"
	"testing"

	"github.com/uptrace/bun"
)

const dynamicRuntimeExecutorTestModel = "test.dyn.account"

func setupDynamicRuntimeExecutorFixture(t *testing.T) (context.Context, *bun.DB, func()) {
	t.Helper()

	bunDB, err := NewBunDB(runtimeGraphEnvPostgresConfig())
	if err != nil {
		t.Fatalf("open postgres dynamic runtime executor fixture: %v", err)
	}
	db := bunDB.DB

	previous := GetBunDB()
	SetBunDB(bunDB)
	t.Cleanup(func() {
		if previous == nil {
			SetBunDB(nil)
			return
		}
		SetBunDB(&BunDB{DB: previous})
	})

	ctx := context.Background()

	if _, err := db.NewCreateTable().Model((*systemModelRow)(nil)).IfNotExists().Exec(ctx); err != nil {
		t.Fatalf("create sys_model fixture table: %v", err)
	}
	if _, err := db.NewCreateTable().Model((*systemModelFieldRow)(nil)).IfNotExists().Exec(ctx); err != nil {
		t.Fatalf("create sys_model_field fixture table: %v", err)
	}
	// Other test files (e.g. model_registry_migrate_rename_test.go) share this
	// same live test database and may have already created sys_model /
	// sys_model_field with a bare minimal shape via CREATE TABLE IF NOT
	// EXISTS, in which case the CreateTable calls above silently no-op and
	// leave columns systemModelRow/systemModelFieldRow need missing. Add them
	// explicitly rather than depending on table-creation order across files.
	alterStatements := []string{
		`ALTER TABLE sys_model ADD COLUMN IF NOT EXISTS bid TEXT`,
		`ALTER TABLE sys_model ADD COLUMN IF NOT EXISTS label TEXT`,
		`ALTER TABLE sys_model ADD COLUMN IF NOT EXISTS description TEXT`,
		`ALTER TABLE sys_model ADD COLUMN IF NOT EXISTS storage_backend TEXT`,
		`ALTER TABLE sys_model ADD COLUMN IF NOT EXISTS storage_name TEXT`,
		`ALTER TABLE sys_model ADD COLUMN IF NOT EXISTS helper_data TEXT`,
		`ALTER TABLE sys_model ADD COLUMN IF NOT EXISTS json_dto JSONB`,
		`ALTER TABLE sys_model ADD COLUMN IF NOT EXISTS registered_at TIMESTAMPTZ`,
		`ALTER TABLE sys_model ADD COLUMN IF NOT EXISTS synced_at TIMESTAMPTZ`,
		`ALTER TABLE sys_model ADD COLUMN IF NOT EXISTS status TEXT`,
		`ALTER TABLE sys_model ADD COLUMN IF NOT EXISTS has_ui BOOLEAN NOT NULL DEFAULT false`,
		`ALTER TABLE sys_model ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ`,
		`ALTER TABLE sys_model ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ`,
		`ALTER TABLE sys_model_field ADD COLUMN IF NOT EXISTS bid TEXT`,
		`ALTER TABLE sys_model_field ADD COLUMN IF NOT EXISTS go_name TEXT`,
		`ALTER TABLE sys_model_field ADD COLUMN IF NOT EXISTS label TEXT`,
		`ALTER TABLE sys_model_field ADD COLUMN IF NOT EXISTS data_type TEXT`,
		`ALTER TABLE sys_model_field ADD COLUMN IF NOT EXISTS relation_model TEXT`,
		`ALTER TABLE sys_model_field ADD COLUMN IF NOT EXISTS bson_name TEXT`,
		`ALTER TABLE sys_model_field ADD COLUMN IF NOT EXISTS json_name TEXT`,
		`ALTER TABLE sys_model_field ADD COLUMN IF NOT EXISTS bun_column TEXT`,
		`ALTER TABLE sys_model_field ADD COLUMN IF NOT EXISTS is_primary_key BOOLEAN NOT NULL DEFAULT false`,
		`ALTER TABLE sys_model_field ADD COLUMN IF NOT EXISTS is_required BOOLEAN NOT NULL DEFAULT false`,
		`ALTER TABLE sys_model_field ADD COLUMN IF NOT EXISTS field_index INT NOT NULL DEFAULT 0`,
		`ALTER TABLE sys_model_field ADD COLUMN IF NOT EXISTS status TEXT`,
		`ALTER TABLE sys_model_field ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ`,
		`ALTER TABLE sys_model_field ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ`,
	}
	for _, stmt := range alterStatements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("prepare dynamic runtime executor fixture (%s): %v", stmt, err)
		}
	}
	// AutoMigrateRegisteredModels (called from PublishModel) always runs
	// ensureLegacyModelNameRenames first, which UPDATEs a fixed list of
	// tables/columns (model_registry_migrate.go's legacyModelNameColumns) as
	// long as the table exists at all -- it does not check the column
	// exists. This shared test database has several of those tables (from
	// other test files/modules) without the expected column, so publishing
	// anything here fails on an unrelated legacy-rename step unless every
	// column is present. IF NOT EXISTS on both statements make this a no-op
	// against a database that already has the real, full-width tables.
	for _, target := range legacyModelNameColumns {
		if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+quoteIdent(target.table)+` (id BIGSERIAL PRIMARY KEY)`); err != nil {
			t.Fatalf("prepare %s fixture table: %v", target.table, err)
		}
		if _, err := db.ExecContext(ctx, `ALTER TABLE `+quoteIdent(target.table)+` ADD COLUMN IF NOT EXISTS `+quoteIdent(target.column)+` TEXT`); err != nil {
			t.Fatalf("prepare %s.%s fixture column: %v", target.table, target.column, err)
		}
	}

	// Clean any leftovers from a previous failed run before seeding.
	if _, err := db.NewDelete().Model((*systemModelFieldRow)(nil)).Where("model_name = ?", dynamicRuntimeExecutorTestModel).Exec(ctx); err != nil {
		t.Fatalf("clean sys_model_field fixture rows: %v", err)
	}
	if _, err := db.NewDelete().Model((*systemModelRow)(nil)).Where("name = ?", dynamicRuntimeExecutorTestModel).Exec(ctx); err != nil {
		t.Fatalf("clean sys_model fixture rows: %v", err)
	}
	if _, err := db.NewDropTable().ModelTableExpr("tbl_dyn_account").IfExists().Cascade().Exec(ctx); err != nil {
		t.Fatalf("drop stale dynamic model table: %v", err)
	}

	catalogRow := &systemModelRow{
		BID:   "bid-dyn-account",
		Name:  dynamicRuntimeExecutorTestModel,
		Label: "Test Dynamic Account",
	}
	if _, err := db.NewInsert().Model(catalogRow).Exec(ctx); err != nil {
		t.Fatalf("seed sys_model fixture row: %v", err)
	}
	fieldRows := []systemModelFieldRow{
		{BID: "bid-dyn-field-balance", ModelName: dynamicRuntimeExecutorTestModel, FieldName: "balance", Label: "Balance", DataType: "float", FieldIndex: 1},
		{BID: "bid-dyn-field-owner", ModelName: dynamicRuntimeExecutorTestModel, FieldName: "owner_name", Label: "Owner Name", DataType: "text", FieldIndex: 2},
	}
	if _, err := db.NewInsert().Model(&fieldRows).Exec(ctx); err != nil {
		t.Fatalf("seed sys_model_field fixture rows: %v", err)
	}

	cleanup := func() {
		UnregisterRuntimeExecutor(dynamicRuntimeExecutorTestModel)
		DefaultModelRegistry().Unregister(dynamicRuntimeExecutorTestModel)
		_, _ = db.NewDropTable().ModelTableExpr("tbl_dyn_account").IfExists().Cascade().Exec(context.Background())
		_, _ = db.NewDelete().Model((*systemModelFieldRow)(nil)).Where("model_name = ?", dynamicRuntimeExecutorTestModel).Exec(context.Background())
		_, _ = db.NewDelete().Model((*systemModelRow)(nil)).Where("name = ?", dynamicRuntimeExecutorTestModel).Exec(context.Background())
		_ = bunDB.Close()
	}
	return ctx, db, cleanup
}

func TestPublishModelRegistersWorkingRuntimeExecutor(t *testing.T) {
	ctx, _, cleanup := setupDynamicRuntimeExecutorFixture(t)
	defer cleanup()

	if _, ok := lookupRuntimeExecutor(dynamicRuntimeExecutorTestModel); ok {
		t.Fatalf("expected no runtime executor registered before publish")
	}

	meta, err := PublishModel(ctx, dynamicRuntimeExecutorTestModel)
	if err != nil {
		t.Fatalf("PublishModel: %v", err)
	}
	if meta == nil {
		t.Fatal("PublishModel returned nil metadata")
	}

	// This is the exact assertion that reproduces and verifies the fix for
	// the reported bug: publishing a model must make it immediately usable
	// through the runtime dispatch path the admin UI/API calls through.
	executor, ok := lookupRuntimeExecutor(dynamicRuntimeExecutorTestModel)
	if !ok {
		t.Fatal("expected PublishModel to register a runtime executor for the published model")
	}

	created, err := executor.RuntimeCreate(ctx, RecordMap{"balance": 42.5, "owner_name": "Alice"})
	if err != nil {
		t.Fatalf("RuntimeCreate: %v", err)
	}
	id, ok := runtimeStringID(created["id"])
	if !ok || id == "" {
		t.Fatalf("expected a created id, got %#v", created)
	}
	if created["owner_name"] != "Alice" {
		t.Errorf("owner_name = %#v, want Alice", created["owner_name"])
	}

	rows, err := executor.RuntimeSearchRead(ctx, nil, []string{"id", "balance", "owner_name"})
	if err != nil {
		t.Fatalf("RuntimeSearchRead: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected exactly one row after create, got %#v", rows)
	}

	if _, err := executor.RuntimeWrite(ctx, []string{id}, RecordMap{"owner_name": "Bob"}); err != nil {
		t.Fatalf("RuntimeWrite: %v", err)
	}
	updated, err := executor.RuntimeRead(ctx, []string{id}, []string{"owner_name"})
	if err != nil {
		t.Fatalf("RuntimeRead after write: %v", err)
	}
	if len(updated) != 1 || updated[0]["owner_name"] != "Bob" {
		t.Fatalf("expected owner_name to be updated to Bob, got %#v", updated)
	}

	if _, err := executor.RuntimeUnlink(ctx, []string{id}); err != nil {
		t.Fatalf("RuntimeUnlink: %v", err)
	}
	remaining, err := executor.RuntimeSearchRead(ctx, nil, []string{"id"})
	if err != nil {
		t.Fatalf("RuntimeSearchRead after unlink: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected no rows after unlink, got %#v", remaining)
	}
}

func TestUnpublishModelRemovesRuntimeExecutor(t *testing.T) {
	ctx, _, cleanup := setupDynamicRuntimeExecutorFixture(t)
	defer cleanup()

	if _, err := PublishModel(ctx, dynamicRuntimeExecutorTestModel); err != nil {
		t.Fatalf("PublishModel: %v", err)
	}
	if _, ok := lookupRuntimeExecutor(dynamicRuntimeExecutorTestModel); !ok {
		t.Fatal("expected a runtime executor right after publish")
	}

	if err := UnpublishModel(ctx, dynamicRuntimeExecutorTestModel); err != nil {
		t.Fatalf("UnpublishModel: %v", err)
	}

	if _, ok := lookupRuntimeExecutor(dynamicRuntimeExecutorTestModel); ok {
		t.Fatal("expected UnpublishModel to also remove the runtime executor, not just the registry entry")
	}
}
