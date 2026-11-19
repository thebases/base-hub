// Base UI gettext service (nodexui/i18n, see docs/i18n.md) wired into the app. This file is imported first by main.js and
// makes translation available everywhere without per-module imports:
//
//   - `t(text, params?)` is a global function (globalThis.t), so any frontend JS file can call t("Save") directly.
//   - `tHtml(text, params?)` is the same for texts that carry trusted markup (<strong>, <code>) in the msgid; values in
//     params are HTML-escaped and the result is an Owl markup() string for t-out.
//   - Both are also defined on Component.prototype, because Owl resolves template expressions against the component
//     instance (`t-esc="t('Save')"`, `label="t('Email')"`), not against window.
//
// Catalogs: gettext `.po` files beside each frontend module, named exactly by the language code configured below
// (`<folder>/i18n/vi_vn.po`; the codes are the app's canonical ll_cc codes, so there is no code mapping anywhere); every folder in CATALOG_DIRS is
// loaded here, in parallel, before any other module evaluates (top-level await). English (the source language) loads
// nothing. A folder that does not exist on this branch simply 404s, which Base UI ignores.
//
// Two translation paths coexist: strings authored in this app go through t(); server-driven text (view arch labels,
// field names) still goes through tbl.lang.get_terms (stores/i18n-service.js + services/i18n/dom-translator.js).
//
// The language is owned by authStore (switcher choice, else the user's own language). Base UI picks its language once
// per page load, so before init() we mirror that choice into its storage key, and syncCatalogLanguage() reloads once
// when the choice changes without a reload (a login as a user whose language differs).

import { Component, markup } from "nodexui/owl";
import { init, loadCatalog, currentLanguage, languages, t, translateHead } from "nodexui/i18n";
import { normalizeLang } from "@/services/i18n/lang.js";

const STORAGE_KEY = "basehub-lang";
const AUTH_KEY = "basehub-auth";
const RELOAD_GUARD_KEY = "basehub-lang-reload";
const DEFAULT_LANGUAGE = "en_us";

// Folders (relative to this file) that hold an i18n/<code>.po catalog.
const CATALOG_DIRS = [
  "./",
  "./modules/base/",
  "./modules/base-bus/",
  "./modules/knowledge/",
  "./modules/website/",
  "./modules/workflow/",
  "./modules/edc/",
];

// persistedCode reads the signed-in language straight from the persisted auth state, so this module does not
// import the auth store (which imports it) and the answer is available synchronously at boot.
function persistedCode() {
  try {
    const state = JSON.parse(sessionStorage.getItem(AUTH_KEY) ?? "null")?.state;
    if (!state || (!state.accessToken && !state.uiLang)) return "";
    return normalizeLang(state.uiLang || state.lang);
  } catch {
    return "";
  }
}

function remember(code) {
  try {
    localStorage.setItem(STORAGE_KEY, code);
  } catch {
    // Storage blocked: Base UI falls back to the browser language for this visit.
  }
}

function clearReloadGuard() {
  try {
    sessionStorage.removeItem(RELOAD_GUARD_KEY);
  } catch {
    // ignore
  }
}

const escapeHtml = (value) =>
  String(value).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);

// tHtml translates a msgid that contains trusted markup. Only the msgid/translation is trusted; params are escaped.
function tHtml(text, params) {
  const safe = params ? Object.fromEntries(Object.entries(params).map(([key, value]) => [key, escapeHtml(value)])) : undefined;
  return markup(t(text, safe));
}

for (const [name, fn] of [["t", t], ["tHtml", tHtml]]) {
  globalThis[name] = fn;
  Object.defineProperty(Component.prototype, name, { value: fn, writable: true, configurable: true });
}

const initial = persistedCode();
if (initial) remember(initial);

init({
  languages: [
    { code: "en_us", label: "English" },
    { code: "vi_vn", label: "Tiếng Việt" },
  ],
  defaultLanguage: DEFAULT_LANGUAGE,
  storageKey: STORAGE_KEY,
});

// The first loadCatalog() call picks the language synchronously; the rest are plain parallel fetches.
await Promise.all(CATALOG_DIRS.map((dir) => loadCatalog(new URL(dir, import.meta.url))));
translateHead();

/**
 * syncCatalogLanguage is called by the auth store whenever the effective UI language may have changed. It records
 * the choice for the next page load and, when the loaded catalog differs, reloads once (guarded against loops).
 */
export function syncCatalogLanguage(activeLang) {
  const wanted = normalizeLang(activeLang);
  if (!wanted || !languages().some((language) => language.code === wanted)) return;
  remember(wanted);
  if (wanted === currentLanguage()) {
    clearReloadGuard();
    return;
  }
  try {
    if (sessionStorage.getItem(RELOAD_GUARD_KEY) === wanted) return;
    sessionStorage.setItem(RELOAD_GUARD_KEY, wanted);
  } catch {
    return;
  }
  // Deferred so the caller (e.g. the login flow) can finish navigating before the page reloads.
  setTimeout(() => window.location.reload(), 250);
}

export { currentLanguage, languages, t, tHtml };
