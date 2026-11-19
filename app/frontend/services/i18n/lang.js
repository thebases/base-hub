// Canonical language-code format: lowercase `<language>_<country>` ("vi_vn",
// "en_gb"). Mirrors internal/i18n/lang.go NormalizeLang; keep the two in sync.

const DEFAULT_COUNTRY = {
  en: "us", vi: "vn", ja: "jp", jp: "jp", ko: "kr", zh: "cn", fr: "fr", de: "de",
  es: "es", it: "it", ru: "ru", th: "th", id: "id", ms: "my", km: "kh", lo: "la", pt: "br",
};

// DEFAULT_LANG is the last-resort UI language when neither the switcher choice
// nor the user's own language (country-derived) is known.
export const DEFAULT_LANG = "en_us";

const normalized = new Map();

/** normalizeLang returns the canonical code, or "" for empty input. */
export function normalizeLang(code) {
  const key = String(code ?? "");
  let result = normalized.get(key);
  if (result === undefined) {
    result = canonicalLang(key);
    normalized.set(key, result);
  }
  return result;
}

// canonicalLang does the actual normalization; normalizeLang memoizes it because the DOM translator calls it per node.
function canonicalLang(input) {
  const value = input.trim().toLowerCase().replace(/-/g, "_");
  if (!value) return "";
  const [language, ...rest] = value.split("_");
  const lang = language === "jp" ? "ja" : language;
  if (rest.length === 0) {
    const country = DEFAULT_COUNTRY[language];
    return country ? `${lang}_${country}` : value;
  }
  return [lang, ...rest].join("_");
}

/** isSourceLang reports whether code is English, the language the UI strings are authored in. */
export function isSourceLang(code) {
  return normalizeLang(code).split("_")[0] === "en";
}
