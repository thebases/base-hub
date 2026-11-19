package orm

// File overview:
// - Responsibility: T20 regression: a client domain that traverses a many2one or uses any on a
//   relation gets the target's read record rule ANDed in, or FALSE when the target's read ACL is
//   denied; sudo/internal callers and unrestricted targets keep the original domain.

import (
	"context"
	"strings"
	"testing"
)

const (
	domainScopeRootModel   = "test.domain_scope.root"
	domainScopeRuleModel   = "test.domain_scope.ruled"
	domainScopeHiddenModel = "test.domain_scope.hidden"
	domainScopeOpenModel   = "test.domain_scope.open"
)

type domainScopeRoot struct {
	BaseModel
	Name     string `bun:"name" json:"name"`
	RuledID  *int64 `bun:"ruled_id" json:"ruled_id" meta:"kind:many2one,model:test.domain_scope.ruled"`
	HiddenID *int64 `bun:"hidden_id" json:"hidden_id" meta:"kind:many2one,model:test.domain_scope.hidden"`
	OpenID   *int64 `bun:"open_id" json:"open_id" meta:"kind:many2one,model:test.domain_scope.open"`
}

type domainScopeRuled struct {
	BaseModel
	Name   string `bun:"name" json:"name"`
	Email  string `bun:"email" json:"email"`
	OpenID *int64 `bun:"open_id" json:"open_id" meta:"kind:many2one,model:test.domain_scope.open"`
}

type domainScopeHidden struct {
	BaseModel
	Name string `bun:"name" json:"name"`
}

type domainScopeOpen struct {
	BaseModel
	Name    string `bun:"name" json:"name"`
	RuledID *int64 `bun:"ruled_id" json:"ruled_id" meta:"kind:many2one,model:test.domain_scope.ruled"`
}

func registerDomainScopeModel[T any](t *testing.T, name, table string) *ModelMetadata {
	t.Helper()
	meta, err := RegisterModelType[T](ModelSpec{
		Name:       name,
		Label:      name,
		Definition: ModelDefinition(name, (*T)(nil)),
	}.WithStorageName(table))
	if err != nil {
		t.Fatalf("register %s: %v", name, err)
	}
	return meta
}

func setupDomainScopeFixture(t *testing.T) *ModelMetadata {
	t.Helper()
	root := registerDomainScopeModel[domainScopeRoot](t, domainScopeRootModel, "test_domain_scope_root")
	registerDomainScopeModel[domainScopeRuled](t, domainScopeRuleModel, "test_domain_scope_ruled")
	registerDomainScopeModel[domainScopeHidden](t, domainScopeHiddenModel, "test_domain_scope_hidden")
	registerDomainScopeModel[domainScopeOpen](t, domainScopeOpenModel, "test_domain_scope_open")
	RegisterRecordRule(domainScopeRuleModel, func(_ context.Context, env *Environment, _ *ModelMetadata, _ string) (DomainNode, error) {
		return Where("name", "!=", "private"), nil
	})
	// The kernel default RBAC handler needs sys_model_access rows; stand in for "ACL grants read".
	allow := func(context.Context, *Environment, *ModelMetadata, string, []string, RecordMap) error { return nil }
	RegisterAccessHandler(domainScopeRuleModel, allow)
	RegisterAccessHandler(domainScopeOpenModel, allow)
	RegisterAccessHandler(domainScopeHiddenModel, func(_ context.Context, _ *Environment, _ *ModelMetadata, operation string, _ []string, _ RecordMap) error {
		return Forbiddenf("no %s on hidden", operation)
	})
	return root
}

func domainScopeUserContext() context.Context {
	return ContextWithEnvironment(context.Background(), EnvironmentForIdentity(&RBACIdentity{
		UserID: 9, Login: "u", RoleID: 2, AuthMode: "bearer",
	}))
}

