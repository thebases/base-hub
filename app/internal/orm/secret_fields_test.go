package orm

// File overview:
// - Responsibility: T19 regression: `meta:"secret"` fields are stripped from client results and
//   cannot be used in a domain, direct filter, order or group-by by non-sudo RPC callers.

import (
	"context"
	"errors"
	"testing"
)

const secretFieldsTestModel = "test.secret_fields"

type secretFieldsTestDocument struct {
	BaseModel
	Name     string `bun:"name" json:"name"`
	Password string `bun:"password" json:"password" meta:"secret,label:Password"`
	ParentID *int64 `bun:"parent_id" json:"parent_id" meta:"kind:many2one,model:test.secret_fields"`
}

func registerSecretFieldsMeta(t *testing.T) *ModelMetadata {
	t.Helper()
	meta, err := RegisterModelType[secretFieldsTestDocument](ModelSpec{
		Name:       secretFieldsTestModel,
		Label:      "Secret Fields",
		Definition: ModelDefinition(secretFieldsTestModel, (*secretFieldsTestDocument)(nil)),
	}.WithStorageName("test_secret_fields"))
	if err != nil {
		t.Fatalf("register model: %v", err)
	}
	return meta
}

func secretFieldsRPCContext(sudo bool) context.Context {
	return ContextWithEnvironment(context.Background(), EnvironmentForIdentity(&RBACIdentity{
		UserID: 7, Login: "u", RoleID: 2, AuthMode: "bearer", IsSuperadmin: sudo,
	}))
}

func TestSecretMetaParsed(t *testing.T) {
	meta := registerSecretFieldsMeta(t)
	field, err := meta.Field("password")
	if err != nil {
		t.Fatalf("field: %v", err)
	}
	if !field.Secret {
		t.Fatal("password should carry Secret from meta:\"secret\"")
	}
	if name, _ := meta.Field("name"); name.Secret {
		t.Fatal("name must not be secret")
	}
}

func TestRedactSecretFieldsForClient(t *testing.T) {
	meta := registerSecretFieldsMeta(t)

	rows := []RecordMap{{"id": 1, "name": "a", "password": "$2a$hash"}}
	RedactSecretFieldsForClient(meta, "search_read", rows)
	if _, ok := rows[0]["password"]; ok || rows[0]["name"] != "a" {
		t.Fatalf("search_read row not redacted correctly: %#v", rows[0])
	}

	groups := []RecordMap{{"__count": 2, "password": "x", "password:sum": 1.0}}
	RedactSecretFieldsForClient(meta, "read_group", groups)
	if len(groups[0]) != 1 {
		t.Fatalf("read_group secret keys survived: %#v", groups[0])
	}

	onchange := map[string]any{"value": map[string]any{"password": "x", "name": "b"}}
	RedactSecretFieldsForClient(meta, "onchange", onchange)
	if _, ok := onchange["value"].(map[string]any)["password"]; ok {
		t.Fatal("onchange nested value not redacted")
	}

	// Metadata methods are keyed by field name: leave them alone.
	fields := map[string]any{"password": map[string]any{"type": "string"}}
	RedactSecretFieldsForClient(meta, "fields_get", fields)
	if _, ok := fields["password"]; !ok {
		t.Fatal("fields_get must keep the secret field's metadata")
	}
}

func TestSecretFieldsRefusedInSearch(t *testing.T) {
	meta := registerSecretFieldsMeta(t)
	ctx := secretFieldsRPCContext(false)

	cases := map[string]RecordMap{
		"domain":         {"__domain": Where("password", "like", "$2a%")},
		"nested domain":  {"__domain": Or(Where("name", "=", "x"), Not(Where("password", "=", "y")))},
		"many2one path":  {"__domain": Where("parent_id.password", "=", "y")},
		"order":          {"__order": "name asc, password desc"},
		"order shortcut": {"__order": "-password"},
		"direct filter":  {"password": "y"},
	}
	for name, filter := range cases {
		err := ensureNoSecretFieldsInSearch(ctx, meta, filter)
		var typed *Error
		if !errors.As(err, &typed) || typed.Code != ErrInvalidParams {
			t.Errorf("%s: expected invalid-params error, got %v", name, err)
		}
	}

	if err := ensureNoSecretFieldsInSearch(ctx, meta, RecordMap{
		"__domain": Where("parent_id.name", "=", "x"), "__order": "name", "name": "x",
	}); err != nil {
		t.Fatalf("non-secret search refused: %v", err)
	}
}

func TestRuntimeSearchFilterRefusesSecretDomain(t *testing.T) {
	meta := registerSecretFieldsMeta(t)
	_, err := runtimeSearchFilter(secretFieldsRPCContext(false), meta, nil, map[string]any{
		"domain": []any{[]any{"password", "like", "$2a%"}},
	})
	if err == nil {
		t.Fatal("search_read domain on a secret field must be refused")
	}
}

func TestSecretFieldsRefusedInGroupBy(t *testing.T) {
	meta := registerSecretFieldsMeta(t)
	ctx := secretFieldsRPCContext(false)
	if err := ensureNoSecretFieldNames(ctx, meta, []string{"name", "password:count"}); err == nil {
		t.Fatal("group-by/aggregate on a secret field must be refused")
	}
	if err := ensureNoSecretFieldNames(ctx, meta, []string{"name"}); err != nil {
		t.Fatalf("plain group-by refused: %v", err)
	}
}

func TestSecretFieldSearchAllowedForSudoAndInternal(t *testing.T) {
	meta := registerSecretFieldsMeta(t)
	filter := RecordMap{"__domain": Where("password", "=", "y"), "__order": "password"}
	for name, ctx := range map[string]context.Context{
		"superadmin": secretFieldsRPCContext(true),
		"internal":   context.Background(),
	} {
		if err := ensureNoSecretFieldsInSearch(ctx, meta, filter); err != nil {
			t.Errorf("%s: secret search should be allowed, got %v", name, err)
		}
	}
}
