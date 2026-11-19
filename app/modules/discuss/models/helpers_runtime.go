package models

// File overview:
// - Responsibility: shared RPC-argument parsing helpers for discuss's custom
//   RuntimeMethods (PostMessage/Subscribe/Unsubscribe/activity/canned-response
//   actions). Ownership: discuss module.
// - Key symbols: `mapValue`, `stringValue`, `int64PointerFromAny`.
// - Pattern: mirrors base-workflow's/base-calendar's own `helpers_runtime.go`
//   arg-parsing helpers — each module keeps its own small copy rather than a
//   shared internal/orm export, matching this repo's per-module convention.

import "git.thebase.vn/base_hub/internal/utils/anyconv"

// mapValue looks up key in a possibly-nil kwargs map.
func mapValue(values map[string]any, key string) any {
	if values == nil {
		return nil
	}
	return values[key]
}

// argValue returns args[index], or nil when index is out of range.
func argValue(args []any, index int) any {
	if index < 0 || index >= len(args) {
		return nil
	}
	return args[index]
}

// firstNonNil returns the first non-nil value among the arguments given, or
// nil if every one of them is nil.
func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

// stringPointerFromAny returns a *string for a non-empty trimmed string (via
// mail_sender.go's own stringValue), or nil when raw is empty/absent — the
// shape every optional text RPC field (Subject/Body/Summary/Note/...) needs
// so "not provided" and "explicitly cleared" both collapse to nil rather
// than a pointer to "".
func stringPointerFromAny(raw any) *string {
	value := stringValue(raw)
	if value == "" {
		return nil
	}
	return &value
}

// int64PointerFromAny resolves a many2one-shaped value (a bare id, the
// [id, display_name] tuple runtime reads return, or {"id": n}) into a *int64,
// returning nil for anything empty, unset or non-positive.
func int64PointerFromAny(raw any) *int64 {
	id, ok := anyconv.Many2OneID(raw)
	if !ok {
		return nil
	}
	return &id
}

// int64SliceFromAny normalizes an RPC list-of-ids value (many2many field
// shape) into a []int64, skipping anything that doesn't resolve to an id.
func int64SliceFromAny(raw any) []int64 {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]int64, 0, len(items))
	for _, item := range items {
		if value := int64PointerFromAny(item); value != nil {
			out = append(out, *value)
		}
	}
	return out
}
