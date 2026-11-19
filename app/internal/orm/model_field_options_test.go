package orm

// File overview:
// - Responsibility: verifies ORM runtime semantics, repository behavior, and registry coordination for model field options.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down runtime invariants so refactors preserve transaction, RBAC, registry, and storage behavior.

// These tests document the supported field-option grammar and protect the
// normalization rules used by metadata extraction.

import "testing"

func TestParseTagOptionsSupportsFlagsAndKeyValuePairs(t *testing.T) {
	options := parseTagOptions("filter,label:Bank BIC,indexed,help:Used for lookup")

	if options["filter"] != "true" {
		t.Fatalf("expected bare filter flag to default to true, got %q", options["filter"])
	}
	if options["label"] != "Bank BIC" {
		t.Fatalf("expected label option to be parsed, got %q", options["label"])
	}
	if options["help"] != "Used for lookup" {
		t.Fatalf("expected help option to be parsed, got %q", options["help"])
	}
	if options["indexed"] != "true" {
		t.Fatalf("expected indexed flag to default to true, got %q", options["indexed"])
	}
}

// help is free prose: it is the last key and keeps its commas instead of
// leaking fragments as flags.
func TestParseTagOptionsHelpConsumesRestOfTag(t *testing.T) {
	options := parseTagOptions("index,label:Type,help:Values: hmac_key, aes_key, token, other.")

	if got, want := options["help"], "Values: hmac_key, aes_key, token, other."; got != want {
		t.Fatalf("help = %q, want %q", got, want)
	}
	if options["index"] != "true" || options["label"] != "Type" {
		t.Fatalf("keys before help lost: %#v", options)
	}
	for _, leaked := range []string{"aes_key", "token", "other."} {
		if _, ok := options[leaked]; ok {
			t.Fatalf("help fragment %q leaked as a flag: %#v", leaked, options)
		}
	}
}

func TestParseBoolOptionSupportsExplicitFalseAndBareFlags(t *testing.T) {
	options := parseTagOptions("filter,indexed:false")

	if !parseBoolOption(options, "filter") {
		t.Fatalf("expected bare filter flag to resolve to true")
	}
	if parseBoolOption(options, "indexed") {
		t.Fatalf("expected indexed:false to resolve to false")
	}
	if parseBoolOption(options, "missing") {
		t.Fatalf("expected missing bool option to resolve to false")
	}
}

func TestParseStoredOptionDefaultsByFieldKind(t *testing.T) {
	if !parseStoredOption(FieldKindText, parseTagOptions("")) {
		t.Fatalf("expected scalar fields to default to stored=true")
	}
	if !parseStoredOption(FieldKindText, parseTagOptions("stored:true")) {
		t.Fatalf("expected stored:true to remain true")
	}
	if parseStoredOption(FieldKindText, parseTagOptions("stored:false")) {
		t.Fatalf("expected stored:false to disable storage")
	}
	if parseStoredOption(FieldKindOneToMany, parseTagOptions("")) {
		t.Fatalf("expected one2many fields without stored option to default to false")
	}
	if parseStoredOption(FieldKindManyToMany, parseTagOptions("")) {
		t.Fatalf("expected many2many fields without stored option to default to false")
	}
}

func TestResolveFieldKindOptionPrefersKindAndFallsBackToLegacyType(t *testing.T) {
	if got := resolveFieldKindOption(parseTagOptions("kind:html")); got != "html" {
		t.Fatalf("expected kind override to resolve to html, got %q", got)
	}
	if got := resolveFieldKindOption(parseTagOptions("type:json")); got != "json" {
		t.Fatalf("expected legacy type override to resolve to json, got %q", got)
	}
	if got := resolveFieldKindOption(parseTagOptions("kind:html,type:text")); got != "html" {
		t.Fatalf("expected canonical kind override to win, got %q", got)
	}
}

func TestResolveFilterKeyHonorsOptSemantics(t *testing.T) {
	if got := resolveFilterKey("bank_bic", parseTagOptions("")); got != "" {
		t.Fatalf("expected missing filter option to disable alias lookup, got %q", got)
	}
	if got := resolveFilterKey("bank_bic", parseTagOptions("filter")); got != "bank_bic" {
		t.Fatalf("expected bare filter flag to use field name, got %q", got)
	}
	if got := resolveFilterKey("bank_bic", parseTagOptions("filter:bic")); got != "bic" {
		t.Fatalf("expected filter alias to use explicit value, got %q", got)
	}
	if got := resolveFilterKey("bank_bic", parseTagOptions("filter:false")); got != "" {
		t.Fatalf("expected filter:false to suppress alias lookup, got %q", got)
	}
}
