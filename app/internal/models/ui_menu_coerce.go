package models

// File overview:
// - Responsibility: value coercion helpers (time, int64, bool, action map) used by the ui.menu row and tree builders.
// - Key symbols: `timeValue`, `optionalInt64FromRecord`, `optionalInt64FromAny`, `int64Value`.

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// timeValue accepts the ORM-managed timestamp shapes used by runtime reads so
// cache revision tracking stays resilient in both tests and live storage reads.
func timeValue(value any) time.Time {
	switch typed := value.(type) {
	case time.Time:
		return typed
	case *time.Time:
		if typed != nil {
			return *typed
		}
	}
	return time.Time{}
}

func optionalInt64FromRecord(values orm.RecordMap, key string) (*int64, bool, error) {
	// Report whether the key was present separately from whether it resolved to a
	// non-nil integer so callers can distinguish "clear parent" from "unchanged".
	raw, ok := values[key]
	if !ok {
		return nil, false, nil
	}
	value, _, err := optionalInt64FromAny(raw)
	return value, true, err
}

func optionalInt64FromAny(value any) (*int64, bool, error) {
	// Treat nil, empty string, and zero as "no relation" so RPC callers can
	// clear parent_id without having to know the storage representation.
	if value == nil {
		return nil, false, nil
	}
	switch typed := value.(type) {
	case bool:
		if !typed {
			return nil, false, nil
		}
		log.Errorf("ui.menu invalid integer value %T", value)
		return nil, false, fmt.Errorf("ui.menu invalid integer value %T", value)
	case int64:
		if typed <= 0 {
			return nil, false, nil
		}
		return &typed, true, nil
	case int:
		if typed <= 0 {
			return nil, false, nil
		}
		out := int64(typed)
		return &out, true, nil
	case float64:
		if typed <= 0 {
			return nil, false, nil
		}
		out := int64(typed)
		return &out, true, nil
	case string:
		text := strings.TrimSpace(typed)
		if text == "" || text == "0" {
			return nil, false, nil
		}
		out, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			log.Errorf("ui.menu invalid integer value %q", typed)
			return nil, false, fmt.Errorf("ui.menu invalid integer value %q", typed)
		}
		if out <= 0 {
			return nil, false, nil
		}
		return &out, true, nil
	case [2]any:
		return optionalInt64FromAny(typed[0])
	case []any:
		if len(typed) == 0 {
			return nil, false, nil
		}
		return optionalInt64FromAny(typed[0])
	default:
		log.Errorf("ui.menu invalid integer value %T", value)
		return nil, false, fmt.Errorf("ui.menu invalid integer value %T", value)
	}
}

func int64Value(value any) int64 {
	// Reuse the tolerant converter when the caller prefers a zero default.
	if out, ok := int64FromAny(value); ok {
		return out
	}
	return 0
}

func boolValue(value any) bool {
	// Only true booleans should serialize as active to avoid surprising truthy
	// coercions from loosely typed payloads.
	if out, ok := value.(bool); ok {
		return out
	}
	return false
}

func canonicalActionURL(value any) string {
	// Extract the URL from a canonical action map when callers need a simple
	// route string for sidebar navigation or fallback normalization.
	action, err := actionMapValue(value)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(stringValue(action["url"]))
}

func actionMapValue(value any) (map[string]any, error) {
	// Accept both plain maps and orm.RecordMap because runtime rows may expose
	// JSON columns using either representation.
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case map[string]any:
		return typed, nil
	case orm.RecordMap:
		return map[string]any(typed), nil
	default:
		log.Errorf("ui.menu action must be an object")
		return nil, fmt.Errorf("ui.menu action must be an object")
	}
}
