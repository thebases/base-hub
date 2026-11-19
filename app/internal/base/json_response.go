package base

// File overview:
// - Responsibility: captures shared API response and constant primitives for json response, keeping cross-cutting behavior consistent across routes.
// - Key symbols: `JSONMarshal`, `normalizeJSONNulls`.
// - Pattern: Keeps one focused responsibility per file so adjacent packages can reuse this logic without depending on unrelated transport or persistence details.

import "encoding/json"

// JSONMarshal keeps the app's JSON transport behavior in one place so Fiber
// can normalize API payloads consistently for every c.JSON(...) response.
func JSONMarshal(v any) ([]byte, error) {
	payload, err := json.Marshal(TimesToUnix(v))
	if err != nil {
		return nil, err
	}

	var decoded any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil, err
	}
	return json.Marshal(normalizeJSONNulls(decoded))
}

func normalizeJSONNulls(v any) any {
	switch typed := v.(type) {
	case nil:
		return false
	case map[string]any:
		for key, value := range typed {
			typed[key] = normalizeJSONNulls(value)
		}
		return typed
	case []any:
		for i, value := range typed {
			typed[i] = normalizeJSONNulls(value)
		}
		return typed
	default:
		return v
	}
}
