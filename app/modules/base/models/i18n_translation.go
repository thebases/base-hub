package models

// File overview:
// - Responsibility: implements the tbl.lang.get_terms RPC method for batch
//   translation lookups so the frontend can retrieve translated labels in one
//   call instead of one call per string.
// - Key symbols: getTermsResLang.
// - Why: a dedicated translations endpoint is used;
//   contract here keeps frontend code portable across both runtimes.

import (
	"context"
	"strings"

	"git.thebase.vn/base_hub/internal/i18n"
	"git.thebase.vn/base_hub/internal/orm"
)

// getTermsResLang translates a batch of source strings into the requested
// language and returns a map of msgid → msgstr for each requested term.
//
// RPC call shape (positional):
//
//	model:   "tbl.lang"
//	method:  "get_terms"
//	args:    [lang_code, ["term1", "term2", ...]]
//
// RPC call shape (keyword):
//
//	kwargs:  {lang: "vi", terms: ["term1", "term2", ...]}
//
// Response: {"term1": "translated1", "term2": "translated2", ...}
// Any term without a translation is echoed back as-is (source-language fallback).
func getTermsResLang(_ context.Context, _ *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	// Resolve language code from positional arg[0] or kwargs["lang"].
	lang := firstNonEmpty(
		strings.TrimSpace(stringArg(args, 0)),
		strings.TrimSpace(stringValue(kwargs["lang"])),
	)

	// Resolve term list from positional arg[1] or kwargs["terms"].
	var rawTerms any
	if len(args) > 1 {
		rawTerms = args[1]
	} else {
		rawTerms = kwargs["terms"]
	}

	msgids := stringSlice(rawTerms)
	if lang == "" || len(msgids) == 0 {
		return map[string]any{}, nil
	}

	// Translate all terms in one batch call (single read-lock acquisition).
	translated := i18n.DefaultRegistry().TranslateMany(lang, msgids)

	result := make(map[string]any, len(msgids))
	for idx, msgid := range msgids {
		result[msgid] = translated[idx]
	}
	return result, nil
}
