package models

// File overview:
// - Responsibility: verifies kernel model metadata, storage helpers, and registry-backed behavior for sys model log.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down metadata and persistence contracts so schema and runtime helpers stay compatible with the rest of the kernel.

import (
	"context"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestMetadataChangedFieldsIgnoresAuditColumns(t *testing.T) {
	before := orm.RecordMap{
		"id":        int64(1),
		"updatedAt": "2026-05-01T10:00:00Z",
		"label":     "Before",
	}
	after := orm.RecordMap{
		"id":        int64(1),
		"updatedAt": "2026-05-01T10:05:00Z",
		"label":     "After",
	}

	changed := metadataChangedFields(before, after)
	if len(changed) != 1 || changed[0] != "label" {
		t.Fatalf("metadataChangedFields() = %#v, want [label]", changed)
	}
}

func TestBuildMetadataChangeLogEntryUsesFieldMetadataIdentity(t *testing.T) {
	entry := buildMetadataChangeLogEntry(context.Background(), SysModelFieldModelName, "write", "runtime_crud", orm.RecordMap{
		"id":         int64(7),
		"model_name": "tbl.partner",
		"field_name": "email",
		"label":      "Email",
	}, orm.RecordMap{
		"id":         int64(7),
		"model_name": "tbl.partner",
		"field_name": "email",
		"label":      "Primary Email",
	})

	if entry.ChangeType != "field" {
		t.Fatalf("ChangeType = %q, want field", entry.ChangeType)
	}
	if entry.TargetModel != SysModelFieldModelName {
		t.Fatalf("TargetModel = %q, want %q", entry.TargetModel, SysModelFieldModelName)
	}
	if entry.TargetRecord != "7" {
		t.Fatalf("TargetRecord = %q, want 7", entry.TargetRecord)
	}
	if entry.SubjectModel != "tbl.partner" {
		t.Fatalf("SubjectModel = %q, want tbl.partner", entry.SubjectModel)
	}
	if entry.SubjectName != "email" {
		t.Fatalf("SubjectName = %q, want email", entry.SubjectName)
	}
	if len(entry.ChangedFields) != 1 || entry.ChangedFields[0] != "label" {
		t.Fatalf("ChangedFields = %#v, want [label]", entry.ChangedFields)
	}
}
