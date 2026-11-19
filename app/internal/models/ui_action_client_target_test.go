package models

import (
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

// TestNormalizeUIActionClientTargets covers the client-only presentation
// targets (dialog/sheet/window) next to the shared window-action set.
func TestNormalizeUIActionClientTargets(t *testing.T) {
	cases := []struct {
		target  string
		want    string
		wantErr bool
	}{
		{target: "dialog", want: "dialog"},
		{target: " Sheet ", want: "sheet"},
		{target: "window", want: "window"},
		{target: "new", want: "new"},
		{target: "current", want: "current"},
		{target: "", want: "current"},
		{target: "popover", wantErr: true},
	}
	for _, tc := range cases {
		values, err := normalizeUIActionClientValues(orm.RecordMap{"tag": "open_widget", "target": tc.target}, true)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("target %q: expected error", tc.target)
			}
			continue
		}
		if err != nil {
			t.Fatalf("target %q: unexpected error %v", tc.target, err)
		}
		if got := values["target"]; got != tc.want {
			t.Fatalf("target %q: got %#v, want %q", tc.target, got, tc.want)
		}
	}
}

// TestNormalizeUIActionClientPresentationParams checks that dialog size and
// sheet side are validated, while unrelated params pass through untouched.
func TestNormalizeUIActionClientPresentationParams(t *testing.T) {
	valid := map[string]any{"widget": "edc_device_status_panel", "size": "lg", "side": "right"}
	values, err := normalizeUIActionClientValues(orm.RecordMap{"tag": "open_widget", "params": valid}, true)
	if err != nil {
		t.Fatalf("valid params: unexpected error %v", err)
	}
	if params := values["params"].(map[string]any); params["widget"] != "edc_device_status_panel" {
		t.Fatalf("params widget lost: %#v", params)
	}
	for _, bad := range []map[string]any{{"size": "huge"}, {"side": "bottom"}, {"size": 3}} {
		if _, err := normalizeUIActionClientValues(orm.RecordMap{"tag": "open_widget", "params": bad}, true); err == nil {
			t.Fatalf("params %#v: expected error", bad)
		}
	}
}
