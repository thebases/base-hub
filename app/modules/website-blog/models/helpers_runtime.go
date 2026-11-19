package models

// File overview:
// - Responsibility: module-local args/RecordMap helpers shared by the website-blog models (kept per
//   module on purpose; modules never import another module's copy).
// - Key symbols: `recordMapFromArgs`, `cloneRecordMap`, `stringValue`, `int64FromAny`, `slugify`.

import (
	"reflect"
	"strconv"
	"strings"
	"unicode"

	"git.thebase.vn/base_hub/internal/orm"
	"golang.org/x/text/unicode/norm"
)

func recordMapFromArgs(args []any, kwargs map[string]any, keys ...string) (orm.RecordMap, error) {
	if len(args) > 0 {
		if record, ok := args[0].(map[string]any); ok {
			return orm.RecordMap(record), nil
		}
		if record, ok := args[0].(orm.RecordMap); ok {
			return cloneRecordMap(record), nil
		}
	}
	for _, key := range keys {
		if record, ok := kwargs[key].(map[string]any); ok {
			return orm.RecordMap(record), nil
		}
		if record, ok := kwargs[key].(orm.RecordMap); ok {
			return cloneRecordMap(record), nil
		}
	}
	return nil, nil
}

func cloneRecordMap(values orm.RecordMap) orm.RecordMap {
	cloned := make(orm.RecordMap, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func hasRecordMapKey(values orm.RecordMap, key string) bool {
	_, ok := values[key]
	return ok
}

func stringValue(raw any) string {
	if typed, ok := raw.(string); ok {
		return strings.TrimSpace(typed)
	}
	return ""
}

// int64FromAny peels ids out of every shape the runtime produces: numbers, numeric strings and the
// many2one `[id, name]` tuple (`[]any` or `[2]any`).
func int64FromAny(raw any) (int64, bool) {
	switch typed := raw.(type) {
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	case int32:
		return int64(typed), true
	case float64:
		return int64(typed), true
	case string:
		value, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return value, err == nil
	case []any:
		if len(typed) == 0 {
			return 0, false
		}
		return int64FromAny(typed[0])
	case [2]any:
		return int64FromAny(typed[0])
	case map[string]any:
		return int64FromAny(typed["id"])
	default:
		if rv := reflect.ValueOf(raw); rv.IsValid() && (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) && rv.Len() > 0 {
			return int64FromAny(rv.Index(0).Interface())
		}
		return 0, false
	}
}

// slugify lowercases and collapses every non [a-z0-9] run into one dash. Non-latin titles slug to
// the fallback, so callers pass a stable fallback (usually the record kind).
func slugify(raw, fallback string) string {
	var builder strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(foldDiacritics(raw))) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	slug := strings.Trim(builder.String(), "-")
	if slug == "" {
		return fallback
	}
	return slug
}

// foldDiacritics strips combining marks (NFD) and maps đ/Đ, so Vietnamese titles slug to readable
// ASCII instead of empty strings.
func foldDiacritics(raw string) string {
	decomposed := norm.NFD.String(raw)
	var builder strings.Builder
	for _, r := range decomposed {
		switch {
		case unicode.Is(unicode.Mn, r):
			continue
		case r == 'đ':
			builder.WriteByte('d')
		case r == 'Đ':
			builder.WriteByte('D')
		default:
			builder.WriteRune(r)
		}
	}
	return builder.String()
}
