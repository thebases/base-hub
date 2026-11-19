package models

// File overview:
// - Responsibility: verifies the res.users language field keeps the requested many2one API contract.
// - Key symbols: focused Test cases plus schema assertions.
// - Pattern: Locks down business-model rules so base entities keep the same schema, defaults, and runtime semantics.

import (
	"context"
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

func TestResUserLangFieldMapsToResLangManyToOne(t *testing.T) {
	field, err := ResUserDefinition.Field("lang_id")
	if err != nil {
		t.Fatalf("expected lang_id field definition: %v", err)
	}
	if field.Kind != orm.FieldKindManyToOne {
		t.Fatalf("expected lang_id field kind %q, got %q", orm.FieldKindManyToOne, field.Kind)
	}
	if field.Relation != ResLangModelName {
		t.Fatalf("expected lang_id relation %q, got %q", ResLangModelName, field.Relation)
	}
}

func TestNormalizeResUsersLangIgnoresLegacyLangAlias(t *testing.T) {
	values := map[string]any{"lang": "en_US"}
	if err := normalizeResUsersLang(context.Background(), values); err != nil {
		t.Fatalf("normalizeResUsersLang returned error: %v", err)
	}
	if _, ok := values["lang_id"]; ok {
		t.Fatalf("expected removed lang alias to stay unmapped, got %#v", values["lang_id"])
	}
}

func TestSplitResUsersReadFieldsIgnoresLegacyLangAlias(t *testing.T) {
	userFields, partnerFields, err := splitResUsersReadFields(ResUserDefinition, ResPartnerDefinition, []string{"lang", "lang_id", "mobile"})
	if err != nil {
		t.Fatalf("splitResUsersReadFields returned error: %v", err)
	}
	if len(partnerFields) != 1 || partnerFields[0] != "mobile" {
		t.Fatalf("unexpected partner fields: %#v", partnerFields)
	}
	if len(userFields) != 1 || userFields[0] != "lang_id" {
		t.Fatalf("unexpected user fields: %#v", userFields)
	}
}

func TestMapResUsersLangCodesToRelationIDsUsesResLangStorageTable(t *testing.T) {
	db := bun.NewDB(nil, pgdialect.New())
	ctx := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironment(db, 0, nil, false))

	langRows := []ResLangRecord{}
	sql := resLangSelectQuery(ctx, &langRows).Column("id", "code").Where("code IN (?)", bun.In([]any{"en_US"})).String()

	if !strings.Contains(sql, `FROM "tbl_lang" AS "res_lang_record"`) {
		t.Fatalf("expected res.users lang lookup to read from tbl_lang storage table, sql=%q", sql)
	}
	if strings.Contains(sql, `tbl_lang_record`) {
		t.Fatalf("expected res.users lang lookup to avoid a stale tbl_lang_record alias, sql=%q", sql)
	}
	if strings.Contains(sql, `tbl_lang_records`) {
		t.Fatalf("expected res.users lang lookup to avoid inferred tbl_lang_records table, sql=%q", sql)
	}
}
