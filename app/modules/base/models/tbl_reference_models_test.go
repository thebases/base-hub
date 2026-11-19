package models

// File overview:
// - Responsibility: verifies base-module business model behavior and runtime metadata for res reference models.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down business-model rules so base entities keep the same schema, defaults, and runtime semantics.

import (
	"context"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestResCountryDefaultsAndFormattingHelpers(t *testing.T) {
	values := map[string]any{"code": "vn"}
	applyResCountryDefaults(values)
	uppercaseValue(values, "code")

	if values["code"] != "VN" || values["name_position"] != "before" || values["zip_required"] != true {
		t.Fatalf("unexpected country defaults: %#v", values)
	}
	if got := countryFlagURL("GF"); got != "/base/static/img/country_flags/fr.png" {
		t.Fatalf("unexpected mapped flag url: %q", got)
	}
	fields := countryAddressFormatFields("%(street)s %(city)s %(country_code)s")
	if len(fields) != 3 || fields[0] != "street" || fields[1] != "city" || fields[2] != "country_code" {
		t.Fatalf("unexpected address fields: %#v", fields)
	}
	if err := validateCountryAddressFormat("%(unknown)s"); err == nil {
		t.Fatal("expected unknown address format key to fail")
	}
}

func TestResLangDefaultsAndFormatHelpers(t *testing.T) {
	values := map[string]any{"code": "en_US", "iso_code": "en"}
	applyResLangDefaults(values)

	if values["iso_code"] != "en" || values["direction"] != "ltr" || values["time_format"] != "%H:%M:%S" {
		t.Fatalf("unexpected language defaults: %#v", values)
	}
	if !normalizeResLangClockFormat(map[string]any{"time_format": "%I:%M:%S %p"}) {
		t.Fatal("expected 12-hour format to be normalized")
	}
	row := ResLangRecord{DecimalPoint: ",", ThousandsSep: "."}
	if got := applyResLangNumericSeparators("12345.67", row, true); got != "12.345,67" {
		t.Fatalf("unexpected localized number: %s", got)
	}
}

func TestNormalizeResUsersCreatePayload(t *testing.T) {
	values := map[string]any{"login": "admin"}
	one, err := normalizeResUsersCreatePayload(values)
	if err != nil {
		t.Fatalf("single payload failed: %v", err)
	}
	if len(one) != 1 || one[0]["login"] != "admin" {
		t.Fatalf("unexpected single payload: %#v", one)
	}

	many, err := normalizeResUsersCreatePayload([]any{map[string]any{"login": "demo"}, map[string]any{"login": "portal"}})
	if err != nil {
		t.Fatalf("multi payload failed: %v", err)
	}
	if len(many) != 2 || many[1]["login"] != "portal" {
		t.Fatalf("unexpected multi payload: %#v", many)
	}
}

func TestNormalizeResUsersPasswordHashesPlaintext(t *testing.T) {
	values := map[string]any{"new_password": "secret"}
	if err := normalizeResUsersPassword(values); err != nil {
		t.Fatalf("normalizing password failed: %v", err)
	}
	if _, ok := values["new_password"]; ok {
		t.Fatalf("new_password should not be persisted: %#v", values)
	}
	hash := stringValue(values["password"])
	if hash == "" || hash == "secret" {
		t.Fatalf("expected hashed password, got %#v", values["password"])
	}
}

func TestParseResUsersGroupSpec(t *testing.T) {
	positives, negatives := parseResUsersGroupSpec("base.group_user, !base.group_system, base.group_portal")
	if len(positives) != 2 || positives[0] != "base.group_user" || positives[1] != "base.group_portal" {
		t.Fatalf("unexpected positives: %#v", positives)
	}
	if len(negatives) != 1 || negatives[0] != "base.group_system" {
		t.Fatalf("unexpected negatives: %#v", negatives)
	}
}

func TestSplitResUsersDelegatedPartnerValues(t *testing.T) {
	userValues, partnerValues := splitResUsersDelegatedPartnerValues(
		ResUserDefinition,
		ResPartnerDefinition,
		map[string]any{
			"login":      "admin",
			"image_1920": "base64-image",
			"x_unknown":  "keep-on-user",
		},
	)

	if userValues["login"] != "admin" {
		t.Fatalf("expected login to remain on base.users payload: %#v", userValues)
	}
	if _, ok := userValues["image_1920"]; ok {
		t.Fatalf("expected image_1920 to be delegated to tbl.partner: %#v", userValues)
	}
	if partnerValues["image_1920"] != "base64-image" {
		t.Fatalf("expected image_1920 on delegated partner payload: %#v", partnerValues)
	}
	if userValues["x_unknown"] != "keep-on-user" {
		t.Fatalf("expected unknown fields to remain on base.users for normal validation: %#v", userValues)
	}
}

func TestSplitResUsersReadFields(t *testing.T) {
	userFields, partnerFields, err := splitResUsersReadFields(ResUserDefinition, ResPartnerDefinition, []string{"login", "image_1920"})
	if err != nil {
		t.Fatalf("splitResUsersReadFields failed: %v", err)
	}
	if len(userFields) != 1 || userFields[0] != "login" {
		t.Fatalf("expected base.users field list to stay on user model: %#v", userFields)
	}
	if len(partnerFields) != 1 || partnerFields[0] != "image_1920" {
		t.Fatalf("expected image_1920 to be read from tbl.partner: %#v", partnerFields)
	}
}

func TestSyncResUsersCreateNameWithPartnerFallsBackToPartnerName(t *testing.T) {
	// Stub the shared ORM runtime repository seam so the name-sync rule runs
	// without a registered tbl.partner executor.
	restore := orm.StubRuntimeRepository(ResPartnerModelName, orm.RuntimeRepositoryStub{
		Read: func(_ context.Context, ids []string, fields []string) ([]orm.RecordMap, error) {
			if len(ids) != 1 || ids[0] != "7" {
				t.Fatalf("unexpected ids: %#v", ids)
			}
			if len(fields) != 1 || fields[0] != "name" {
				t.Fatalf("unexpected fields: %#v", fields)
			}
			return []orm.RecordMap{{"id": int64(7), "name": "Partner Display"}}, nil
		},
	})
	defer restore()

	userValues := orm.RecordMap{"partner_id": int64(7), "name": ""}
	partnerValues := orm.RecordMap{}
	if err := syncResUsersCreateNameWithPartner(context.Background(), userValues, partnerValues); err != nil {
		t.Fatalf("syncResUsersCreateNameWithPartner() error = %v", err)
	}
	if got := userValues["name"]; got != "Partner Display" {
		t.Fatalf("user name = %#v, want Partner Display", got)
	}
	if _, ok := partnerValues["name"]; ok {
		t.Fatalf("partner name should not be overwritten during fallback: %#v", partnerValues)
	}
}

func TestPlanResUsersWriteNameSyncPushesExplicitNameToPartner(t *testing.T) {
	userValues := orm.RecordMap{"name": "  Administrator  "}
	partnerValues := orm.RecordMap{}

	plan := planResUsersWriteNameSync(userValues, partnerValues)
	if plan.FallbackName {
		t.Fatalf("expected explicit name plan, got fallback: %#v", plan)
	}
	if plan.ExplicitName != "Administrator" {
		t.Fatalf("explicit name = %q, want Administrator", plan.ExplicitName)
	}
	if got := userValues["name"]; got != "Administrator" {
		t.Fatalf("trimmed user name = %#v, want Administrator", got)
	}
	if got := partnerValues["name"]; got != "Administrator" {
		t.Fatalf("partner name = %#v, want Administrator", got)
	}
}

func TestPlanResUsersWriteNameSyncMarksBlankNameForFallback(t *testing.T) {
	userValues := orm.RecordMap{"name": ""}
	partnerValues := orm.RecordMap{}

	plan := planResUsersWriteNameSync(userValues, partnerValues)
	if !plan.FallbackName {
		t.Fatalf("expected fallback plan, got %#v", plan)
	}
	if _, ok := userValues["name"]; ok {
		t.Fatalf("blank name should be removed from bulk write payload: %#v", userValues)
	}
	if _, ok := partnerValues["name"]; ok {
		t.Fatalf("partner name should stay untouched for fallback writes: %#v", partnerValues)
	}
}
