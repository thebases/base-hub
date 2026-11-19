package models

import (
	"context"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func imexTestFields() map[string]orm.FieldMetadata {
	return map[string]orm.FieldMetadata{
		"name":       {Name: "name", Kind: orm.FieldKindText, Label: "Name"},
		"active":     {Name: "active", Kind: orm.FieldKindBoolean, Label: "Active"},
		"qty":        {Name: "qty", Kind: orm.FieldKindInteger, Label: "Qty"},
		"state":      {Name: "state", Kind: orm.FieldKindSelection, Label: "State", Selection: []orm.SelectionOption{{Value: "draft", Label: "Draft"}, {Value: "done", Label: "Done"}}},
		"partner_id": {Name: "partner_id", Kind: orm.FieldKindManyToOne, Relation: "res.partner", Label: "Partner"},
		"tag_ids":    {Name: "tag_ids", Kind: orm.FieldKindManyToMany, Label: "Tags"},
		"created_by": {Name: "created_by", Kind: orm.FieldKindText, ReadOnly: true, Label: "Created By"},
	}
}

func TestImexRowValuesCoercesCellsAndSplitsID(t *testing.T) {
	id, values, err := imexRowValues(context.Background(), imexTestFields(), map[string]any{
		"id": "42", "name": " Widget ", "active": "YES", "qty": "7", "state": "Done", "partner_id": "9", "tag_ids": "",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "42" {
		t.Fatalf("id = %q, want 42", id)
	}
	want := orm.RecordMap{"name": "Widget", "active": true, "qty": int64(7), "state": "done", "partner_id": int64(9)}
	for key, expected := range want {
		if values[key] != expected {
			t.Errorf("%s = %#v, want %#v", key, values[key], expected)
		}
	}
	// Blank cells must never clear a stored value on update.
	if _, ok := values["tag_ids"]; ok {
		t.Errorf("blank tag_ids cell should be skipped")
	}
}

func TestImexRowValuesRejectsBadCells(t *testing.T) {
	cases := map[string]map[string]any{
		"unknown field":   {"nope": "x"},
		"readonly field":  {"created_by": "x"},
		"bad bool":        {"active": "maybe"},
		"bad int":         {"qty": "1.5"},
		"bad selection":   {"state": "archived"},
		"relation list":   {"tag_ids": "1,2"},
		"bad id":          {"id": "abc", "name": "x"},
		"non-positive id": {"id": "0", "name": "x"},
	}
	for name, cells := range cases {
		if _, _, err := imexRowValues(context.Background(), imexTestFields(), cells); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestImexRowValuesIDOnlyRowHasNothingToWrite(t *testing.T) {
	_, values, err := imexRowValues(context.Background(), imexTestFields(), map[string]any{"id": "3", "name": "  "})
	if err != nil || values != nil {
		t.Fatalf("values=%v err=%v, want nil/nil", values, err)
	}
}
