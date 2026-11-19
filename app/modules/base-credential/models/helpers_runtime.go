package models

// File overview:
// - Responsibility: small RPC-argument normalization helpers shared by the credential models,
//   mirroring the equivalent helpers in modules/base-automation/models/helpers_runtime.go (each
//   module package owns its own copy since these are unexported and the packages must not depend
//   on each other).
// - Key symbols: `recordMapFromArgs`, `recordMapListFromArgs`, `cloneRecordMap`.

import (
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// recordMapFromArgs extracts one record map from positional args or kwargs.
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

// recordMapListFromArgs extracts one or many record maps from positional args or kwargs.
func recordMapListFromArgs(args []any, kwargs map[string]any, keys ...string) ([]orm.RecordMap, error) {
	if record, err := recordMapFromArgs(args, kwargs, keys...); err != nil {
		return nil, err
	} else if record != nil {
		return []orm.RecordMap{record}, nil
	}
	if len(args) > 0 {
		if rows, ok := args[0].([]any); ok {
			return recordMapsFromAnySlice(rows)
		}
	}
	for _, key := range keys {
		if rows, ok := mapValue(kwargs, key).([]any); ok {
			return recordMapsFromAnySlice(rows)
		}
	}
	return nil, nil
}

func recordMapsFromAnySlice(rows []any) ([]orm.RecordMap, error) {
	out := make([]orm.RecordMap, 0, len(rows))
	for _, row := range rows {
		switch typed := row.(type) {
		case map[string]any:
			out = append(out, orm.RecordMap(typed))
		case orm.RecordMap:
			out = append(out, cloneRecordMap(typed))
		default:
			log.Errorf("credential model values list contains %T, want object", row)
			return nil, orm.InvalidParamsf("credential model values list contains %T, want object", row)
		}
	}
	return out, nil
}

// cloneRecordMap returns a shallow copy so normalization never mutates the caller's map.
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

func stringFromAny(raw any) (string, bool) {
	switch typed := raw.(type) {
	case string:
		return strings.TrimSpace(typed), true
	default:
		return "", false
	}
}

func stringValue(raw any) string {
	value, _ := stringFromAny(raw)
	return value
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
		value, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return value, err == nil
	default:
		return 0, false
	}
}
