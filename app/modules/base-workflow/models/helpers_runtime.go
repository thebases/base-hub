package models

// File overview:
// - Responsibility: defines the `workflow` module model contract for helpers runtime, including field metadata, runtime methods, and module-specific invariants.
// - Key symbols: `recordMapFromArgs`, `recordMapListFromArgs`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Follows a state-machine style flow where persisted workflow state determines whether execution starts fresh, resumes, retries, or hands work to another step.

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/google/uuid"
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
					log.Errorf("workflow model values list contains %T, want object", row)
					return nil, orm.InvalidParamsf("workflow model values list contains %T, want object", row)
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
					log.Errorf("workflow model values list contains %T, want object", row)
					return nil, orm.InvalidParamsf("workflow model values list contains %T, want object", row)
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

func int64FromNonNegativeAny(raw any) (int64, bool) {
	switch typed := raw.(type) {
	case int64:
		return typed, typed >= 0
	case int:
		return int64(typed), typed >= 0
	case int32:
		return int64(typed), typed >= 0
	case float64:
		value := int64(typed)
		return value, value >= 0
	case string:
		value, err := parseNonNegativeInt64(typed)
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

// parseNonNegativeInt64 parses the non negative int 64 handled by this package.
func parseNonNegativeInt64(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		log.Errorf("value is empty")
		return 0, orm.InvalidParamsf("value is empty")
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, err
	}
	if value < 0 {
		log.Errorf("value must be non-negative")
		return 0, orm.InvalidParamsf("value must be non-negative")
	}
	return value, nil
}

func int64Value(value any) int64 {
	if out, ok := int64FromAny(value); ok {
		return out
	}
	return 0
}

// companyIDValue stores an inherited company as a plain id (nil when shared): the guarded
// create/write company checks read company_id with a plain integer conversion, which would see
// a *int64 as "shared" and refuse the write for non-main-company callers.
func companyIDValue(companyID *int64) any {
	if companyID == nil {
		return nil
	}
	return *companyID
}

func int64PointerFromAny(raw any) *int64 {
	value, ok := int64PointerValue(raw)
	if !ok {
		return nil
	}
	return value
}

func int64PointerValue(raw any) (*int64, bool) {
	switch typed := raw.(type) {
	case nil:
		return nil, true
	case *int64:
		return typed, true
	case map[string]any:
		return int64PointerValue(firstNonNil(typed["id"], typed["value"]))
	case []any:
		if len(typed) == 0 {
			return nil, true
		}
		return int64PointerValue(typed[0])
	case [2]any:
		// RuntimeRepositorySearchRead/RuntimeRead expand many2one fields (e.g. catalog_id)
		// into a [2]any{id, displayName} tuple. Cloning a read row as a create payload
		// (workflow duplicate) hands this shape back in, so unwrap it like []any.
		return int64PointerValue(typed[0])
	case int64:
		value := typed
		return &value, true
	case int:
		value := int64(typed)
		return &value, true
	case float64:
		value := int64(typed)
		return &value, true
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return nil, true
		}
		value, ok := int64FromString(trimmed)
		if !ok {
			return nil, false
		}
		return &value, true
	default:
		return nil, false
	}
}

func stringSliceFromAny(raw any) []string {
	switch typed := raw.(type) {
	case nil:
		return []string{}
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text := strings.TrimSpace(stringValue(item))
			if text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		text := strings.TrimSpace(stringValue(raw))
		if text == "" {
			return []string{}
		}
		parts := strings.Split(text, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
		return out
	}
}

func int64FromString(raw string) (int64, bool) {
	parsed, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func boolValue(value any) bool {
	if out, ok := value.(bool); ok {
		return out
	}
	return false
}

func normalizeUIActionContext(raw any) (map[string]any, error) {
	return normalizeObjectMap(raw)
}

func normalizeObjectMap(raw any) (map[string]any, error) {
	switch typed := raw.(type) {
	case nil:
		return map[string]any{}, nil
	case map[string]any:
		return typed, nil
	case orm.RecordMap:
		return map[string]any(typed), nil
	case string:
		// Accept JSON-editor submissions that still arrive as strings so object-backed
		// JSONB fields round-trip safely through generic textarea-based clients.
		return normalizeJSONStringObject(typed)
	default:
		log.Errorf("must be an object")
		return nil, orm.InvalidParamsf("must be an object")
	}
}

// It uses TrimSpace and Unmarshal to accept stringified JSON objects while still
// rejecting arrays, scalars, and malformed payloads for object-only fields.
func normalizeJSONStringObject(raw string) (map[string]any, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return map[string]any{}, nil
	}

	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		log.Errorf("must be an object")
		return nil, orm.InvalidParamsf("must be an object")
	}

	object, ok := decoded.(map[string]any)
	if !ok {
		log.Errorf("must be an object")
		return nil, orm.InvalidParamsf("must be an object")
	}
	return object, nil
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

func timestampedUUID4RequestID(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "request"
	}
	return fmt.Sprintf("%s-%d-%s", prefix, time.Now().UTC().UnixMilli(), uuid.NewString())
}

func timestampedUUID4Name(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "name"
	}
	return fmt.Sprintf("%s-%d-%s", prefix, time.Now().UTC().UnixMilli(), uuid.NewString())
}

func uuid4String() string {
	return uuid.NewString()
}
