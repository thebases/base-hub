package orm

import (
	"reflect"
	"testing"
)

type sharedLookupDoc struct {
	ID   int64  `json:"id" bun:"id,pk"`
	Name string `json:"name" bun:"name"`
}

func TestLookupSharedSurvivesReRegistration(t *testing.T) {
	reg := NewModelRegistry()
	typ := reflect.TypeOf(sharedLookupDoc{})
	if _, err := reg.Register(typ, ModelSpec{Name: "tbl.shared_lookup", Label: "One"}); err != nil {
		t.Fatal(err)
	}
	before, ok := reg.LookupShared("tbl.shared_lookup")
	if !ok {
		t.Fatal("missing")
	}
	if _, err := reg.Register(typ, ModelSpec{Name: "tbl.shared_lookup", Label: "Two"}); err != nil {
		t.Fatal(err)
	}
	if before.Label != "One" {
		t.Fatalf("published entry was mutated in place: %q", before.Label)
	}
	after, _ := reg.LookupShared("tbl.shared_lookup")
	if after.Label != "Two" {
		t.Fatalf("refresh not visible: %q", after.Label)
	}
	cloned, _ := reg.Lookup("tbl.shared_lookup")
	cloned.Label = "mutated"
	if again, _ := reg.LookupShared("tbl.shared_lookup"); again.Label != "Two" {
		t.Fatal("Lookup must still return an isolated clone")
	}
}

func BenchmarkLookupClone(b *testing.B) {
	reg := NewModelRegistry()
	_, _ = reg.Register(reflect.TypeOf(sharedLookupDoc{}), ModelSpec{Name: "tbl.shared_lookup"})
	b.Run("clone", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			reg.Lookup("tbl.shared_lookup")
		}
	})
	b.Run("shared", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			reg.LookupShared("tbl.shared_lookup")
		}
	})
}
