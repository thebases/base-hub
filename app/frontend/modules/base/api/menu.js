// Port of frontend-admin's modules/base/api/menu.ts (ui.menu SDUI sidebar
// source of truth): loads the ui.menu tree via UIMenu.call("load_menus"),
// normalizes each row into a sidebar-ready record (name/url/icon/children +
// a narrowed windowAction for rows carrying an act_window-shaped action), and
// caches the result in localStorage scoped to the current auth
// (db/uid/company) so a reload doesn't flash an empty sidebar. Same RPC
// contract and cache-key shape as the React app; kept in a dedicated module
// (not jsonv2.js) since it depends on auth-service.js, which itself imports
// jsonv2.js — importing it back from jsonv2.js would create a cycle.

import { UIMenu } from "@/services/api/jsonv2.js";
import { authStore } from "@/stores/auth-service.js";

const SIDEBAR_MENU_CACHE_KEY = "basehub.ui.menu.cache.v1";

// UI_MENU_UPDATED_EVENT: dispatched (see bus-root.js's handleUIMenuUpdated)
// whenever the backend's "ui.menu.updated" bus event arrives — a module
// install/uninstall/reinit, or a direct ui.menu create/write, changed the
// sidebar's source data. Shell.js listens for this to soft-refresh the
// sidebar (loadMenu()) without a full page reload, same wiring shape as
// knowledge/api.js's MARKDOWN_MENU_UPDATED_EVENT for the doc tree.
export const UI_MENU_UPDATED_EVENT = "basehub:ui-menu-updated";

export function emitUIMenuUpdated() {
  if (typeof window === "undefined") return;
  window.dispatchEvent(new CustomEvent(UI_MENU_UPDATED_EVENT));
}

/** Fetches the sidebar menu tree from the backend and refreshes the cache. */
export async function fetchSidebarMenus() {
  return (await loadSidebarMenus()).records;
}

async function loadSidebarMenus() {
  const response = await UIMenu.call("load_menus");
  const result = normalizeLoadMenusPayload(response.result);
  writeCachedSidebarMenus(result);
  return result;
}

export function readCachedSidebarMenus() {
  if (typeof window === "undefined") return null;
  try {
    const raw = window.localStorage.getItem(sidebarMenuStorageKey());
    if (!raw) return null;
    const parsed = JSON.parse(raw);
    return isSidebarMenuLoadResult(parsed) ? parsed : null;
  } catch {
    return null;
  }
}

function writeCachedSidebarMenus(result) {
  if (typeof window === "undefined" || !result.menu_cache_key) return;
  // Drop the original global cache entry so older builds cannot leak one
  // tenant's sidebar into another scope after moving to scoped cache keys.
  window.localStorage.removeItem(SIDEBAR_MENU_CACHE_KEY);
  window.localStorage.setItem(sidebarMenuStorageKey(), JSON.stringify(result));
}

// clearCachedSidebarMenus drops the current user's scoped sidebar cache
// entry. Must run before auth state is reset, since the cache key is derived
// from the live db/uid/company values in authStore.
export function clearCachedSidebarMenus() {
  if (typeof window === "undefined") return;
  window.localStorage.removeItem(sidebarMenuStorageKey());
}

// readableModelsFromSidebar derives the set of models the current user's
// sidebar actually exposes, walking the already-ACL-filtered ui.menu tree
// (see ui_menu.go's allowUIMenuRowForCurrentUser). Any dashboard/quick-link
// list must gate against this set instead of probing a model's access
// directly: the generic /rpc/{model}/{method} dispatcher enforces read-ACL
// before running any method body (including has_access itself), so probing a
// restricted model still 403s and pollutes logs — this is the only way to
// know what's visible without asking a permission-gated endpoint "am I
// allowed to read this?" for a model the caller may not be allowed to read.
// Extracted out of pages/Home/Home.js (2026-09-22) so the retired Home page
// and the new generic DashboardView share one implementation.
//
// windowAction.res_model only exists for rows carrying a real act_window
// action; nearly every seeded menuitem in this repo (menu-base.xml and
// friends) is declared with a bare `route_path="/web/<model>"` instead, which
// never resolves to a windowAction (menuWindowAction requires an actual
// act_window action object). Without the modelFromMenuRouteURL fallback below,
// readableModelsFromSidebar returns an empty set for a normal install and
// every dashboard card/quicklink gets ACL-filtered out (2026-09-23 fix).
export async function readableModelsFromSidebar() {
  let records = readCachedSidebarMenus()?.records;
  if (!records) {
    try {
      records = await fetchSidebarMenus();
    } catch {
      records = [];
    }
  }
  const models = new Set();
  const walk = (rows) => {
    for (const row of rows ?? []) {
      const model = row.windowAction?.res_model ?? modelFromMenuRouteURL(row.url);
      if (model) models.add(model);
      walk(row.children);
    }
  };
  walk(records);
  return models;
}

