package utils

import "testing"

func TestBuildQueryStringFromJSONSortsKeysWithoutMutatingInput(t *testing.T) {
	keys := []string{"z", "a"}

	got, err := BuildQueryStringFromJSON(map[string]interface{}{
		"a": 1,
		"z": 2,
	}, keys)
	if err != nil {
		t.Fatalf("BuildQueryStringFromJSON() error = %v", err)
	}
	if got != "a=1&z=2" {
		t.Fatalf("BuildQueryStringFromJSON() = %q, want %q", got, "a=1&z=2")
	}
	if keys[0] != "z" || keys[1] != "a" {
		t.Fatalf("BuildQueryStringFromJSON() mutated input keys: %v", keys)
	}
}
