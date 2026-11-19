package models

// File overview:
// - Responsibility: defines the `base` module model contract for helpers runtime, including field metadata, runtime methods, and module-specific invariants.
// - Key symbols: `recordMapFromArgs`, `recordMapListFromArgs`, `createRecordList`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.

import (
	"context"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"git.thebase.vn/base_hub/internal/utils/anyconv"
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
			out := make([]orm.RecordMap, 0, len(rows))
			for _, row := range rows {
				switch typed := row.(type) {
				case map[string]any:
					out = append(out, orm.RecordMap(typed))
				case orm.RecordMap:
					out = append(out, cloneRecordMap(typed))
				default:
					log.Errorf("base model values list contains %T, want object", row)
					return nil, orm.InvalidParamsf("base model values list contains %T, want object", row)
				}
			}
			return out, nil
		}
	}
	for _, key := range keys {
		switch typed := mapValue(kwargs, key).(type) {
		case []any:
			out := make([]orm.RecordMap, 0, len(typed))
			for _, row := range typed {
				switch mapped := row.(type) {
				case map[string]any:
					out = append(out, orm.RecordMap(mapped))
				case orm.RecordMap:
					out = append(out, cloneRecordMap(mapped))
				default:
					log.Errorf("base model values list contains %T, want object", row)
					return nil, orm.InvalidParamsf("base model values list contains %T, want object", row)
				}
			}
			return out, nil
		}
	}
	return nil, nil
}

// cloneRecordMap clones the record map handled by this package.
// createRecordList keeps the list-payload create contract (one object or a list of objects) of the
// base reference models on top of the guarded orm.CreateRecord, which takes one object: the default
// create would refuse a list. Rows are created one by one, as before; one row returns the record,
// several return the list.
func createRecordList(ctx context.Context, model *orm.ModelMetadata, args []any, kwargs map[string]any) (any, error) {
	valuesList, err := recordMapListFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if len(valuesList) == 0 {
		log.Errorf("%s.create requires values", model.Name)
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

func cloneRecordMap(values orm.RecordMap) orm.RecordMap {
	cloned := make(orm.RecordMap, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func stringArg(args []any, index int) string {
	return stringValue(argValue(args, index))
}

func argValue(args []any, index int) any {
	if index < 0 || index >= len(args) {
		return nil
	}
	return args[index]
}

func mapValue(values map[string]any, key string) any {
	if values == nil {
		return nil
	}
	return values[key]
}

func stringValue(raw any) string {
	value, _ := stringFromAny(raw)
	return value
}

func stringFromAny(raw any) (string, bool) {
	switch typed := raw.(type) {
	case string:
		return strings.TrimSpace(typed), true
	default:
		return "", false
	}
}

// many2oneID reads a many2one value. Runtime reads (RuntimeRead/RuntimeSearchRead/
// RuntimeCreate) return many2one fields as an [id, display_name] tuple, which
// int64FromAny rejects, so every row read of a many2one column goes through here.
// int64FromAny stays for plain id arguments and payload values, where accepting a
// tuple would widen what a caller may pass.
func many2oneID(raw any) (int64, bool) { return anyconv.Many2OneID(raw) }

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
		value, err := parsePositiveInt64(typed)
		return value, err == nil
	default:
		return 0, false
	}
}

func stringSlice(raw any) []string {
	switch typed := raw.(type) {
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if item = strings.TrimSpace(item); item != "" {
				out = append(out, item)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := stringValue(item); text != "" {
				out = append(out, text)
			}
		}
		return out
	case string:
		if typed = strings.TrimSpace(typed); typed != "" {
			return []string{typed}
		}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

// parsePositiveInt64 parses the positive int 64 handled by this package.
func parsePositiveInt64(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		log.Errorf("value is empty")
		return 0, orm.InvalidParamsf("value is empty")
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, err
	}
	if value <= 0 {
		log.Errorf("value must be positive")
		return 0, orm.InvalidParamsf("value must be positive")
	}
	return value, nil
}

func int64Value(value any) int64 {
	if out, ok := int64FromAny(value); ok {
		return out
	}
	return 0
}

func boolValue(value any) bool {
	if out, ok := value.(bool); ok {
		return out
	}
	return false
}

func idsToAny(ids []string) []any {
	out := make([]any, 0, len(ids))
	for _, id := range ids {
		if numericID, ok := int64FromAny(id); ok {
			out = append(out, numericID)
			continue
		}
		out = append(out, id)
	}
	return out
}