// RESERVED_SIDEBAR_ROUTE_SEGMENTS mirrors the literal single-segment "/web/*"
// routes declared above the "/web/:actionModel" catch-all in router.js — those
// segments are pages, not model names, and must never be treated as one.
const RESERVED_SIDEBAR_ROUTE_SEGMENTS = new Set(["login", "markdown", "apidoc", "instruction", "notifications", "system-health", "dashboard"]);

// modelFromMenuRouteURL recovers the model name a plain route_path menu entry
// points at (e.g. "/web/tbl.partner" -> "tbl.partner"), the same single-segment
// convention router.js's "/web/:actionModel" wildcard resolves at navigation
// time. Multi-segment routes (e.g. the website page editor) and reserved page
// segments are left undefined so they're never mistaken for a model.
function modelFromMenuRouteURL(url) {
  const match = typeof url === "string" ? url.trim().match(/^\/web\/([^/]+)\/?$/) : null;
  if (!match) return undefined;
  const segment = match[1];
  return RESERVED_SIDEBAR_ROUTE_SEGMENTS.has(segment) ? undefined : segment;
}

function normalizeLoadMenusPayload(raw) {
  if (Array.isArray(raw)) {
    return {
      menu_scope_key: "",
      scope_key: "",
      menu_version: 0,
      menu_cache_key: "",
      records: mapSidebarMenuRecords(raw),
    };
  }

  const payload = isRecord(raw) ? raw : {};
  const scopeKey = stringFromUnknown(payload.scope_key);
  const menuScopeKey = stringFromUnknown(payload.menu_scope_key) || scopeKey;
  const records = Array.isArray(payload.records) ? payload.records : [];
  return {
    menu_scope_key: menuScopeKey,
    scope_key: scopeKey || menuScopeKey,
    menu_version: numberFromUnknown(payload.menu_version),
    menu_cache_key: stringFromUnknown(payload.menu_cache_key),
    records: mapSidebarMenuRecords(records),
  };
}

function isSidebarMenuLoadResult(value) {
  if (!isRecord(value)) return false;
  return (
    typeof value.menu_scope_key === "string" &&
    typeof value.scope_key === "string" &&
    typeof value.menu_version === "number" &&
    typeof value.menu_cache_key === "string" &&
    Array.isArray(value.records)
  );
}

function stringFromUnknown(value) {
  return typeof value === "string" ? value : "";
}

