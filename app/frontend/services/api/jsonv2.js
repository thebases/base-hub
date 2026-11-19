// Faithful JS port of frontend-admin's services/api/jsonv2.ts core RPC client
// (BaseJsonV2Rpc, OAuth token helpers, session/db-name resolution). Types are
// dropped since this is plain JS; behavior — envelope handling, one-shot 401
// refresh-and-retry, path-ID fallback, session-context injection — is kept
// identical so the backend sees the same request shapes from either frontend.
//
// The routed model/view helpers used by ModelView live at the bottom of this
// file so the no-bundler frontend can keep one RPC contract shared by list,
// form, kanban, and calendar views.

import { BACKEND_API_PREFIX, DEFAULT_DB_NAME } from "./config.js";
import { EXTRA_WINDOW_ACTION_VIEW_MODES, VIEW_TYPE_CONFIG, VIEW_TYPES, WINDOW_ACTION_VIEW_ORDER } from "@/modules/base/components/views/view-registry.js";

const baseURL = BACKEND_API_PREFIX;
const DEFAULT_RPC_DB_NAME = DEFAULT_DB_NAME;
const DEFAULT_SEARCH_READ_LIMIT = 80;
const RPC_DB_COOKIE_NAME = "basehub_db_name";
const RPC_DB_LOCK_COOKIE_NAME = "basehub_db_locked";

const REQUEST_TIMEOUT_MS = 15_000;
let currentRpcAccessToken = "";
let currentRpcDatabaseName = readPinnedRpcDatabaseName() || DEFAULT_RPC_DB_NAME;
let currentRpcSessionContext = {};
// Guard so concurrent 403 responses only emit one auth-invalid event instead
// of flooding the app's handler and causing a re-mount/request loop.
let authEventEmitted = false;

export const AUTH_INVALID_EVENT = "basehub:auth-invalid";

// ──────────────────────────────────────────────────────────────────────────
// OAuth 2.0 access-token refresh plumbing
// ──────────────────────────────────────────────────────────────────────────

// auth-service.js registers its refresh implementation here at module load,
// so a 401 hit anywhere in BaseJsonV2Rpc can trigger a silent refresh-and-retry
// without this module importing the auth store (which itself imports this module).
let authRefreshHandler = null;
let inFlightRefresh = null;

export function registerAuthRefreshHandler(handler) {
  authRefreshHandler = handler;
}

// Dedup guard: concurrent 401s from parallel in-flight requests share one
// refresh call instead of each racing their own grant_type=refresh_token call.
function runAuthRefresh() {
  if (!authRefreshHandler) {
    return Promise.resolve(false);
  }
  if (!inFlightRefresh) {
    inFlightRefresh = authRefreshHandler().finally(() => {
      inFlightRefresh = null;
    });
  }
  return inFlightRefresh;
}

// ──────────────────────────────────────────────────────────────────────────
// Internal helpers
// ──────────────────────────────────────────────────────────────────────────

function clone(v) {
  return Array.isArray(v) ? v.slice() : { ...v };
}

function readCookie(name) {
  if (typeof document === "undefined") {
    return "";
  }
  const prefix = `${name}=`;
  for (const part of document.cookie.split("; ")) {
    if (part.startsWith(prefix)) {
      return decodeURIComponent(part.slice(prefix.length));
    }
  }
  return "";
}

function readPinnedRpcDatabaseName() {
  if (readCookie(RPC_DB_LOCK_COOKIE_NAME) !== "1") {
    return "";
  }
  return readCookie(RPC_DB_COOKIE_NAME).trim();
}

// Reads the persisted auth-service snapshot for raw fetches that can fire
// before the app's rehydrate step has copied the token into this module.
function readPersistedAuthState() {
  if (typeof sessionStorage === "undefined") {
    return {};
  }
  const raw = sessionStorage.getItem("basehub-auth");
  if (!raw) {
    return {};
  }
  try {
    const parsed = JSON.parse(raw);
    const state = parsed && parsed.state ? parsed.state : {};
    return {
      accessToken: typeof state.accessToken === "string" ? state.accessToken : "",
      db: typeof state.db === "string" ? state.db : "",
    };
  } catch {
    return {};
  }
}

