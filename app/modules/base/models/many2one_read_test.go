package models

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
	"github.com/uptrace/bun"
)

// Runtime reads return many2one fields as [id, display_name]; these tests stub that shape.

func TestResUsersGroupMembershipReadsMany2oneTuple(t *testing.T) {
	rows := []orm.RecordMap{
		{"id": int64(10), "group_id": [2]any{int64(3), "System"}},
		{"id": int64(11), "group_id": []any{float64(4), "Technical"}},
		{"id": int64(12), "group_id": false},
	}
	got := resUsersGroupMembership(rows)
	want := map[string]struct{}{"3": {}, "4": {}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("membership = %v, want %v", got, want)
	}
}

func TestCompanyWriteMirrorsToPartnerFromMany2oneTuple(t *testing.T) {
	var partnerIDs []string
	restoreCompany := orm.StubRuntimeRepository(ResCompanyModelName, orm.RuntimeRepositoryStub{
		Read: func(_ context.Context, _ []string, _ []string) ([]orm.RecordMap, error) {
			return []orm.RecordMap{{"id": int64(5), "partner_id": [2]any{int64(9), "Acme"}}}, nil
		},
	})
	defer restoreCompany()
	restorePartner := orm.StubRuntimeRepository(ResPartnerModelName, orm.RuntimeRepositoryStub{
		Write: func(_ context.Context, ids []string, _ orm.RecordMap) (bool, error) {
			partnerIDs = ids
			return true, nil
		},
	})
	defer restorePartner()

	if err := syncCompanyValuesToPartners(context.Background(), []string{"5"}, orm.RecordMap{"city": "HCM"}); err != nil {
		t.Fatalf("sync returned error: %v", err)
	}
	if !reflect.DeepEqual(partnerIDs, []string{"9"}) {
		t.Fatalf("expected partner 9 to be written, got %v", partnerIDs)
	}
}

func TestResCompanyParentRowsFromMany2oneTuple(t *testing.T) {
	var requested []string
	restore := orm.StubRuntimeRepository(ResCompanyModelName, orm.RuntimeRepositoryStub{
		Read: func(_ context.Context, ids []string, _ []string) ([]orm.RecordMap, error) {
			requested = ids
			return []orm.RecordMap{{"id": int64(1), "country_id": [2]any{int64(241), "Vietnam"}}}, nil
		},
	})
	defer restore()
	rows := []orm.RecordMap{{"id": int64(2), "parent_id": [2]any{int64(1), "Main"}}}
	parents, err := loadResCompanyParentRows(context.Background(), rows)
	if err != nil {
		t.Fatalf("load parents: %v", err)
	}
	if !reflect.DeepEqual(requested, []string{"1"}) || len(parents) != 1 {
		t.Fatalf("requested %v, parents %v; want parent 1 loaded", requested, parents)
	}
}

func TestRuntimeDBForModelNeverFallsBackToDefault(t *testing.T) {
	if _, err := runtimeDBForModel(context.Background()); !errors.Is(err, errNoRuntimeDB) {
		t.Fatalf("no environment: err = %v, want errNoRuntimeDB", err)
	}
	bare := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironment(nil, 1, nil, false))
	if _, err := runtimeDBForModel(bare); !errors.Is(err, errNoRuntimeDB) {
		t.Fatalf("environment without DB: err = %v, want errNoRuntimeDB", err)
	}
	tenant := &bun.DB{}
	bound := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironment(tenant, 1, nil, false))
	got, err := runtimeDBForModel(bound)
	if err != nil || got != bun.IDB(tenant) {
		t.Fatalf("bound environment: got %v, %v; want the environment DB", got, err)
	}
}
