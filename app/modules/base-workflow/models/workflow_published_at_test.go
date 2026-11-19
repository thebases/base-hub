package models

import (
	"encoding/json"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/base"
	"git.thebase.vn/base_hub/internal/orm"
)

// An unpublished workflow's published_at must answer as unset (null/false), never the
// "0001-01-01T00:00:00Z" a zero time.Time marshals to on struct read paths (Bun fallback search
// inside a transaction, published snapshots), which base.TimesToUnix cannot recognise.
func TestWorkflowPublishedAtUnsetIsNotZeroTimeString(t *testing.T) {
	raw, err := json.Marshal(FlowRecord{})
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	if got := record["published_at"]; got != nil {
		t.Fatalf("unpublished published_at = %#v, want null", got)
	}

	at := time.Date(2026, 11, 15, 1, 2, 3, 0, time.UTC)
	answer := base.TimesToUnix(orm.RecordMap{"published_at": FlowRecord{PublishedAt: &at}.PublishedAt}).(map[string]any)
	if answer["published_at"] != at.Unix() {
		t.Fatalf("published published_at answer = %#v, want %d", answer["published_at"], at.Unix())
	}
}