// Read the HTTP response body as parsed JSON; fall back to raw text so the
// caller always has something to inspect even on unexpected responses.
export async function readResponsePayload(response) {
  const text = await response.text();
  if (!text) return null;
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

function responsePayloadText(payload) {
  if (typeof payload === "string") {
    return payload;
  }
  try {
    return JSON.stringify(payload);
  } catch {
    return "";
  }
}

// Detect the backend's explicit "wrong RPC route shape" error so callers can
// retry the same method on the legacy body-ID path without masking real errors.
function shouldFallbackFromPathID(error) {
  const message = error instanceof Error ? error.message : String(error ?? "");
  return message.includes("uses /rpc/{model}/{method}, not /rpc/{model}/{method}/{id}");
}

export function shouldInvalidateSession(response) {
  // Only 401 (bearer token missing/invalid/expired) ends the session. 403 is
  // the backend's ordinary permission denial for one call and must not log the
  // user out. BaseJsonV2Rpc.request() already attempts one silent refresh
  // before a 401 reaches here, so a 401 at this point cannot be recovered.
  return response.status === 401;
}

// Typed error for an HTTP 403: the call was understood but the user lacks
// permission. Callers can test `err.permissionDenied` / `err.status === 403`.
class PermissionError extends Error {
  constructor(message, body) {
    super(message);
    this.name = "PermissionError";
    this.status = 403;
    this.permissionDenied = true;
    this.body = body;
  }
}

// Reads the integer error.code of a { error: { code, message } } body; undefined otherwise.
function rpcEnvelopeCode(data) {
  const code = data?.error?.code;
  return Number.isInteger(code) ? code : undefined;
}

// Read-only RPC method names; every other POST is treated as a possible write.
const READ_ONLY_RPC_METHODS = new Set([
  "search", "read", "search_read", "read_group", "name_search", "name_get",
  "fields_get", "search_count", "count", "default_get", "get_views", "load_views",
]);

// Says whether the call may change data, from the RPC method in the URL
// (/rpc/{model}/{method}[/{id}]); used to word an aborted-request message.
function isMutatingCall(method, url) {
  if (method === "GET") return false;
  const segments = String(url).split("?")[0].split("/").filter(Boolean);
  if (/^\d+$/.test(segments[segments.length - 1] ?? "")) segments.pop();
  return !READ_ONLY_RPC_METHODS.has(segments[segments.length - 1] ?? "");
}

export function emitInvalidAuthEvent() {
  if (typeof window === "undefined") return;
  if (authEventEmitted) return;
  authEventEmitted = true;
  window.dispatchEvent(new CustomEvent(AUTH_INVALID_EVENT));
}

function rpcHeaders(accessToken) {
  const headers = { "Content-Type": "application/json" };
  if (accessToken) {
    headers.Authorization = `Bearer ${accessToken}`;
  }
  const dbName = resolveRpcDatabaseName();
  if (dbName) {
    headers["X-Database-Name"] = dbName;
  }
  return headers;
}

// buildBackendURL resolves app-relative paths against the configured backend
// origin so non-RPC endpoints can share the same deployment target.
export function buildBackendURL(path) {
  return new URL(path, baseURL).toString();
}

// authenticatedHeaders exposes the active session headers for non-RPC fetches.
export function authenticatedHeaders(headers = {}) {
  return { ...rpcHeaders(resolveRpcAccessToken()), ...headers };
}

// Resolves the latest auth token at request time so module-level RPC clients
// created before auth-service hydration do not keep sending an empty Bearer header.
function resolveRpcAccessToken(instanceAccessToken = "") {
  if (currentRpcAccessToken) {
    return currentRpcAccessToken;
  }
  if (instanceAccessToken) {
    return instanceAccessToken;
  }
  const persistedAuth = readPersistedAuthState();
  const accessToken = persistedAuth.accessToken || "";
  if (accessToken) {
    setRpcToken(accessToken);
  }
  return accessToken;
}

// Resolves the active database alongside token fallback so stale RPC clients
// still honor a pinned or hydrated tenant database on their first request.
function resolveRpcDatabaseName() {
  const persistedAuth = currentRpcDatabaseName ? {} : readPersistedAuthState();
  const dbName = currentRpcDatabaseName || readPinnedRpcDatabaseName() || persistedAuth.db || DEFAULT_RPC_DB_NAME;
  if (dbName && dbName !== currentRpcDatabaseName) {
    setRpcDatabase(dbName);
  }
  return dbName;
}

// ──────────────────────────────────────────────────────────────────────────
// BaseJsonV2Rpc – per-model RPC client
// ──────────────────────────────────────────────────────────────────────────

export class BaseJsonV2Rpc {
  constructor(model, basePath = "/rpc") {
    this.model = model;
    this.basePath = basePath;
    this.accessToken = "";
    // 0 disables the abort timer for long-running calls (module reinit).
    this.timeoutMs = REQUEST_TIMEOUT_MS;
  }

  // Shared transport for post()/get(): builds auth headers, enforces a
  // 15-second timeout via AbortController, retries once after a silent token
  // refresh on 401, and maps the RPC error envelope { error: { code, message } }
  // to thrown Errors.
  async request(method, url, body) {
    const controller = new AbortController();
    const timeoutID = this.timeoutMs > 0 ? globalThis.setTimeout(() => controller.abort(), this.timeoutMs) : 0;
    const fullURL = new URL(url, baseURL).toString();
    const send = (accessToken) =>
      fetch(fullURL, {
        method,
        headers: rpcHeaders(accessToken),
        body: method === "GET" ? undefined : JSON.stringify(body ?? {}),
        signal: controller.signal,
      });

    try {
      const accessToken = resolveRpcAccessToken(this.accessToken);
      if (accessToken !== this.accessToken) {
        this.setAccessToken(accessToken);
      }
      let response = await send(accessToken);

      // Access token expired/invalid: try exactly one silent refresh, then
      // retry this request once with the new token before giving up.
      if (response.status === 401 && (await runAuthRefresh())) {
        const refreshedToken = resolveRpcAccessToken();
        this.setAccessToken(refreshedToken);
        response = await send(refreshedToken);
      }

      const data = await readResponsePayload(response);

      if (!response.ok) {
        console.error("Response error:", data);
        if (shouldInvalidateSession(response)) {
          emitInvalidAuthEvent();
        }
        const code = rpcEnvelopeCode(data);
        if (response.status === 403) {
          const denied = new PermissionError(t("You do not have permission to perform this action."), data);
          if (code !== undefined) denied.code = code;
          throw denied;
        }
        // The message text is kept as-is (callers may match it); status, the envelope code and the
        // body are also set as fields (like PermissionError) so callers need not parse the text.
        const httpErr = new Error(t("Response error: {status} - {body}", { status: response.status, body: JSON.stringify(data) }));
        httpErr.status = response.status;
        httpErr.body = data;
        if (code !== undefined) httpErr.code = code;
        throw httpErr;
      }

      // Map the RPC error envelope: { error: { code: number, message: string } }
      if (data?.error) {
        const { code, message } = data.error;
        const err = new Error(message);
        err.code = code;
        throw err;
      }

      if (!data) {
        throw new Error(t("Response error: empty response body"));
      }

      return data;
    } catch (e) {
      if (e?.name === "AbortError") {
        console.error("No response received:", e);
        // A timed-out write may still have committed on the server, so don't
        // claim "no response" there: tell the user to check before retrying.
        const mutating = isMutatingCall(method, url);
        const abortErr = new Error(
          mutating
            ? t("The server did not respond in time. The change may or may not have been saved; refresh and check before trying again.")
            : t("No response received from server"),
        );
        abortErr.transport = true;
        abortErr.outcomeUnknown = mutating;
        throw abortErr;
      }

      if (e instanceof Error && e.name !== "TypeError") {
        throw e;
      }

      console.error("Request setup error:", e.message);
      const setupErr = new Error(t("Request setup error: {message}", { message: e.message }));
      setupErr.transport = true;
      throw setupErr;
    } finally {
      if (timeoutID) globalThis.clearTimeout(timeoutID);
    }
  }

  // POST helper used by all model / db / meta call variants.
  async post(url, body) {
    return this.request("POST", url, body);
  }

  // GET helper for read-only endpoints (GET /rpc, GET /oauth/me).
  async get(url) {
    return this.request("GET", url);
  }

  // POST /rpc/{model}/{method}. Body: { args, kwargs }.
  async call(method, args = [], kwargs = {}) {
    return this.post(`${this.basePath}/${this.model}/${method}`, rpcCallBody(method, args, kwargs));
  }

  // POST /rpc/{model}/{method}/{id}. Record-bound variant.
  async callById(method, id, args = [], kwargs = {}) {
    return this.post(`${this.basePath}/${this.model}/${method}/${id}`, rpcCallBody(method, args, kwargs));
  }

  // callByIdFirst prefers the path-ID contract and only falls back when the
  // backend explicitly reports that the method still expects IDs in args[0].
  async callByIdFirst(method, id, args = [], fallbackArgs = args, kwargs = {}) {
    try {
      return await this.callById(method, id, args, kwargs);
    } catch (error) {
      if (!shouldFallbackFromPathID(error)) {
        throw error;
      }
      return this.call(method, fallbackArgs, kwargs);
    }
  }

  // POST /rpc/db/{method}. Body: { id?, params }.
  async callDb(method, params = {}, id) {
    return this.post(`/rpc/db/${method}`, { id, params });
  }

  // POST /rpc_meta/model/{method}. Body: { id?, params }.
  async callMeta(method, params = {}, id) {
    return this.post(`/rpc_meta/model/${method}`, { id, params });
  }

  // GET /rpc — RPC surface metadata.
  async getRpcRoot() {
    return this.get(this.basePath);
  }

  // search_read – fetch records matching a domain.
  async search(domain = [], fields = ["id", "name"], order = "id DESC", offset, limit, _withCompany = true, context) {
    const kwargs = { domain, fields };
    if (context) kwargs.context = context;
    if (order) kwargs.order = order;
    if (limit !== undefined) kwargs.limit = limit;
    if (offset !== undefined) kwargs.offset = offset;
    return this.call("search_read", [], kwargs);
  }

  // read_group – aggregate / group records.
  async read_group(domain = [], fields = [], groupby = "", orderby, _withCompany = true, offset, limit) {
    const kwargs = { domain, fields, groupby };
    if (orderby) kwargs.orderby = orderby;
    if (limit !== undefined) kwargs.limit = limit;
    if (offset !== undefined) kwargs.offset = offset;
    return this.call("read_group", [], kwargs);
  }

  async create(vals) {
    return this.call("create", [vals]);
  }

  async createValList(vals_list) {
    return this.call("create", [vals_list]);
  }

  // write – update records by IDs (path-ID first, comma-joined for many).
  async write(ids, vals) {
    return this.callByIdFirst("write", ids.join(","), [vals], [ids, vals]);
  }

  async writeValue(ids, values) {
    return this.call("write", [], { ids, values });
  }

  async delete(ids) {
    return this.callByIdFirst("unlink", ids.join(","), [], [ids]);
  }

  async read(ids, fields) {
    return this.callByIdFirst("read", ids.join(","), [fields], [ids, fields]);
  }

  async setActive(ids, active) {
    return this.call("write", [ids, { active }]);
  }

  // run – invoke a record-bound custom RuntimeMethod (path-ID first).
  async run(ids, method, vals) {
    return this.callByIdFirst(method, ids.join(","), [], [ids], vals);
  }

  async whoIsMerchant() {
    return this.post(`${this.basePath}/whoismerchant`, {});
  }

  checkAccessToken() {
    return this.accessToken !== undefined && this.accessToken !== "";
  }

  setAccessToken(accessToken) {
    this.accessToken = accessToken;
  }
}

// ──────────────────────────────────────────────────────────────────────────
// Standalone session helpers – no model context required
// ──────────────────────────────────────────────────────────────────────────

// POST /oauth/token (form-encoded, RFC 6749 §4/§5.1) — shared by every grant
// type below. Errors surface the backend's response body.
async function postOAuthToken(form) {
  const response = await fetch(new URL("/oauth/token", baseURL).toString(), {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: form.toString(),
  });
  const data = await readResponsePayload(response);
  if (!response.ok) {
    throw new Error(t("OAuth token request failed: {status} - {body}", { status: response.status, body: responsePayloadText(data) }));
  }
  return data;
}

// POST /oauth/token, grant_type=password — first-party login. Caller still
// needs a follow-up getSessionInfo() call since the token response carries
// only the token pair, not uid/username/user_context.
export async function oauthLoginPassword(params) {
  return postOAuthToken(
    new URLSearchParams({
      grant_type: "password",
      username: params.login,
      password: params.password,
      db: params.db,
    })
  );
}

// POST /oauth/token, grant_type=refresh_token — rotates the refresh token on every use.
export async function oauthRefreshToken(refreshToken, db) {
  return postOAuthToken(new URLSearchParams({ grant_type: "refresh_token", refresh_token: refreshToken, db }));
}

// POST /oauth/revoke — best-effort, fire-and-forget on logout.
export async function oauthRevokeToken(refreshToken, db) {
  try {
    await fetch(new URL("/oauth/revoke", baseURL).toString(), {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams({ refresh_token: refreshToken, db }).toString(),
    });
  } catch {
    // Best-effort per RFC 7009 §2.2: logout must not block on this.
  }
}

// GET /oauth/me — reports whether the supplied credentials are valid and,
// when they are, returns the resolved uid/username/user_context.
export async function getSessionInfo(accessToken, db = DEFAULT_RPC_DB_NAME) {
  const headers = rpcHeaders(accessToken);
  if (db) {
    headers["X-Database-Name"] = db;
  }
  const response = await fetch(new URL("/oauth/me", baseURL).toString(), { method: "GET", headers });
  if (!response.ok) {
    if (shouldInvalidateSession(response)) {
      emitInvalidAuthEvent();
    }
    throw new Error(t("Session info failed: {status}", { status: response.status }));
  }
  return response.json();
}

// ──────────────────────────────────────────────────────────────────────────
// Shared model RPC instances
// ──────────────────────────────────────────────────────────────────────────

export const UIMenu = new BaseJsonV2Rpc("ui.menu");
export const UserV2Rpc = new BaseJsonV2Rpc("base.users");
const PartnerV2Rpc = new BaseJsonV2Rpc("tbl.partner");
const CompanyV2Rpc = new BaseJsonV2Rpc("tbl.company");
const DefaultRecordV2Rpc = new BaseJsonV2Rpc("tbl.partner");
const UIViewV2Rpc = new BaseJsonV2Rpc("ui.view");
export const ResLangRpc = new BaseJsonV2Rpc("tbl.lang");
const SHARED_RPC_CLIENTS = [UIMenu, UserV2Rpc, PartnerV2Rpc, CompanyV2Rpc, DefaultRecordV2Rpc, UIViewV2Rpc, ResLangRpc];

// Creates a model-scoped RPC client that reuses the latest session token.
// Dynamic `/web/:actionModel` routes use this instead of hard-coded singletons.
export function createModelRpc(model) {
  const client = new BaseJsonV2Rpc(model);
  client.setAccessToken(currentRpcAccessToken);
  return client;
}

// Propagate a new access token to all shared model RPC instances at once.
// Call this after a successful POST /oauth/token response (login or refresh).
export function setRpcToken(accessToken = "") {
  currentRpcAccessToken = accessToken;
  // Reset the dedup guard only on a real login (non-empty token). When
  // clearing the token on logout, we must NOT reset it — concurrent in-flight
  // requests will receive 403 after clearAuth() runs, and resetting here
  // would allow them to re-dispatch the auth-invalid event, restarting the
  // logout/re-mount loop.
  if (accessToken) {
    authEventEmitted = false;
  }
  SHARED_RPC_CLIENTS.forEach((client) => client.setAccessToken(accessToken));
}

// getTerms calls tbl.lang.get_terms to batch-translate source strings into the
// requested language. Returns a map of msgid → msgstr; untranslated terms are
// echoed back as the original msgid.
export async function getTerms(lang, terms) {
  if (!lang || terms.length === 0) {
    return {};
  }
  const response = await ResLangRpc.call("get_terms", [lang, terms]);
  return response.result ?? {};
}

// setRpcDatabase keeps the shared transport headers aligned with the database
// name returned by /oauth/token or restored from persisted auth.
export function setRpcDatabase(dbName = DEFAULT_RPC_DB_NAME) {
  currentRpcDatabaseName = dbName;
}

export function getPinnedRpcDatabaseName() {
  return readPinnedRpcDatabaseName();
}

export function isRpcDatabasePinned() {
  return readPinnedRpcDatabaseName() !== "";
}

export function setRpcSessionContext(context = {}) {
  currentRpcSessionContext = clone(context);
}

// rpcCallBody builds the { args, kwargs } body shared by call()/callById().
function rpcCallBody(method, args, kwargs) {
  return { args, kwargs: withSessionContext(withDefaultSearchReadLimit(method, kwargs)) };
}

function withSessionContext(kwargs = {}) {
  const next = clone(kwargs ?? {});
  const context = { ...currentRpcSessionContext, ...(next.context ?? {}) };
  // No lang here is fine: the backend defaults to the signed-in user's own language.
  if (Object.keys(context).length > 0) {
    next.context = context;
  }
  return next;
}

function withDefaultSearchReadLimit(method, kwargs = {}) {
  if (method !== "search_read" || kwargs.limit !== undefined) {
    return kwargs;
  }
  return { ...kwargs, limit: DEFAULT_SEARCH_READ_LIMIT };
}

// ──────────────────────────────────────────────────────────────────────────
// Routed model/view helpers — SDUI: the window action + view arch + fields_get
// metadata drive what renders, no per-model page code. Ported from
// frontend-admin's jsonv2.ts "routed model/view helpers" section, MVP subset:
// list view only (the ModelView page's SDUI list renderer). Still NOT ported:
// form/kanban/calendar fallbacks, relation-choice hydration
// (fetchRelationChoices/searchRelationChoices), createRemoteOnchangeHandler —
// not needed for the list-only MVP.
// ──────────────────────────────────────────────────────────────────────────

const CRM_DEFAULT_ACTION_LIMIT = 80;

const FIELDS_CACHE_TTL_MS = 5000;
const rawFieldsCache = new Map();

// fetchRawFields shares one fields_get call between the callers that ask for the same model within a
// few seconds (the list view's arch lookup and its record fetch used to each pay a round trip).
function fetchRawFields(model) {
  const hit = rawFieldsCache.get(model);
  if (hit && Date.now() - hit.at < FIELDS_CACHE_TTL_MS) return hit.promise;
  const promise = createModelRpc(model)
    .call("fields_get")
    .then((response) => response.result ?? {});
  rawFieldsCache.set(model, { at: Date.now(), promise });
  promise.catch(() => rawFieldsCache.delete(model));
  return promise;
}

// fetchModelFields loads and normalizes model field metadata from RPC.
export async function fetchModelFields(model) {
  const rawFields = await fetchRawFields(model);
  return Object.fromEntries(Object.entries(rawFields).map(([fieldName, rawField]) => [fieldName, toFieldMeta(fieldName, rawField)]));
}

// toFieldMeta adapts generic fields_get metadata into the local FieldMeta shape.
function toFieldMeta(fieldName, rawField) {
  const kind = normalizeRuntimeToken(rawField.kind);
  const backendType = normalizeRuntimeToken(rawField.type);
  const type = normalizeFieldMetaType(kind, backendType);
  const selection = normalizeSelection(rawField.selection);
  return {
    type,
    kind,
    backendType,
    string: fieldsGetFieldLabel(fieldName, rawField),
    help: typeof rawField.help === "string" ? rawField.help : undefined,
    relation: typeof rawField.relation === "string" ? rawField.relation : undefined,
    required: rawField.required === true,
    readonly: rawField.readonly === true || rawField.read_only === true,
    // translate:true fields store one value per language (jsonb); the form shows a translation editor for them.
    translate: rawField.translate === true,
    // Non-stored compute/related fields hold no editable value; the form shows them as plain text.
    computed: rawField.stored === false && (Boolean(rawField.compute) || (Array.isArray(rawField.related) && rawField.related.length > 0)),
    // Mirrors the kernel's rejectComputedFieldValues: any compute field (stored or not, related
    // excluded) is refused on create/write, so the form never submits it. fields_get omits
    // `stored` when false (omitempty), so this keys on `compute` alone.
    serverComputed: Boolean(rawField.compute) && !(Array.isArray(rawField.related) && rawField.related.length > 0),
    searchable: rawField.stored !== false,
    sortable: rawField.stored !== false,
    selection,
    relation_field: typeof rawField.inverse === "string" ? rawField.inverse : undefined,
    has_onchange: rawField.has_onchange === true,
  };
}

function normalizeFieldMetaType(kind, backendType) {
  switch (kind) {
    case "boolean":
      return "boolean";
    case "date":
      return "date";
    case "datetime":
      return "datetime";
    case "selection":
      return "selection";
    case "many2one":
      return "many2one";
    case "many2many":
      return "many2many";
    case "one2many":
      return "one2many";
    case "one2one":
      return "many2one";
    case "float":
      return "float";
    case "integer":
    case "id":
      return "integer";
    case "json":
      return "text";
    case "text":
      return inferTypeFromBackendType(backendType);
  }

  switch (backendType) {
    case "boolean":
    case "char":
    case "date":
    case "datetime":
    case "float":
    case "html":
    case "integer":
    case "many2many":
    case "many2one":
    case "monetary":
    case "one2many":
    case "selection":
    case "text":
      return backendType;
    default:
      return inferTypeFromBackendType(backendType);
  }
}

// inferTypeFromBackendType maps a raw Go/backend type name (int64, time.time,
// json.RawMessage…) onto the FieldMeta type; anything unrecognized is "char".
function inferTypeFromBackendType(backendType) {
  if (isBooleanBackendType(backendType)) return "boolean";
  if (isDateTimeBackendType(backendType)) return "datetime";
  if (isDateBackendType(backendType)) return "date";
  if (isFloatBackendType(backendType)) return "float";
  if (isIntegerBackendType(backendType)) return "integer";
  if (isJSONBackendType(backendType)) return "text";
  return "char";
}

// normalizeRuntimeToken lowercases/trims a fields_get kind or type string;
// blank or non-string values become undefined.
function normalizeRuntimeToken(value) {
  if (typeof value !== "string") return undefined;
  const normalized = value.trim().toLowerCase();
  return normalized ? normalized : undefined;
}

function isIntegerBackendType(value) {
  if (!value) return false;
  return /^(u?int|int8|int16|int32|int64|uint8|uint16|uint32|uint64)$/.test(value);
}

function isFloatBackendType(value) {
  return value === "float32" || value === "float64";
}

function isBooleanBackendType(value) {
  return value === "bool" || value === "boolean";
}

function isDateBackendType(value) {
  return value === "date";
}

function isDateTimeBackendType(value) {
  return value === "datetime" || value === "time.time";
}

function isJSONBackendType(value) {
  if (!value) return false;
  return value === "json" || value.includes("json.") || value.includes("rawmessage") || value.startsWith("map[") || value === "map" || value.includes("recordmap");
}

// normalizeSelection accepts both tuple-style and object-style selection values.
function normalizeSelection(value) {
  if (!Array.isArray(value)) return undefined;
  const tuples = value.flatMap((item) => {
    if (Array.isArray(item) && item.length >= 2) {
      return [[String(item[0]), String(item[1])]];
    }
    if (typeof item === "object" && item !== null) {
      const rawValue = item.value;
      const rawLabel = item.label;
      if (rawValue !== undefined && rawValue !== null) {
        return [[String(rawValue), String(rawLabel ?? rawValue)]];
      }
    }
    return [];
  });
  return tuples.length > 0 ? tuples : undefined;
}

// fieldsGetFieldLabel: explicit labels win, then the field object's technical
// name, then the map key for older backends.
function fieldsGetFieldLabel(fieldName, rawField) {
  if (Object.hasOwn(rawField, "label")) return String(rawField.label ?? "");
  if (Object.hasOwn(rawField, "name")) return String(rawField.name ?? "");
  return fieldName;
}

function emptyToolbarActions() {
  return { action: [], print: [], relate: [] };
}

// firstResultRow returns the first row of an RPC list result, or undefined.
function firstResultRow(response) {
  return Array.isArray(response.result) ? response.result[0] : undefined;
}

// primaryViewIds remembers the view ids fetchWindowAction just resolved ("model:type" -> id, 0 = none),
// so the view-metadata fetch that follows skips a duplicate ui.view lookup; every window-action load refreshes it.
const primaryViewIds = new Map();

// fetchPrimaryUIViewID resolves the current primary view row for one type by
// querying ui.view directly instead of depending on a dedicated default_view RPC.
async function fetchPrimaryUIViewID(model, viewType) {
  const known = primaryViewIds.get(`${model}:${viewType}`);
  if (known !== undefined) return known || null;
  const response = await UIViewV2Rpc.search(
    [
      ["model", "=", model],
      ["type", "=", viewType],
    ],
    ["id"],
    "priority ASC, id ASC",
    0,
    1,
    false
  );
  const row = firstResultRow(response);
  const viewID = row && typeof row === "object" ? Number(row.id ?? 0) : 0;
  return viewID > 0 ? viewID : null;
}

// fetchUIViewRow reads one ui.view row by id.
async function fetchUIViewRow(id) {
  const response = await UIViewV2Rpc.search([["id", "=", id]], ["id", "name", "type", "statusbar_visible"], "priority ASC", 0, 1, false);
  const row = firstResultRow(response);
  if (!row || typeof row !== "object") {
    throw new Error(t("ui.view {id} not found", { id }));
  }
  return {
    id: Number(row.id ?? id),
    name: String(row.name ?? `View ${id}`),
    type: String(row.type ?? "form"),
    statusbar_visible: typeof row.statusbar_visible === "string" ? row.statusbar_visible : undefined,
  };
}

// fetchUIViewCombinedArch reads the effective arch string for one ui.view.
async function fetchUIViewCombinedArch(id) {
  const response = await UIViewV2Rpc.call("get_combined_arch", [[id]]);
  return String(response.result ?? "");
}

function humanizeModelName(model) {
  return model
    .split(".")
    .filter(Boolean)
    .map((segment) => segment[0]?.toUpperCase() + segment.slice(1))
    .join(" ");
}

// buildFallbackViewMetadata: when no ui.view arch row exists for a renderable
// view type, fall back to fields_get so the SDUI view can still render instead
// of failing. Kanban keeps only id/name so an arch-less board stays readable.
function buildFallbackViewMetadata(model, fields, viewType) {
  const labels = { list: "List", form: "Form", kanban: "Kanban", calendar: "Calendar" };
  let viewFields = fields;
  if (viewType === "kanban") {
    const minimal = Object.fromEntries(Object.entries(fields).filter(([name]) => name === "id" || name === "name"));
    viewFields = Object.keys(minimal).length > 0 ? minimal : fields;
  }
  return {
    name: `${humanizeModelName(model)} ${labels[viewType] ?? viewType}`,
    type: viewType,
    model,
    view_id: 0,
    arch: "",
    fields: viewFields,
    toolbar: emptyToolbarActions(),
    statusbar_visible: "",
  };
}

// FALLBACK_VIEW_TYPES is derived from view-registry.js's VIEW_TYPE_CONFIG
// instead of a separately hardcoded set, so adding a view type there also
// controls its arch-fallback eligibility here.
const FALLBACK_VIEW_TYPES = new Set(VIEW_TYPES.filter((viewType) => VIEW_TYPE_CONFIG.get(viewType)?.synthesizeArch));

// fetchModelSingleView composes one view payload from RPC view + field data.
async function fetchModelSingleView(model, fields, viewType, viewID) {
  const resolvedViewID = viewID ?? (await fetchPrimaryUIViewID(model, viewType));
  if (!resolvedViewID) {
    if (FALLBACK_VIEW_TYPES.has(viewType)) {
      return buildFallbackViewMetadata(model, fields, viewType);
    }
    throw new Error(t("No {viewType} view found for {model}", { viewType, model }));
  }

  const [viewRow, arch] = await Promise.all([fetchUIViewRow(resolvedViewID), fetchUIViewCombinedArch(resolvedViewID)]);

  return {
    name: viewRow.name,
    type: viewType,
    model,
    view_id: resolvedViewID,
    arch,
    fields,
    toolbar: emptyToolbarActions(),
    statusbar_visible: typeof viewRow.statusbar_visible === "string" ? viewRow.statusbar_visible : "",
  };
}

// fetchViewMetadataForType resolves a single view metadata entry by view type.
export async function fetchViewMetadataForType(viewType, model) {
  return fetchModelSingleView(model, await fetchModelFields(model), viewType);
}

// fetchModelSearchRead reads records through search_read RPC.
async function fetchModelSearchRead(model, domain, fieldNames, limit, offset, context) {
  const response = await createModelRpc(model).search(domain, fieldNames, "id DESC", offset, limit, false, context);
  return Array.isArray(response.result) ? response.result : [];
}

// fetchModelSearchCount reads the total matching count through RPC.
async function fetchModelSearchCount(model, domain, context) {
  const response = await createModelRpc(model).call("search_count", [], context ? { domain, context } : { domain });
  return Number(response.result ?? 0);
}

// fetchModelListRecords fetches the current list records + total count.
// `context` (e.g. { active_test: false }) lifts the server's default active=true filter.
export async function fetchModelListRecords({ model, limit = 80, offset = 0, domain = [], context } = {}) {
  const fields = await fetchModelFields(model);
  const fieldNames = Object.keys(fields);
  const [records, length] = await Promise.all([fetchModelSearchRead(model, domain, fieldNames, limit, offset, context), fetchModelSearchCount(model, domain, context)]);
  return { model, length, records, fields };
}

// fetchWindowAction builds a window action directly from view lookups (no
// sys.model.data / menu resolution needed) — the SDUI entry point for a
// /web/:actionModel route.
export async function fetchWindowAction(model) {
  // Membership (which types this model requests) is registry-driven via
  // includeInWindowAction; WINDOW_ACTION_VIEW_ORDER only fixes the ordering,
  // matching the previous hardcoded array's list/kanban/calendar/form order.
  const requestedTypes = [
    ...WINDOW_ACTION_VIEW_ORDER.filter((viewType) => VIEW_TYPE_CONFIG.get(viewType)?.includeInWindowAction(model)),
    "search",
  ];
  // One ui.view query for every requested type (rows come back in priority order, so the first row per type
  // is the primary view) instead of one round trip per type.
  const response = await UIViewV2Rpc.search(
    [
      ["model", "=", model],
      ["type", "in", requestedTypes],
    ],
    ["id", "type"],
    "priority ASC, id ASC",
    0,
    requestedTypes.length * 20,
    false
  );
  const primaryByType = new Map();
  for (const row of Array.isArray(response.result) ? response.result : []) {
    if (row && typeof row === "object" && Number(row.id) > 0 && !primaryByType.has(row.type)) primaryByType.set(row.type, Number(row.id));
  }
  for (const viewType of requestedTypes) primaryViewIds.set(`${model}:${viewType}`, primaryByType.get(viewType) ?? 0);
  const tuples = requestedTypes.flatMap((viewType) => {
    const viewID = primaryByType.get(viewType);
    if (viewID) return [[viewID, viewType]];
    return viewType === "list" ? [[0, "list"]] : [];
  });

  const searchTuple = tuples.find(([, type]) => type === "search");
  const renderableViewModes = tuples.map(([, type]) => type).filter((type) => type !== "search");

  return {
    type: "ui.actions.window",
    id: tuples[0]?.[0] ?? 0,
    name: humanizeModelName(model),
    res_model: model,
    target: "current",
    context: {},
    domain: [],
    views: tuples,
    view_mode: renderableViewModes.join(","),
    search_view_id: searchTuple ?? [0, "search"],
    help: "",
    limit: CRM_DEFAULT_ACTION_LIMIT,
  };
}

// viewModesFromWindowAction converts a window action's comma-separated
// view_mode into renderable modes.
const RENDERABLE_VIEW_MODES = new Set([...VIEW_TYPES, ...EXTRA_WINDOW_ACTION_VIEW_MODES]);

export function viewModesFromWindowAction(action) {
  return action.view_mode
    .split(",")
    .map((mode) => mode.trim())
    .filter((mode) => RENDERABLE_VIEW_MODES.has(mode));
}

// ──────────────────────────────────────────────────────────────────────────
// XMLID-resolved actions — arch buttons of type="action"/"client" carry a
// %(xmlid)d-style name instead of an object method. Ported from
// frontend-admin's jsonv2.ts extractArchButtonXMLID/fetchWindowActionByXMLID/
// fetchClientActionByXMLID, trimmed to what this tree's model-name-only
// routing needs: resolve res_model (window) or tag/params (client), no
// menu/view-id plumbing.
// ──────────────────────────────────────────────────────────────────────────

const SYS_MODEL_DATA_MODEL = "sys.model.data";
const UI_ACTION_WINDOW_MODEL = "ui.actions.window";
const UI_ACTION_CLIENT_MODEL = "ui.actions.client";
const WINDOW_ACTION_MODEL_ALIASES = new Set([UI_ACTION_WINDOW_MODEL, "ir.actions.act_window", "sys.actions.act_window"]);
const CLIENT_ACTION_MODEL_ALIASES = new Set([UI_ACTION_CLIENT_MODEL]);

// extractArchButtonXMLID unwraps the `%(module.xmlid)d` placeholder syntax
// arch buttons use for type="action"/"client" `name` attributes.
export function extractArchButtonXMLID(rawName) {
  const trimmed = rawName?.trim();
  if (!trimmed) return null;
  const placeholderMatch = trimmed.match(/^%\(([^)]+)\)d$/);
  if (placeholderMatch?.[1]) return placeholderMatch[1];
  return trimmed;
}

