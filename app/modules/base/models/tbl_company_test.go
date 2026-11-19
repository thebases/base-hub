package models

import (
	"context"
	"reflect"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestResCompanyModelSpecResolveStorageName(t *testing.T) {
	if got := ResCompanyModelSpec.ResolveStorageName(); got != "tbl_company" {
		t.Fatalf("expected tbl.company storage name to resolve to tbl_company, got %q", got)
	}
}

func TestValidateResCompanyBranchRulesBatchesParentReads(t *testing.T) {
	callIDs := make([][]string, 0, 2)
	// Stub the shared ORM runtime repository seam so the business rule runs
	// without a registered executor or database.
	restore := orm.StubRuntimeRepository(ResCompanyModelName, orm.RuntimeRepositoryStub{
		Read: func(_ context.Context, ids []string, _ []string) ([]orm.RecordMap, error) {
			callIDs = append(callIDs, append([]string(nil), ids...))
			if len(callIDs) == 1 {
				return []orm.RecordMap{
					{"id": int64(10), "parent_id": int64(1), "country_id": int64(84), "currency_code": "USD"},
					{"id": int64(11), "parent_id": int64(1), "country_id": int64(84), "currency_code": "usd"},
				}, nil
			}
			return []orm.RecordMap{
				{"id": int64(1), "country_id": int64(84), "currency_code": "USD"},
			}, nil
		},
	})
	defer restore()

	records := orm.NewRecordSet(nil, nil, []string{"10", "11"})
	if err := validateResCompanyBranchRules(context.Background(), records, nil); err != nil {
		t.Fatalf("validateResCompanyBranchRules returned error: %v", err)
	}
	if len(callIDs) != 2 {
		t.Fatalf("expected 2 runtime reads, got %d", len(callIDs))
	}
	if !reflect.DeepEqual(callIDs[0], []string{"10", "11"}) {
		t.Fatalf("expected first read to load branch rows, got %v", callIDs[0])
	}
	if !reflect.DeepEqual(callIDs[1], []string{"1"}) {
		t.Fatalf("expected second read to batch one parent id, got %v", callIDs[1])
	}
}
