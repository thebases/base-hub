// Port of frontend-admin's stores/auth-store.ts. There is no Zustand here, so
// this is a tiny hand-rolled observable store persisted to sessionStorage
// under the same "basehub-auth" key jsonv2.js's readPersistedAuthState() and
// the sw.js version-check flow already expect — same storage contract,
// different implementation of the store itself.

import { normalizeLang } from "@/services/i18n/lang.js";
import { syncCatalogLanguage } from "@/i18n.js";
import {
  getPinnedRpcDatabaseName,
  getSessionInfo,
  oauthRefreshToken,
  oauthRevokeToken,
  registerAuthRefreshHandler,
  setRpcDatabase,
  setRpcSessionContext,
  setRpcToken,
  UserV2Rpc,
} from "@/services/api/jsonv2.js";
import { DEFAULT_DB_NAME } from "@/services/api/config.js";

const STORAGE_KEY = "basehub-auth";
// Refresh proactively this long before the access token actually expires, so
// a page reload just inside the window doesn't have to wait for a 401 round trip.
const REFRESH_SKEW_MS = 30_000;

function defaultState() {
  return {
    username: "",
    accessToken: "",
    refreshToken: "",
    accessTokenExpiresAt: null,
    db: getPinnedRpcDatabaseName() || DEFAULT_DB_NAME,
    uid: null,
    dbHeaderName: "X-Database-Name",
    // Company scope (Odoo-style multi-company): authorizedCompanyIds/companies are what the
    // user may pick in the navbar switcher (server-collapsed set from /oauth/me);
    // allowedCompanyIds is the active selection sent as context.allowed_company_ids (reads see
    // those companies + shared rows); currentCompanyId is context.company_id (stamped on create).
    authorizedCompanyIds: [],
    companies: [],
    allowedCompanyIds: [],
    currentCompanyId: null,
    lang: "en_us",
    // IANA display timezone of the user's address (state, else country); see services/server-time.js.
    timezone: "",
    // uiLang is the language explicitly picked in the shell switcher; empty =
    // follow `lang`, the user's own language (derived from their country).
    uiLang: "",
  };
}

function readPersisted() {
  try {
    const raw = sessionStorage.getItem(STORAGE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw);
    return parsed && typeof parsed.state === "object" ? parsed.state : null;
  } catch {
    return null;
  }
}

function persist(state) {
  try {
    sessionStorage.setItem(STORAGE_KEY, JSON.stringify({ state, version: 0 }));
  } catch {
    // sessionStorage unavailable (private mode, quota) — best effort only.
  }
}

function accessTokenExpiryFromNow(expiresIn) {
  return Date.now() + Math.max(expiresIn, 0) * 1000;
}

function normalizeCompanyIDs(companyIDs) {
  const seen = new Set();
  return (companyIDs || []).filter((companyID) => {
    if (!Number.isFinite(companyID) || companyID <= 0 || seen.has(companyID)) {
      return false;
    }
    seen.add(companyID);
    return true;
  });
}

// normalizeCompanies keeps {id, name} options for authorized ids only, in the server's order.
function normalizeCompanies(companies, authorizedCompanyIds) {
  if (!Array.isArray(companies)) return [];
  const seen = new Set();
  return companies.flatMap((company) => {
    const id = Number(company?.id);
    if (!authorizedCompanyIds.includes(id) || seen.has(id)) return [];
    seen.add(id);
    return [{ id, name: String(company?.name ?? id) }];
  });
}

function normalizeCurrentCompanyID(companyID, allowedCompanyIds) {
  if (typeof companyID === "number" && Number.isFinite(companyID) && companyID > 0 && allowedCompanyIds.includes(companyID)) {
    return companyID;
  }
  return allowedCompanyIds[0] ?? null;
}

function buildRpcSessionContext(currentCompanyId, allowedCompanyIds, uiLang = "") {
  const context = {};
  if (uiLang) {
    context.lang = uiLang;
  }
  if (allowedCompanyIds.length > 0) {
    const orderedCompanyIDs = currentCompanyId
      ? [currentCompanyId, ...allowedCompanyIds.filter((id) => id !== currentCompanyId)]
      : allowedCompanyIds;
    context.allowed_company_ids = orderedCompanyIDs;
  }
  if (currentCompanyId) {
    context.company_id = currentCompanyId;
  }
  return context;
}