async function fetchActionReferenceByXMLID(xmlID) {
  const trimmedXMLID = xmlID.trim();
  if (!trimmedXMLID) throw new Error(t("Action XMLID is required"));
  const sysModelData = createModelRpc(SYS_MODEL_DATA_MODEL);
  const byCompleteName = await searchWindowActionModelData(sysModelData, [["complete_name", "=", trimmedXMLID]]);
  const row = byCompleteName ?? (!trimmedXMLID.includes(".") ? await searchWindowActionModelData(sysModelData, [["name", "=", trimmedXMLID]]) : undefined);
  if (!row || typeof row !== "object") throw new Error(t('Action XMLID "{xmlID}" was not found', { xmlID: trimmedXMLID }));
  const actionID = Number(row.model_id ?? 0);
  if (!Number.isFinite(actionID) || actionID <= 0) throw new Error(t('Action XMLID "{xmlID}" does not reference a valid action', { xmlID: trimmedXMLID }));
  return { id: actionID, model: String(row.model ?? "").trim(), xmlID: trimmedXMLID };
}

async function searchWindowActionModelData(client, domain) {
  return firstResultRow(await client.search(domain, ["id", "model", "model_id"], "id ASC", 0, 1, false));
}

// readActionRowByXMLID resolves an XMLID, checks it points at one of the
// accepted action models, then reads the requested columns of that row.
async function readActionRowByXMLID(xmlID, allowedModels, actionModel, fields) {
  const actionRef = await fetchActionReferenceByXMLID(xmlID);
  if (!allowedModels.has(actionRef.model)) {
    throw new Error(t('Action XMLID "{xmlID}" resolves to unsupported model "{model}"', { xmlID: actionRef.xmlID, model: actionRef.model }));
  }
  const response = await createModelRpc(actionModel).read([actionRef.id], fields);
  return { actionID: actionRef.id, row: firstResultRow(response) };
}

