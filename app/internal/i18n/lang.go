package i18n

// File overview:
// - Responsibility: canonical language-code format for the whole app.
// - Key symbols: NormalizeLang.
// - Why: UI, config, .po filenames and RPC context historically mixed "vi",
//   "vi_VN", "en-US" and "en_US". Every edge now collapses to one lowercase
//   `<language>_<country>` form ("vi_vn", "en_gb") so lookups never miss on case
//   or separator.

import "strings"

// defaultCountry gives a bare language its default region so "vi" and "vi_vn"
// address the same translations. Unlisted bare codes are returned unchanged.
var defaultCountry = map[string]string{
	"en": "us", "vi": "vn", "ja": "jp", "jp": "jp", "ko": "kr", "zh": "cn",
	"fr": "fr", "de": "de", "es": "es", "it": "it", "ru": "ru", "th": "th",
	"id": "id", "ms": "my", "km": "kh", "lo": "la", "pt": "br",
}

// languageAlias fixes non-ISO language subtags that appear in config
// ("jp" is the country code; the language subtag is "ja").
var languageAlias = map[string]string{"jp": "ja"}

// NormalizeLang returns the canonical `<language>_<country>` lowercase code.
// Empty input stays empty. Script subtags (zh_hant_tw) are kept as-is.
func NormalizeLang(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	if code == "" {
		return ""
	}
	code = strings.ReplaceAll(code, "-", "_")
	if !strings.Contains(code, "_") {
		country, ok := defaultCountry[code]
		if !ok {
			return code
		}
		if alias, aliased := languageAlias[code]; aliased {
			code = alias
		}
		return code + "_" + country
	}
	if lang, rest, _ := strings.Cut(code, "_"); languageAlias[lang] != "" {
		code = languageAlias[lang] + "_" + rest
	}
	return code
}

// LangForCountry returns the canonical language code spoken in an ISO 3166
// country ("VN" -> "vi_vn", "US" -> "en_us"), or "" when the country has no
// known default language. It is the inverse of defaultCountry.
func LangForCountry(countryCode string) string {
	cc := strings.ToLower(strings.TrimSpace(countryCode))
	if cc == "" {
		return ""
	}
	for lang, country := range defaultCountry {
		if country == cc && languageAlias[lang] == "" {
			return lang + "_" + cc
		}
	}
	return ""
}
