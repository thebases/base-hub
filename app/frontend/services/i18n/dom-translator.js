// Global DOM translator: swaps rendered English text (text nodes plus
// placeholder/title/aria-label attributes) for the active UI language, using
// the server's .po registry via tbl.lang.get_terms. Templates stay untouched;
// only exact (trimmed) matches translate, and anything unknown stays English.
// Role next to Base UI's t() (frontend/i18n.js): t() translates strings authored in this SPA; this translator remains
// for server-driven text (view arch labels, field names, server messages) that has no t() call site.

import { isSourceLang, normalizeLang } from "./lang.js";
import { authStore } from "@/stores/auth-service.js";
import { loadI18nTermsForLang, lookupTerm } from "@/stores/i18n-service.js";

const ATTRS = ["placeholder", "title", "aria-label"];
const SKIP_SELECTOR = "script,style,pre,code,textarea,noscript,[data-no-i18n]";
const ATTR_SELECTOR = ATTRS.map((name) => `[${name}]`).join(",");
const MAX_LEN = 200;
// Source strings are English. Text with Latin letters outside plain ASCII (Vietnamese diacritics) was already translated
// by t() or the server, so it is never looked up again (saves a get_terms round trip per translated string).
const TRANSLATED = /[\u00C0-\u1EF9]/;
const FLUSH_MS = 80;

// Slots hold what we last wrote, so our own mutations are not re-processed.
const written = new WeakMap();
let pending = new Map(); // msgid -> [apply callbacks]
let timer = 0;
let retries = 0;

let langState = null;
let langCode = "";

// activeLang returns the canonical code ("vi_vn"), or "" when no translation is needed. The auth store
// replaces its state object on every change, so the result is reused until that happens (this runs per DOM node).
function activeLang() {
  const state = authStore.getState();
  if (state === langState) return langCode;
  const code = normalizeLang(state.uiLang || state.lang);
  langState = state;
  langCode = !code || isSourceLang(code) ? "" : code;
  return langCode;
}

function candidate(text) {
  const trimmed = text.trim();
  if (!trimmed || trimmed.length > MAX_LEN || !/[A-Za-z]{2}/.test(trimmed) || TRANSLATED.test(trimmed)) return "";
  return trimmed;
}

// skipped reports whether el sits inside markup that must never be translated (code, scripts, opted-out areas).
function skipped(el) {
  return el.closest(SKIP_SELECTOR) !== null;
}

// queue records a translate-on-arrival callback and schedules one batched fetch.
function queue(msgid, apply) {
  const list = pending.get(msgid) ?? [];
  list.push(apply);
  pending.set(msgid, list);
  if (!timer) timer = setTimeout(flush, FLUSH_MS);
}

async function flush() {
  timer = 0;
  const batch = pending;
  pending = new Map();
  const lang = activeLang();
  if (!lang) return;
  try {
    await loadI18nTermsForLang(lang, [...batch.keys()]);
  } catch (error) {
    // Not signed in yet or backend down: stay English and re-scan shortly.
    console.warn("[i18n] get_terms failed; will retry", error);
    if (retries++ < 5) setTimeout(() => document.body && walk(document.body), 1500 * retries);
    return;
  }
  retries = 0;
  for (const [msgid, applies] of batch) {
    const translated = lookupTerm(lang, msgid);
    if (translated && translated !== msgid) applies.forEach((apply) => apply(translated));
  }
}

// translateText translates one text node; `trusted` means walk() already proved nothing around it is skipped.
function translateText(node, trusted = false) {
  const lang = activeLang();
  if (!lang || !node.parentElement || (!trusted && skipped(node.parentElement))) return;
  const raw = node.nodeValue ?? "";
  if (written.get(node) === raw) return;
  const msgid = candidate(raw);
  if (!msgid) return;
  const apply = (translated) => {
    if (node.nodeValue !== raw) return; // re-rendered meanwhile
    const next = raw.replace(msgid, () => translated);
    written.set(node, next);
    node.nodeValue = next;
  };
  const known = lookupTerm(lang, msgid);
  if (known !== undefined) {
    if (known !== msgid) apply(known);
    return;
  }
  queue(msgid, apply);
}

function translateAttrs(el) {
  const lang = activeLang();
  if (!lang || skipped(el)) return;
  for (const name of ATTRS) {
    const raw = el.getAttribute(name);
    if (raw == null || written.get(el)?.[name] === raw) continue;
    const msgid = candidate(raw);
    if (!msgid) continue;
    const apply = (translated) => {
      if (el.getAttribute(name) !== raw) return;
      const store = written.get(el) ?? {};
      store[name] = translated;
      written.set(el, store);
      el.setAttribute(name, translated);
    };
    const known = lookupTerm(lang, msgid);
    if (known !== undefined) {
      if (known !== msgid) apply(known);
    } else {
      queue(msgid, apply);
    }
  }
}

// walk translates a newly added node and everything under it. Nothing to do for the English source language;
// otherwise only text nodes are visited, and only the few elements that carry a translatable attribute.
function walk(root) {
  if (!activeLang()) return;
  if (root.nodeType === 3) return translateText(root);
  if (root.nodeType !== 1) return;
  if (skipped(root)) return;
  if (root.matches(ATTR_SELECTOR)) translateAttrs(root);
  root.querySelectorAll(ATTR_SELECTOR).forEach(translateAttrs);
  // One check per added subtree instead of one ancestor lookup per text node: with no skip markup inside, every node is safe.
  const trusted = root.querySelector(SKIP_SELECTOR) === null;
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  for (let n = walker.nextNode(); n; n = walker.nextNode()) translateText(n, trusted);
}

/** startDomTranslator begins observing the document; safe to call once at boot. */
export function startDomTranslator() {
  const observer = new MutationObserver((records) => {
    for (const rec of records) {
      if (rec.type === "childList") rec.addedNodes.forEach(walk);
      else if (rec.type === "characterData") translateText(rec.target);
      else if (rec.type === "attributes") translateAttrs(rec.target);
    }
  });
  const begin = () => {
    walk(document.body);
    observer.observe(document.body, {
      childList: true,
      subtree: true,
      characterData: true,
      attributes: true,
      attributeFilter: ATTRS,
    });
  };
  if (document.body) begin();
  else document.addEventListener("DOMContentLoaded", begin, { once: true });
}
