package models

// File overview:
// - Responsibility: verifies base-module business model behavior and runtime metadata for res country.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down business-model rules so base entities keep the same schema, defaults, and runtime semantics.

import "testing"

func TestResCountryModelSpecsResolveRuntimeStorageNames(t *testing.T) {
	if got := ResCountryModelSpec.ResolveStorageName(); got != "tbl_country" {
		t.Fatalf("expected tbl.country storage name to resolve to tbl_country, got %q", got)
	}
	if got := ResCountryGroupModelSpec.ResolveStorageName(); got != "tbl_country_group" {
		t.Fatalf("expected tbl.country.group storage name to resolve to tbl_country_group, got %q", got)
	}
	if got := ResCountryStateModelSpec.ResolveStorageName(); got != "tbl_country_state" {
		t.Fatalf("expected tbl.country.state storage name to resolve to tbl_country_state, got %q", got)
	}
	if got := ResCountryWardModelSpec.ResolveStorageName(); got != "tbl_country_ward" {
		t.Fatalf("expected tbl.country.ward storage name to resolve to tbl_country_ward, got %q", got)
	}
}
