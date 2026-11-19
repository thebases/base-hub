package orm

import (
	"reflect"
	"testing"

	"github.com/uptrace/bun"
)

type typeIndexBase struct {
	bun.BaseModel `bun:"table:t"`
	ID            int64 `json:"id" bun:"id,pk"`
}

type typeIndexDoc struct {
	typeIndexBase
	DisplayName string         `json:"display_name" bun:"display_name"`
	Payload     map[string]any `json:"payload" bun:"payload,type:jsonb"`
	NoTags      string
}

func TestBunTypeIndexMatchesTagWalk(t *testing.T) {
	typ := reflect.TypeOf(typeIndexDoc{})
	for key, want := range map[string]string{
		"ID": "id", "id": "id", "display_name": "display_name", "DisplayName": "display_name",
		"payload": "payload", "NoTags": "no_tags", "no_tags": "no_tags",
	} {
		got, ok := lookupColumnName(typ, key)
		if !ok || got != want {
			t.Errorf("lookupColumnName(%q) = %q,%v want %q", key, got, ok, want)
		}
	}
	if _, ok := lookupColumnName(typ, "missing"); ok {
		t.Error("unknown key must not resolve")
	}
	if !isJSONBUpdateColumn(typ, "payload") || isJSONBUpdateColumn(typ, "display_name") || isJSONBUpdateColumn(typ, "missing") {
		t.Error("jsonb detection wrong")
	}
}

func BenchmarkLookupColumnName(b *testing.B) {
	typ := reflect.TypeOf(typeIndexDoc{})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		lookupColumnName(typ, "no_tags")
	}
}
