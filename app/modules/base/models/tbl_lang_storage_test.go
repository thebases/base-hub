package models

// File overview:
// - Responsibility: verifies base-module business model behavior and runtime metadata for res lang storage.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down business-model rules so base entities keep the same schema, defaults, and runtime semantics.

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

func TestResLangModelSpecResolvesStorageName(t *testing.T) {
	if got := ResLangModelSpec.ResolveStorageName(); got != "tbl_lang" {
		t.Fatalf("expected tbl.lang storage name to resolve to tbl_lang, got %q", got)
	}
}

func TestResLangRecordBunTableName(t *testing.T) {
	field, ok := reflect.TypeOf(ResLangRecord{}).FieldByName("BaseModel")
	if !ok {
		t.Fatal("expected ResLangRecord to embed orm.BaseModel")
	}
	if field.Type != reflect.TypeOf(orm.BaseModel{}) {
		t.Fatalf("expected ResLangRecord BaseModel type %v, got %v", reflect.TypeOf(orm.BaseModel{}), field.Type)
	}
}

func TestResLangSelectQueryUsesRegisteredStorageTable(t *testing.T) {
	db := bun.NewDB(nil, pgdialect.New())
	ctx := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironment(db, 0, nil, false))

	rows := []ResLangRecord{}
	sql := resLangSelectQuery(ctx, &rows).Column("id").Where("code = ?", "en_US").String()

	if !strings.Contains(sql, `FROM "tbl_lang" AS "res_lang_record"`) {
		t.Fatalf("expected tbl.lang helper query to bind tbl_lang storage table, sql=%q", sql)
	}
	if strings.Contains(sql, `tbl_lang_record`) {
		t.Fatalf("expected tbl.lang helper query to avoid a stale tbl_lang_record alias, sql=%q", sql)
	}
	if strings.Contains(sql, `tbl_lang_records`) {
		t.Fatalf("expected tbl.lang helper query to avoid inferred tbl_lang_records table, sql=%q", sql)
	}
}
