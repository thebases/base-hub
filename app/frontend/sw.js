// Offline-first: cache-first for the app shell/assets, background-refreshed
// on every hit (stale-while-revalidate). version.json is the one exception -
// it always goes to the network so main.js can compare it against the
// localStorage-stored build id and detect a new deploy. The SW has no
// localStorage access, so that comparison (and the resulting CLEAR_CACHES
// purge + reload) lives in main.js; this file only supplies the cache to
// purge and the version.json freshness guarantee it depends on.
const CACHE_NAME = "base-hub-frontend-offline-v1";

// Backend data/API path prefixes that must never be served cache-first: their
// responses are per-request/per-identity (auth tokens, session info, RPC
// records, bus notifications, ...) and the Cache Storage API keys entries by
// URL only, ignoring the Authorization header. Serving these cache-first let
// GET /oauth/me return a *different* previously-cached user's session after a
// fresh login, since caches.match() can't tell the new bearer token apart
// from the old one at the same URL - only the network response reflects the
// caller actually making the request. Keep in sync with the backend route
// prefixes registered in internal/cmd/server.go and modules/*/routes/*.go;
// deliberately excludes the SPA's own client-routed "/web/:actionModel"
// paths (and bare "/"), which stay cache-first as the offline app shell.
const BYPASS_CACHE_PATH_PREFIXES = [
  "/oauth",
  "/auth",
  "/rpc",
  "/rpc_meta",
  "/bus",
  "/websocket",
  "/wf",
  "/wh",
  // RuntimeMethod alias routes (`GET /kvt/:key`, `GET /base/admin/users`, legacy `/api/...`).
  "/api",
  "/base",
  "/kvt",
  "/md",
  "/web/content",
  "/report",
  "/health",
  "/metrics",
  "/doc",
  "/mqtt",
  "/publish",
  "/discuss",
  "/website/runtime",
  "/_site",
  "/blog",
  "/keys",
  "/tools",
  "/qr",
  "/banklist",
  "/languages",
  "/frontend-modules.json",
  "/bridge",
  "/jsonstring",
  "/aes",
  "/aeshmac",
  "/manifest.webmanifest",
];

function isBypassCachePath(pathname) {
  return BYPASS_CACHE_PATH_PREFIXES.some(
    (prefix) => pathname === prefix || pathname.startsWith(`${prefix}/`),
  );
}

self.addEventListener("install", (event) => {
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) =>
        Promise.all(keys.filter((key) => key !== CACHE_NAME).map((key) => caches.delete(key))),
      )
      .then(() => self.clients.claim()),
  );
});

self.addEventListener("fetch", (event) => {
  if (event.request.method !== "GET") {
    return;
  }

  const url = new URL(event.request.url);
  if (url.pathname.endsWith("/version.json")) {
    event.respondWith(networkFirstVersionCheck(event.request));
    return;
  }

  if (isBypassCachePath(url.pathname)) {
    // Let the browser handle this request normally - no Cache Storage
    // read/write at all, so every call reaches the network with whatever
    // Authorization/X-Database-Name header the caller just set.
    return;
  }

  event.respondWith(cacheFirst(event.request));
});

// fetchRevalidated goes to the network with cache: "no-cache" so the
// browser HTTP cache must revalidate (cheap 304 via Last-Modified) instead
// of answering from its own copy. The server sends max-age=3600 on static
// assets, so a plain fetch() here could re-store an hour-stale module into
// this SW cache even right after a version-bump CLEAR_CACHES purge.
function fetchRevalidated(request) {
  return fetch(request, { cache: "no-cache" });
}

async function cacheFirst(request) {
  const cached = await caches.match(request);
  if (cached) {
    // Don't block this response on the network - refresh the cache for next
    // time instead.
    refreshCache(request);
    return cached;
  }

  const response = await fetchRevalidated(request);
  if (response.ok) {
    refreshCache(request, response.clone());
  }
  return response;
}

async function refreshCache(request, response) {
  try {
    const fresh = response ?? (await fetchRevalidated(request));
    if (fresh.ok) {
      const cache = await caches.open(CACHE_NAME);
      await cache.put(request, fresh);
    }
  } catch {
    // Offline or request failed - keep serving whatever is already cached.
  }
}

async function networkFirstVersionCheck(request) {
  try {
    const response = await fetch(request, { cache: "no-store" });
    if (response.ok) {
      const cache = await caches.open(CACHE_NAME);
      await cache.put(request, response.clone());
    }
    return response;
  } catch (error) {
    const cached = await caches.match(request);
    if (cached) {
      return cached;
    }
    throw error;
  }
}

self.addEventListener("message", (event) => {
  if (event.data?.type !== "CLEAR_CACHES") {
    return;
  }

  const respond = () => event.ports[0]?.postMessage({ ok: true });
  event.waitUntil(deleteAllCaches().then(respond, respond));
});

async function deleteAllCaches() {
  const keys = await caches.keys();
  await Promise.all(keys.map((key) => caches.delete(key)));
}
