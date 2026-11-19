package models

// T21: unique constraints are column lists (built with bun.Ident), and raw-SQL constraint types
// (check, unique_index) can only be authored by sudo callers.

import (
	"context"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

type constraintSQLTestDocument struct {
	orm.BaseModel
	Name string `bun:"name" json:"name"`
	Code string `bun:"code" json:"code"`
}

const constraintSQLTestModel = "test.constraint_sql"

func registerConstraintSQLTestModel(t *testing.T) {
	t.Helper()
	if _, err := orm.RegisterModelType[constraintSQLTestDocument](orm.ModelSpec{
		Name:       constraintSQLTestModel,
		Label:      "Constraint SQL",
		Definition: orm.ModelDefinition(constraintSQLTestModel, (*constraintSQLTestDocument)(nil)),
	}.WithStorageName("test_constraint_sql")); err != nil {
		t.Fatalf("register: %v", err)
	}
}

func TestParseUniqueConstraintColumns(t *testing.T) {
	for _, ok := range []string{"name", "name, code", "(name,code)", " ( name ) "} {
		if _, err := parseUniqueConstraintColumns(ok); err != nil {
			t.Errorf("%q should parse: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"", "lower(name)", "name) WHERE (1=1", `"name"`, "name, name", "name code",
		"name); DROP TABLE x; --", "name, (select 1)",
	} {
		if _, err := parseUniqueConstraintColumns(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
}

func TestValidateUniqueConstraintNeedsStoredFields(t *testing.T) {
	registerConstraintSQLTestModel(t)
	base := orm.RecordMap{
		"model_name": constraintSQLTestModel, "constraint_type": sysModelConstraintTypeUnique,
		"message": map[string]any{"en": "duplicate"},
	}

	base["sql_definition"] = "name, code"
	if err := validateSysModelConstraintValues(base); err != nil {
		t.Fatalf("valid unique refused: %v", err)
	}
	base["sql_definition"] = "name, missing_col"
	if err := validateSysModelConstraintValues(base); err == nil {
		t.Fatal("unknown column accepted")
	}
	base["sql_definition"] = "lower(name)"
	if err := validateSysModelConstraintValues(base); err == nil {
		t.Fatal("expression accepted for a unique constraint")
	}
}

func TestRawConstraintSQLNeedsSudo(t *testing.T) {
	userCtx := orm.ContextWithEnvironment(context.Background(), orm.EnvironmentForIdentity(&orm.RBACIdentity{
		UserID: 5, Login: "admin2", RoleID: 2, AuthMode: "bearer",
	}))
	sudoCtx := orm.ContextWithEnvironment(context.Background(), orm.EnvironmentForIdentity(&orm.RBACIdentity{
		UserID: 1, Login: "admin", RoleID: 1, AuthMode: "bearer", IsSuperadmin: true,
	}))
	for _, constraintType := range []string{sysModelConstraintTypeCheck, sysModelConstraintTypeUniqueIndex} {
		if err := ensureRawConstraintSQLAllowed(userCtx, constraintType); err == nil {
			t.Errorf("%s: non-sudo RPC caller allowed", constraintType)
		}
		for name, ctx := range map[string]context.Context{"sudo": sudoCtx, "internal": context.Background()} {
			if err := ensureRawConstraintSQLAllowed(ctx, constraintType); err != nil {
				t.Errorf("%s: %s caller refused: %v", constraintType, name, err)
			}
		}
	}
	for _, constraintType := range []string{sysModelConstraintTypeUnique, sysModelConstraintTypeBusinessRule} {
		if err := ensureRawConstraintSQLAllowed(userCtx, constraintType); err != nil {
			t.Errorf("%s should stay open to the model ACL: %v", constraintType, err)
		}
	}
}
