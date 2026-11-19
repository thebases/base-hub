package orm

// File overview:
// - Responsibility: verifies the translated stored-field runtime (SRS-F-021) —
//   metadata validation, SQL expression building, envelope resolution, the
//   jsonb migration wrap, and the language-scoped write/read/search contract.
// - Pattern: unit tests run without a database; the integration test reuses the
//   runtime-graph PostgreSQL fixture conventions.

import (
	"context"
	"strings"
	"testing"
)

func TestSanitizedLangCodeFallsBackToEn(t *testing.T) {
	cases := map[string]string{
		"":                          "en",
		"vi_VN":                     "vi_vn",
		"vi":                        "vi_vn",
		"pt-BR":                     "pt_br",
		"en":                        "en",
		"en_US":                     "en",
		"en-us":                     "en",
		"en_GB":                     "en_gb",
		"x'; DROP":                  "en",
		"averyveryverylonglangcode": "en",
	}
	for input, want := range cases {
		if got := sanitizedLangCode(input); got != want {
			t.Fatalf("sanitizedLangCode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestTranslatedColumnSQL(t *testing.T) {
	if got := translatedColumnSQL(`"m"`, "name", "en"); got != `("m"."name"->>'en')` {
		t.Fatalf("unexpected en expression %q", got)
	}
	if got := translatedColumnSQL(`"m"`, "name", "vi_VN"); got != `COALESCE("m"."name"->>'vi_vn', "m"."name"->>'en')` {
		t.Fatalf("unexpected vi expression %q", got)
	}
	// Malicious lang codes must collapse to the base language, never reach SQL.
	if got := translatedColumnSQL(`"m"`, "name", "vi'; DROP TABLE x;--"); got != `("m"."name"->>'en')` {
		t.Fatalf("unexpected sanitized expression %q", got)
	}
}

func TestResolveTranslatedRecordValuesCollapsesEnvelopes(t *testing.T) {
	meta := &ModelMetadata{
		Name: "test.translate.meta",
		Fields: []FieldMetadata{
			{Name: "name", BunColumn: "name", Kind: FieldKindText, Stored: true, Translate: true},
			{Name: "code", BunColumn: "code", Kind: FieldKindText, Stored: true},
		},
	}
	records := []RecordMap{
		{"name": map[string]any{"en": "Hello", "vi_VN": "Xin chào"}, "code": `{"raw": "untouched"}`},
		{"name": `{"en": "Plain"}`},
		{"name": "already-resolved"},
	}
	resolveTranslatedRecordValues(meta, records, "vi_VN")
	if records[0]["name"] != "Xin chào" {
		t.Fatalf("expected vi resolution, got %#v", records[0]["name"])
	}
	if records[0]["code"] != `{"raw": "untouched"}` {
		t.Fatalf("non-translated field must not be rewritten, got %#v", records[0]["code"])
	}
	if records[1]["name"] != "Plain" {
		t.Fatalf("expected en fallback from JSON-text envelope, got %#v", records[1]["name"])
	}
	if records[2]["name"] != "already-resolved" {
		t.Fatalf("plain strings must pass through, got %#v", records[2]["name"])
	}
}

func TestTranslateMetaTagValidation(t *testing.T) {
	type badKind struct {
		ID    int64 `bun:"id,pk,autoincrement" json:"id"`
		Count int64 `bun:"count" json:"count" meta:"translate"`
	}
	if _, err := ModelDefinitionFromRecord("test.translate.badkind", (*badKind)(nil)); err == nil {
		t.Fatalf("expected translate on integer field to fail registration")
	}

	type badCompute struct {
		ID   int64  `bun:"id,pk,autoincrement" json:"id"`
		Name string `bun:"name" json:"name" meta:"translate,compute:compute_name"`
	}
	if _, err := ModelDefinitionFromRecord("test.translate.badcompute", (*badCompute)(nil)); err == nil {
		t.Fatalf("expected translate on computed field to fail registration")
	}

	type good struct {
		ID   int64  `bun:"id,pk,autoincrement" json:"id"`
		Name string `bun:"name" json:"name" meta:"translate,label:Name"`
	}
	definition, err := ModelDefinitionFromRecord("test.translate.good", (*good)(nil))
	if err != nil {
		t.Fatalf("expected translate on text field to register: %v", err)
	}
	field, err := definition.Field("name")
	if err != nil || !field.Translate {
		t.Fatalf("expected name field to carry Translate=true, got %#v (%v)", field, err)
	}
}

func TestTranslatedIndexNameStaysWithinIdentifierLimit(t *testing.T) {
	name := translatedIndexName(strings.Repeat("verylongtable", 4), "translated_column_name", "vi_VN")
	if len(name) > 63 {
		t.Fatalf("index name %q exceeds the PostgreSQL identifier limit", name)
	}
	short := translatedIndexName("ui_menu", "name", "vi_VN")
	if short != "idx_ui_menu_name_tr_vi_VN" {
		t.Fatalf("unexpected short index name %q", short)
	}
}

type runtimeTranslateDoc struct {
	BaseModel
	Name string `bun:"name" json:"name" meta:"translate,index,label:Name"`
	Code string `bun:"code" json:"code"`
}

const runtimeTranslateModel = "test.runtime.translate.doc"

// translateTestEnv builds a sudo environment bound to the fixture DB with the
// requested context language, mirroring how the RPC layer injects lang.
func translateTestEnv(db *BunDB, lang string) context.Context {
	env := NewEnvironment(db.DB, 1, map[string]any{"lang": lang}, true)
	return ContextWithEnvironment(context.Background(), env)
}

func TestTranslatedFieldLifecycleAgainstPostgres(t *testing.T) {
	bunDB, err := NewBunDB(runtimeGraphEnvPostgresConfig())
	if err != nil {
		t.Skipf("postgres fixture unavailable: %v", err)
	}
	t.Cleanup(func() { _ = bunDB.Close() })
	db := bunDB.DB

	previous := GetBunDB()
	SetBunDB(bunDB)
	t.Cleanup(func() {
		if previous == nil {
			SetBunDB(nil)
			return
		}
		SetBunDB(&BunDB{DB: previous})
	})

	NewHandlerWithModel[runtimeTranslateDoc](ModelSpec{
		Name:       runtimeTranslateModel,
		Label:      "Runtime Translate Doc",
		Definition: ModelDefinition(runtimeTranslateModel, (*runtimeTranslateDoc)(nil)),
	}.WithStorageName("runtime_translate_doc")).InitDefault()

	ctxBoot := context.Background()
	// Simulate a pre-translate legacy table: name is plain TEXT with data, so
	// ensureTranslatedColumns must wrap it as {"en": current} in place.
	if _, err := db.ExecContext(ctxBoot, `DROP TABLE IF EXISTS runtime_translate_doc`); err != nil {
		t.Fatalf("drop fixture table: %v", err)
	}
	if _, err := db.ExecContext(ctxBoot, `CREATE TABLE runtime_translate_doc (id BIGSERIAL PRIMARY KEY, created_at TIMESTAMPTZ, updated_at TIMESTAMPTZ, bid TEXT, active BOOLEAN, name TEXT, code TEXT)`); err != nil {
		t.Fatalf("create fixture table: %v", err)
	}
	if _, err := db.ExecContext(ctxBoot, `INSERT INTO runtime_translate_doc (name, code, active) VALUES ('Legacy Row', 'legacy', TRUE)`); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	meta, ok := LookupModel(runtimeTranslateModel)
	if !ok {
		t.Fatalf("model %s not registered", runtimeTranslateModel)
	}
	if err := ensureTranslatedColumns(ctxBoot, db, meta); err != nil {
		t.Fatalf("ensure translated columns: %v", err)
	}
	dataType, err := lookupColumnDataType(ctxBoot, db, "runtime_translate_doc", "name")
	if err != nil || dataType != "jsonb" {
		t.Fatalf("expected name column to become jsonb, got %q (%v)", dataType, err)
	}
	// Idempotency: a second migration pass must not touch the converted column.
	if err := ensureTranslatedColumns(ctxBoot, db, meta); err != nil {
		t.Fatalf("re-run translated migration: %v", err)
	}

	// Most `name` columns are NOT NULL: create must satisfy it inside the INSERT itself.
	if _, err := db.ExecContext(ctxBoot, `ALTER TABLE runtime_translate_doc ALTER COLUMN name SET NOT NULL`); err != nil {
		t.Fatalf("set name NOT NULL: %v", err)
	}

	executor, ok := lookupRuntimeExecutor(runtimeTranslateModel)
	if !ok {
		t.Fatalf("executor for %s not registered", runtimeTranslateModel)
	}

	// The legacy row reads back as its wrapped en value under every language.
	legacyRows, err := executor.RuntimeSearchRead(translateTestEnv(bunDB, "vi_VN"), RecordMap{"code": "legacy"}, []string{"id", "name"})
	if err != nil || len(legacyRows) != 1 {
		t.Fatalf("read legacy row: %v %#v", err, legacyRows)
	}
	if legacyRows[0]["name"] != "Legacy Row" {
		t.Fatalf("expected en fallback for legacy row, got %#v", legacyRows[0]["name"])
	}

	// Create under vi_VN seeds both the en base and the vi key.
	created, err := executor.RuntimeCreate(translateTestEnv(bunDB, "vi_VN"), RecordMap{"name": "Tài liệu", "code": "doc-a"})
	if err != nil {
		t.Fatalf("create under vi_VN: %v", err)
	}
	createdID, _ := runtimeStringID(created["id"])
	if createdID == "" {
		t.Fatalf("create did not return an id: %#v", created)
	}
	if created["name"] != "Tài liệu" {
		t.Fatalf("create readback should resolve vi value, got %#v", created["name"])
	}

	// A vi_VN write must update only the vi key: en keeps its own value.
	if _, err := executor.RuntimeWrite(translateTestEnv(bunDB, "en"), []string{createdID}, RecordMap{"name": "Document A"}); err != nil {
		t.Fatalf("write en value: %v", err)
	}
	if _, err := executor.RuntimeWrite(translateTestEnv(bunDB, "vi_VN"), []string{createdID}, RecordMap{"name": "Tài liệu A"}); err != nil {
		t.Fatalf("write vi value: %v", err)
	}
	enRows, err := executor.RuntimeRead(translateTestEnv(bunDB, "en"), []string{createdID}, []string{"id", "name"})
	if err != nil || len(enRows) != 1 || enRows[0]["name"] != "Document A" {
		t.Fatalf("expected en read to keep en value, got %#v (%v)", enRows, err)
	}
	viRows, err := executor.RuntimeRead(translateTestEnv(bunDB, "vi_VN"), []string{createdID}, []string{"id", "name"})
	if err != nil || len(viRows) != 1 || viRows[0]["name"] != "Tài liệu A" {
		t.Fatalf("expected vi read to resolve vi value, got %#v (%v)", viRows, err)
	}
	// Unknown context languages fall back to en (soft, per SRS-F-021).
	deRows, err := executor.RuntimeRead(translateTestEnv(bunDB, "de_DE"), []string{createdID}, []string{"id", "name"})
	if err != nil || len(deRows) != 1 || deRows[0]["name"] != "Document A" {
		t.Fatalf("expected unknown lang to fall back to en, got %#v (%v)", deRows, err)
	}

	// Domain search and ordering resolve against the environment language.
	if _, err := executor.RuntimeCreate(translateTestEnv(bunDB, "en"), RecordMap{"name": "Alpha", "code": "doc-b"}); err != nil {
		t.Fatalf("create second doc: %v", err)
	}
	found, err := executor.RuntimeSearchRead(translateTestEnv(bunDB, "vi_VN"), RecordMap{
		"__domain": mustParseDomain(t, []any{[]any{"name", "=", "Tài liệu A"}}),
	}, []string{"id", "name"})
	if err != nil || len(found) != 1 {
		t.Fatalf("expected vi domain match, got %#v (%v)", found, err)
	}
	ordered, err := executor.RuntimeSearchRead(translateTestEnv(bunDB, "en"), RecordMap{"__order": "name asc", "code": []any{"doc-a", "doc-b"}}, []string{"id", "name"})
	if err != nil || len(ordered) != 2 {
		t.Fatalf("ordered search failed: %#v (%v)", ordered, err)
	}
	if ordered[0]["name"] != "Alpha" || ordered[1]["name"] != "Document A" {
		t.Fatalf("expected en-resolved ordering [Alpha, Document A], got %#v", ordered)
	}

	// update_field_translations edits multiple languages at once and never
	// clobbers untouched keys.
	handler, ok := executor.(interface {
		RuntimeApplyFieldTranslations(ctx context.Context, ids []string, field FieldMetadata, translations map[string]any) error
	})
	if !ok {
		t.Fatalf("executor does not expose RuntimeApplyFieldTranslations")
	}
	nameField, _ := runtimeLookupField(meta, "name")
	if err := handler.RuntimeApplyFieldTranslations(translateTestEnv(bunDB, "en"), []string{createdID}, nameField, map[string]any{
		"vi_VN": "Tài liệu cập nhật",
	}); err != nil {
		t.Fatalf("update field translations: %v", err)
	}
	viRows, err = executor.RuntimeRead(translateTestEnv(bunDB, "vi_VN"), []string{createdID}, []string{"id", "name"})
	if err != nil || viRows[0]["name"] != "Tài liệu cập nhật" {
		t.Fatalf("expected updated vi translation, got %#v (%v)", viRows, err)
	}
	enRows, err = executor.RuntimeRead(translateTestEnv(bunDB, "en"), []string{createdID}, []string{"id", "name"})
	if err != nil || enRows[0]["name"] != "Document A" {
		t.Fatalf("expected en value untouched by vi-only edit, got %#v (%v)", enRows, err)
	}

	// lang "*" is the explicit all-languages opt-in: the read returns the whole envelope with
	// canonical (lowercase) keys, never a single resolved string.
	noLangEnv := ContextWithEnvironment(context.Background(), NewEnvironment(db, 1, map[string]any{"lang": "*"}, true))
	allRows, err := executor.RuntimeRead(noLangEnv, []string{createdID}, []string{"id", "name"})
	if err != nil || len(allRows) != 1 {
		t.Fatalf("all-languages read failed: %#v (%v)", allRows, err)
	}
	allNames, ok := allRows[0]["name"].(map[string]any)
	if !ok || allNames["en"] != "Document A" || allNames["vi_vn"] != "Tài liệu cập nhật" {
		t.Fatalf("expected {en, vi_vn} envelope, got %#v", allRows[0]["name"])
	}
	// Search in all-languages mode still filters on the en base.
	baseFound, err := executor.RuntimeSearchRead(noLangEnv, RecordMap{
		"__domain": mustParseDomain(t, []any{[]any{"name", "=", "Document A"}}),
	}, []string{"id", "name"})
	if err != nil || len(baseFound) != 1 {
		t.Fatalf("expected en-base domain match in all-languages mode, got %#v (%v)", baseFound, err)
	}

	// The SPA's `en_us` must write the en base key, not fork an `en_us` key.
	if _, err := executor.RuntimeWrite(translateTestEnv(bunDB, "en_us"), []string{createdID}, RecordMap{"name": "Document A2"}); err != nil {
		t.Fatalf("write under en_us: %v", err)
	}
	allRows, err = executor.RuntimeRead(noLangEnv, []string{createdID}, []string{"id", "name"})
	if err != nil {
		t.Fatalf("re-read envelope: %v", err)
	}
	allNames, _ = allRows[0]["name"].(map[string]any)
	if allNames["en"] != "Document A2" || len(allNames) != 2 {
		t.Fatalf("en_us write must update en without a new key, got %#v", allNames)
	}

	// An envelope value writes every listed key and is what a read returns, so
	// reads round-trip. Create requires the en base key.
	if _, err := executor.RuntimeWrite(noLangEnv, []string{createdID}, RecordMap{"name": map[string]any{"fr_FR": "Document A (fr)"}}); err != nil {
		t.Fatalf("envelope write: %v", err)
	}
	frRows, err := executor.RuntimeRead(translateTestEnv(bunDB, "fr_fr"), []string{createdID}, []string{"id", "name"})
	if err != nil || frRows[0]["name"] != "Document A (fr)" {
		t.Fatalf("expected envelope-written fr value, got %#v (%v)", frRows, err)
	}
	if _, err := executor.RuntimeCreate(noLangEnv, RecordMap{"name": map[string]any{"vi_vn": "Chỉ tiếng Việt"}, "code": "doc-c"}); err == nil {
		t.Fatalf("create envelope without en base must fail")
	}
	createdEnv, err := executor.RuntimeCreate(noLangEnv, RecordMap{"name": map[string]any{"en": "Doc C", "vi_vn": "Tài liệu C"}, "code": "doc-c"})
	if err != nil {
		t.Fatalf("create with envelope: %v", err)
	}
	if got, _ := createdEnv["name"].(map[string]any); got["en"] != "Doc C" || got["vi_vn"] != "Tài liệu C" {
		t.Fatalf("create readback should echo the envelope, got %#v", createdEnv["name"])
	}
}

func mustParseDomain(t *testing.T, tokens []any) DomainNode {
	t.Helper()
	domain, err := ParseDomain(tokens)
	if err != nil {
		t.Fatalf("parse domain: %v", err)
	}
	return domain
}

func TestTranslatedProjectionSQL(t *testing.T) {
	if got := translatedProjectionSQL(`"m"`, "name", ""); got != `("m"."name")::text` {
		t.Fatalf("all-languages projection must emit the envelope as text, got %q", got)
	}
	if got := translatedProjectionSQL("", "name", ""); got != `("name")::text` {
		t.Fatalf("unaliased all-languages projection, got %q", got)
	}
	if got := translatedProjectionSQL(`"m"`, "name", "vi_vn"); got != translatedColumnSQL(`"m"`, "name", "vi_vn") {
		t.Fatalf("concrete lang must keep the resolved projection, got %q", got)
	}
	// Filters/ordering with no lang compare the base language.
	if got := translatedColumnSQL(`"m"`, "name", ""); got != `("m"."name"->>'en')` {
		t.Fatalf("empty lang must filter on the en base, got %q", got)
	}
}

func TestResolveTranslatedRecordValuesAllLanguages(t *testing.T) {
	meta := &ModelMetadata{
		Name:   "test.translate.all",
		Fields: []FieldMetadata{{Name: "name", BunColumn: "name", Kind: FieldKindText, Stored: true, Translate: true}},
	}
	records := []RecordMap{
		{"name": `{"en": "Hello", "vi_vn": "Xin chào"}`},
		{"name": map[string]any{"en": "Map"}},
		{"name": nil},
		{"name": "not-an-envelope"},
	}
	resolveTranslatedRecordValues(meta, records, "")
	envelope, ok := records[0]["name"].(map[string]any)
	if !ok || envelope["en"] != "Hello" || envelope["vi_vn"] != "Xin chào" {
		t.Fatalf("expected decoded envelope, got %#v", records[0]["name"])
	}
	if got, ok := records[1]["name"].(map[string]any); !ok || got["en"] != "Map" {
		t.Fatalf("map envelope must be kept, got %#v", records[1]["name"])
	}
	if records[2]["name"] != nil {
		t.Fatalf("null stays null, got %#v", records[2]["name"])
	}
	if records[3]["name"] != "not-an-envelope" {
		t.Fatalf("plain text passes through, got %#v", records[3]["name"])
	}
}

func TestTranslationFromEnvelopeMatchesLegacyKeyCase(t *testing.T) {
	envelope := map[string]any{"en": "Hello", "vi_VN": "Xin chào"}
	if got := translationFromEnvelope(envelope, "vi_vn"); got != "Xin chào" {
		t.Fatalf("legacy-cased key must resolve, got %#v", got)
	}
	if got := translationFromEnvelope(envelope, "fr_fr"); got != "Hello" {
		t.Fatalf("missing language falls back to en, got %#v", got)
	}
}

func TestCanonicalTranslationPayload(t *testing.T) {
	values, order, err := canonicalTranslationPayload(map[string]any{"vi_VN": "Xin", "en_US": "Hi", "fr-FR": nil})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(order) != 3 || order[0] != "en" || order[1] != "fr_fr" || order[2] != "vi_vn" {
		t.Fatalf("en must lead, rest sorted, got %v", order)
	}
	if values["en"] == nil || *values["en"] != "Hi" || values["fr_fr"] != nil {
		t.Fatalf("unexpected values %#v", values)
	}
	if _, _, err := canonicalTranslationPayload(map[string]any{"en_US": nil}); err == nil {
		t.Fatalf("removing the en base must be rejected")
	}
	if _, _, err := canonicalTranslationPayload(map[string]any{"x'; DROP": "v"}); err == nil {
		t.Fatalf("malformed lang code must be rejected")
	}
}

func TestRelationLabelValueCollapsesEnvelope(t *testing.T) {
	if got := relationLabelValue(map[string]any{"en": "Menu", "vi_vn": "Thực đơn"}); got != "Menu" {
		t.Fatalf("relation label must collapse to en, got %#v", got)
	}
	if got := relationLabelValue("plain"); got != "plain" {
		t.Fatalf("plain label passes through, got %#v", got)
	}
}

func TestRuntimeRequestLangAndEnsureContextLang(t *testing.T) {
	bare := ContextWithEnvironment(context.Background(), NewEnvironment(nil, 1, nil, false))
	if got := runtimeRequestLang(bare); got != "en" {
		t.Fatalf("no lang and no user language must fall back to en, got %q", got)
	}
	if got := runtimeEnvironmentLang(bare); got != "en" {
		t.Fatalf("writes default to en, got %q", got)
	}
	star := ContextWithEnvironment(context.Background(), NewEnvironment(nil, 1, map[string]any{"lang": "*"}, false))
	if got := runtimeRequestLang(EnsureContextLang(star, "")); got != "en" {
		t.Fatalf("EnsureContextLang must replace the all-languages sentinel with en, got %q", got)
	}
	vi := ContextWithEnvironment(context.Background(), NewEnvironment(nil, 1, map[string]any{"lang": "vi_VN"}, false))
	if got := runtimeRequestLang(EnsureContextLang(vi, "en")); got != "vi_vn" {
		t.Fatalf("explicit lang must be kept and canonical, got %q", got)
	}
}

func TestRuntimeContextWithEnvironmentHonorsTopLevelLang(t *testing.T) {
	base := ContextWithEnvironment(context.Background(), NewEnvironment(nil, 1, nil, false))
	model := &ModelMetadata{Name: "test.translate.lang"}

	ctx := runtimeContextWithEnvironment(base, model, map[string]any{"lang": "vi_VN"})
	if got := runtimeRequestLang(ctx); got != "vi_vn" {
		t.Fatalf("top-level lang kwarg must set the context lang, got %q", got)
	}
	ctx = runtimeContextWithEnvironment(base, model, map[string]any{"lang": "vi_VN", "context": map[string]any{"lang": "fr_FR"}})
	if got := runtimeRequestLang(ctx); got != "fr_fr" {
		t.Fatalf("explicit context.lang must win over the shorthand, got %q", got)
	}
	ctx = runtimeContextWithEnvironment(base, model, map[string]any{"limit": 5})
	if got := runtimeRequestLang(ctx); got != "en" {
		t.Fatalf("no lang anywhere must resolve to en, got %q", got)
	}
}

func TestRuntimeRequestLangAllLanguagesSentinel(t *testing.T) {
	env := NewEnvironment(nil, 1, map[string]any{"lang": "vi_vn"}, false)
	ctx := ContextWithEnvironment(context.Background(), env)
	if got := runtimeRequestLang(ctx); got != "vi_vn" {
		t.Fatalf("user default language must apply, got %q", got)
	}
	star := ContextWithEnvironment(context.Background(), env.WithContext(map[string]any{"lang": "*"}))
	if got := runtimeRequestLang(star); got != "" {
		t.Fatalf("lang \"*\" must select all-languages mode, got %q", got)
	}
	if got := runtimeEnvironmentLang(star); got != "en" {
		t.Fatalf("writes under \"*\" target the en base, got %q", got)
	}
	override := ContextWithEnvironment(context.Background(), env.WithContext(map[string]any{"lang": "fr_FR"}))
	if got := runtimeRequestLang(override); got != "fr_fr" {
		t.Fatalf("request lang must override the user default, got %q", got)
	}
}

func TestRuntimeUserLangDefaultsFromUserRow(t *testing.T) {
	bunDB, err := NewBunDB(runtimeGraphEnvPostgresConfig())
	if err != nil {
		t.Skipf("postgres fixture unavailable: %v", err)
	}
	t.Cleanup(func() { _ = bunDB.Close() })
	ctx := context.Background()
	if _, err := bunDB.DB.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS base_users (id BIGINT PRIMARY KEY, lang TEXT)`); err != nil {
		t.Skipf("cannot prepare base_users fixture: %v", err)
	}
	const uid = 987654321
	if _, err := bunDB.DB.ExecContext(ctx, `INSERT INTO base_users (id, lang) VALUES (?, 'vi_VN') ON CONFLICT (id) DO UPDATE SET lang = 'vi_VN'`, uid); err != nil {
		t.Skipf("cannot seed base_users fixture: %v", err)
	}
	t.Cleanup(func() { _, _ = bunDB.DB.ExecContext(ctx, `DELETE FROM base_users WHERE id = ?`, uid) })

	noLang := ContextWithEnvironment(ctx, NewEnvironment(bunDB.DB, uid, nil, false))
	if got := runtimeRequestLang(noLang); got != "vi_vn" {
		t.Fatalf("a request without lang must use the user's language, got %q", got)
	}
	explicit := ContextWithEnvironment(ctx, NewEnvironment(bunDB.DB, uid, map[string]any{"lang": "fr_FR"}, false))
	if got := runtimeRequestLang(explicit); got != "fr_fr" {
		t.Fatalf("an explicit lang must beat the user's language, got %q", got)
	}
	// Public/anonymous, cron and default environments carry user 0: English, no lookup.
	public := ContextWithEnvironment(ctx, NewEnvironment(bunDB.DB, 0, nil, true))
	if got := runtimeRequestLang(public); got != "en" {
		t.Fatalf("a request with no user must default to en, got %q", got)
	}
	missing := ContextWithEnvironment(ctx, NewEnvironment(bunDB.DB, 987654322, nil, false))
	if got := runtimeRequestLang(missing); got != "en" {
		t.Fatalf("an unknown user must fall back to en, got %q", got)
	}
}
