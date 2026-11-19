package models

// File overview:
// - Responsibility: guards the convention that the `name` field of every tbl.* model is a
//   translate:true field (per-language jsonb), except the models listed below.
// - Pattern: a new tbl.* model's `name` must either carry `meta:"translate"` or be added to
//   translateNameExclusions with the reason it must stay a plain string.

import (
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

// translateNameExclusions are tbl.* models whose `name` must NOT be translated.
var translateNameExclusions = map[string]string{
	"tbl.currency":      "name is the ISO currency code, not a label",
	"tbl.currency.rate": "name is the rate date (YYYY-MM-DD), not a label",
	"tbl.partner":       "a person/legal-entity name is not a label; searched and matched as plain text everywhere",
	"tbl.company":       "unique legal-entity name used as a lookup key",
	"tbl.lang":          "name is the unique language list entry read by raw SQL (get_installed, ingest reference)",
}

func TestTblModelNameFieldsAreTranslated(t *testing.T) {
	// The model registry is only populated at runtime bootstrap, so inspect the specs directly.
	specs := []orm.ModelSpec{
		ResPartnerModelSpec, ResCompanyModelSpec, ResCountryModelSpec, ResCountryGroupModelSpec,
		ResCountryStateModelSpec, ResCountryWardModelSpec, BankModelSpec,
		ResCurrencyModelSpec, ResCurrencyRateModelSpec, ResLangModelSpec,
	}
	checked := 0
	for _, spec := range specs {
		if spec.Definition == nil || !strings.HasPrefix(spec.Name, "tbl.") {
			t.Fatalf("spec %q has no definition or is not a tbl.* model", spec.Name)
		}
		nameField := spec.Definition.Fields["name"]
		if nameField == nil {
			continue
		}
		checked++
		if reason, excluded := translateNameExclusions[spec.Name]; excluded {
			if nameField.Translate {
				t.Errorf("%s.name is translated but is on the exclusion list (%s)", spec.Name, reason)
			}
			continue
		}
		if !nameField.Translate {
			t.Errorf("%s.name must be meta:\"translate\" (or be added to translateNameExclusions with a reason)", spec.Name)
		}
	}
	if checked == 0 {
		t.Fatal("no spec with a name field was checked")
	}
}
