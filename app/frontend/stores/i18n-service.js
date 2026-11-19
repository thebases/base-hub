// Port of frontend-admin's stores/i18n-store.ts, without Zustand — a plain
// module-level cache keyed by lang, refilled from tbl.lang.get_terms.
//
// seedModuleTranslations() is a no-op here: frontend-admin's version merges
// every registered module's static translations (modules/registry.ts), which
// has no equivalent yet on this tree (modules/knowledge, modules/website,
// modules/workflow are still unbuilt — see the rebuild plan's Phase 4/5/7).
// Wire this up once a module registry exists on this side.

import { getTerms } from "@/services/api/jsonv2.js";
import { DEFAULT_LANG, normalizeLang } from "@/services/i18n/lang.js";
import { authStore } from "./auth-service.js";

// TranslationCache: lang -> (msgid -> msgstr).
const cache = {};

/** Fetches missing translations for lang and merges them into the cache. */
export async function loadI18nTermsForLang(lang, msgids) {
  if (!lang || msgids.length === 0) return;
  const missing = msgids.filter((id) => !(id in (cache[lang] ?? {})));
  if (missing.length === 0) return;
  const fetched = await getTerms(lang, missing);
  // Merge into the live bucket after the await: overlapping batched fetches
  // (dom-translator flushes) must not overwrite each other's results.
  cache[lang] = Object.assign(cache[lang] ?? {}, fetched);
}

/** Pre-fetches translations for the given terms using the current session's lang. */
export async function loadI18nTerms(msgids) {
  const { uiLang, lang: userLang } = authStore.getState();
  const lang = normalizeLang(uiLang || userLang) || DEFAULT_LANG;
  return loadI18nTermsForLang(lang, msgids);
}

/**
 * t(msgid) returns the cached translation for the current session's lang, or
 * the msgid itself if not (yet) translated — rendering is never blocked.
 */
export function t(msgid) {
  const { uiLang, lang: userLang } = authStore.getState();
  const lang = normalizeLang(uiLang || userLang) || DEFAULT_LANG;
  return cache[lang]?.[msgid] ?? msgid;
}

/** lookupTerm returns the cached translation, or undefined when never fetched for lang. */
export function lookupTerm(lang, msgid) {
  return cache[lang]?.[msgid];
}

// TODO(Phase 4/5/7): once a module registry exists, merge each module's
// static translations here the way frontend-admin's seedModuleTranslations() does.
function seedModuleTranslations(_lang) {}
