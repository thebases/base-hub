package orm

// File overview:
// - Responsibility: implements the translated stored-field runtime (SRS-F-021) —
//   language resolution, jsonb column SQL expressions, write scoping, and the
//   `update_field_translations` runtime method shared by every registered model.
// - Key symbols: `runtimeEnvironmentLang`, `translatedColumnSQL`,
//   `applyTranslatedValues`, `runtimeUpdateFieldTranslations`.
// - Storage contract: a `translate:true` column is jsonb `{"en": "...", "vi_VN": "..."}`;
//   `en` is the required base key (mirrors the .pot-as-en rule in SRS-F-013) and
//   every read/search/order resolves `COALESCE(col->>lang, col->>'en')`.
// - Lang keys are canonical (`i18n.NormalizeLang`, lowercase `vi_vn`; `en`/`en_us`
//   collapse to the base key `en`) so the SPA's `en_us`/`vi_vn` and
//   `vi_VN` payloads address the same jsonb key.
// - Read modes: a request with `context.lang` receives the resolved string; a
//   request WITHOUT it falls back to the environment user's own language (then
//   `en`), and only the explicit `lang: "*"` receives the whole `{lang: value}`
//   envelope (translation dict). Filters,
//   ordering and rec-name search always use the base language in that mode.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/i18n"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

// translationBaseLang is the required base key of every translated jsonb value.
const translationBaseLang = "en"