function numberFromUnknown(value) {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

// sidebarMenuStorageKey binds the browser cache to the authenticated menu
// scope so cached trees cannot bleed across DB, user, or company changes.
function sidebarMenuStorageKey() {
  const state = authStore.getState();
  const db = state.db?.trim() || "default";
  const uid = typeof state.uid === "number" && state.uid > 0 ? state.uid : 0;
  const currentCompanyID = typeof state.currentCompanyId === "number" && state.currentCompanyId > 0 ? state.currentCompanyId : 0;
  const allowedCompanyIDs = normalizeCompanyScope(state.allowedCompanyIds);
  return `${SIDEBAR_MENU_CACHE_KEY}:${db}:${uid}:${currentCompanyID}:${allowedCompanyIDs}`;
}

function normalizeCompanyScope(companyIDs) {
  if (!Array.isArray(companyIDs) || companyIDs.length === 0) return "none";
  return companyIDs
    .filter((companyID) => Number.isFinite(companyID) && companyID > 0)
    .join(",");
}

// mapSidebarMenuRecords preserves the backend tree so the sidebar can treat
// level-1 rows as dynamic groups and deeper rows as nested menus.
function mapSidebarMenuRecords(rows) {
  return (rows ?? []).map((row) => {
    const action = menuAction(row);
    return {
      id: row.id,
      name: row.name,
      url: menuURL(row),
      sequence: row.sequence ?? 0,
      icon: row.icon?.trim() || "file",
      action,
      windowAction: menuWindowAction(action, row.action_model),
      children: mapSidebarMenuRecords(menuChildren(row)),
    };
  });
}

function menuChildren(row) {
  const childRows = row.child_ids ?? row.children;
  return Array.isArray(childRows) ? childRows : undefined;
}

// menuURL keeps real menu routes when present and falls back to "#", matching
// the sidebar button behavior for non-routable items.
function menuURL(row) {
  const url = row.url?.trim();
  if (url) return url;
  const actionURL = row.action?.url?.trim();
  return actionURL || "#";
}

function menuAction(row) {
  return row.action && typeof row.action === "object" ? row.action : undefined;
}

// menuWindowAction narrows backend action dictionaries to the models this
// shell can currently open via /web/:actionModel.
function menuWindowAction(action, fallbackModel) {
  const actionType = normalizeWindowActionType(action?.type);
  if (!actionType || !action) return undefined;

  const model = toMenuActionModel(action.res_model) ?? fallbackModel;
  if (!model) return undefined;

  const target = normalizeWindowActionTarget(action.target);
  const id = typeof action.id === "number" ? action.id : null;
  const name = typeof action.name === "string" ? action.name : null;
  const viewMode = normalizeViewMode(action.view_mode, model);
  if (!target || id === null || !name || !viewMode) return undefined;

  return {
    type: actionType,
    res_model: model,
    id,
    name,
    target,
    context: normalizeWindowActionContext(action.context),
    domain: normalizeWindowActionDomain(action.domain),
    views: normalizeWindowActionViews(action.views),
    view_mode: viewMode,
    search_view_id: normalizeWindowActionSearchView(action.search_view_id),
    help: typeof action.help === "string" ? action.help : "",
    limit: typeof action.limit === "number" ? action.limit : 80,
    url: action.url,
  };
}

function normalizeWindowActionType(value) {
  switch (value) {
    case "ui.actions.window":
    case "sys.actions.act_window":
    case "ir.actions.act_window":
      return value;
    default:
      return null;
  }
}

function toMenuActionModel(value) {
  const model = value?.trim();
  return model && model.length > 0 ? model : undefined;
}

function isRecord(value) {
  return !!value && typeof value === "object" && !Array.isArray(value);
}

function isWindowActionTarget(value) {
  return value === "current" || value === "new" || value === "fullscreen" || value === "main";
}

function normalizeWindowActionTarget(value) {
  // The menu API may still emit legacy "self" targets for URL actions, so map
  // it to the shell's current-page target when normalizing act_window payloads.
  if (value === "self") return "current";
  return isWindowActionTarget(value) ? value : null;
}

function normalizeWindowActionContext(value) {
  if (isRecord(value)) return value;
  if (typeof value !== "string" || value.trim() === "") return {};
  try {
    const parsed = JSON.parse(value);
    return isRecord(parsed) ? parsed : {};
  } catch {
    return {};
  }
}

function normalizeWindowActionDomain(value) {
  return Array.isArray(value) ? value : [];
}

function normalizeWindowActionViews(value) {
  if (!Array.isArray(value)) return [];
  return value.flatMap((item) => {
    if (!Array.isArray(item) || item.length < 2) return [];
    const [viewID, viewType] = item;
    if (typeof viewID !== "number" || (viewType !== "list" && viewType !== "form" && viewType !== "kanban" && viewType !== "canvas" && viewType !== "search")) {
      return [];
    }
    return [[viewID, viewType]];
  });
}

function normalizeWindowActionSearchView(value) {
  if (Array.isArray(value) && value.length >= 2 && typeof value[0] === "number" && typeof value[1] === "string") {
    return [value[0], value[1]];
  }
  return [0, "search"];
}

function normalizeViewMode(value, model) {
  // Preserve the backend-declared act_window modes exactly when present. Only
  // fall back to the model default when the action omits view_mode.
  const rawValue = typeof value === "string" && value.trim().length > 0 ? value : model === "base.workflow" ? "list,kanban,canvas,form" : "list,kanban,form";
  const seen = new Set();
  const modes = [];
  const appendMode = (mode) => {
    if (seen.has(mode)) return;
    seen.add(mode);
    modes.push(mode);
  };
  for (const mode of rawValue.split(",").map((part) => part.trim()).filter(Boolean)) {
    if (mode === "list" || mode === "form" || mode === "kanban" || mode === "canvas" || mode === "search") {
      appendMode(mode);
    }
  }
  return modes.join(",") || null;
}
