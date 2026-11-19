package base

import (
	"encoding/json"
	"testing"
	"time"
)

type timeEmbedded struct {
	UpdatedAt time.Time `json:"updated_at"`
}

type timeRecord struct {
	timeEmbedded
	ID      int64      `json:"id"`
	Created time.Time  `json:"created_at"`
	Seen    *time.Time `json:"seen_at"`
	Gone    *time.Time `json:"gone_at,omitempty"`
	Note    string     `json:"note"`
	Hidden  time.Time  `json:"-"`
}

func TestJSONMarshalConvertsTimesToUnixSeconds(t *testing.T) {
	loc := time.FixedZone("x", 7*3600)
	at := time.Date(2026, 10, 11, 7, 0, 0, 0, loc) // 2026-10-11T00:00:00Z
	want := at.UTC().Unix()
	payload := map[string]any{
		"rec":   timeRecord{timeEmbedded: timeEmbedded{UpdatedAt: at}, ID: 1, Created: at, Seen: &at, Note: "2026-10-11T00:00:00Z"},
		"rows":  []map[string]any{{"at": at, "zero": time.Time{}}},
		"plain": "2026-10-11T00:00:00Z",
	}
	raw, err := JSONMarshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	rec := got["rec"].(map[string]any)
	for _, key := range []string{"updated_at", "created_at", "seen_at"} {
		if rec[key] != float64(want) {
			t.Fatalf("%s = %v, want %d", key, rec[key], want)
		}
	}
	if _, ok := rec["gone_at"]; ok {
		t.Fatal("omitempty nil pointer should be dropped")
	}
	if rec["note"] != "2026-10-11T00:00:00Z" || got["plain"] != "2026-10-11T00:00:00Z" {
		t.Fatal("plain strings must not be touched")
	}
	row := got["rows"].([]any)[0].(map[string]any)
	if row["at"] != float64(want) || row["zero"] != false {
		t.Fatalf("row = %v", row)
	}
}
