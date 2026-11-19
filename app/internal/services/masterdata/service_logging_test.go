package masterdata

// File overview:
// - Responsibility: verifies masterdata import, alignment, and rendered view output for service logging.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down import and render contracts so bootstrap data and generated XML stay stable across refactors.

import (
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestRecordValueKeysSortsDeterministically(t *testing.T) {
	values := orm.RecordMap{
		"symbol": "$",
		"name":   "USD",
		"active": false,
	}

	if got := recordValueKeys(values); got != "[active,name,symbol]" {
		t.Fatalf("recordValueKeys() = %q, want [active,name,symbol]", got)
	}
}

func TestRecordValuesJSONIncludesResolvedPayload(t *testing.T) {
	values := orm.RecordMap{
		"name":   "USD",
		"active": false,
	}

	got := recordValuesJSON(values)
	if got == "" || got == "{}" {
		t.Fatalf("recordValuesJSON() = %q, want populated JSON", got)
	}
}
