package routes

// File overview:
// - Responsibility: request path/args parsing and normalization helpers for the rpc route surface (legacy id-arg stripping, path id parsing, generic RPC value coercion).
// - Key symbols: `stripLegacyRPCIDArg`, `parseRPCPathIDs`, `anyToRPCID`, `cloneAnyMap`.

import (
	"fmt"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/services/log"
)

// It uses the resolved runtime method metadata so transport normalization stays
// aligned with model-specific overrides instead of a global method-name list.
func shouldStripLegacyRPCIDArg(pathCarriesID, methodUsesPathID bool, ids []string, stripped bool) bool {
	if !stripped {
		return false
	}
	if len(ids) == 0 {
		// Keep stripping empty legacy placeholders such as [] that older RPC
		// callers prepend ahead of create/get_view-style positional args.
		return true
	}
	// Only strip non-empty id prefixes for routes that actually normalize ids
	// out of the transport layer. Methods like ui.view.get_combined_arch expect
	// a real positional id list in args[0] and must keep it intact.
	return pathCarriesID || methodUsesPathID
}

// stripLegacyRPCIDArg detects the legacy JSON-RPC ids prefix without
// misclassifying search domains like [["name", "=", "A"]] as record IDs.
func stripLegacyRPCIDArg(args []any) ([]string, bool) {
	if len(args) == 0 {
		return nil, false
	}

	rawList, ok := args[0].([]any)
	if !ok {
		return nil, false
	}
	if len(rawList) == 0 {
		return []string{}, true
	}
	if looksLikeRPCDomain(rawList) {
		return nil, false
	}

	ids := make([]string, 0, len(rawList))
	for _, item := range rawList {
		id, ok := anyToRPCID(item)
		if !ok {
			return nil, false
		}
		ids = append(ids, id)
	}
	return ids, true
}

func looksLikeRPCDomain(rawList []any) bool {
	if len(rawList) == 0 {
		return false
	}
	first, ok := rawList[0].([]any)
	if !ok {
		return false
	}
	return len(first) >= 2
}

// parseRPCPathIDs parses the rpc path i ds handled by this package.
func parseRPCPathIDs(raw string, allowZero bool) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		log.Errorf("path id is required")
		return nil, fmt.Errorf("path id is required")
	}

	parts := strings.Split(raw, ",")
	ids := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			log.Errorf("path id must not contain empty values")
			return nil, fmt.Errorf("path id must not contain empty values")
		}
		if _, err := strconv.ParseInt(value, 10, 64); err != nil {
			log.Errorf("path id %q must be a positive integer", value)
			return nil, fmt.Errorf("path id %q must be a positive integer", value)
		}
		if (!allowZero && value == "0") || strings.HasPrefix(value, "-") {
			log.Errorf("path id %q must be a positive integer", value)
			return nil, fmt.Errorf("path id %q must be a positive integer", value)
		}
		ids = append(ids, value)
	}
	return ids, nil
}

func methodAllowsZeroPathID(method string) bool {
	switch strings.TrimSpace(method) {
	case "has_access", "check_access_rights":
		return true
	default:
		return false
	}
}

func anyToRPCID(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return "", false
		}
		if _, err := strconv.ParseInt(trimmed, 10, 64); err != nil {
			return "", false
		}
		if trimmed == "0" || strings.HasPrefix(trimmed, "-") {
			return "", false
		}
		return trimmed, true
	case float64:
		if typed <= 0 || typed != float64(int64(typed)) {
			return "", false
		}
		return strconv.FormatInt(int64(typed), 10), true
	case int:
		if typed <= 0 {
			return "", false
		}
		return strconv.Itoa(typed), true
	case int64:
		if typed <= 0 {
			return "", false
		}
		return strconv.FormatInt(typed, 10), true
	default:
		return "", false
	}
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// cloneAnyMap clones the any map handled by this package.
func cloneAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
