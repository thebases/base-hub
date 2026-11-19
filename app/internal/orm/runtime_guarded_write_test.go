package orm

// File overview:
// - Responsibility: T87 regression: CreateRecord/WriteRecords (and the default create/write that now call
//   them) run Prepare, pin and check company_id, apply record rules on write ids and reject computed fields.
// - Key symbols: setupGuardedWriteFixture, TestGuardedWriteHelpers.

import (
	"context"
	"strings"
	"testing"
)

const guardedWriteModel = "test.guarded.write.scoped"

type guardedWriteScoped struct {
	BaseModel
	Name      string `bun:"name" json:"name"`
	CompanyID *int64 `bun:"company_id" json:"company_id" meta:"kind:many2one,model:tbl.company"`
	Summary   string `bun:"summary" json:"summary" meta:"compute:_compute_summary,stored:false"`
}

// guardedWritePrepared records each Prepare call so the test can see it ran on both paths.
var guardedWritePrepared []WriteOp

func guardedWritePrepare(_ context.Context, op WriteOp) error {
	guardedWritePrepared = append(guardedWritePrepared, op)
	if op.Values["name"] == "forbidden" {
		return InvalidParamsf("name forbidden")
	}
	if name, ok := op.Values["name"].(string); ok {
		op.Values["name"] = name + "!"
	}
	return nil
}