// fetchWindowActionByXMLID resolves a type="action" button to the model it
// targets, then reuses fetchWindowAction()'s own view-lookup path — this
// tree routes windows by model name only, so the ir.actions.act_window row's
// view_id/views JSON is not needed, just res_model.
export async function fetchWindowActionByXMLID(xmlID) {
  const { actionID, row } = await readActionRowByXMLID(xmlID, WINDOW_ACTION_MODEL_ALIASES, UI_ACTION_WINDOW_MODEL, ["res_model"]);
  const resModel = String(row?.res_model ?? "").trim();
  if (!resModel) throw new Error(t("ui.actions.window {id} is missing res_model", { id: actionID }));
  return fetchWindowAction(resModel);
}

// fetchClientActionByXMLID resolves a type="client" button's ui.actions.client
// row (tag/target/context/params) — the caller interprets `tag`.
export async function fetchClientActionByXMLID(xmlID) {
  const { actionID, row } = await readActionRowByXMLID(xmlID, CLIENT_ACTION_MODEL_ALIASES, UI_ACTION_CLIENT_MODEL, [
    "id",
    "name",
    "tag",
    "target",
    "sys_model",
    "context",
    "params",
  ]);
  if (!row || typeof row !== "object") throw new Error(t("ui.actions.client {id} not found", { id: actionID }));
  return normalizeClientActionRow(row, actionID);
}

