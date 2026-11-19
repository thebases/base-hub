package models

import (
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

// kvtRules mirrors the seeded base.kvt rules: users read type<=1, system users
// do everything on type<=2.
func kvtRules() []sysRuleDBRow {
	return []sysRuleDBRow{
		{ID: 1, DomainForce: `[["type","<=",1]]`, PermRead: true, GroupBIDs: []string{"base.group_user"}},
		{ID: 2, DomainForce: `[["type","<=",2]]`, PermRead: true, PermWrite: true, PermCreate: true, PermUnlink: true, GroupBIDs: []string{"base.group_system"}},
	}
}

func TestSysRuleDomainForFailsClosedWhenNoGroupRuleMatches(t *testing.T) {
	// A plain user writing base.kvt: only the group_system rule covers write.
	got, err := sysRuleDomainFor(kvtRules(), []string{"base.group_user"}, "write")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != orm.DomainKindFalse {
		t.Fatalf("write by group_user: want false domain, got %+v", got)
	}
}

func TestSysRuleDomainForMatchesImpliedGroups(t *testing.T) {
	// A custom role implying base.group_user is restricted by the user rule.
	got, err := sysRuleDomainFor(kvtRules(), []string{"custom.role", "base.group_user"}, "read")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind == orm.DomainKindTrue || got.Kind == orm.DomainKindFalse || got.Kind == "" {
		t.Fatalf("read by implied group_user: want the type<=1 domain, got %+v", got)
	}
}

func TestSysRuleDomainForSystemUserGetsSystemRule(t *testing.T) {
	got, err := sysRuleDomainFor(kvtRules(), []string{"base.group_system", "base.group_user"}, "write")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind == orm.DomainKindTrue || got.Kind == orm.DomainKindFalse || got.Kind == "" {
		t.Fatalf("write by group_system: want the type<=2 domain, got %+v", got)
	}
}

func TestSysRuleDomainForGlobalOnlyAndNoRules(t *testing.T) {
	global := []sysRuleDBRow{{ID: 3, DomainForce: `[["active","=",true]]`, PermRead: true, GlobalRule: true}}
	got, err := sysRuleDomainFor(global, nil, "read")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind == orm.DomainKindTrue || got.Kind == orm.DomainKindFalse || got.Kind == "" {
		t.Fatalf("global rule: want its domain, got %+v", got)
	}
	// No rule covers the operation: unrestricted (ACL still applies).
	got, err = sysRuleDomainFor(global, nil, "write")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != orm.DomainKindTrue {
		t.Fatalf("no rule for write: want true domain, got %+v", got)
	}
}