// langCodePattern accepts locale codes such as `en`, `vi_VN`, or `pt-BR`. The
// code is embedded into SQL identifiers (expression indexes) and jsonb paths,
// so anything outside this shape must fall back to the base language instead of
// reaching SQL.
var langCodePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_@-]{0,15}$`)

// canonicalTranslationLang maps any accepted spelling of a locale to its jsonb
// key: the app-wide lowercase `<language>_<country>` form, with English
// (`en`, `en_us`, `en-US`) collapsing to the base key so the SPA's `en_us`
// never forks a second English value next to `en`.
func canonicalTranslationLang(lang string) string {
	canonical := i18n.NormalizeLang(lang)
	if canonical == "en_us" {
		return translationBaseLang
	}
	return canonical
}

// sanitizedLangCode normalizes one lang code for SQL usage. Invalid or empty
// codes collapse to the base language — SRS-F-021 mandates a soft en fallback
// for unknown context languages instead of a request failure.
func sanitizedLangCode(lang string) string {
	lang = strings.TrimSpace(lang)
	if lang == "" {
		return translationBaseLang
	}
	if !langCodePattern.MatchString(lang) {
		log.Warnf("orm: invalid context lang %q, falling back to %q", lang, translationBaseLang)
		return translationBaseLang
	}
	return canonicalTranslationLang(lang)
}

// runtimeEnvironmentLang resolves the active language of the request-bound
// environment. It never fails: a missing or malformed lang resolves to the base
// language so translated writes always target a key.
func runtimeEnvironmentLang(ctx context.Context) string {
	if lang := runtimeRequestLang(ctx); lang != "" {
		return lang
	}
	return translationBaseLang
}

// translationAllLangs is the explicit `lang` value that asks for the full
// {lang: value} envelope; it is the only way to get the map.
const translationAllLangs = "*"

// runtimeRequestLang is the read-side language, for every caller (HTTP, MCP, Lua,
// workflows, cron): the canonical context lang; else the environment user's own
// language (base_users.lang); else the `en` base. "" is returned only for the
// explicit "*" sentinel, which switches translated projections to the
// full-envelope mode. SQL helpers treat "" as the base language for filtering
// and ordering because sanitizedLangCode("") is `en`.
func runtimeRequestLang(ctx context.Context) string {
	env := EnvironmentFromContext(ctx)
	if env == nil {
		return translationBaseLang
	}
	if raw, ok := env.ContextValue("lang"); ok {
		lang, _ := raw.(string)
		switch strings.TrimSpace(lang) {
		case translationAllLangs:
			return ""
		case "":
		default:
			return sanitizedLangCode(lang)
		}
	}
	return runtimeUserLang(ctx, env)
}

// runtimeUserLang resolves the environment user's stored language, cached on the
// environment so a request pays the lookup once. Any miss (no user, no DB, no
// column, empty value) falls back to the base language: the lookup must never
// fail a read.
func runtimeUserLang(ctx context.Context, env *Environment) string {
	userID := env.UserID()
	if userID <= 0 {
		return translationBaseLang
	}
	id := strconv.FormatInt(userID, 10)
	if cached, ok := env.CachedField("base_users", id, "lang"); ok {
		lang, _ := cached.(string)
		return lang
	}
	lang := translationBaseLang
	// env.DB() only: the process-wide handle would read another tenant's user row.
	if db := env.DB(); db != nil {
		var stored sql.NullString
		if err := db.NewSelect().TableExpr("base_users").ColumnExpr("lang").Where("id = ?", userID).Limit(1).Scan(ctx, &stored); err == nil {
			if trimmed := strings.TrimSpace(stored.String); trimmed != "" {
				lang = sanitizedLangCode(trimmed)
			}
		}
	}
	env.CacheField("base_users", id, "lang", lang)
	return lang
}

// EnsureContextLang returns a context whose environment carries a language.
// Internal Go callers that need plain strings from translated fields (menu
// loading, name derivation) call it before runtime reads, because a read with
// no lang returns the full translation envelope. An explicit lang is kept.
func EnsureContextLang(ctx context.Context, fallback string) context.Context {
	env := EnvironmentFromContext(ctx)
	if env == nil || runtimeRequestLang(ctx) != "" {
		return ctx
	}
	if strings.TrimSpace(fallback) == "" {
		fallback = translationBaseLang
	}
	return ContextWithEnvironment(ctx, env.WithContext(map[string]any{"lang": fallback}))
}

// modelHasTranslatedFields reports whether any registered field on the model
// carries the translate contract, so hot paths can skip translated handling
// entirely for the common untranslated model.
func modelHasTranslatedFields(meta *ModelMetadata) bool {
	if meta == nil {
		return false
	}
	for _, field := range meta.Fields {
		if field.Translate {
			return true
		}
	}
	return false
}

// translatedModelColumns lists every translated storage column of a model so
// insert paths can exclude them from the generic Bun statement — the struct's
// zero-value string would otherwise be written into a jsonb column.
func translatedModelColumns(meta *ModelMetadata) []string {
	if meta == nil {
		return nil
	}
	var columns []string
	for _, field := range meta.Fields {
		if !field.Translate {
			continue
		}
		if column := translatedFieldColumn(field); column != "" {
			columns = append(columns, column)
		}
	}
	return columns
}

// computedVirtualColumns lists non-stored computed scalar columns so INSERT
// can leave them out: they hold no persisted value, and tables created before
// the field existed have no such column (the migrator only manages stored ones).
func computedVirtualColumns(meta *ModelMetadata) []string {
	if meta == nil {
		return nil
	}
	var columns []string
	for _, field := range meta.Fields {
		// Non-stored related fields have no column either, so they must be kept out of INSERT too.
		if field.Stored || (strings.TrimSpace(field.Compute) == "" && len(field.Related) == 0) || field.Translate {
			continue
		}
		if field.Kind == FieldKindOneToMany || field.Kind == FieldKindManyToMany {
			continue
		}
		if column := strings.TrimSpace(field.BunColumn); column != "" {
			columns = append(columns, column)
		}
	}
	return columns
}

// translatedFieldColumn resolves the physical storage column of one field.
func translatedFieldColumn(field FieldMetadata) string {
	column := strings.TrimSpace(field.BunColumn)
	if column == "" {
		column = strings.TrimSpace(field.Name)
	}
	return column
}

// translatedColumnSQL builds the language-resolving read expression for one
// translated jsonb column. baseAlias must already be quoted; the lang code is
// sanitized before being embedded as a SQL literal, so the expression is safe
// to inline into SELECT, WHERE, and ORDER BY clauses.
func translatedColumnSQL(baseAlias, column, lang string) string {
	lang = sanitizedLangCode(lang)
	qualified := column
	if strings.TrimSpace(baseAlias) != "" {
		qualified = baseAlias + "." + quoteIdent(column)
	} else {
		qualified = quoteIdent(column)
	}
	if lang == translationBaseLang {
		// The base language needs no fallback chain but still requires the ->>
		// extraction because the storage column is jsonb, not text.
		return "(" + qualified + "->>'" + translationBaseLang + "')"
	}
	return "COALESCE(" + qualified + "->>'" + lang + "', " + qualified + "->>'" + translationBaseLang + "')"
}

// translatedProjectionSQL is the SELECT-list form of a translated column. A
// concrete lang resolves to its string (en fallback); the empty lang projects
// the whole envelope as JSON text, which resolveTranslatedRecordValues decodes
// into a {lang: value} map after the scan.
func translatedProjectionSQL(baseAlias, column, lang string) string {
	if lang != "" {
		return translatedColumnSQL(baseAlias, column, lang)
	}
	if strings.TrimSpace(baseAlias) != "" {
		return "(" + baseAlias + "." + quoteIdent(column) + ")::text"
	}
	return "(" + quoteIdent(column) + ")::text"
}

// splitTranslatedValues removes translated fields from a direct-value map and
// returns them separately. Translated columns are jsonb and must never reach
// the generic Bun INSERT/UPDATE path with a plain string payload — they are
// applied through applyTranslatedValues inside the same transaction.
func splitTranslatedValues(meta *ModelMetadata, direct RecordMap) (RecordMap, RecordMap) {
	if len(direct) == 0 || !modelHasTranslatedFields(meta) {
		return direct, nil
	}
	translated := RecordMap{}
	for key, value := range direct {
		field, ok := runtimeLookupField(meta, key)
		if !ok || !field.Translate {
			continue
		}
		translated[field.Name] = value
		delete(direct, key)
	}
	if len(translated) == 0 {
		return direct, nil
	}
	return direct, translated
}

// translatedValueString coerces one incoming translated value into a plain
// string. nil stays nil (clear semantics); every other scalar is stringified
// the same way text fields already serialize through the runtime.
func translatedValueString(value any) *string {
	if value == nil {
		return nil
	}
	text := asString(value)
	return &text
}

// applyTranslatedValues persists translated field payloads with jsonb SQL in
// the caller's transaction scope. Write scoping follows SRS-F-021:
//   - create: the value seeds the required "en" base key, plus the active lang
//     key when the environment language is not en;
//   - write: only the active language's key changes; a NULL column is seeded
//     with the value as the en base so the fallback chain never dangles;
//   - nil value: under en the whole column clears, under any other lang only
//     that key is removed (the read falls back to en again).
func applyTranslatedValues(ctx context.Context, db bun.IDB, meta *ModelMetadata, ids []string, translated RecordMap, isCreate bool) error {
	if len(translated) == 0 || len(ids) == 0 {
		return nil
	}
	if db == nil {
		log.Errorf("orm: translated write requires a database handle")
		return fmt.Errorf("orm: translated write requires a database handle")
	}
	lang := runtimeEnvironmentLang(ctx)
	table := strings.TrimSpace(meta.StorageName)
	idValues := idsToFilterValues(ids)

	for key, value := range translated {
		field, ok := runtimeLookupField(meta, key)
		if !ok || !field.Translate {
			continue
		}
		column := translatedFieldColumn(field)
		// A {lang: value} payload (what an all-languages read returns) writes
		// every listed key, so a client can round-trip a record it read without
		// the envelope being stringified into the base value.
		if envelope, isEnvelope := value.(map[string]any); isEnvelope {
			if err := applyTranslationEnvelope(ctx, db, meta, field, ids, envelope, isCreate); err != nil {
				return err
			}
			continue
		}
		text := translatedValueString(value)

		switch {
		case text == nil && lang == translationBaseLang:
			// Clearing the base language clears the whole value: en is the
			// required base key, so a value without en must not exist.
			if _, err := db.NewRaw(
				"UPDATE ? SET ? = NULL WHERE id IN (?)",
				bun.Ident(table), bun.Ident(column), bun.In(idValues),
			).Exec(ctx); err != nil {
				return fmt.Errorf("orm: clear translated %s.%s: %w", meta.Name, field.Name, err)
			}
		case text == nil:
			// Dropping a non-base key keeps the en fallback intact; an object
			// reduced to {} collapses to NULL for stable false-normalization.
			if _, err := db.NewRaw(
				"UPDATE ? SET ? = NULLIF(COALESCE(?, '{}'::jsonb) - ?, '{}'::jsonb) WHERE id IN (?)",
				bun.Ident(table), bun.Ident(column), bun.Ident(column), lang, bun.In(idValues),
			).Exec(ctx); err != nil {
				return fmt.Errorf("orm: drop translated key %s on %s.%s: %w", lang, meta.Name, field.Name, err)
			}
		case isCreate:
			// Create seeds the full envelope in one statement so the row is
			// consistent even if no later write ever touches the field.
			envelope := map[string]string{translationBaseLang: *text}
			if lang != translationBaseLang {
				envelope[lang] = *text
			}
			raw, err := json.Marshal(envelope)
			if err != nil {
				return fmt.Errorf("orm: encode translated %s.%s: %w", meta.Name, field.Name, err)
			}
			if _, err := db.NewRaw(
				"UPDATE ? SET ? = ?::jsonb WHERE id IN (?)",
				bun.Ident(table), bun.Ident(column), string(raw), bun.In(idValues),
			).Exec(ctx); err != nil {
				return fmt.Errorf("orm: seed translated %s.%s: %w", meta.Name, field.Name, err)
			}
		default:
			// Update touches only the active language key. COALESCE seeds a
			// legacy NULL column with the value as en base first so the jsonb_set
			// target always exists and the fallback chain stays complete.
			if _, err := db.NewRaw(
				"UPDATE ? SET ? = jsonb_set(COALESCE(?, jsonb_build_object('en', ?::text)), ARRAY[?::text], to_jsonb(?::text), true) WHERE id IN (?)",
				bun.Ident(table), bun.Ident(column), bun.Ident(column), *text, lang, *text, bun.In(idValues),
			).Exec(ctx); err != nil {
				return fmt.Errorf("orm: write translated %s.%s: %w", meta.Name, field.Name, err)
			}
		}
	}
	return nil
}

// translatedInsertValues turns the translated create payloads into the JSON-text
// envelope each jsonb column receives IN the INSERT, so NOT NULL translated
// columns are satisfied by the row's own statement (a post-insert UPDATE would
// violate the constraint). It returns the field->envelope map to merge into the
// direct values and the translated columns that stay excluded (not provided or
// explicitly nil, which create as NULL). Seeding follows applyTranslatedValues:
// a scalar seeds `en` plus the active lang; a {lang: value} map needs `en`.
func translatedInsertValues(ctx context.Context, meta *ModelMetadata, translated RecordMap) (RecordMap, []string, error) {
	inserted := RecordMap{}
	excluded := make([]string, 0)
	lang := runtimeEnvironmentLang(ctx)
	for _, field := range meta.Fields {
		if !field.Translate {
			continue
		}
		column := translatedFieldColumn(field)
		value, ok := translated[field.Name]
		if !ok || value == nil {
			excluded = append(excluded, column)
			continue
		}
		envelope := map[string]string{}
		if payload, isMap := value.(map[string]any); isMap {
			values, _, err := canonicalTranslationPayload(payload)
			if err != nil {
				return nil, nil, err
			}
			for key, text := range values {
				if text != nil {
					envelope[key] = *text
				}
			}
			if _, hasBase := envelope[translationBaseLang]; !hasBase {
				log.Errorf("orm: create %s.%s: translation payload requires the %q key", meta.Name, field.Name, translationBaseLang)
				return nil, nil, fmt.Errorf("orm: create %s.%s: translation payload requires the %q key", meta.Name, field.Name, translationBaseLang)
			}
		} else {
			text := asString(value)
			envelope[translationBaseLang] = text
			if lang != translationBaseLang {
				envelope[lang] = text
			}
		}
		raw, err := json.Marshal(envelope)
		if err != nil {
			return nil, nil, fmt.Errorf("orm: encode translated %s.%s: %w", meta.Name, field.Name, err)
		}
		inserted[field.Name] = string(raw)
	}
	return inserted, excluded, nil
}

// canonicalTranslationPayload validates one {lang: value|null} map and returns
// it keyed by canonical lang code with the base key first in the order slice,
// so a seed on a NULL column always creates `en` before secondary keys.
func canonicalTranslationPayload(payload map[string]any) (map[string]*string, []string, error) {
	out := make(map[string]*string, len(payload))
	for lang, value := range payload {
		lang = strings.TrimSpace(lang)
		if !langCodePattern.MatchString(lang) {
			log.Errorf("orm: invalid translation lang code %q", lang)
			return nil, nil, fmt.Errorf("orm: invalid translation lang code %q", lang)
		}
		key := canonicalTranslationLang(lang)
		if key == translationBaseLang && value == nil {
			log.Errorf("orm: the %q base translation cannot be removed", translationBaseLang)
			return nil, nil, fmt.Errorf("orm: the %q base translation cannot be removed", translationBaseLang)
		}
		out[key] = translatedValueString(value)
	}
	keys := make([]string, 0, len(out))
	for key := range out {
		if key != translationBaseLang {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if _, ok := out[translationBaseLang]; ok {
		keys = append([]string{translationBaseLang}, keys...)
	}
	return out, keys, nil
}

// setTranslationKeys applies canonical translation keys to one jsonb column:
// a value upserts that language key (seeding a NULL column with the value as
// the en base so the fallback never dangles), nil drops the key and an object
// reduced to {} collapses back to NULL.
func setTranslationKeys(ctx context.Context, db bun.IDB, table, column string, ids []string, values map[string]*string, order []string) error {
	idValues := idsToFilterValues(ids)
	for _, key := range order {
		text := values[key]
		if text == nil {
			if _, err := db.NewRaw(
				"UPDATE ? SET ? = NULLIF(COALESCE(?, '{}'::jsonb) - ?, '{}'::jsonb) WHERE id IN (?)",
				bun.Ident(table), bun.Ident(column), bun.Ident(column), key, bun.In(idValues),
			).Exec(ctx); err != nil {
				return fmt.Errorf("orm: drop translation %s of %s.%s: %w", key, table, column, err)
			}
			continue
		}
		if _, err := db.NewRaw(
			"UPDATE ? SET ? = jsonb_set(COALESCE(?, jsonb_build_object('en', ?::text)), ARRAY[?::text], to_jsonb(?::text), true) WHERE id IN (?)",
			bun.Ident(table), bun.Ident(column), bun.Ident(column), *text, key, *text, bun.In(idValues),
		).Exec(ctx); err != nil {
			return fmt.Errorf("orm: set translation %s of %s.%s: %w", key, table, column, err)
		}
	}
	return nil
}

// applyTranslationEnvelope persists a {lang: value} payload sent as the value
// of a translated field in create/write. Create requires the `en` base key
// (a value without en must not exist); write merges only the listed keys.
func applyTranslationEnvelope(ctx context.Context, db bun.IDB, meta *ModelMetadata, field FieldMetadata, ids []string, envelope map[string]any, isCreate bool) error {
	values, order, err := canonicalTranslationPayload(envelope)
	if err != nil {
		return err
	}
	if len(order) == 0 {
		return nil
	}
	if isCreate {
		if _, ok := values[translationBaseLang]; !ok || values[translationBaseLang] == nil {
			log.Errorf("orm: create %s.%s: translation payload requires the %q key", meta.Name, field.Name, translationBaseLang)
			return fmt.Errorf("orm: create %s.%s: translation payload requires the %q key", meta.Name, field.Name, translationBaseLang)
		}
	}
	return setTranslationKeys(ctx, db, strings.TrimSpace(meta.StorageName), translatedFieldColumn(field), ids, values, order)
}

// translationEnvelopeFromText decodes the JSON text projection of a translated
// column into a {lang: value} map. Non-object text returns nil.
func translationEnvelopeFromText(text string) map[string]any {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "{") {
		return nil
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
		return nil
	}
	return envelope
}

// resolveTranslatedRecordValues is the post-fetch safety net: any translated
// field value that still carries the jsonb envelope (a map from a raw scan, or
// the envelope's JSON text from a path that bypassed the resolved projection)
// collapses to the environment language with en fallback. SQL-side projection
// already resolves the standard search paths; this keeps alternate scan paths
// on the same serialization contract — clients never see the envelope.
func resolveTranslatedRecordValues(meta *ModelMetadata, records []RecordMap, lang string) {
	if len(records) == 0 || !modelHasTranslatedFields(meta) {
		return
	}
	// An empty lang is the all-languages mode: the value stays (or becomes) the
	// full {lang: value} map. A concrete lang collapses it to one string.
	allLangs := strings.TrimSpace(lang) == ""
	if !allLangs {
		lang = sanitizedLangCode(lang)
	}
	for _, field := range meta.Fields {
		if !field.Translate {
			continue
		}
		for _, record := range records {
			value, ok := record[field.Name]
			if !ok || value == nil {
				continue
			}
			switch typed := value.(type) {
			case map[string]any:
				if !allLangs {
					record[field.Name] = translationFromEnvelope(typed, lang)
				}
			case string:
				// A translated column only ever stores a jsonb object, so a raw
				// `{...}` string here is the unresolved envelope, not user text.
				envelope := translationEnvelopeFromText(typed)
				if envelope == nil {
					continue
				}
				if allLangs {
					record[field.Name] = envelope
				} else {
					record[field.Name] = translationFromEnvelope(envelope, lang)
				}
			}
		}
	}
}

// translationFromEnvelope picks the lang value with en fallback from one
// decoded jsonb envelope. A missing value resolves to nil so the standard
// false-normalization turns it into the usual scalar null.
func translationFromEnvelope(envelope map[string]any, lang string) any {
	if value, ok := lookupEnvelopeKey(envelope, lang); ok {
		return value
	}
	if value, ok := lookupEnvelopeKey(envelope, translationBaseLang); ok {
		return value
	}
	return nil
}

// lookupEnvelopeKey finds one language's non-null value, matching the exact key
// first and then any stored key that canonicalizes to the same language, so
// values written before keys were canonical (`vi_VN`) still resolve.
func lookupEnvelopeKey(envelope map[string]any, lang string) (any, bool) {
	if value, ok := envelope[lang]; ok && value != nil {
		return value, true
	}
	want := canonicalTranslationLang(lang)
	for key, value := range envelope {
		if value != nil && canonicalTranslationLang(key) == want {
			return value, true
		}
	}
	return nil, false
}

// runtimeUpdateFieldTranslations implements the `update_field_translations`
// runtime method (SRS-F-021, per-language-key semantics): edit multiple language keys
// of one translated field in a single call. Signature:
//
//	update_field_translations(field_name, {lang: value|null})
//
// A null value removes that language key; removing or nulling the en base key
// is rejected because en is the required fallback.
func runtimeUpdateFieldTranslations(ctx context.Context, model *ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	if model == nil {
		log.Errorf("orm: model metadata is required")
		return nil, fmt.Errorf("orm: model metadata is required")
	}
	resolvedIDs, err := runtimeIDsArg(ids, nil, kwargs)
	if err != nil {
		return nil, err
	}
	if len(resolvedIDs) == 0 {
		log.Errorf("orm: update_field_translations requires at least one id")
		return nil, fmt.Errorf("orm: update_field_translations requires at least one id")
	}

	fieldName := runtimeStringArg(args, kwargs, "field_name", 0)
	if fieldName == "" {
		fieldName = runtimeStringArg(args, kwargs, "field", 0)
	}
	field, ok := runtimeLookupField(model, fieldName)
	if !ok || !field.Translate {
		log.Errorf("orm: field %q on %s is not a translated field", fieldName, model.Name)
		return nil, fmt.Errorf("orm: field %q on %s is not a translated field", fieldName, model.Name)
	}

	translations, err := runtimeTranslationsArg(args, kwargs)
	if err != nil {
		return nil, err
	}
	if len(translations) == 0 {
		log.Errorf("orm: update_field_translations requires a {lang: value} map")
		return nil, fmt.Errorf("orm: update_field_translations requires a {lang: value} map")
	}

	// Unknown or malformed lang keys are a hard validation error here (unlike
	// the soft read fallback) because the caller is explicitly editing keys.
	canonical := make(map[string]any, len(translations))
	for lang, value := range translations {
		if !langCodePattern.MatchString(strings.TrimSpace(lang)) {
			log.Errorf("orm: update_field_translations: invalid lang code %q", lang)
			return nil, fmt.Errorf("orm: update_field_translations: invalid lang code %q", lang)
		}
		key := canonicalTranslationLang(strings.TrimSpace(lang))
		if key == translationBaseLang && value == nil {
			log.Errorf("orm: update_field_translations: the %q base translation cannot be removed", translationBaseLang)
			return nil, fmt.Errorf("orm: update_field_translations: the %q base translation cannot be removed", translationBaseLang)
		}
		canonical[key] = value
	}
	translations = canonical
	if err := ensureRuntimeActiveLangs(ctx, translations); err != nil {
		return nil, err
	}

	// Editing translations is a write: enforce the same ACL + record-rule +
	// company-scope contract as the generic write runtime method.
	if err := ensureModelAccess(ctx, EnvironmentFromContext(ctx), model, "write", resolvedIDs, nil); err != nil {
		return nil, err
	}

	executor, err := requireRuntimeExecutor(model)
	if err != nil {
		return nil, err
	}
	handler, ok := executor.(interface {
		RuntimeApplyFieldTranslations(ctx context.Context, ids []string, field FieldMetadata, translations map[string]any) error
	})
	if !ok {
		log.Errorf("orm: executor for %s does not support translated writes", model.Name)
		return nil, fmt.Errorf("orm: executor for %s does not support translated writes", model.Name)
	}
	if err := handler.RuntimeApplyFieldTranslations(ctx, resolvedIDs, field, translations); err != nil {
		return nil, err
	}
	if env := EnvironmentFromContext(ctx); env != nil {
		env.Invalidate(model.Name, resolvedIDs)
	}
	return true, nil
}

// runtimeTranslationsArg extracts the {lang: value} payload from positional
// args (index 1, after the field name) or the `translations` kwarg.
func runtimeTranslationsArg(args []any, kwargs map[string]any) (map[string]any, error) {
	var raw any
	if len(args) > 1 {
		raw = args[1]
	}
	if raw == nil && kwargs != nil {
		raw = kwargs["translations"]
	}
	if raw == nil {
		return nil, nil
	}
	translations, ok := raw.(map[string]any)
	if !ok {
		log.Errorf("orm: update_field_translations expects a {lang: value} map")
		return nil, fmt.Errorf("orm: update_field_translations expects a {lang: value} map")
	}
	return translations, nil
}

// ensureRuntimeActiveLangs validates edited lang codes against the tbl_lang
// catalog on the environment DB. The table is bound explicitly (raw SQL on a
// runtime model — same rule as tbl_lang helpers) and a missing table degrades
// to pattern-only validation so bootstrap flows without seeded languages keep
// working.
func ensureRuntimeActiveLangs(ctx context.Context, translations map[string]any) error {
	env := EnvironmentFromContext(ctx)
	if env == nil || env.DB() == nil {
		return nil
	}
	var codes []string
	if err := env.DB().NewSelect().
		TableExpr("tbl_lang").
		ColumnExpr("code").
		Scan(ctx, &codes); err != nil {
		// Missing catalog: keep the pattern validation result and log once —
		// the read path tolerates unknown langs, so a hard failure here would
		// be stricter than the storage contract requires.
		log.Warnf("orm: tbl_lang lookup unavailable for translation validation: %v", err)
		return nil
	}
	known := make(map[string]struct{}, len(codes)+1)
	known[translationBaseLang] = struct{}{}
	known[i18n.NormalizeLang(translationBaseLang)] = struct{}{}
	for _, code := range codes {
		known[i18n.NormalizeLang(code)] = struct{}{}
	}
	for lang := range translations {
		if _, ok := known[i18n.NormalizeLang(lang)]; !ok {
			log.Errorf("orm: update_field_translations: unknown lang %q", lang)
			return fmt.Errorf("orm: update_field_translations: unknown lang %q", lang)
		}
	}
	return nil
}

// RuntimeApplyFieldTranslations applies one translated field's multi-language
// payload in a single transaction. It backs the generic
// `update_field_translations` runtime method.
func (h *Handler[T]) RuntimeApplyFieldTranslations(ctx context.Context, ids []string, field FieldMetadata, translations map[string]any) error {
	if len(ids) == 0 || len(translations) == 0 {
		return nil
	}
	return withRuntimeTx(ctx, h, func(txCtx context.Context) error {
		if err := ensureRecordRuleAccess(txCtx, h.metadata, "write", ids); err != nil {
			return err
		}
		db := runtimeDB(txCtx, h)
		if db == nil {
			log.Errorf("orm: BunRepository is not initialized")
			return fmt.Errorf("orm: BunRepository is not initialized")
		}
		values, order, err := canonicalTranslationPayload(translations)
		if err != nil {
			return err
		}
		return setTranslationKeys(txCtx, db, strings.TrimSpace(h.metadata.StorageName), translatedFieldColumn(field), ids, values, order)
	})
}

// relationLabelValue keeps many2one display labels plain strings: a translated
// rec-name read in all-languages mode arrives as a {lang: value} map, and a
// relation tuple [id, label] must stay renderable, so it collapses to the base
// language here.
func relationLabelValue(value any) any {
	if envelope, ok := value.(map[string]any); ok {
		return translationFromEnvelope(envelope, translationBaseLang)
	}
	return value
}

// TranslatedColumnExpr is the raw-SQL counterpart of the ORM's translated read: for module
// queries that select, filter or order on a translate:true column directly (custom
// name_search/name_get helpers, joins). It returns a text expression resolving the column to
// the request language with the en fallback (en alone for the `*` opt-in); `alias` is the
// unquoted table alias, "" for an unqualified column. Reading such a column raw would yield
// the jsonb envelope.
func TranslatedColumnExpr(ctx context.Context, alias, column string) string {
	baseAlias := ""
	if strings.TrimSpace(alias) != "" {
		baseAlias = quoteIdent(alias)
	}
	return translatedColumnSQL(baseAlias, column, runtimeRequestLang(ctx))
}
