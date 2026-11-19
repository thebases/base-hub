package orm

// File overview:
// - Responsibility: T15 regression: a model that replaces the kernel create/write/unlink with its own
//   RuntimeMethod (and inserts through RuntimeRepository* directly) still gets the record-rule and
//   company guards of the default methods when called through ExecuteRuntimeMethod.
// - Key symbols: setupOverrideGuardFixture, TestOverriddenMutationsKeepCompanyGuards.

import (
	"context"
	"strings"
	"testing"
)

const overrideGuardModel = "test.override.guard.scoped"

type overrideGuardScoped struct {
	BaseModel
	Name      string `bun:"name" json:"name"`
	CompanyID *int64 `bun:"company_id" json:"company_id" meta:"kind:many2one,model:tbl.company"`
}

// overrideGuardCalls counts how often the overrides actually reached the repository, so the test
// can tell "guard refused" apart from "override ran and the repository happened to fail".
var overrideGuardCalls int

// The overrides mimic tbl.partner: they read the payload themselves and call RuntimeRepository*
// directly, never the kernel default, which is exactly the shape that skipped the guards.
func overrideGuardCreate(ctx context.Context, model *ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	values, err := runtimeRecordMapArg(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	overrideGuardCalls++
	return RuntimeRepositoryCreate(ctx, model, values)
}

func overrideGuardWrite(ctx context.Context, model *ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	values, err := runtimeRecordMapArg(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	overrideGuardCalls++
	return RuntimeRepositoryWrite(ctx, model, ids, values)
}

func overrideGuardUnlink(ctx context.Context, model *ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	overrideGuardCalls++
	return RuntimeRepositoryUnlink(ctx, model, ids)
}

func setupOverrideGuardFixture(t *testing.T) {
	t.Helper()
	bunDB, err := NewBunDB(runtimeGraphEnvPostgresConfig())
	if err != nil {
		t.Skipf("open postgres override guard fixture: %v", err)
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

	// The test model only checks the guards this change adds; ACL rows are out of scope.
	RegisterAccessHandler(overrideGuardModel, func(context.Context, *Environment, *ModelMetadata, string, []string, RecordMap) error {
		return nil
	})
	handler := NewHandlerWithModel[overrideGuardScoped](ModelSpec{
		Name:       overrideGuardModel,
		Label:      "Override Guard Scoped",
		Definition: ModelDefinition(overrideGuardModel, (*overrideGuardScoped)(nil)),
		StandardHooks: StandardRuntimeHooks{
			Create: overrideGuardCreate,
			Write:  overrideGuardWrite,
			Unlink: overrideGuardUnlink,
		},
	}.WithStorageName("override_guard_scoped")).InitDefault()
	t.Cleanup(handler.Shutdown)

	ctx := context.Background()
	db := bunDB.DB
	if _, err := db.NewDropTable().Model((*overrideGuardScoped)(nil)).ModelTableExpr("override_guard_scoped").IfExists().Cascade().Exec(ctx); err != nil {
		t.Fatalf("drop fixture table: %v", err)
	}
	if _, err := db.NewCreateTable().Model((*overrideGuardScoped)(nil)).ModelTableExpr("override_guard_scoped").Exec(ctx); err != nil {
		t.Fatalf("create fixture table: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.NewDropTable().Model((*overrideGuardScoped)(nil)).ModelTableExpr("override_guard_scoped").IfExists().Exec(context.Background())
	})
	// Row 1 belongs to company 1, row 2 to company 2.
	if _, err := db.ExecContext(ctx, "INSERT INTO override_guard_scoped (name, company_id) VALUES ('c1', 1), ('c2', 2)"); err != nil {
		t.Fatalf("seed fixture rows: %v", err)
	}
}

// overrideGuardCaller binds an RPC-style (non-sudo, authenticated) caller working in companyID only.
func overrideGuardCaller(companyID int64) context.Context {
	env := NewEnvironment(GetBunDB(), 10, map[string]any{
		RBACContextAuthModeKey:      "bearer",
		ContextKeyAllowedCompanyIDs: []int64{companyID},
		ContextKeyCurrentCompanyID:  companyID,
	}, false)
	return ContextWithEnvironment(context.Background(), env)
}

func requireOverrideDenied(t *testing.T, err error, what string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("%s: err = %v, want permission denied", what, err)
	}
}

func TestOverriddenMutationsKeepCompanyGuards(t *testing.T) {
	setupOverrideGuardFixture(t)
	company2 := overrideGuardCaller(2)

	overrideGuardCalls = 0
	_, err := ExecuteRuntimeMethod(company2, overrideGuardModel, "write", []string{"1"}, []any{map[string]any{"name": "hijack"}}, nil)
	requireOverrideDenied(t, err, "company-2 write on a company-1 row")
	_, err = ExecuteRuntimeMethod(company2, overrideGuardModel, "write", []string{"2"}, []any{map[string]any{"company_id": int64(1)}}, nil)
	requireOverrideDenied(t, err, "company-2 moving its row to company 1")
	_, err = ExecuteRuntimeMethod(company2, overrideGuardModel, "unlink", []string{"1"}, nil, nil)
	requireOverrideDenied(t, err, "company-2 unlink of a company-1 row")
	_, err = ExecuteRuntimeMethod(company2, overrideGuardModel, "create", nil, []any{map[string]any{"name": "x", "company_id": int64(1)}}, nil)
	requireOverrideDenied(t, err, "company-2 create in company 1")
	if overrideGuardCalls != 0 {
		t.Fatalf("override reached the repository %d times for denied calls", overrideGuardCalls)
	}

	// The caller's own company keeps working.
	if _, err := ExecuteRuntimeMethod(company2, overrideGuardModel, "write", []string{"2"}, []any{map[string]any{"name": "mine"}}, nil); err != nil {
		t.Fatalf("company-2 write on its own row: %v", err)
	}
	if _, err := ExecuteRuntimeMethod(company2, overrideGuardModel, "create", nil, []any{map[string]any{"name": "new", "company_id": int64(2)}}, nil); err != nil {
		t.Fatalf("company-2 create in company 2: %v", err)
	}

	var name string
	if err := GetBunDB().QueryRowContext(context.Background(), "SELECT name FROM override_guard_scoped WHERE id = 1").Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "c1" {
		t.Fatalf("company-1 row changed to %q", name)
	}
}

func TestKernelMutationDefaultDetection(t *testing.T) {
	if !isKernelMutationDefault("write", runtimeWrite) || !isKernelMutationDefault("create", runtimeCreate) || !isKernelMutationDefault("unlink", runtimeUnlink) {
		t.Fatal("kernel defaults must be recognised so they are not guarded twice")
	}
	if isKernelMutationDefault("write", overrideGuardWrite) || isKernelMutationDefault("create", runtimeWrite) {
		t.Fatal("an override must not be mistaken for the kernel default")
	}
}
