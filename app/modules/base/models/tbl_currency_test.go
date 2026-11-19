package models

// File overview:
// - Responsibility: verifies base-module business model behavior and runtime metadata for res currency.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down business-model rules so base entities keep the same schema, defaults, and runtime semantics.

import (
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestResCurrencyModelSpecsResolveStorageNames(t *testing.T) {
	if got := ResCurrencyModelSpec.ResolveStorageName(); got != "tbl_currency" {
		t.Fatalf("expected tbl.currency storage name to resolve to tbl_currency, got %q", got)
	}
	if got := ResCurrencyRateModelSpec.ResolveStorageName(); got != "tbl_currency_rate" {
		t.Fatalf("expected tbl.currency.rate storage name to resolve to tbl_currency_rate, got %q", got)
	}
}

func TestApplyResCurrencyDefaultsAndValidation(t *testing.T) {
	values := orm.RecordMap{"name": "usd"}
	applyResCurrencyDefaults(values)
	uppercaseValue(values, "name")

	if values["name"] != "USD" {
		t.Fatalf("expected currency code to normalize to uppercase, got %#v", values["name"])
	}
	if values["rounding"] != float64(0.01) {
		t.Fatalf("expected default rounding 0.01, got %#v", values["rounding"])
	}
	if values["position"] != "after" {
		t.Fatalf("expected default position after, got %#v", values["position"])
	}
	if values["decimal_places"] != int64(2) {
		t.Fatalf("expected default decimal_places 2, got %#v", values["decimal_places"])
	}
	if err := validateResCurrencyValues(orm.RecordMap{"rounding": 0}); err == nil {
		t.Fatal("expected zero rounding to fail validation")
	}
}

func TestSanitizeResCurrencyRateVals(t *testing.T) {
	values := orm.RecordMap{
		"rate":                 7.2,
		"company_rate":         7.1,
		"inverse_company_rate": 0.13,
	}
	sanitized := sanitizeResCurrencyRateVals(values)

	if _, exists := sanitized["company_rate"]; exists {
		t.Fatalf("expected company_rate to be removed when rate is provided: %#v", sanitized)
	}
	if _, exists := sanitized["inverse_company_rate"]; exists {
		t.Fatalf("expected inverse_company_rate to be removed when rate is provided: %#v", sanitized)
	}
}

func TestResCurrencyFormattingHelpers(t *testing.T) {
	row := resCurrencyRow{
		Symbol:        "$",
		Position:      "before",
		Rounding:      0.01,
		DecimalPlaces: 2,
	}
	if got := formatCurrencyAmount(row, 12.345); got != "$12.35" {
		t.Fatalf("unexpected formatted amount: %q", got)
	}
	if got := decimalPlacesForRounding(0.001); got != 3 {
		t.Fatalf("unexpected decimal places for 0.001: %d", got)
	}
	if got := roundToFactor(12.344, 0.01); got != 12.34 {
		t.Fatalf("unexpected rounded value: %v", got)
	}
}
