package models

// File overview:
// - Responsibility: verifies base-module business model behavior and runtime metadata for res lang.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down business-model rules so base entities keep the same schema, defaults, and runtime semantics.

import (
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestNormalizeResLangDirectionAcceptsImportLabels(t *testing.T) {
	values := orm.RecordMap{"direction": "Left-to-Right"}

	normalizeResLangDirection(values)

	if got := values["direction"]; got != "ltr" {
		t.Fatalf("direction = %#v, want ltr", got)
	}

	values["direction"] = "Right-to-Left"
	normalizeResLangDirection(values)
	if got := values["direction"]; got != "rtl" {
		t.Fatalf("direction = %#v, want rtl", got)
	}
}

func TestValidateResLangValuesAcceptsNumericWeekStart(t *testing.T) {
	values := orm.RecordMap{
		"name":          "English (US)",
		"code":          "en_US",
		"iso_code":      "en-US",
		"url_code":      "en",
		"direction":     "ltr",
		"date_format":   "%m/%d/%Y",
		"time_format":   "%H:%M:%S",
		"week_start":    int64(7),
		"grouping":      "[3,0]",
		"decimal_point": ".",
	}

	if err := validateResLangValues(values); err != nil {
		t.Fatalf("validateResLangValues() error = %v", err)
	}
}

func TestValidateResLangValuesRejectsMissingWeekStart(t *testing.T) {
	values := orm.RecordMap{
		"name":          "English (US)",
		"code":          "en_US",
		"iso_code":      "en-US",
		"url_code":      "en",
		"direction":     "ltr",
		"date_format":   "%m/%d/%Y",
		"time_format":   "%H:%M:%S",
		"grouping":      "[3,0]",
		"decimal_point": ".",
	}

	err := validateResLangValues(values)
	if err == nil {
		t.Fatal("validateResLangValues() error = nil, want missing week_start")
	}
	if !strings.Contains(err.Error(), "week_start is required") {
		t.Fatalf("validateResLangValues() error = %v, want week_start message", err)
	}
}
