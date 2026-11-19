package models

import (
	"context"
	"testing"
)

func TestCleanFormFields(t *testing.T) {
	got, ok := cleanFormFields(map[string]any{"name": " Ann ", "agree": true, "n": float64(3), "empty": nil})
	if !ok || got["name"] != "Ann" || got["agree"] != "on" || got["n"] != "3" || got["empty"] != "" {
		t.Fatalf("unexpected %v %v", got, ok)
	}
	bad := []map[string]any{
		{},
		{"bad key": "x"},
		{"a": map[string]any{"x": 1}},
		{"a": []any{"x"}},
		{"a": string(make([]rune, formSubmissionMaxValueLen+1))},
	}
	for i, b := range bad {
		if _, ok := cleanFormFields(b); ok {
			t.Errorf("case %d should be rejected", i)
		}
	}
	many := map[string]any{}
	for i := 0; i <= formSubmissionMaxFields; i++ {
		many["f"+string(rune('a'+i%26))+string(rune('a'+i/26))] = "x"
	}
	if _, ok := cleanFormFields(many); ok {
		t.Error("too many fields must be rejected")
	}
}

func TestRegisterWebsiteFormTarget(t *testing.T) {
	noop := func(_ context.Context, _ int64, _ map[string]string) error { return nil }
	if err := RegisterWebsiteFormTarget("Bad Key", noop); err == nil {
		t.Error("invalid key accepted")
	}
	if err := RegisterWebsiteFormTarget("unit-test-target", noop); err != nil {
		t.Fatal(err)
	}
	if err := RegisterWebsiteFormTarget("unit-test-target", noop); err != nil {
		t.Errorf("re-registering must replace, got %v", err)
	}
}