// normalizeClientActionRow turns a ui.actions.client row (or the backend's
// _get_action_dict, which uses the same keys) into the shape runClientAction
// dispatches on.
function normalizeClientActionRow(row, fallbackID) {
  const tag = typeof row.tag === "string" ? row.tag.trim() : "";
  if (!tag) throw new Error(t("ui.actions.client {id} is missing tag", { id: row.id ?? fallbackID }));
  return {
    id: Number(row.id ?? fallbackID),
    name: typeof row.name === "string" && row.name.trim() ? row.name : `Client Action ${row.id ?? fallbackID}`,
    tag,
    // target picks the presentation (current | main | fullscreen | dialog |
    // new | sheet | window) — see ui_action_client.go's UIActionTarget*.
    target: typeof row.target === "string" && row.target.trim() ? row.target.trim().toLowerCase() : "current",
    // The backend column is sys_model (the res_model equivalent).
    resModel: typeof row.sys_model === "string" ? row.sys_model.trim() : "",
    context: row.context && typeof row.context === "object" && !Array.isArray(row.context) ? row.context : {},
    params: row.params && typeof row.params === "object" && !Array.isArray(row.params) ? row.params : {},
  };
}

// fetchBoundModelActions loads the sys.model.action entries bound to `model`
// whose view_type includes `viewType` (the active view mode) for that view's
// Actions dropdown (sequence order); no viewType returns every bound action. Each entry carries
// action_type plus only its resolved target: method_model/method_name,
// window_action (ui.actions.window dict) or clientAction (normalized like
// fetchClientActionByXMLID). The backend skips broken definitions.
export async function fetchBoundModelActions(model, viewType = "") {
  const response = await createModelRpc("sys.model.action").call("get_bound_actions", [], { model, view_type: viewType });
  const rows = Array.isArray(response.result) ? response.result : [];
  return rows.map((row) => ({
    id: Number(row.id),
    name: String(row.name ?? ""),
    icon: String(row.icon ?? ""),
    confirm: String(row.confirm ?? ""),
    actionType: String(row.action_type ?? ""),
    methodModel: String(row.method_model ?? ""),
    methodName: String(row.method_name ?? ""),
    // Declared kwargs of the method (RuntimeMethod.Params); non-empty means the
    // Actions menu must collect them in a popup before running it.
    params: normalizeMethodParams(row.params),
    windowAction: row.window_action ?? null,
    clientAction: row.client_action ? normalizeClientActionRow(row.client_action, row.id) : null,
  }));
}