func TestScopeDomainTraversalAddsTargetRule(t *testing.T) {
	root := setupDomainScopeFixture(t)
	scoped, err := scopeDomainTraversals(domainScopeUserContext(), root, Where("ruled_id.email", "=", "a@x"))
	if err != nil {
		t.Fatal(err)
	}
	if scoped.Kind != DomainKindCondition || scoped.Field != "ruled_id" || scoped.Operator != "any" {
		t.Fatalf("expected ruled_id any, got %#v", scoped)
	}
	child, _ := scoped.Value.(DomainNode)
	if child.Kind != DomainKindAnd || len(child.Children) != 2 || child.Children[0].Field != "email" || child.Children[1].Field != "name" {
		t.Fatalf("expected email condition AND target rule, got %#v", child)
	}

	sql, args, err := scoped.CompileWithBaseAlias(root, `"root"`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, `EXISTS (SELECT 1 FROM "test_domain_scope_ruled" AS "any_test.domain_scope.root_ruled_id"`) ||
		!strings.Contains(sql, `"root"."ruled_id"`) || len(args) != 2 {
		t.Fatalf("unexpected SQL %s %v", sql, args)
	}
}

func TestScopeDomainTraversalDeniedTargetIsFalse(t *testing.T) {
	root := setupDomainScopeFixture(t)
	scoped, err := scopeDomainTraversals(domainScopeUserContext(), root,
		Or(Where("name", "=", "n"), Not(Where("hidden_id.name", "=", "secret"))))
	if err != nil {
		t.Fatal(err)
	}
	notNode := scoped.Children[1]
	if notNode.Kind != DomainKindNot || notNode.Children[0].Kind != DomainKindFalse {
		t.Fatalf("denied traversal should become FALSE inside NOT, got %#v", notNode)
	}
	if scoped.Children[0].Field != "name" {
		t.Fatalf("plain leaf changed: %#v", scoped.Children[0])
	}
}

func TestScopeDomainTraversalNestedHop(t *testing.T) {
	root := setupDomainScopeFixture(t)
	// open has no rule, but its ruled_id hop does: the inner hop is scoped, the outer wraps it.
	scoped, err := scopeDomainTraversals(domainScopeUserContext(), root, Where("open_id.ruled_id.email", "=", "a"))
	if err != nil {
		t.Fatal(err)
	}
	inner, _ := scoped.Value.(DomainNode)
	if scoped.Field != "open_id" || scoped.Operator != "any" || inner.Field != "ruled_id" || inner.Operator != "any" {
		t.Fatalf("expected nested any scoping, got %#v", scoped)
	}
	if _, _, err := scoped.CompileWithBaseAlias(root, `"root"`); err != nil {
		t.Fatalf("compile nested: %v", err)
	}
}

func TestScopeDomainTraversalUnrestrictedAndSudoUnchanged(t *testing.T) {
	root := setupDomainScopeFixture(t)
	plain := Where("open_id.name", "=", "a")
	scoped, err := scopeDomainTraversals(domainScopeUserContext(), root, plain)
	if err != nil || scoped.Field != "open_id.name" || scoped.Operator != "=" {
		t.Fatalf("unrestricted path should be unchanged, got %#v %v", scoped, err)
	}

	sudoCtx := ContextWithEnvironment(context.Background(), EnvironmentForIdentity(&RBACIdentity{
		UserID: 1, Login: "admin", RoleID: 1, AuthMode: "bearer", IsSuperadmin: true,
	}))
	for _, ctx := range []context.Context{sudoCtx, context.Background()} {
		scoped, err := scopeDomainTraversals(ctx, root, Where("hidden_id.name", "=", "x"))
		if err != nil || scoped.Field != "hidden_id.name" {
			t.Fatalf("sudo/internal domain should be unchanged, got %#v %v", scoped, err)
		}
	}
}

func TestScopeDomainAnyGetsTargetRule(t *testing.T) {
	root := setupDomainScopeFixture(t)
	scoped, err := scopeDomainTraversals(domainScopeUserContext(), root,
		DomainNode{Kind: DomainKindCondition, Field: "ruled_id", Operator: "any", Value: Where("email", "=", "a")})
	if err != nil {
		t.Fatal(err)
	}
	child, _ := scoped.Value.(DomainNode)
	if child.Kind != DomainKindAnd {
		t.Fatalf("any child should be ANDed with the target rule, got %#v", child)
	}
}
