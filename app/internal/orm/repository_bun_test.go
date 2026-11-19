package orm

// File overview:
// - Responsibility: verifies ORM runtime semantics, repository behavior, and registry coordination for repository bun.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down runtime invariants so refactors preserve transaction, RBAC, registry, and storage behavior.

// These tests validate the Bun repository contract so generic CRUD semantics
// stay correct as the filter and mapping layers evolve.

import (
	"testing"
	"time"

	"github.com/uptrace/bun"
)

type uiAliasFixtureRecord struct {
	BaseModel
}

type UIViewRecord struct {
	BaseModel
}

type UIActionWindowRecord struct {
	BaseModel
}

type explicitAliasFixtureRecord struct {
	bun.BaseModel `bun:"table:explicit_alias_fixture,alias:explicit_alias"`
	ID            int64 `bun:"id,pk,autoincrement"`
}

func TestResolveFilterColumnNameSupportsOptAlias(t *testing.T) {
	if got := resolveFilterColumnName(goTypeOf[optTaggedFilterDocument](), "bic"); got != "bank_bic" {
		t.Fatalf("expected bic filter to resolve to bank_bic, got %q", got)
	}
}

func TestResolveFilterColumnNameKeepsLegacyLookups(t *testing.T) {
	if got := resolveFilterColumnName(goTypeOf[optTaggedFilterDocument](), "BankBIC"); got != "bank_bic" {
		t.Fatalf("expected Go field name lookup to resolve to bank_bic, got %q", got)
	}
	if got := resolveFilterColumnName(goTypeOf[optTaggedFilterDocument](), "bank_bic"); got != "bank_bic" {
		t.Fatalf("expected column/json lookup to resolve to bank_bic, got %q", got)
	}
}

func TestResolveFilterColumnNameIgnoresDisabledOptAlias(t *testing.T) {
	if got := resolveFilterColumnName(goTypeOf[optTaggedFilterDocument](), "hidden_value"); got != "hidden_value" {
		t.Fatalf("expected direct column lookup to remain available, got %q", got)
	}
	if _, ok := lookupFieldMetadataByType(goTypeOf[optTaggedFilterDocument](), "hidden_value"); ok {
		t.Fatalf("expected filter:false field to be excluded from opt alias lookup")
	}
}

func TestModelTableAliasPreservesAcronyms(t *testing.T) {
	if got := modelTableAlias(goTypeOf[UIViewRecord]()); got != "ui_view_record" {
		t.Fatalf("expected UIViewRecord alias to be ui_view_record, got %q", got)
	}
	if got := modelTableAlias(goTypeOf[UIActionWindowRecord]()); got != "ui_action_window_record" {
		t.Fatalf("expected UIActionWindowRecord alias to be ui_action_window_record, got %q", got)
	}
}

func TestModelTableAliasUsesExplicitBunAlias(t *testing.T) {
	if got := modelTableAlias(goTypeOf[explicitAliasFixtureRecord]()); got != "explicit_alias" {
		t.Fatalf("expected explicit bun alias to win, got %q", got)
	}
}

func TestRuntimeFieldBackedBySQLColumnExcludesVirtualAndX2ManyFields(t *testing.T) {
	if runtimeFieldBackedBySQLColumn(FieldMetadata{Name: "statusbar_visible", Kind: FieldKindText, Stored: false}) {
		t.Fatalf("expected non-stored compute field to stay out of SQL projection")
	}
	if runtimeFieldBackedBySQLColumn(FieldMetadata{Name: "child_ids", Kind: FieldKindOneToMany, Stored: true}) {
		t.Fatalf("expected one2many field to stay out of SQL projection")
	}
	if !runtimeFieldBackedBySQLColumn(FieldMetadata{Name: "parent_id", Kind: FieldKindManyToOne, Stored: true}) {
		t.Fatalf("expected stored many2one field to remain in SQL projection")
	}
}

func TestSanitizeUpdateMapCanonicalizesTimestampAliases(t *testing.T) {
	update := sanitizeUpdateMap(goTypeOf[optTaggedFilterDocument](), RecordMap{
		"id":         11,
		"created_at": "2026-04-30T00:00:00Z",
		"updated_at": "2026-04-30T00:00:00Z",
		"updatedAt":  "2026-04-30T01:00:00Z",
		"BankBIC":    "BIDVVNVX",
		"bank_bic":   "VCBVVNVX",
	})

	if _, exists := update["id"]; exists {
		t.Fatalf("expected id to be removed from sanitized update")
	}
	if _, exists := update["created_at"]; exists {
		t.Fatalf("expected created_at to be removed from sanitized update")
	}
	if got, ok := update["bank_bic"]; !ok || got != "VCBVVNVX" {
		t.Fatalf("expected canonical bank_bic value to win, got %#v", got)
	}
	if _, exists := update["updatedAt"]; exists {
		t.Fatalf("expected updatedAt alias to be removed after normalization")
	}
	if got, ok := update["updated_at"]; !ok {
		t.Fatalf("expected repository-managed updated_at to be injected")
	} else if _, ok := got.(time.Time); !ok {
		t.Fatalf("expected updated_at to be a time.Time, got %T", got)
	}
}