// normalizeMethodParams coerces the backend RuntimeMethodParam list into the
// shape ModelActionParamsDialog renders; nameless entries are dropped.
function normalizeMethodParams(raw) {
  if (!Array.isArray(raw)) return [];
  return raw
    .filter((param) => param && typeof param.name === "string" && param.name.trim())
    .map((param) => ({
      name: param.name.trim(),
      label: String(param.label || param.name),
      type: String(param.type || "char"),
      required: param.required === true,
      help: String(param.help ?? ""),
      default: param.default ?? null,
      selection: Array.isArray(param.selection) ? param.selection.map((option) => ({ value: String(option.value), label: String(option.label ?? option.value) })) : [],
    }));
}

// replaceActionContextPlaceholders expands "{{key}}" tokens embedded inside
// a string value (e.g. a route param) against a placeholder map; whole-value
// matches also resolve to the raw (non-stringified) placeholder value.
export function replaceActionContextPlaceholders(value, placeholders) {
  if (typeof value === "string") {
    if (Object.prototype.hasOwnProperty.call(placeholders, value)) return placeholders[value];
    return value.replace(/\{\{([a-zA-Z0-9_]+)\}\}/g, (fullMatch, key) => {
      if (!Object.prototype.hasOwnProperty.call(placeholders, key)) return fullMatch;
      const replacement = placeholders[key];
      return replacement === null || replacement === undefined ? "" : String(replacement);
    });
  }
  if (Array.isArray(value)) return value.map((item) => replaceActionContextPlaceholders(item, placeholders));
  if (!value || typeof value !== "object") return value;
  return Object.fromEntries(Object.entries(value).map(([key, entryValue]) => [key, replaceActionContextPlaceholders(entryValue, placeholders)]));
}

function readArchBoolean(value) {
  if (value === null || value === undefined || value === "") return undefined;
  if (value === "1" || value.toLowerCase() === "true") return true;
  if (value === "0" || value.toLowerCase() === "false") return false;
  return undefined;
}

function resolveFieldLabel(label, fieldName) {
  const trimmed = label?.trim();
  // A label identical to the technical name ("partner_id") means the model never set one.
  if (trimmed && trimmed !== fieldName) return trimmed;
  return humanizeFieldName(fieldName);
}

function humanizeFieldName(fieldName) {
  const normalized = fieldName.replace(/[_\-.]+/g, " ").replace(/\s+/g, " ").trim();
  if (!normalized) return fieldName;
  return normalized.replace(/\b\w/g, (char) => char.toUpperCase());
}

// readListDecorations collects a <field>'s decoration-<variant>="expr"
// attributes into { variant: expr } for ListView's badgeVariant; returns
// undefined when the field declares none so column defs stay lean.
function readListDecorations(node) {
  const decorations = {};
  for (const attr of Array.from(node.attributes)) {
    if (!attr.name.startsWith("decoration-") || !attr.value) continue;
    decorations[attr.name.slice("decoration-".length)] = attr.value;
  }
  return Object.keys(decorations).length > 0 ? decorations : undefined;
}

// listArchChildren parses a list view arch and returns the element children
// matching `selector` (e.g. "list > field"); empty when the arch is unparsable.
function listArchChildren(arch, selector) {
  const document = parseArchDocument(arch);
  return document ? Array.from(document.querySelectorAll(selector)) : [];
}

// parseArchDocument parses an arch string as XML; null when unparsable.
function parseArchDocument(arch) {
  if (typeof DOMParser === "undefined" || typeof arch !== "string") return null;
  const document = new DOMParser().parseFromString(arch, "text/xml");
  return document.querySelector("parsererror") ? null : document;
}

// archNodeLabel prefers the node's string="" attribute, then its collapsed text.
function archNodeLabel(node, fallback) {
  return node.getAttribute("string") ?? node.textContent?.replace(/\s+/g, " ").trim() ?? fallback;
}

function parseListFieldNodes(arch) {
  return listArchChildren(arch, "list > field").flatMap((node) => {
    const name = node.getAttribute("name");
    if (!name) return [];
    const optional = node.getAttribute("optional");
    return [
      {
        name,
        string: node.getAttribute("string") ?? undefined,
        optional: optional === "show" || optional === "hide" ? optional : undefined,
        invisible: readArchBoolean(node.getAttribute("invisible")),
        readonly: readArchBoolean(node.getAttribute("readonly")),
        required: readArchBoolean(node.getAttribute("required")),
        widget: node.getAttribute("widget") ?? undefined,
        path: node.getAttribute("path") ?? undefined,
        rawValue: readArchBoolean(node.getAttribute("raw_value")),
        decorations: readListDecorations(node),
        attrs: readArchAttributes(node),
      },
    ];
  });
}

// readArchAttributes copies every raw XML attribute of an arch node, so a
// registered cell widget can read its own options (like form widgets' node.attrs).
function readArchAttributes(node) {
  return Object.fromEntries(Array.from(node.attributes).map((attribute) => [attribute.name, attribute.value]));
}

function fieldDefFromFieldMeta(name, field) {
  const selection = field.selection?.map(([value, label]) => ({ value, label }));
  return {
    name,
    label: resolveFieldLabel(field.string, name),
    help: field.help,
    type: field.type,
    kind: field.kind,
    backendType: field.backendType,
    required: field.required,
    readonly: field.readonly,
    translate: field.translate === true,
    sortable: field.sortable,
    options: selection,
    selection,
    currencyField: field.currency_field,
    digits: field.digits,
    relation: field.relation,
    relationField: field.relation_field,
    hasOnchange: field.has_onchange,
    // FormView.save drops these from create payloads (kernel rejects computed fields).
    serverComputed: field.serverComputed === true,
  };
}

// listFieldDefsFromView converts a backend list view response into ordered
// column defs. The XML arch controls column order/visibility; `fields`
// supplies type/display metadata — this is the core SDUI mechanism: the
// backend's ui.view row decides what renders, no per-model column list here.
export function listFieldDefsFromView(view) {
  const fieldNodes = parseListFieldNodes(view.arch);
  if (fieldNodes.length === 0) {
    return Object.entries(view.fields).map(([name, field]) => fieldDefFromFieldMeta(name, field));
  }
  return fieldNodes.flatMap((node) => {
    const field = view.fields[node.name];
    if (!field) return [];
    if (node.invisible) return [];
    return [
      {
        ...fieldDefFromFieldMeta(node.name, field),
        label: resolveFieldLabel(node.string ?? field.string, node.name),
        required: node.required ?? field.required,
        readonly: node.readonly ?? field.readonly,
        // Display-only arch hints consumed by ListView: `widget` picks the
        // cell renderer, `path` reads a dotted sub-key of a json field, and
        // `decorations` colours badge cells, and `rawValue` (raw_value="1")
        // shows a selection's stored key instead of its label. `attrs` are the
        // raw arch attributes, handed to a registered cell widget as node.attrs.
        widget: node.widget,
        path: node.path,
        rawValue: node.rawValue,
        decorations: node.decorations,
        attrs: node.attrs,
      },
    ];
  });
}

// listRowActionsFromView parses a list view's <action> block into trailing
// row actions. Only direct <button>/<a> children are recognized.
export function listRowActionsFromView(view) {
  return listArchChildren(view.arch, "list > action:first-of-type > *").flatMap((node) => {
    const tag = node.tagName.toLowerCase();
    if (tag !== "button" && tag !== "a") return [];
    const name = node.getAttribute("name") ?? undefined;
    return [
      {
        tag,
        name,
        label: archNodeLabel(node, name ?? "Action"),
        buttonType: node.getAttribute("type") ?? undefined,
        className: node.getAttribute("class") ?? undefined,
        href: tag === "a" ? (node.getAttribute("href") ?? undefined) : undefined,
        invisible: node.getAttribute("invisible") ?? undefined,
        confirm: node.getAttribute("confirm") ?? undefined,
        reload: node.getAttribute("reload") === "true",
        // `icon` renders an icon-only button (label becomes its tooltip);
        // `tab` is the form notebook page a type="open" action lands on;
        // `dialog` is a hint (?dialog=) a form widget may read to auto-open
        // one of its own dialogs (e.g. edc_device_actions' status/QR).
        icon: node.getAttribute("icon") ?? undefined,
        tab: node.getAttribute("tab") ?? undefined,
        dialog: node.getAttribute("dialog") ?? undefined,
        // type="widget" opens the registered form widget `widget` in a dialog
        // over the list (no navigation), titled "<row[title_field]> — <dialog_title>".
        widget: node.getAttribute("widget") ?? undefined,
        titleField: node.getAttribute("title_field") ?? undefined,
        dialogTitle: node.getAttribute("dialog_title") ?? undefined,
      },
    ];
  });
}

