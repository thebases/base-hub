package models

// File overview:
// - Responsibility: small RPC-argument normalization helpers shared by the auth models,
//   mirroring modules/base-credential/models/helpers_runtime.go (each module package owns its
//   own copy since these are unexported and the packages must not depend on each other).
// - Key symbols: `recordMapFromArgs`, `recordMapListFromArgs`, `cloneRecordMap`.

import (
	"fmt"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
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
			log.Errorf("auth model values list contains %T, want object", row)
			return nil, fmt.Errorf("auth model values list contains %T, want object", row)
		}
	}
	return out, nil
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

func stringValue(raw any) string {
	if typed, ok := raw.(string); ok {
		return typed
	}
	return ""
}
