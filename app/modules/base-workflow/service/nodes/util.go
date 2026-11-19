package nodes

// File overview:
// - Responsibility: tiny standalone value-coercion helpers used across node files.
//   Duplicated from the models package (rather than exported/shared) to avoid widening that
//   package's public surface for one-line helpers — "a little copying is better than a little
//   dependency."

import (
	"strings"

	"github.com/google/uuid"
)

// stringValue coerces a value to a trimmed string; only string values coerce, everything else yields "".
func stringValue(raw any) string {
	if s, ok := raw.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

// stringSliceFromAny normalizes []string, []any, or a comma-separated string into a []string.
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

// uuid4String generates a random v4 UUID string.
func uuid4String() string {
	return uuid.NewString()
}
