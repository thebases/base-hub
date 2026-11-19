package models

import (
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestNormalizeWebsitePublishAt(t *testing.T) {
	v := orm.RecordMap{"publish_at": "2026-10-20T10:00:00+07:00"}
	if err := normalizeWebsitePublishAt(v); err != nil {
		t.Fatal(err)
	}
	if v["publish_at"] != "2026-10-20T03:00:00Z" || v["publish_pending"] != true {
		t.Fatalf("unexpected %v", v)
	}
	clear := orm.RecordMap{"publish_at": ""}
	_ = normalizeWebsitePublishAt(clear)
	if clear["publish_pending"] != false {
		t.Fatalf("empty must disarm: %v", clear)
	}
	untouched := orm.RecordMap{"name": "x"}
	_ = normalizeWebsitePublishAt(untouched)
	if _, ok := untouched["publish_pending"]; ok {
		t.Fatal("content-only write must not touch the schedule")
	}
	if err := normalizeWebsitePublishAt(orm.RecordMap{"publish_at": "tomorrow"}); err == nil {
		t.Fatal("garbage must be rejected")
	}
}

func TestValidateWebsiteTheme(t *testing.T) {
	ok := map[string]any{"colors": map[string]any{"main": []any{"#112233", "rgb(1, 2, 3)"}}, "headings": map[string]any{"fontFamily": "'Inter Tight', system-ui, sans-serif", "fontSize": "48px"}}
	if err := validateWebsiteTheme(ok, ""); err != nil {
		t.Fatalf("valid theme rejected: %v", err)
	}
	for _, bad := range []string{"red; } body { display:none", "url(http://x)", "a{b}", "@import x", "x</style>"} {
		if err := validateWebsiteTheme(map[string]any{"a": bad}, ""); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}
