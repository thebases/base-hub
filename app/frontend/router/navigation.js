// This SPA is mounted at root "/" (BASE_URL, see env.js, is ""). Internal route paths (main.js's ROUTES, itemHref() in
// MenuTree.js, etc.) stay prefix-free ("/web/...") so route tables and
// comparisons never need to know the mount point; this module is the only
// place that would add/remove BASE_URL, at the browser-URL boundary, and is a
// no-op today since BASE_URL is empty — kept in case a prefixed mount returns.
export const BASE_URL = (window.__ENV__?.BASE_URL || "").replace(/\/+$/, "");

export function toRoutePath(fullPath) {
  if (BASE_URL && fullPath.startsWith(BASE_URL)) {
    const rest = fullPath.slice(BASE_URL.length);
    return rest === "" ? "/" : rest;
  }
  return fullPath;
}

export function toFullPath(routePath) {
  return routePath === "/" ? BASE_URL || "/" : `${BASE_URL}${routePath}`;
}

// Pushes routePath (prefix-free) onto the browser URL as a full path, then
// fires popstate so main.js's single listener re-mounts — the one code path
// used both for in-app link clicks and for programmatic redirects (login
// success, logout).
export function navigate(routePath) {
  history.pushState({}, "", toFullPath(routePath));
  window.dispatchEvent(new PopStateEvent("popstate"));
}

// Re-mounts the current page so it reloads its data from the server, without adding a browser
// history entry — for form widgets that saved through their own RPC (AppRoot re-mounts on every
// popstate, even for an unchanged path).
export function reloadRoute() {
  window.dispatchEvent(new PopStateEvent("popstate"));
}
