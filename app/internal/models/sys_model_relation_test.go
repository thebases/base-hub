package models

// File overview:
// - Responsibility: verifies kernel model metadata, storage helpers, and registry-backed behavior for sys model relation.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down metadata and persistence contracts so schema and runtime helpers stay compatible with the rest of the kernel.

import (
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestBuildSysModelRelationRowsCollectsManyToManyMetadata(t *testing.T) {
	rows := buildSysModelRelationRows([]orm.ModelMetadata{
		{
			Name: "crm.lead",
			Fields: []orm.FieldMetadata{
				{
					Name:           "tag_ids",
					Kind:           orm.FieldKindManyToMany,
					Relation:       "crm.tag",
					RelationTable:  "crm_lead_crm_tag_rel",
					RelationColumn: "crm_lead_id",
					RelationKey:    "crm_tag_id",
				},
				{
					Name:      "name",
					Kind:      orm.FieldKindText,
					BunColumn: "name",
				},
			},
		},
	})

	if len(rows) != 1 {
		t.Fatalf("row count = %d, want 1", len(rows))
	}
	if rows[0].Name != "crm.lead.tag_ids" {
		t.Fatalf("name = %q, want crm.lead.tag_ids", rows[0].Name)
	}
	if rows[0].RelationTable != "crm_lead_crm_tag_rel" {
		t.Fatalf("relation table = %q, want crm_lead_crm_tag_rel", rows[0].RelationTable)
	}
	if rows[0].Module != "crm" {
		t.Fatalf("module = %q, want crm", rows[0].Module)
	}
}