func setupGuardedWriteFixture(t *testing.T) *ModelMetadata {
	t.Helper()
	bunDB, err := NewBunDB(runtimeGraphEnvPostgresConfig())
	if err != nil {
		t.Skipf("open postgres guarded write fixture: %v", err)
	}
	previous := GetBunDB()
	SetBunDB(bunDB)
	t.Cleanup(func() {
		_ = bunDB.Close()
		if previous == nil {
			SetBunDB(nil)
			return
		}
		SetBunDB(&BunDB{DB: previous})
	})

	RegisterAccessHandler(guardedWriteModel, func(context.Context, *Environment, *ModelMetadata, string, []string, RecordMap) error {
		return nil
	})
	handler := NewHandlerWithModel[guardedWriteScoped](ModelSpec{
		Name:       guardedWriteModel,
		Label:      "Guarded Write Scoped",
		Definition: ModelDefinition(guardedWriteModel, (*guardedWriteScoped)(nil)),
		Prepare:    guardedWritePrepare,
	}.WithStorageName("guarded_write_scoped")).InitDefault()
	t.Cleanup(handler.Shutdown)

	ctx := context.Background()
	db := bunDB.DB
	if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS guarded_write_scoped"); err != nil {
		t.Fatalf("drop fixture table: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE guarded_write_scoped (
		id bigserial PRIMARY KEY, active boolean DEFAULT true, created_at timestamptz, updated_at timestamptz,
		created_by bigint, updated_by bigint, name text, company_id bigint)`); err != nil {
		t.Fatalf("create fixture table: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), "DROP TABLE IF EXISTS guarded_write_scoped")
	})
	// Row 1 belongs to company 1, row 2 to company 2.
	if _, err := db.ExecContext(ctx, "INSERT INTO guarded_write_scoped (name, company_id) VALUES ('c1', 1), ('c2', 2)"); err != nil {
		t.Fatalf("seed fixture rows: %v", err)
	}
	meta, ok := LookupModel(guardedWriteModel)
	if !ok {
		t.Fatal("fixture model not registered")
	}
	return meta
}

func guardedWriteRow(t *testing.T, id int64) (string, int64) {
	t.Helper()
	var name string
	var companyID int64
	if err := GetBunDB().QueryRowContext(context.Background(), "SELECT name, company_id FROM guarded_write_scoped WHERE id = ?", id).Scan(&name, &companyID); err != nil {
		t.Fatal(err)
	}
	return name, companyID
}

func TestGuardedWriteHelpers(t *testing.T) {
	model := setupGuardedWriteFixture(t)
	company2 := overrideGuardCaller(2)
	guardedWritePrepared = nil

	// Default create through RPC-style dispatch runs Prepare and pins company 2.
	created, err := ExecuteRuntimeMethod(company2, guardedWriteModel, "create", nil, []any{map[string]any{"name": "rpc"}}, nil)
	if err != nil {
		t.Fatalf("dispatch create: %v", err)
	}
	createdID := toInt64(created.(RecordMap)["id"])
	if name, companyID := guardedWriteRow(t, createdID); name != "rpc!" || companyID != 2 {
		t.Fatalf("dispatch create stored (%q, %d), want (rpc!, 2)", name, companyID)
	}

	// Default write through dispatch runs Prepare with the ids.
	if _, err := ExecuteRuntimeMethod(company2, guardedWriteModel, "write", []string{"2"}, []any{map[string]any{"name": "mine"}}, nil); err != nil {
		t.Fatalf("dispatch write: %v", err)
	}
	if name, _ := guardedWriteRow(t, 2); name != "mine!" {
		t.Fatalf("dispatch write stored %q, want mine!", name)
	}
	if len(guardedWritePrepared) != 2 || !guardedWritePrepared[0].Create || guardedWritePrepared[1].Create ||
		strings.Join(guardedWritePrepared[1].IDs, ",") != "2" {
		t.Fatalf("Prepare calls = %+v, want one create then one write on [2]", guardedWritePrepared)
	}

	// CreateRecord called directly (override shape) pins company_id from the environment.
	direct, err := CreateRecord(company2, model, RecordMap{"name": "direct"})
	if err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}
	if _, companyID := guardedWriteRow(t, toInt64(direct["id"])); companyID != 2 {
		t.Fatalf("CreateRecord company_id = %d, want 2", companyID)
	}

	// WriteRecords denies a company-2 caller on a company-1 row and leaves it untouched.
	_, err = WriteRecords(company2, model, []string{"1"}, RecordMap{"name": "hijack"})
	requireOverrideDenied(t, err, "WriteRecords on a company-1 row")
	if name, _ := guardedWriteRow(t, 1); name != "c1" {
		t.Fatalf("company-1 row changed to %q", name)
	}

	// Computed fields are refused before Prepare or the repository run.
	before := len(guardedWritePrepared)
	if _, err := CreateRecord(company2, model, RecordMap{"name": "x", "summary": "forged"}); err == nil || !strings.Contains(err.Error(), "computed") {
		t.Fatalf("CreateRecord with computed field: err = %v, want computed rejection", err)
	}
	if len(guardedWritePrepared) != before {
		t.Fatal("Prepare ran for a payload the computed-field guard rejects")
	}
}

// TestCreateRecordKeepsExplicitCompany: the company pin fills only an absent company_id. An
// explicit company (the user's choice, or one Prepare inherited from a parent record) is kept and
// left to the allowed-company check, never replaced with the caller's active company.
func TestCreateRecordKeepsExplicitCompany(t *testing.T) {
	model := setupGuardedWriteFixture(t)
	env := NewEnvironment(GetBunDB(), 10, map[string]any{
		RBACContextAuthModeKey:      "bearer",
		ContextKeyAllowedCompanyIDs: []int64{2, 4},
		ContextKeyCurrentCompanyID:  int64(2),
	}, false)
	ctx := ContextWithEnvironment(context.Background(), env)

	explicit, err := CreateRecord(ctx, model, RecordMap{"name": "explicit", "company_id": int64(4)})
	if err != nil {
		t.Fatalf("CreateRecord explicit company 4: %v", err)
	}
	if _, companyID := guardedWriteRow(t, toInt64(explicit["id"])); companyID != 4 {
		t.Fatalf("explicit company_id stored %d, want 4", companyID)
	}

	absent, err := CreateRecord(ctx, model, RecordMap{"name": "absent"})
	if err != nil {
		t.Fatalf("CreateRecord without company: %v", err)
	}
	if _, companyID := guardedWriteRow(t, toInt64(absent["id"])); companyID != 2 {
		t.Fatalf("absent company_id stored %d, want 2 (active company)", companyID)
	}

	_, err = CreateRecord(ctx, model, RecordMap{"name": "foreign", "company_id": int64(5)})
	requireOverrideDenied(t, err, "CreateRecord into a company outside the allowed set")
}

// TestFindOrCreateAndCopyRunPrepare: find_or_create (both branches) and copy create/write through
// the repository, so they must run ModelSpec.Prepare like CreateRecord/WriteRecords or a model's
// sanitising and validation could be bypassed.
func TestFindOrCreateAndCopyRunPrepare(t *testing.T) {
	setupGuardedWriteFixture(t)
	ctx := overrideGuardCaller(2)
	findOrCreate := func(domainName string, values map[string]any) (RecordMap, error) {
		result, err := ExecuteRuntimeMethod(ctx, guardedWriteModel, "find_or_create", nil, nil, map[string]any{
			"domain": []any{[]any{"name", "=", domainName}},
			"values": values,
		})
		if err != nil {
			return nil, err
		}
		return result.(RecordMap), nil
	}

	// Create branch: the domain seed passes through Prepare.
	created, err := findOrCreate("fresh", nil)
	if err != nil {
		t.Fatalf("find_or_create create branch: %v", err)
	}
	if name, companyID := guardedWriteRow(t, toInt64(created["id"])); name != "fresh!" || companyID != 2 {
		t.Fatalf("create branch stored (%q, %d), want (fresh!, 2)", name, companyID)
	}
	if _, err := findOrCreate("forbidden", nil); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("create branch with a Prepare-rejected payload: err = %v, want rejection", err)
	}

	// Write branch: row 2 ("c2", company 2) is found and the values go through Prepare.
	if _, err := findOrCreate("c2", map[string]any{"name": "renamed"}); err != nil {
		t.Fatalf("find_or_create write branch: %v", err)
	}
	if name, _ := guardedWriteRow(t, 2); name != "renamed!" {
		t.Fatalf("write branch stored %q, want renamed!", name)
	}
	if _, err := findOrCreate("renamed!", map[string]any{"name": "forbidden"}); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("write branch with a Prepare-rejected payload: err = %v, want rejection", err)
	}

	// copy: the duplicated row passes through Prepare (and its computed value is dropped).
	copied, err := ExecuteRuntimeMethod(ctx, guardedWriteModel, "copy", []string{"2"}, nil, nil)
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if name, companyID := guardedWriteRow(t, toInt64(copied.(RecordMap)["id"])); name != "renamed!!" || companyID != 2 {
		t.Fatalf("copy stored (%q, %d), want (renamed!!, 2)", name, companyID)
	}
	if _, err := GetBunDB().ExecContext(context.Background(), "UPDATE guarded_write_scoped SET name = 'forbidden' WHERE id = 2"); err != nil {
		t.Fatal(err)
	}
	if _, err := ExecuteRuntimeMethod(ctx, guardedWriteModel, "copy", []string{"2"}, nil, nil); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("copy of a row Prepare rejects: err = %v, want rejection", err)
	}
}
