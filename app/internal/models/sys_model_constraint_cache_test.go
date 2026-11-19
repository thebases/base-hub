package models

import (
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestBusinessRuleCacheIsolatedPerDB(t *testing.T) {
	row := orm.RecordMap{"id": int64(901), "model_name": "tbl.cache_iso_test", "constraint_type": sysModelConstraintTypeBusinessRule, "state": "active"}
	refreshBusinessRuleCacheRow("db_a", row)
	defer removeBusinessRuleCacheRow("db_a", row)

	businessRuleCache.mu.RLock()
	inA := len(businessRuleCache.byRow[businessRuleCacheKey("db_a", "tbl.cache_iso_test")])
	inB := len(businessRuleCache.byRow[businessRuleCacheKey("db_b", "tbl.cache_iso_test")])
	businessRuleCache.mu.RUnlock()
	if inA != 1 || inB != 0 {
		t.Fatalf("expected rule only in db_a, got a=%d b=%d", inA, inB)
	}
}