class AuthStore {
  constructor() {
    this.state = { ...defaultState(), ...(readPersisted() ?? {}) };
    this.listeners = new Set();
    this.#syncDocumentLang();
    this.#rehydrate();
  }

  getState() {
    return this.state;
  }

  subscribe(listener) {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  #set(partial) {
    this.state = { ...this.state, ...partial };
    persist(this.state);
    this.#syncDocumentLang();
    // Signed-out visitors keep the browser/last-used language; only a signed-in user or an explicit pick drives it.
    if (this.state.accessToken || this.state.uiLang) syncCatalogLanguage(this.#activeLang());
    this.listeners.forEach((listener) => listener(this.state));
  }

  // #syncDocumentLang mirrors the active UI language onto <html lang> (BCP 47 "vi-vn") for a11y/hyphenation.
  #syncDocumentLang() {
    if (typeof document === "undefined") return;
    const lang = this.#activeLang().replace(/_/g, "-");
    if (lang) document.documentElement.lang = lang;
  }

  // #activeLang is the language every RPC carries: the switcher choice, else the user's own.
  #activeLang() {
    return normalizeLang(this.state.uiLang || this.state.lang);
  }

  #rehydrate() {
    setRpcToken(this.state.accessToken ?? "");
    setRpcDatabase(getPinnedRpcDatabaseName() || this.state.db || DEFAULT_DB_NAME);
    setRpcSessionContext(buildRpcSessionContext(this.state.currentCompanyId ?? null, this.state.allowedCompanyIds ?? [], this.#activeLang()));
    // Refresh timezone and country-derived language on every load so an address change applies.
    if (this.state.accessToken) {
      void this.loadTimezone();
    }

    // A tab reopened after the short-TTL access token expired but within the
    // long-TTL refresh token window would otherwise look logged out until the
    // first API call's 401 triggers the reactive refresh path.
    if (
      this.state.refreshToken &&
      typeof this.state.accessTokenExpiresAt === "number" &&
      Date.now() >= this.state.accessTokenExpiresAt - REFRESH_SKEW_MS
    ) {
      void this.performTokenRefresh();
    }
  }

  setOAuthTokens(username, accessToken, refreshToken, expiresIn, options = {}) {
    const db = options.db || DEFAULT_DB_NAME;
    const authorizedCompanyIds = normalizeCompanyIDs(options.allowedCompanyIds ?? []);
    const currentCompanyId = normalizeCurrentCompanyID(options.currentCompanyId, authorizedCompanyIds);
    // A fresh login starts in the user's own company only (as Odoo does); the navbar
    // switcher widens the active set within authorizedCompanyIds.
    const allowedCompanyIds = currentCompanyId ? [currentCompanyId] : authorizedCompanyIds;
    this.#set({
      username,
      accessToken,
      refreshToken,
      accessTokenExpiresAt: accessTokenExpiryFromNow(expiresIn),
      db,
      uid: options.uid ?? null,
      dbHeaderName: options.dbHeaderName || "X-Database-Name",
      authorizedCompanyIds,
      companies: normalizeCompanies(options.companies, authorizedCompanyIds),
      allowedCompanyIds,
      currentCompanyId,
      lang: normalizeLang(options.lang) || "en_us",
      // A fresh login follows the user's own language; drop any earlier switcher choice.
      uiLang: "",
      // Reset so a previous user's zone never leaks; the login page awaits loadTimezone().
      timezone: "",
    });
    setRpcToken(accessToken);
    setRpcDatabase(db);
    setRpcSessionContext(buildRpcSessionContext(currentCompanyId, allowedCompanyIds, this.#activeLang()));
  }

  applyTokenPair(accessToken, refreshToken, expiresIn) {
    this.#set({ accessToken, refreshToken, accessTokenExpiresAt: accessTokenExpiryFromNow(expiresIn) });
    setRpcToken(accessToken);
  }

  // authorizedCompanyIds is the switcher's choosable set; sessions persisted before it existed
  // fall back to their allowedCompanyIds (which then still held the full authorized set).
  authorizedCompanyIds() {
    return this.state.authorizedCompanyIds?.length ? this.state.authorizedCompanyIds : this.state.allowedCompanyIds ?? [];
  }

  // setCompanyContext applies a switcher choice: currentCompanyId becomes context.company_id and
  // options.allowedCompanyIds (default: the current selection) the active set, both kept inside
  // the authorized set (the server rejects anything else with "access to unauthorized company").
  setCompanyContext(currentCompanyId, options = {}) {
    const authorized = this.authorizedCompanyIds();
    const allowedCompanyIds = normalizeCompanyIDs(options.allowedCompanyIds?.length ? options.allowedCompanyIds : this.state.allowedCompanyIds)
      .filter((id) => authorized.length === 0 || authorized.includes(id));
    const nextCurrentCompanyId = normalizeCurrentCompanyID(currentCompanyId, allowedCompanyIds);
    setRpcSessionContext(buildRpcSessionContext(nextCurrentCompanyId, allowedCompanyIds, this.#activeLang()));
    this.#set({ currentCompanyId: nextCurrentCompanyId, allowedCompanyIds });
  }

  // loadCompanies fills the switcher's {id, name} options from GET /oauth/me for sessions
  // persisted before login stored them. Best effort: on failure the switcher stays hidden.
  async loadCompanies() {
    const { accessToken, db } = this.state;
    if (!accessToken) return;
    try {
      const info = await getSessionInfo(accessToken, db);
      const userContext = info?.result?.user_context ?? {};
      const authorizedCompanyIds = normalizeCompanyIDs(userContext.allowed_company_ids ?? []);
      if (authorizedCompanyIds.length === 0) return;
      this.#set({ authorizedCompanyIds, companies: normalizeCompanies(userContext.allowed_companies, authorizedCompanyIds) });
    } catch {
      // Keep the switcher hidden.
    }
  }

  // setUiLanguage records the switcher choice and re-applies the RPC context so
  // every later call carries context.lang (consumed by the ORM/i18n layer).
  setUiLanguage(uiLang) {
    const next = normalizeLang(uiLang);
    setRpcSessionContext(buildRpcSessionContext(this.state.currentCompanyId ?? null, this.state.allowedCompanyIds ?? [], next));
    this.#set({ uiLang: next });
  }

  // loadTimezone fetches the display timezone via base.users/context_get and stores it.
  // Best effort: without it times simply render in the browser's zone.
  async loadTimezone() {
    try {
      const data = await UserV2Rpc.call("context_get");
      const zone = String(data?.result?.timezone ?? data?.timezone ?? "").trim();
      if (zone && zone !== this.state.timezone) {
        this.#set({ timezone: zone });
      }
      // The user's language follows their country (see base.users/context_get).
      const lang = normalizeLang(data?.result?.lang ?? data?.lang);
      if (lang && lang !== this.state.lang) {
        this.#set({ lang });
        setRpcSessionContext(buildRpcSessionContext(this.state.currentCompanyId ?? null, this.state.allowedCompanyIds ?? [], this.#activeLang()));
      }
    } catch {
      // Leave timezone empty; the display zone falls back to the browser.
    }
  }

  clearAuth() {
    const { refreshToken, db } = this.state;
    this.#set(defaultState());
    setRpcToken("");
    setRpcDatabase(getPinnedRpcDatabaseName() || DEFAULT_DB_NAME);
    setRpcSessionContext({});
    if (refreshToken) {
      // Best-effort per RFC 7009 §2.2: don't block logout UI on this.
      void oauthRevokeToken(refreshToken, db);
    }
  }

  // performTokenRefresh is both the proactive-on-rehydrate path above and the
  // reactive handler jsonv2.js's BaseJsonV2Rpc.post()/get() calls on a 401 —
  // registered once below so jsonv2.js never has to import this store.
  async performTokenRefresh() {
    const { refreshToken, db } = this.state;
    if (!refreshToken) {
      return false;
    }
    try {
      const pair = await oauthRefreshToken(refreshToken, db);
      this.applyTokenPair(pair.access_token, pair.refresh_token, pair.expires_in);
      return true;
    } catch {
      // Refresh token expired/revoked/reused: the session cannot be recovered.
      this.clearAuth();
      return false;
    }
  }
}

export const authStore = new AuthStore();
registerAuthRefreshHandler(() => authStore.performTokenRefresh());