// listRowActionsColumnFromView reads the list's <action> block attributes:
// `string` titles the row-actions column (default "Actions") and `before`
// names the field column it is placed in front of (default: last column).
export function listRowActionsColumnFromView(view) {
  const node = parseArchDocument(view.arch)?.querySelector("list > action");
  return {
    label: node?.getAttribute("string") || "Actions",
    before: node?.getAttribute("before") || undefined,
  };
}

// listToolbarActionsFromView parses a list view's <toolbar> block into
// toolbar-level actions (no target record id, dispatched as a model-level RPC call).
export function listToolbarActionsFromView(view) {
  return listArchChildren(view.arch, "list > toolbar:first-of-type > *").flatMap((node) => {
    if (node.tagName.toLowerCase() !== "button") return [];
    const name = node.getAttribute("name");
    if (!name) return [];
    return [
      {
        name,
        label: archNodeLabel(node, name),
        className: node.getAttribute("class") ?? undefined,
        confirm: node.getAttribute("confirm") ?? undefined,
        selection: node.getAttribute("selection") === "true",
        reload: node.getAttribute("reload") === "true",
      },
    ];
  });
}

// listCreateWidgetFromView reads <list create_widget="<form widget>" create_title="" create_size="">:
// when set, the list's New button opens that widget in a dialog (e.g. a template picker) instead
// of navigating straight to a blank form. Null when the arch has no create_widget.
export function listCreateWidgetFromView(view) {
  const node = listArchChildren(view.arch, "list")[0];
  const widget = node?.getAttribute("create_widget");
  if (!widget) return null;
  return {
    widget,
    label: node.getAttribute("create_title") ?? "New",
    description: node.getAttribute("create_description") ?? "",
    size: node.getAttribute("create_size") ?? "",
  };
}

// requireMethodName trims a method name and refuses blank ones up front so a
// misconfigured button fails clearly instead of hitting a bad RPC route.
function requireMethodName(method, message) {
  const trimmedMethod = method.trim();
  if (!trimmedMethod) throw new Error(message);
  return trimmedMethod;
}

// executeObjectButtonMethod invokes a record-bound RuntimeMethod (path-ID first).
export async function executeObjectButtonMethod(model, id, method) {
  return executeObjectMethodWithArgs(model, id, method);
}

// executeObjectMethodWithArgs is executeObjectButtonMethod's superset: same
// path-ID-first RPC shape, but forwards positional args and/or kwargs to the
// backend method. Added for the Phase 6 base-view app-specific dialogs
// (base.database.action_duplicate's `new_name` kwarg, action_reinit's
// `modules` kwarg, base.users.action_set_password's positional new-password
// arg) — a straight port of frontend-admin's jsonv2.ts helper of the same name.
export async function executeObjectMethodWithArgs(model, id, method, args = [], kwargs = {}) {
  const trimmedMethod = requireMethodName(method, t("Object button method is required"));
  const response = await createModelRpc(model).callByIdFirst(trimmedMethod, id, args, [[id], ...args], kwargs);
  return response.result;
}

// executeLongRunningObjectMethod is executeObjectMethodWithArgs without the
// 15s abort timer, for actions that legitimately run for minutes (module
// reinit). A dropped connection still rejects with err.transport === true so
// the caller can wait for the server instead of treating it as a failure.
export async function executeLongRunningObjectMethod(model, id, method, kwargs = {}) {
  const trimmedMethod = requireMethodName(method, t("Object button method is required"));
  const client = createModelRpc(model);
  client.timeoutMs = 0;
  const response = await client.callByIdFirst(trimmedMethod, id, [], [[id]], kwargs);
  return response.result;
}

// executeModelMethod calls a model-level RPC method (no target ids), e.g. a
// list toolbar action with no selection. `kwargs` carries the inputs a
// sys.model.action params popup collected (empty for parameterless methods).
export async function executeModelMethod(model, method, kwargs = {}) {
  const trimmedMethod = requireMethodName(method, t("Model method is required"));
  const response = await createModelRpc(model).call(trimmedMethod, [], kwargs);
  return response.result;
}

// executeModelMethodOnRecords calls a path-ID RuntimeMethod against a set of
// ids, e.g. a list toolbar action that runs over the current row selection.
export async function executeModelMethodOnRecords(model, ids, method, kwargs = {}) {
  const trimmedMethod = requireMethodName(method, t("Model method is required"));
  if (ids.length === 0) throw new Error(t('"{method}" requires at least one selected record', { method: trimmedMethod }));
  const response = await createModelRpc(model).run(ids, trimmedMethod, kwargs);
  return response.result;
}

// ---------------------------------------------------------------------------
// form-view helpers (Phase 2c) — ported from frontend-admin's jsonv2.ts
// ---------------------------------------------------------------------------

// fieldDefsFromFields converts a fields_get metadata map into the FieldDef
// shape the form renderer consumes. Unlike listFieldDefsFromView there is no
// arch column filter here — the form arch decides placement, this only
// supplies per-field type/label/selection metadata.
export function fieldDefsFromFields(fields) {
  return Object.entries(fields).map(([name, field]) => fieldDefFromFieldMeta(name, field));
}

// buildFormGroupsFromFields is the arch-less fallback layout: two fields per
// group, first group unlabeled.
function buildFormGroupsFromFields(fields) {
  const visible = fields.filter((field) => !field.invisible).map((field) => field.name);
  if (visible.length === 0) return [{ fields: [] }];
  const groups = [];
  for (let index = 0; index < visible.length; index += 2) {
    const groupFields = visible.slice(index, index + 2);
    groups.push({
      label: groups.length === 0 ? undefined : `Section ${groups.length + 1}`,
      fields: groupFields,
      columns: groupFields.length > 1 ? 2 : 1,
    });
  }
  return groups;
}

// fetchModelRecordByID reads one record through the record-id RPC path.
async function fetchModelRecordByID(model, id, fieldNames) {
  const response = await createModelRpc(model).callById("read", id, [fieldNames]);
  const rows = response.result;
  return Array.isArray(rows) && rows.length > 0 ? rows[0] : {};
}

// fetchModelDefaultValues resolves create-mode defaults through default_get.
async function fetchModelDefaultValues(model, fieldNames) {
  const response = await createModelRpc(model).call("default_get", [fieldNames]);
  return response.result ?? {};
}

// fetchModelFormRecord reads one record (or create-mode defaults when id is
// falsy) plus the fields_get metadata the form renderer needs.
export async function fetchModelFormRecord(model, id) {
  const fields = await fetchModelFields(model);
  const fieldNames = Object.keys(fields);
  const record = id ? await fetchModelRecordByID(model, id, fieldNames) : await fetchModelDefaultValues(model, fieldNames);
  return { model, id: id ?? false, record, fields };
}

// writeModelRecord persists changed values on an existing record.
export async function writeModelRecord(model, id, values) {
  const response = await createModelRpc(model).callById("write", id, [values]);
  if (!response.result) throw new Error(t("Failed to write {model} {id}", { model, id }));
  return { model, id, values, success: true };
}

// createModelRecord creates a record and returns the created row.
export async function createModelRecord(model, values) {
  const response = await createModelRpc(model).call("create", [values]);
  if (!response.result || typeof response.result !== "object") {
    throw new Error(t("Failed to create {model}", { model }));
  }
  return response.result;
}

// deleteModelRecords removes records by id.
export async function deleteModelRecords(model, ids) {
  if (ids.length === 0) throw new Error(t("Delete requires at least one record"));
  const response = await createModelRpc(model).delete(ids);
  return response.result;
}

// duplicateModelRecord clones one record through the model's copy method.
export async function duplicateModelRecord(model, id) {
  const response = await createModelRpc(model).callByIdFirst("copy", id, [], [[id]]);
  return response.result;
}

// callModelOnchange asks the backend to recompute dependent values.
async function callModelOnchange(model, values, fieldNames) {
  const response = await createModelRpc(model).call("onchange", [values, fieldNames]);
  if (!response.result || typeof response.result !== "object") return {};
  const nextValues = response.result.value;
  return nextValues && typeof nextValues === "object" ? nextValues : {};
}

// createRemoteOnchangeHandler builds a model-agnostic onchange callback: a
// field only round-trips to the backend when its own fields_get metadata says
// an onchange handler is registered (hasOnchange) — no client allowlist.
export function createRemoteOnchangeHandler(model, fields) {
  return async (fieldName, _nextValue, nextRecord) => {
    if (!fields[fieldName]?.hasOnchange) return undefined;
    return callModelOnchange(model, nextRecord, [fieldName]);
  };
}

// toRelationChoices turns [id, name] pairs into {id, name} options, dropping
// rows without a usable id.
function toRelationChoices(pairs) {
  return pairs.flatMap(([id, name]) => (id === null || id === undefined || id === "" ? [] : [{ id, name: String(name ?? id) }]));
}

