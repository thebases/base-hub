package apidoc

// File overview:
// - Responsibility: resolves the display language of API documents (`<module>/<lang>/...`) and reads a document in that language with an English fallback.
// - Key symbols: `Languages`, `DefaultLanguage`, `NormalizeLanguage`, `ContextWithLanguage`, `LanguageFromContext`, `ReadDocument`.
// - Pattern: the language travels in the request context so Service method signatures stay stable; `DescriptionFile` references stay language-neutral ("edc/x.md").

import (
	"context"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
)

// Language describes one documentation language: Code is the `<module>/<code>` folder name.
type Language struct {
	Code  string `json:"code"`
	Label string `json:"label"`
}

// DefaultLanguage is shown on first start and is the fallback when a translation file is missing.
const DefaultLanguage = "en"

// Languages lists the supported documentation languages; add `<lang>` folders under each module's documents and an entry here to add one.
var Languages = []Language{
	{Code: "en", Label: "English"},
	{Code: "vn", Label: "Tiếng Việt"},
}

type languageKey struct{}

// NormalizeLanguage maps user input ("VN", "vi", "vi-VN", "en-US") to a supported code, or DefaultLanguage when unknown.
func NormalizeLanguage(input string) string {
	code := strings.ToLower(strings.TrimSpace(input))
	if i := strings.IndexAny(code, "-_"); i > 0 {
		code = code[:i]
	}
	if code == "vi" {
		code = "vn"
	}
	for _, lang := range Languages {
		if lang.Code == code {
			return code
		}
	}
	return DefaultLanguage
}

// ContextWithLanguage returns ctx carrying the normalized documentation language.
func ContextWithLanguage(ctx context.Context, lang string) context.Context {
	return context.WithValue(ctx, languageKey{}, NormalizeLanguage(lang))
}

// LanguageFromContext returns the language stored by ContextWithLanguage, or DefaultLanguage.
func LanguageFromContext(ctx context.Context) string {
	if lang, ok := ctx.Value(languageKey{}).(string); ok && lang != "" {
		return lang
	}
	return DefaultLanguage
}

// ReadDocument reads ref ("<module>/x.md", e.g. "edc/x.md" or "base/README.md") from
// <module>/<lang>/x.md (root documents, then the module's apidoc), falling back to the default
// language and finally to the bare ref so flat (non-localized) roots keep working.
func ReadDocument(lang, ref string) (string, bool) {
	lang = NormalizeLanguage(lang)
	var candidates []string
	if module, rest, ok := strings.Cut(strings.TrimPrefix(ref, "/"), "/"); ok {
		candidates = append(candidates, module+"/"+lang+"/"+rest)
		if lang != DefaultLanguage {
			candidates = append(candidates, module+"/"+DefaultLanguage+"/"+rest)
		}
	}
	candidates = append(candidates, ref)
	for _, candidate := range candidates {
		if body, ok := orm.ReadDescriptionFile(candidate); ok {
			return body, true
		}
	}
	return "", false
}
