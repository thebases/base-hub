package nodes

// File overview:
// - Responsibility: shared param/payload helpers reused by every node's action code
//   (JSON decoding, param coercion, deep-cloning). Not itself a node.
// - Key symbols: `decodeJSONObject`, `actionObjectParam`, `firstStringParam`, `cloneJSONObject`.

import (
	"encoding/json"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/services/log"
)

// decodeJSONObject decodes the json object handled by this package.
func decodeJSONObject(payload []byte) (map[string]any, error) {
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		log.Errorf("expected JSON object payload: %v", err)
		return nil, fmt.Errorf("expected JSON object payload: %w", err)
	}
	if decoded == nil {
		log.Errorf("expected JSON object payload")
		return nil, fmt.Errorf("expected JSON object payload")
	}
	return decoded, nil
}

func actionObjectParam(params map[string]any, keys ...string) (map[string]any, bool, error) {
	for _, key := range keys {
		raw, exists := params[key]
		if !exists || raw == nil {
			continue
		}
		switch typed := raw.(type) {
		case map[string]any:
			return cloneJSONObject(typed), true, nil
		case string:
			if strings.TrimSpace(typed) == "" {
				continue
			}
			var decoded map[string]any
			if err := json.Unmarshal([]byte(typed), &decoded); err != nil {
				log.Errorf("params.%s must be a JSON object", key)
				return nil, true, fmt.Errorf("params.%s must be a JSON object", key)
			}
			return decoded, true, nil
		default:
			data, err := json.Marshal(typed)
			if err != nil {
				log.Errorf("params.%s must be a JSON object", key)
				return nil, true, fmt.Errorf("params.%s must be a JSON object", key)
			}
			var decoded map[string]any
			if err := json.Unmarshal(data, &decoded); err != nil {
				log.Errorf("params.%s must be a JSON object", key)
				return nil, true, fmt.Errorf("params.%s must be a JSON object", key)
			}
			return decoded, true, nil
		}
	}
	return nil, false, nil
}

func objectListFromAny(key string, raw any) ([]map[string]any, error) {
	switch typed := raw.(type) {
	case []map[string]any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, cloneJSONObject(item))
		}
		return out, nil
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			object, err := objectFromAny(key, item)
			if err != nil {
				return nil, err
			}
			out = append(out, object)
		}
		return out, nil
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil, nil
		}
		var decoded []map[string]any
		if err := json.Unmarshal([]byte(typed), &decoded); err != nil {
			log.Errorf("params.%s must be a JSON object array", key)
			return nil, fmt.Errorf("params.%s must be a JSON object array", key)
		}
		return decoded, nil
	default:
		object, err := objectFromAny(key, raw)
		if err != nil {
			return nil, err
		}
		return []map[string]any{object}, nil
	}
}

func objectFromAny(key string, raw any) (map[string]any, error) {
	switch typed := raw.(type) {
	case map[string]any:
		return cloneJSONObject(typed), nil
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			log.Errorf("params.%s must be a JSON object", key)
			return nil, fmt.Errorf("params.%s must be a JSON object", key)
		}
		var decoded map[string]any
		if err := json.Unmarshal(data, &decoded); err != nil {
			log.Errorf("params.%s must be a JSON object", key)
			return nil, fmt.Errorf("params.%s must be a JSON object", key)
		}
		return decoded, nil
	}
}

func firstStringParam(params map[string]any, keys ...string) string {
	for _, key := range keys {
		raw, exists := params[key]
		if !exists {
			continue
		}
		if value, ok := raw.(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func boolParamDefault(params map[string]any, key string, fallback bool) bool {
	raw, exists := params[key]
	if !exists {
		return fallback
	}
	switch typed := raw.(type) {
	case bool:
		return typed
	case string:
		trimmed := strings.TrimSpace(strings.ToLower(typed))
		if trimmed == "true" {
			return true
		}
		if trimmed == "false" {
			return false
		}
	}
	return fallback
}

func intParamDefault(params map[string]any, key string, fallback int) int {
	raw, exists := params[key]
	if !exists {
		return fallback
	}
	switch typed := raw.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return int(parsed)
		}
	case string:
		var decoded json.Number = json.Number(strings.TrimSpace(typed))
		if parsed, err := decoded.Int64(); err == nil {
			return int(parsed)
		}
	}
	return fallback
}

func stringSliceParam(params map[string]any, keys ...string) []string {
	for _, key := range keys {
		raw, exists := params[key]
		if !exists || raw == nil {
			continue
		}
		return stringSliceFromAny(raw)
	}
	return nil
}

// headersToAny converts an HTTP header map to map[string]any so it satisfies
// the object-shaped types normalizeObjectMap accepts when persisted as a
// base.workflow.outbound JSONB values field.
func headersToAny(headers map[string]string) map[string]any {
	out := make(map[string]any, len(headers))
	for key, value := range headers {
		out[key] = value
	}
	return out
}

// cloneJSONObject clones the json object handled by this package.
func cloneJSONObject(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = deepCloneJSONValue(value)
	}
	return out
}

func deepCloneJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneJSONObject(typed)
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, deepCloneJSONValue(item))
		}
		return out
	default:
		return typed
	}
}
