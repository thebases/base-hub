package models

// File overview:
// - Responsibility: RPC argument and value coercion helpers shared by the ui.view runtime methods.
// - Key symbols: `parsePositiveInt64`, `recordMapFromArgs`, `recordMapListFromArgs`, `cloneRecordMap`.

import (
	"encoding/xml"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// parsePositiveInt64 parses the positive int 64 handled by this package.
func parsePositiveInt64(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		log.Errorf("id is required")
		return 0, fmt.Errorf("id is required")
	}
	var value int64
	_, err := fmt.Sscan(raw, &value)
	if err != nil || value <= 0 {
		log.Errorf("invalid positive id %q", raw)
		return 0, fmt.Errorf("invalid positive id %q", raw)
	}
	return value, nil
}

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
					log.Errorf("ui.view.create values list contains %T, want object", row)
					return nil, fmt.Errorf("ui.view.create values list contains %T, want object", row)
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
					log.Errorf("ui.view.create values list contains %T, want object", row)
					return nil, fmt.Errorf("ui.view.create values list contains %T, want object", row)
				}
			}
			return out, nil
		}
	}
	return nil, nil
}

// cloneRecordMap clones the record map handled by this package.
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

func stringMapValue(values map[string]any, key string) string {
	return stringValue(mapValue(values, key))
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

// firstNonEmpty returns the non empty handled by this package.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// firstNonNil returns the non nil handled by this package.
func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func xmlEscape(value string) string {
	var builder strings.Builder
	if err := xml.EscapeText(&builder, []byte(value)); err != nil {
		return value
	}
	return builder.String()
}

func isUIViewDBUnavailable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "initialized runtime database")
}

func runtimeIDList(ids []string, args []any, kwargs map[string]any) ([]string, error) {
	if len(ids) > 0 {
		return append([]string(nil), ids...), nil
	}
	if len(args) > 0 {
		switch typed := args[0].(type) {
		case []any:
			out := make([]string, 0, len(typed))
			for _, item := range typed {
				id, ok := uiViewIDFromAny(item)
				if !ok {
					log.Errorf("ui.view requires valid ids")
					return nil, fmt.Errorf("ui.view requires valid ids")
				}
				out = append(out, fmt.Sprintf("%d", id))
			}
			return out, nil
		case []string:
			return append([]string(nil), typed...), nil
		}
	}
	if kwargs != nil {
		if value, ok := kwargs["ids"].([]string); ok {
			return append([]string(nil), value...), nil
		}
	}
	log.Errorf("ui.view requires ids")
	return nil, fmt.Errorf("ui.view requires ids")
}

func boolMapValue(values map[string]any, key string) bool {
	raw, ok := values[key]
	if !ok {
		return false
	}
	typed, ok := raw.(bool)
	return ok && typed
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

func recordIDs(rows []orm.RecordMap) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if id, ok := int64FromAny(row["id"]); ok && id > 0 {
			out = append(out, fmt.Sprintf("%d", id))
		}
	}
	return out
}
