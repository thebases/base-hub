package orm

import "testing"

func TestAliasOnlyOverrideKeepsDescriptionFile(t *testing.T) {
	_, meta, err := mergeRuntimeMethods([]RuntimeMethod{{
		Name: "read", Description: "txt", DescriptionFile: "x/read.md",
		Aliases: []MethodAlias{{Route: "GET /base/x/:id"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range meta {
		if m.Name == "read" && (m.DescriptionFile != "x/read.md" || m.Description != "txt") {
			t.Fatalf("read lost docs: %+v", m)
		}
	}
}
