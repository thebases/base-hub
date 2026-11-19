// Extracted from main.js so a backend-pushed version-bump event (see
// bus-root.js's "app.frontend_version_updated" subscription) can reuse the
// exact same offline-cache-purge + reload path as the on-load check, instead
// of duplicating it.
//
// Offline-first cache-busting: compare the server's stamped build id
// (version.json, always fetched with cache: "no-store" so this check itself
// can't be answered by a stale SW cache entry) against the last one this
// browser saw (localStorage). A missing/mismatched value means the SW's
// offline cache may be serving an older build, so purge it via the SW's
// CLEAR_CACHES message and reload once to pick up the new version from the
// server.
const VERSION_STORAGE_KEY = "base-hub-version";

export function registerServiceWorker() {
  if (!("serviceWorker" in navigator)) return;

  navigator.serviceWorker.register("./sw.js").catch(() => {});
  // Force a one-time reload the moment a new service worker takes control.
  // sw.js caches offline-first (cache-first + background refresh) rather
  // than purging on every install, but an already-open tab still keeps
  // whatever it already rendered/fetched under the old controller until it
  // reloads — this makes that pickup automatic instead of requiring a manual
  // hard refresh.
  let reloadedForNewServiceWorker = false;
  navigator.serviceWorker.addEventListener("controllerchange", () => {
    if (reloadedForNewServiceWorker) return;
    reloadedForNewServiceWorker = true;
    window.location.reload();
  });
}

export async function checkForNewVersion() {
  try {
    const response = await fetch("./version.json", { cache: "no-store" });
    if (!response.ok) return;
    const payload = await response.json();
    const serverVersion = typeof payload.version === "string" ? payload.version : "";
    if (!serverVersion) return;
    await applyServerVersion(serverVersion);
  } catch {
    // Offline or version.json unreachable - keep serving offline-first from
    // whatever the SW already has cached.
  }
}

// applyServerVersion is the shared comparison+purge+reload step: called with
// a version.json fetch's result from checkForNewVersion() (page load), and
// with a version string pushed live over the bus by
// "app.frontend_version_updated" (see bus-root.js) — a module install/
// uninstall bumps the server's version.json and pushes that event so already
// -open tabs don't have to wait for their next full page load to notice.
export async function applyServerVersion(serverVersion) {
  if (!serverVersion) return;

  const storedVersion = localStorage.getItem(VERSION_STORAGE_KEY);
  if (storedVersion === serverVersion) return;

  localStorage.setItem(VERSION_STORAGE_KEY, serverVersion);
  if (storedVersion) {
    // Not the first-ever load - an older build (and, after a module
    // install/uninstall, stale seeded/menu/view data) may already be cached,
    // so purge it before trusting the SW's offline cache again.
    await clearServiceWorkerCaches();
    window.location.reload();
  }
}

function clearServiceWorkerCaches() {
  return new Promise((resolve) => {
    const controller = navigator.serviceWorker.controller;
    if (!controller) {
      resolve();
      return;
    }
    const channel = new MessageChannel();
    channel.port1.onmessage = () => resolve();
    controller.postMessage({ type: "CLEAR_CACHES" }, [channel.port2]);
    setTimeout(resolve, 2000);
  });
}
