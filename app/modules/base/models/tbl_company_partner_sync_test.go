package models

import (
	"context"
	"reflect"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestCompanyWriteMirrorsSharedFieldsToPartner(t *testing.T) {
	var partnerIDs []string
	var partnerValues orm.RecordMap
	restoreCompany := orm.StubRuntimeRepository(ResCompanyModelName, orm.RuntimeRepositoryStub{
		Read: func(_ context.Context, _ []string, _ []string) ([]orm.RecordMap, error) {
			return []orm.RecordMap{{"id": int64(5), "partner_id": int64(9)}}, nil
		},
	})
	defer restoreCompany()
	restorePartner := orm.StubRuntimeRepository(ResPartnerModelName, orm.RuntimeRepositoryStub{
		Write: func(ctx context.Context, ids []string, values orm.RecordMap) (bool, error) {
			if !inCompanyPartnerSync(ctx) {
				t.Fatalf("partner write must carry the sync flag")
			}
			partnerIDs, partnerValues = ids, values
			return true, nil
		},
	})
	defer restorePartner()

	// parent_id is not shared and must never reach the partner.
	shared := pickCompanyPartnerSharedValues(orm.RecordMap{"name": "Acme", "city": "HCM", "parent_id": int64(1)})
	if err := syncCompanyValuesToPartners(context.Background(), []string{"5"}, shared); err != nil {
		t.Fatalf("sync returned error: %v", err)
	}
	if !reflect.DeepEqual(partnerIDs, []string{"9"}) {
		t.Fatalf("expected partner 9 to be written, got %v", partnerIDs)
	}
	want := orm.RecordMap{"name": "Acme", "city": "HCM", "complete_name": "Acme"}
	if !reflect.DeepEqual(partnerValues, want) {
		t.Fatalf("partner values = %v, want %v", partnerValues, want)
	}
}

func TestPartnerWriteMirrorsToCompaniesAndSkipsEcho(t *testing.T) {
	var companyIDs []string
	var companyValues orm.RecordMap
	restore := orm.StubRuntimeRepository(ResCompanyModelName, orm.RuntimeRepositoryStub{
		SearchRead: func(_ context.Context, _ orm.RecordMap, _ []string) ([]orm.RecordMap, error) {
			return []orm.RecordMap{{"id": int64(5)}}, nil
		},
		Write: func(ctx context.Context, ids []string, values orm.RecordMap) (bool, error) {
			if !inCompanyPartnerSync(ctx) {
				t.Fatalf("company write must carry the sync flag")
			}
			companyIDs, companyValues = ids, values
			return true, nil
		},
	})
	defer restore()

	shared := orm.RecordMap{"email": "a@b.vn"}
	if err := syncPartnerValuesToCompanies(context.Background(), []string{"9"}, shared); err != nil {
		t.Fatalf("sync returned error: %v", err)
	}
	if !reflect.DeepEqual(companyIDs, []string{"5"}) || !reflect.DeepEqual(companyValues, shared) {
		t.Fatalf("unexpected company write: ids=%v values=%v", companyIDs, companyValues)
	}

	// A write already issued by the sync must not bounce back.
	companyIDs = nil
	if err := syncPartnerValuesToCompanies(withCompanyPartnerSync(context.Background()), []string{"9"}, shared); err != nil {
		t.Fatalf("echo sync returned error: %v", err)
	}
	if companyIDs != nil {
		t.Fatalf("echo write must be skipped, got %v", companyIDs)
	}
}

func TestGrantRootSuperadminsCompanyAppendsOnlyMissingIDs(t *testing.T) {
	writes := map[string][]int64{}
	restoreGroups := orm.StubRuntimeRepository(ResGroupModelName, orm.RuntimeRepositoryStub{
		SearchRead: func(_ context.Context, filter orm.RecordMap, _ []string) ([]orm.RecordMap, error) {
			if filter["bid"] != "base.group_no_one" {
				t.Fatalf("unexpected group filter %v", filter)
			}
			return []orm.RecordMap{{"id": int64(7)}}, nil
		},
	})
	defer restoreGroups()
	restoreUsers := orm.StubRuntimeRepository(ResUserModelName, orm.RuntimeRepositoryStub{
		SearchRead: func(_ context.Context, filter orm.RecordMap, _ []string) ([]orm.RecordMap, error) {
			if filter["group_id"] != int64(7) || filter["company_id"] != int64(1) {
				t.Fatalf("users must be limited to the Technical Features group of company 1, got %v", filter)
			}
			return []orm.RecordMap{
				{"id": int64(1), "company_ids": []int64{1, 2}},
				{"id": int64(2), "company_ids": []int64{1, 2, 3}},
			}, nil
		},
		Write: func(_ context.Context, ids []string, values orm.RecordMap) (bool, error) {
			writes[ids[0]] = values["company_ids"].([]int64)
			return true, nil
		},
	})
	defer restoreUsers()

	if err := grantRootSuperadminsCompany(context.Background(), 3); err != nil {
		t.Fatalf("grant returned error: %v", err)
	}
	if !reflect.DeepEqual(writes, map[string][]int64{"1": {1, 2, 3}}) {
		t.Fatalf("expected only user 1 to gain company 3, got %v", writes)
	}
}

func TestPartnerRenameMirrorsToLinkedUsers(t *testing.T) {
	var userIDs []string
	var userValues orm.RecordMap
	restore := orm.StubRuntimeRepository(ResUserModelName, orm.RuntimeRepositoryStub{
		SearchRead: func(_ context.Context, _ orm.RecordMap, _ []string) ([]orm.RecordMap, error) {
			return []orm.RecordMap{{"id": int64(3), "name": "Old"}, {"id": int64(4), "name": "New Name"}}, nil
		},
		Write: func(_ context.Context, ids []string, values orm.RecordMap) (bool, error) {
			userIDs, userValues = ids, values
			return true, nil
		},
	})
	defer restore()

	if err := syncPartnerNameToUsers(context.Background(), []string{"9"}, orm.RecordMap{"name": " New Name "}); err != nil {
		t.Fatalf("sync returned error: %v", err)
	}
	// User 4 already has the name, so only user 3 is written.
	if !reflect.DeepEqual(userIDs, []string{"3"}) || userValues["name"] != "New Name" {
		t.Fatalf("unexpected user write: ids=%v values=%v", userIDs, userValues)
	}

	userIDs = nil
	if err := syncPartnerNameToUsers(context.Background(), []string{"9"}, orm.RecordMap{"email": "a@b.vn"}); err != nil || userIDs != nil {
		t.Fatalf("non-name write must not touch users: err=%v ids=%v", err, userIDs)
	}
}
