package models

import "testing"

func TestBaseKvtSpec(t *testing.T) {
	if BaseKvtModelName != "base.kvt" {
		t.Fatalf("model name = %q", BaseKvtModelName)
	}
	var found bool
	for _, m := range BaseKvtModelSpec.RuntimeMethods {
		if m.Name == "get_public" {
			found = m.AliasAnonymous && m.AliasRoute == "GET /kvt/:key" && m.ModelOnly && m.ReadOnly
		}
	}
	if !found {
		t.Fatal("get_public must be an anonymous read-only GET /kvt/:key alias")
	}
}
