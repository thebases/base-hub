package orm

// File overview:
// - Responsibility: verifies ORM runtime semantics, repository behavior, and registry coordination for model registry store.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down runtime invariants so refactors preserve transaction, RBAC, registry, and storage behavior.

// These tests cover the persistence helpers that store and restore registry
// metadata as durable state rather than transient process memory.

import (
	"testing"
	"time"
)

func TestNewSystemModelRow(t *testing.T) {
	now := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	meta := ModelMetadata{
		Name:           "bank.account",
		Label:          "Bank Account",
		Description:    "Partner bank account and proxy metadata",
		StorageBackend: StorageBackendBun,
		StorageName:    "bank_accounts",
		GoType:         "git.thebase.vn/base_hub/internal/models.BankDocument",
		Fields: []FieldMetadata{
			{Name: "account_number", Label: "Account Number", Help: "Primary account identifier"},
		},
		RegisteredAt: now.Add(-time.Minute),
	}

	row := newSystemModelRow(meta, now)
	if row.Name != meta.Name {
		t.Fatalf("expected model name %q, got %q", meta.Name, row.Name)
	}
	if row.StorageBackend != string(StorageBackendBun) {
		t.Fatalf("expected bun storage backend, got %q", row.StorageBackend)
	}
	if row.HelperData == "" {
		t.Fatalf("expected helper data to be generated")
	}
	if row.JsonDTO == nil {
		t.Fatalf("expected json dto to be generated")
	}
	if !row.SyncedAt.Equal(now) {
		t.Fatalf("expected synced time %v, got %v", now, row.SyncedAt)
	}
}

func TestNewSystemModelFieldRows(t *testing.T) {
	now := time.Date(2026, 4, 30, 10, 0, 0, 0, time.UTC)
	meta := ModelMetadata{
		Name: "webhook.schema",
		Fields: []FieldMetadata{
			{
				Name:       "inputpath",
				GoName:     "InputPath",
				Type:       "string",
				BunColumn:  "inputpath",
				Required:   true,
				PrimaryKey: false,
			},
			{
				Name:       "id",
				GoName:     "ID",
				Type:       "string",
				BunColumn:  "id",
				PrimaryKey: true,
			},
		},
	}

	rows := newSystemModelFieldRows(meta, now)
	if len(rows) != 2 {
		t.Fatalf("expected 2 field rows, got %d", len(rows))
	}
	if rows[0].ModelName != "webhook.schema" {
		t.Fatalf("expected model name webhook.schema, got %q", rows[0].ModelName)
	}
	if rows[0].FieldIndex != 0 || rows[1].FieldIndex != 1 {
		t.Fatalf("expected stable field ordering, got %d and %d", rows[0].FieldIndex, rows[1].FieldIndex)
	}
	if !rows[1].IsPrimaryKey {
		t.Fatalf("expected second field to preserve primary key flag")
	}
	if !rows[0].UpdatedAt.Equal(now) {
		t.Fatalf("expected synced timestamp propagation")
	}
}

func TestChangedSystemModelFieldColumns(t *testing.T) {
	before := systemModelFieldRow{
		ModelName:    "tbl.partner",
		FieldName:    "email",
		GoName:       "Email",
		Label:        "Email",
		DataType:     "string",
		BunColumn:    "email",
		IsRequired:   false,
		FieldIndex:   3,
		IsPrimaryKey: false,
	}
	after := before
	after.Label = "Primary Email"
	after.IsRequired = true

	changed := changedSystemModelFieldColumns(before, after)
	if len(changed) != 2 {
		t.Fatalf("expected 2 changed columns, got %#v", changed)
	}
	if changed[0] != "label" || changed[1] != "is_required" {
		t.Fatalf("unexpected changed columns: %#v", changed)
	}
}
