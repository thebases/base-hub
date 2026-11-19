package models

// File overview:
// - Responsibility: small value normalization helpers shared by the base-calendar models,
//   mirroring the equivalent helpers in modules/base-automation/models/helpers_runtime.go (each
//   module package owns its own copy since these are unexported and modules must not cross-import
//   each other's models packages).
// - Key symbols: `cloneRecordMap`, `companyIDValue`, `stringValue`, `int64FromAny`.

import (
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/utils/anyconv"
)

// cloneRecordMap returns a shallow copy so normalization never mutates the caller's map.
func cloneRecordMap(values orm.RecordMap) orm.RecordMap {
	cloned := make(orm.RecordMap, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
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

func stringValue(raw any) string {
	switch typed := raw.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
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
