package models

// File overview:
// - Responsibility: small local arg-parsing helpers shared by this module's
//   custom RuntimeMethod/StandardHooks.Create callbacks. Each modules/*/models
//   package keeps its own private copy of these (no exported orm-level
//   equivalent exists) — mirrors modules/helpdesk/models/helpers_runtime.go.
// - Key symbols: `recordMapFromArgs`, `cloneRecordMap`, `mapValue`, `int64FromAny`.

import (
	"fmt"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
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
		if record, ok := mapValue(kwargs, key).(map[string]any); ok {
			return orm.RecordMap(record), nil
		}
		if record, ok := mapValue(kwargs, key).(orm.RecordMap); ok {
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

func mapValue(values map[string]any, key string) any {
	if values == nil {
		return nil
	}
	return values[key]
}

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
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return 0, false
		}
		value, err := strconv.ParseInt(trimmed, 10, 64)
		return value, err == nil
	// RuntimeRead/RuntimeSearchRead expand many2one fields into an [id,
	// displayName] tuple; peel the id out the same way other modules do.
	case []any:
		if len(typed) == 0 {
			return 0, false
		}
		return int64FromAny(typed[0])
	// RuntimeRead/RuntimeSearchRead/create-readback actually enrich many2one
	// fields into a fixed-size [id, display_name] ARRAY (see
	// internal/orm/runtime_relations.go's prefetchManyToOneDisplaysCore), not
	// the []any slice above — a real, distinct Go type the []any case above
	// never matches. This is the exact bug that caused createProjectUpdate's
	// int64Value(created["project_id"]) to silently resolve to 0, skipping
	// the write-back onto tbl.project entirely with no error (see
	// tbl_project_update.go's createProjectUpdate).
	case [2]any:
		return int64FromAny(typed[0])
	case map[string]any:
		return int64FromAny(typed["id"])
	default:
		return 0, false
	}
}

// int64Value is the zero-value-on-miss convenience wrapper around
// int64FromAny, used wherever a caller wants "0" instead of threading an ok bool.
func int64Value(raw any) int64 {
	value, _ := int64FromAny(raw)
	return value
}

// stringValue trims raw into a plain string, or "" for anything not a string.
func stringValue(raw any) string {
	if typed, ok := raw.(string); ok {
		return strings.TrimSpace(typed)
	}
	return ""
}

// boolValue coerces raw into a bool, defaulting to false for anything else.
func boolValue(raw any) bool {
	value, _ := raw.(bool)
	return value
}

// hasRecordMapKey reports whether values explicitly carries key (present,
// even if the value itself is nil) — distinct from "absent" for write hooks
// that only need to react to fields the caller actually touched.
func hasRecordMapKey(values orm.RecordMap, key string) bool {
	if values == nil {
		return false
	}
	_, ok := values[key]
	return ok
}

// firstRow returns the first row of rows, or an error naming label when empty.
func firstRow(rows []orm.RecordMap, label string) (orm.RecordMap, error) {
	if len(rows) == 0 {
		return nil, fmt.Errorf("%s not found", label)
	}
	return rows[0], nil
}

// int64SliceFromAny coerces raw (a []any of ids, or a single id) into []int64.
func int64SliceFromAny(raw any) []int64 {
	switch typed := raw.(type) {
	case []any:
		out := make([]int64, 0, len(typed))
		for _, entry := range typed {
			if value, ok := int64FromAny(entry); ok {
				out = append(out, value)
			}
		}
		return out
	case nil:
		return nil
	default:
		if value, ok := int64FromAny(raw); ok {
			return []int64{value}
		}
		return nil
	}
}

// containsInt64 reports whether needle is present in haystack.
func containsInt64(haystack []int64, needle int64) bool {
	for _, id := range haystack {
		if id == needle {
			return true
		}
	}
	return false
}