// fetchRelationChoices lists the first `limit` rows of a related model.
export async function fetchRelationChoices(model, limit = 80, domain = []) {
  const response = await createModelRpc(model).search(domain, ["id", "name"], "id ASC", 0, limit, false);
  return toRelationChoices((response.result ?? []).map((record) => [record.id, record.name]));
}

// cachedUntilFailure memoizes a catalog loader for the page's lifetime, but
// forgets a failed load so the next caller retries instead of reusing the error.
function cachedUntilFailure(load) {
  let promise = null;
  return () => {
    promise ??= load().catch((error) => {
      promise = null;
      throw error;
    });
    return promise;
  };
}

// MODEL_NAME_CHOICE_LIMIT bounds the sys.model catalog read for the
// sys_model_name widget — comfortably above the registered model count, so
// the dropdown lists every model instead of a first page.
const MODEL_NAME_CHOICE_LIMIT = 2000;

// fetchModelNameChoices lists registered models as {name, label} for the
// sys_model_name form widget, sorted by technical name. Cached per page load
// (the catalog only changes on module install/publish, which reloads the SPA).
export const fetchModelNameChoices = cachedUntilFailure(async () => {
  const response = await createModelRpc("sys.model").search([], ["name", "label"], "name ASC", 0, MODEL_NAME_CHOICE_LIMIT, false);
  return (response.result ?? []).flatMap((record) => {
    const name = String(record.name ?? "").trim();
    return name ? [{ name, label: String(record.label ?? "").trim() }] : [];
  });
});

// fetchViewTypeChoices lists the ui.view.type catalog as
// {name, label, icon, isViewMode} in sequence order for the ui_view_type /
// ui_view_types widgets. Cached per page load.
export const fetchViewTypeChoices = cachedUntilFailure(async () => {
  const response = await createModelRpc("ui.view.type").search([], ["name", "label", "icon", "is_view_mode"], "sequence ASC, id ASC", 0, 200, false);
  return (response.result ?? []).flatMap((record) => {
    const name = String(record.name ?? "").trim().toLowerCase();
    if (!name) return [];
    return [{ name, label: String(record.label ?? "").trim() || name, icon: String(record.icon ?? "").trim(), isViewMode: record.is_view_mode === true }];
  });
});

const kvtChoiceCache = new Map();

// fetchKvtChoices reads the base.kvt record named `key` whose JSON value is a list of option records
// ({value, label?, offset?}) and normalizes it to [{value, label}]. Cached per key for the page's
// lifetime; a failed or malformed read is forgotten so the next caller retries.
export function fetchKvtChoices(key) {
  if (!kvtChoiceCache.has(key)) {
    const load = createModelRpc("base.kvt")
      .search([["name", "=", key]], ["value"], "id ASC", 0, 1, false)
      .then((response) => {
        let list = response.result?.[0]?.value;
        if (typeof list === "string") list = JSON.parse(list);
        if (!Array.isArray(list)) throw new Error(`base.kvt "${key}" is missing or not a list`);
        return list.flatMap((item) => {
          const value = String(typeof item === "string" ? item : item?.value ?? "").trim();
          if (!value) return [];
          return [{ value, label: String(typeof item === "string" ? "" : item?.label ?? "").trim() || value }];
        });
      });
    kvtChoiceCache.set(key, load);
    load.catch(() => kvtChoiceCache.delete(key));
  }
  return kvtChoiceCache.get(key);
}

// searchRelationChoices resolves many2one options for a typed query through
// name_search; an empty query falls back to the plain first-page listing.
export async function searchRelationChoices(model, query, limit = DEFAULT_SEARCH_READ_LIMIT, domain = []) {
  const normalizedQuery = query.trim();
  if (!normalizedQuery) return fetchRelationChoices(model, limit, domain);
  const response = await createModelRpc(model).call("name_search", [normalizedQuery], { limit, domain });
  const tuples = Array.isArray(response.result) ? response.result : [];
  return toRelationChoices(tuples.filter((item) => Array.isArray(item) && item.length >= 2));
}

// ---------------------------------------------------------------------------
// kanban-view helpers (Phase 2d) — ported from frontend-admin's jsonv2.ts
// ---------------------------------------------------------------------------

// fetchModelGroups reads grouped counts for kanban columns through read_group.
async function fetchModelGroups(model, groupBy, domain = []) {
  const response = await createModelRpc(model).call("read_group", [], {
    domain,
    fields: [groupBy, "__count"],
    groupby: [groupBy],
  });
  // A many2one group arrives as an [id, name] tuple; String() would give
  // "3,Done" and never match the record-side column key (the bare id), so
  // split it into the id (value) and the display name (label).
  return (response.result ?? []).map((row) => {
    const raw = row[groupBy];
    const isTuple = Array.isArray(raw) && raw.length >= 1;
    const value = String((isTuple ? raw[0] : raw) ?? "undefined");
    return { value, label: isTuple && raw.length > 1 ? String(raw[1]) : value, count: Number(row.__count ?? 0) };
  });
}

// fetchKanbanColumnOrder orders many2one board columns by the comodel's
// `sequence` (then id, ascending) so stage 1 is the left-most column. Returns
// the sorted ids plus whether the comodel has a writable `sequence`, which is
// what gates column drag-and-drop; any failure degrades to plain id order.
export async function fetchKanbanColumnOrder(relationModel, columnIds) {
  const numericIds = columnIds.map(Number).filter(Number.isFinite);
  const byId = (a, b) => Number(a) - Number(b);
  if (!relationModel || numericIds.length === 0) return { ids: [...columnIds].sort(byId), sequenceModel: "" };
  try {
    const response = await createModelRpc(relationModel).search([["id", "in", numericIds]], ["id", "sequence"], "sequence ASC, id ASC", 0, numericIds.length, false);
    const rows = Array.isArray(response.result) ? response.result : [];
    if (!rows.length || rows.some((row) => row.sequence === undefined)) throw new Error("no sequence");
    const sequence = new Map(rows.map((row) => [String(row.id), Number(row.sequence)]));
    const ids = [...columnIds].sort((a, b) => (sequence.get(String(a)) ?? Infinity) - (sequence.get(String(b)) ?? Infinity) || byId(a, b));
    return { ids, sequenceModel: relationModel };
  } catch {
    return { ids: [...columnIds].sort(byId), sequenceModel: "" };
  }
}

// fetchModelKanbanRecords fetches one page of records plus the per-group
// totals the board needs for its column counts / "load more" affordance.
export async function fetchModelKanbanRecords({ model, groupBy = "stage_id", groupValue, limit = 80, offset = 0, domain = [] } = {}) {
  const fields = await fetchModelFields(model);
  const fieldNames = Object.keys(fields);
  const scopedDomain = [...domain];
  if (groupValue) scopedDomain.push([groupBy, "=", groupValue]);
  const [records, length, groups] = await Promise.all([
    fetchModelSearchRead(model, scopedDomain, fieldNames, limit, offset),
    fetchModelSearchCount(model, scopedDomain),
    fetchModelGroups(model, groupBy, domain),
  ]);
  return { model, length, records, fields, groups };
}

// inferKanbanGroupByField picks a reasonable board axis from field metadata —
// relation/selection columns map naturally onto lanes.
export function inferKanbanGroupByField(fields) {
  const preferred = ["stage_id", "state", "status", "user_id", "team_id", "company_id"];
  for (const fieldName of preferred) {
    const field = fields[fieldName];
    if (field && isKanbanGroupableField(field)) return fieldName;
  }
  return Object.entries(fields).find(([, field]) => isKanbanGroupableField(field))?.[0];
}

function isKanbanGroupableField(field) {
  return field.type === "selection" || field.type === "many2one";
}

// ---------------------------------------------------------------------------
// calendar-view helpers (Phase 2e) — ported from frontend-admin's jsonv2.ts
// ---------------------------------------------------------------------------

// fetchCalendarOccurrences reads plain + expanded-recurring occurrences for one
// model within [dateFrom, dateTo) through base.calendar.recurrence.
export async function fetchCalendarOccurrences({ model, dateFrom, dateTo, domain = [] }) {
  const response = await createModelRpc("base.calendar.recurrence").call("occurrences", [], {
    sys_model: model,
    date_from: dateFrom,
    date_to: dateTo,
    domain,
  });
  return response.result ?? { records: [] };
}

// rescheduleCalendarOccurrence moves one dragged occurrence with a
// this/following/all scope. Non-recurring rows use writeModelRecord instead.
export async function rescheduleCalendarOccurrence({ recurrenceID, originalOccurrenceDate, newDatetime, scope }) {
  const response = await createModelRpc("base.calendar.recurrence").callById("reschedule_occurrence", recurrenceID, [], {
    original_occurrence_date: originalOccurrenceDate,
    new_datetime: newDatetime,
    scope,
  });
  return response.result;
}

// createCalendarRecurrence creates a recurrence rule for a master record.
export async function createCalendarRecurrence(values) {
  return createModelRpc("base.calendar.recurrence").create(values);
}
