package models

// File overview:
// - Responsibility: verifies kernel model metadata, storage helpers, and registry-backed behavior for one2many storage.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down metadata and persistence contracts so schema and runtime helpers stay compatible with the rest of the kernel.

import (
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestOneToManyFieldsAreExplicitlyNonStored(t *testing.T) {
	cases := []struct {
		modelName string
		spec      orm.ModelSpec
		fieldName string
	}{
		{modelName: ResGroupPrivilegeModelName, spec: ResGroupPrivilegeModelSpec, fieldName: "group_ids"},
		{modelName: ResCountryModelName, spec: ResCountryModelSpec, fieldName: "state_ids"},
		{modelName: UIMenuModelName, spec: UIMenuModelSpec, fieldName: "child_ids"},
		{modelName: UIViewModelName, spec: UIViewModelSpec, fieldName: "inherit_children_ids"},
	}

	for _, tc := range cases {
		t.Run(tc.modelName+"."+tc.fieldName, func(t *testing.T) {
			field, err := tc.spec.Definition.Field(tc.fieldName)
			if err != nil {
				t.Fatalf("expected field %s: %v", tc.fieldName, err)
			}
			if field.Stored {
				t.Fatalf("expected %s.%s to be non-stored", tc.modelName, tc.fieldName)
			}
		})
	}
}
