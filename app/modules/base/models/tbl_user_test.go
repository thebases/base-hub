package models

// File overview:
// - Responsibility: verifies base-module business model behavior and runtime metadata for res user.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down business-model rules so base entities keep the same schema, defaults, and runtime semantics.

import "testing"

func TestResUserAndGroupModelSpecsResolveLegacyStorageNames(t *testing.T) {
	if got := ResUserModelSpec.ResolveStorageName(); got != "base_users" {
		t.Fatalf("expected base.users storage name to use default base_users mapping, got %q", got)
	}
	if got := ResGroupModelSpec.ResolveStorageName(); got != "tbl_groups" {
		t.Fatalf("expected tbl.groups storage name to use default tbl_groups mapping, got %q", got)
	}
}

func TestResUserSecurityObjectMethodsUsePathID(t *testing.T) {
	// Saved-record security buttons are invoked through /rpc/{model}/{method}/{id},
	// so these runtime methods must read their target IDs from the path instead of args[0].
	methodsByName := map[string]bool{}
	for _, method := range ResUserModelSpec.RuntimeMethods {
		methodsByName[method.Name] = method.UsesPathID
	}
	if !methodsByName["action_set_password"] {
		t.Fatal("expected action_set_password to use path ids")
	}
}

func TestInt64SliceFromAnyParsesNumericStringSlice(t *testing.T) {
	// masterdata.Service.resolveReferenceList resolves seed data many2many
	// external-id arrays (e.g. company_ids: ["company.main"]) into a genuine
	// []string of numeric ids, never []any -- ensureResUsersCompanyScopeConsistent
	// silently dropped every id here before this case was added, which broke
	// user.admin's create seed with "company_id 1 must be included in company_ids".
	got := int64SliceFromAny([]string{"1", "2"})
	want := []int64{1, 2}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, got)
		}
	}
}

func TestInt64SliceFromAnyParsesX2ManyCommands(t *testing.T) {
	// The many2many_tags widget submits company_ids as [[6,0,[1]]]; it must
	// flatten to [1] or company_id validation wrongly rejects the payload.
	got := int64SliceFromAny([]any{[]any{float64(6), float64(0), []any{float64(1), float64(2)}}})
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("expected [1 2], got %v", got)
	}
	got = int64SliceFromAny([]any{[]any{float64(4), float64(3)}})
	if len(got) != 1 || got[0] != 3 {
		t.Fatalf("expected [3], got %v", got)
	}
}
