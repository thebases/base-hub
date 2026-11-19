package models

// File overview:
// - Responsibility: T87 regression for the base module: the create/write logic moved into ModelSpec.Prepare
//   (country, country group, lang, currency) and the overrides switched to orm.CreateRecord/WriteRecords
//   (partner, country list create) keep their rules and now get the guarded sequence.
// - Key symbols: registerGuardedTestModels, companyCaller, focused Test cases.

import (
	"context"
	"strings"
	"sync"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

var guardedTestModelsOnce sync.Once

// registerGuardedTestModels registers the metadata the guarded helpers need (field lookup, Prepare,
// computed flags). Registration needs no database; repository calls are stubbed per test.
func registerGuardedTestModels(t *testing.T) {
	t.Helper()
	guardedTestModelsOnce.Do(func() {
		ResCountryModel.InitDefault()
		ResPartnerModel.InitDefault()
	})
}

// companyCaller is a non-sudo caller whose active (and only allowed) company is companyID.
func companyCaller(companyID int64) context.Context {
	env := orm.NewEnvironment(nil, 10, map[string]any{
		orm.ContextKeyAllowedCompanyIDs: []int64{companyID},
		orm.ContextKeyCurrentCompanyID:  companyID,
	}, false)
	return orm.ContextWithEnvironment(context.Background(), env)
}

func TestPrepareResCountry(t *testing.T) {
	op := orm.WriteOp{Create: true, Values: orm.RecordMap{"name": "Viet Nam", "code": "vn"}}
	if err := prepareResCountry(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	if op.Values["code"] != "VN" {
		t.Fatalf("code not upper-cased: %v", op.Values)
	}
	bad := orm.WriteOp{IDs: []string{"1"}, Values: orm.RecordMap{"address_format": "%(nope)s"}}
	if err := prepareResCountry(context.Background(), bad); err == nil {
		t.Fatal("unknown address_format placeholder must be rejected")
	}
	if err := prepareResCountry(context.Background(), orm.WriteOp{Values: orm.RecordMap{"code": "vn"}}); err == nil {
		t.Fatal("write without ids must be rejected")
	}
}

func TestPrepareResCountryGroup(t *testing.T) {
	op := orm.WriteOp{IDs: []string{"1"}, Values: orm.RecordMap{"code": "asean"}}
	if err := prepareResCountryGroup(context.Background(), op); err != nil || op.Values["code"] != "ASEAN" {
		t.Fatalf("code = %v, err = %v", op.Values["code"], err)
	}
}

func TestPrepareResLangCreate(t *testing.T) {
	op := orm.WriteOp{Create: true, Values: orm.RecordMap{"name": "Tieng Viet", "code": "vi_VN", "iso_code": "vi", "direction": "RTL"}}
	if err := prepareResLang(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	if op.Values["direction"] != "rtl" {
		t.Fatalf("direction not normalised: %v", op.Values["direction"])
	}
	bad := orm.WriteOp{Create: true, Values: orm.RecordMap{"name": "X", "code": "x", "iso_code": "x", "date_format": "%C"}}
	if err := prepareResLang(context.Background(), bad); err == nil {
		t.Fatal("disallowed date_format token must be rejected")
	}
}

func TestPrepareResCurrency(t *testing.T) {
	op := orm.WriteOp{Create: true, Values: orm.RecordMap{"name": "vnd", "rounding": 1.0}}
	if err := prepareResCurrency(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	if op.Values["name"] != "VND" || op.Values["active"] != true || op.Values["position"] != "after" {
		t.Fatalf("defaults not applied: %v", op.Values)
	}
	bad := orm.WriteOp{IDs: []string{"1"}, Values: orm.RecordMap{"rounding": 0.0}}
	if err := prepareResCurrency(context.Background(), bad); err == nil {
		t.Fatal("non-positive rounding must be rejected")
	}
}

// The list create now goes through CreateRecord: Prepare runs per row and a computed field is refused.
func TestCreateResCountryListUsesGuardedCreate(t *testing.T) {
	registerGuardedTestModels(t)
	var stored []orm.RecordMap
	t.Cleanup(orm.StubRuntimeRepository(ResCountryModelName, orm.RuntimeRepositoryStub{
		Create: func(_ context.Context, values orm.RecordMap) (orm.RecordMap, error) {
			stored = append(stored, values)
			return orm.RecordMap{"id": int64(len(stored)), "code": values["code"]}, nil
		},
	}))

	out, err := createResCountry(context.Background(), nil, nil, []any{[]any{
		map[string]any{"name": "A", "code": "aa"}, map[string]any{"name": "B", "code": "bb"},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rows, ok := out.([]orm.RecordMap); !ok || len(rows) != 2 || stored[0]["code"] != "AA" || stored[1]["code"] != "BB" {
		t.Fatalf("list create = %v, stored = %v", out, stored)
	}

	stored = nil
	_, err = createResCountry(context.Background(), nil, nil, []any{map[string]any{"name": "C", "code": "cc", "image_url": "/x.png"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "computed") || stored != nil {
		t.Fatalf("computed image_url must be refused before the repository: err=%v stored=%v", err, stored)
	}
}

// createResPartner pins company_id to the caller's active company (it stored NULL before), and
// writeResPartner refuses a company the caller is not active in before the repository runs.
func TestResPartnerOverridesUseGuardedPath(t *testing.T) {
	registerGuardedTestModels(t)
	var created orm.RecordMap
	wrote := false
	t.Cleanup(orm.StubRuntimeRepository(ResPartnerModelName, orm.RuntimeRepositoryStub{
		Create: func(_ context.Context, values orm.RecordMap) (orm.RecordMap, error) {
			created = values
			return orm.RecordMap{"id": int64(7), "name": values["name"]}, nil
		},
		Write: func(context.Context, []string, orm.RecordMap) (bool, error) {
			wrote = true
			return true, nil
		},
	}))
	caller := companyCaller(2)

	if _, err := createResPartner(caller, nil, nil, []any{map[string]any{"name": "Acme"}}, nil); err != nil {
		t.Fatal(err)
	}
	if created["company_id"] != int64(2) || created["complete_name"] != "Acme" || created["type"] != "contact" {
		t.Fatalf("created values = %v, want company 2 with override defaults", created)
	}

	_, err := writeResPartner(caller, nil, []string{"7"}, []any{map[string]any{"company_id": int64(1)}}, nil)
	if err == nil || !strings.Contains(err.Error(), "permission denied") || wrote {
		t.Fatalf("write to company 1 must be denied before the repository: err=%v wrote=%v", err, wrote)
	}
}
