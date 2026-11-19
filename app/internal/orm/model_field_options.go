package orm

// File overview:
// - Responsibility: implements the metadata-driven ORM/runtime kernel for model field options, where storage, runtime dispatch, and security rules stay centralized.
// - Key symbols: `parseTagOptions`, `parseFieldMetaOptions`, `fieldTagOptions`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.

import (
	"reflect"
	"strings"
)

type fieldTagOptions map[string]string

// parseTagOptions reads the compact meta-tag format used across orm metadata.
// It supports both boolean-style flags like `filter` and key/value pairs like
// `label:Bank BIC`.
func parseTagOptions(tag string) fieldTagOptions {
	options := make(fieldTagOptions)
	if strings.TrimSpace(tag) == "" {
		return options
	}

	// Walk each split(tag, ",") entry in order so validation and side effects stay scoped to the current element.
	parts := strings.Split(tag, ",")
	for index, rawPart := range parts {
		part := strings.TrimSpace(rawPart)
		if part == "" {
			continue
		}

		// `help:` is free prose that may contain commas, so it is the last key
		// and consumes the rest of the tag; splitting it would cut the text and
		// turn its fragments into bogus flags.
		if help, ok := strings.CutPrefix(part, "help:"); ok {
			rest := strings.Join(append([]string{help}, parts[index+1:]...), ",")
			options["help"] = strings.TrimSpace(rest)
			break
		}

		if key, value, ok := strings.Cut(part, ":"); ok {
			options[strings.TrimSpace(key)] = strings.TrimSpace(value)
			continue
		}

		options[part] = "true"
	}
	return options
}

// parseFieldMetaOptions reads the canonical `meta` tag while temporarily
// tolerating the legacy `opt` tag during the rename migration.
func parseFieldMetaOptions(field reflect.StructField) fieldTagOptions {
	tag := strings.TrimSpace(field.Tag.Get("meta"))
	if tag == "" {
		tag = strings.TrimSpace(field.Tag.Get("opt"))
	}
	return parseTagOptions(tag)
}

// firstNonEmpty returns the first non-blank candidate after trimming so tag
// fallbacks can stay deterministic even when one source is present but empty.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// parseBoolOption resolves boolean-style meta values while preserving support
// for bare flags such as `filter` or `indexed`.
func parseBoolOption(options fieldTagOptions, key string) bool {
	if value, ok := options[key]; ok {
		return value == "" || strings.EqualFold(strings.TrimSpace(value), "true")
	}
	if value, ok := options[strings.ToLower(key)]; ok {
		return value == "" || strings.EqualFold(strings.TrimSpace(value), "true")
	}
	return false
}

// resolveFieldKindOption reads the runtime field-family override from the meta
// DSL. `kind:` is the canonical key; `type:` remains a temporary fallback so
// older in-repo or user-authored models do not break during migration.
func resolveFieldKindOption(options fieldTagOptions) string {
	if value, ok := options["kind"]; ok {
		return strings.TrimSpace(value)
	}
	if value, ok := options["type"]; ok {
		return strings.TrimSpace(value)
	}
	return ""
}

// parseStoredOption resolves the meta-tagged stored contract while preserving
// one2many/many2many as runtime-only fields unless authors explicitly opt in.
func parseStoredOption(kind FieldKind, options fieldTagOptions) bool {
	value, ok := options["stored"]
	if !ok {
		return kind != FieldKindOneToMany && kind != FieldKindManyToMany
	}
	return !strings.EqualFold(strings.TrimSpace(value), "false")
}

// resolveFilterKey maps the meta filter declaration to the public filter alias
// while keeping `filter:false` as an explicit opt-out.
func resolveFilterKey(name string, options fieldTagOptions) string {
	if !hasTagOption(options, "filter") {
		return ""
	}

	filterValue := strings.TrimSpace(options["filter"])
	switch strings.ToLower(filterValue) {
	case "", "true":
		return name
	case "false", "-":
		return ""
	default:
		return filterValue
	}
}

// hasTagOption checks the exact and lowercase keys so helpers remain tolerant
// of older callers that may normalize lookup names differently.
func hasTagOption(options fieldTagOptions, key string) bool {
	if _, ok := options[key]; ok {
		return true
	}
	_, ok := options[strings.ToLower(key)]
	return ok
}

// parseBunTagOptions reads Bun's compact tag format while preserving the fact
// that the first segment may be either a column name or a key/value option such
// as `table:base_users` or `rel:belongs-to`.
func parseBunTagOptions(tag string) fieldTagOptions {
	options := make(fieldTagOptions)
	if strings.TrimSpace(tag) == "" {
		return options
	}

	parts := strings.Split(tag, ",")
	if len(parts) == 0 {
		return options
	}
	first := strings.TrimSpace(parts[0])
	if first != "" {
		if key, value, ok := strings.Cut(first, ":"); ok {
			options[strings.TrimSpace(key)] = strings.TrimSpace(value)
		} else {
			options["__name__"] = first
		}
	}
	// Walk each parts[1: entry in order so validation and side effects stay scoped to the current element.
	for _, rawPart := range parts[1:] {
		part := strings.TrimSpace(rawPart)
		if part == "" {
			continue
		}
		if key, value, ok := strings.Cut(part, ":"); ok {
			options[strings.TrimSpace(key)] = strings.TrimSpace(value)
			continue
		}
		options[part] = "true"
	}
	return options
}
