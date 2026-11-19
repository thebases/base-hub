package models

// File overview:
// - Responsibility: small RPC-argument normalization helpers shared by the automation models, mirroring
//   the equivalent helpers in modules/workflow/models/helpers_runtime.go (each module package owns its
//   own copy since these are unexported and the packages must not depend on each other).
// - Key symbols: `recordMapFromArgs`, `recordMapListFromArgs`, `createRecordsFromArgs`, `replaceRecordMap`,
//   `companyIDValue`, `cloneRecordMap`.

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"git.thebase.vn/base_hub/internal/utils/anyconv"
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
			log.Errorf("automation model values list contains %T, want object", row)
			return nil, fmt.Errorf("automation model values list contains %T, want object", row)
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

// createRecordsFromArgs runs the guarded create (orm.CreateRecord) once per row of a single or
// list `values` payload, returning one record for one row and a list otherwise, as the module's
// create methods always did.
func createRecordsFromArgs(ctx context.Context, model *orm.ModelMetadata, args []any, kwargs map[string]any) (any, error) {
	valuesList, err := recordMapListFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if len(valuesList) == 0 {
		return nil, orm.InvalidParamsf("%s.create requires values", model.Name)
	}
	created := make([]orm.RecordMap, 0, len(valuesList))
	for _, values := range valuesList {
		record, err := orm.CreateRecord(ctx, model, values)
		if err != nil {
			return nil, err
		}
		created = append(created, record)
	}
	if len(created) == 1 {
		return created[0], nil
	}
	return created, nil
}

// replaceRecordMap makes dst hold exactly src: ModelSpec.Prepare must rewrite op.Values in place.
func replaceRecordMap(dst, src orm.RecordMap) {
	for key := range dst {
		delete(dst, key)
	}
	for key, value := range src {
		dst[key] = value
	}
}

// companyIDValue turns a company_id read back from the repository (int, *int64 or a many2one
// pair) into a plain int64, or nil for a shared row. The guarded create/write company checks
// read the value with a plain integer conversion, so a pair or pointer would look "shared".
func companyIDValue(raw any) any {
	if ptr, ok := raw.(*int64); ok {
		if ptr == nil {
			return nil
		}
		raw = *ptr
	}
	if id, ok := anyconv.Many2OneID(raw); ok {
		return id
	}
	return nil
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
