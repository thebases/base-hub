const CACHE_NAME = "bwe-site-2-v24-1792086285";
const PRECACHE_URLS = [
  "./",
  "./index.html",
  "./styles.css",
  "./manifest.json",
  "./sitemap.xml",
  "./main.js",
  "./vendor/baseui.min.js",
  "./vendor/bootstrap.min.css",
  "./pages/Home/Home.js",
  "./pages/Home/Home.xml",
  "./pages/Home/Home.css"
];

self.addEventListener("install", (event) => {
  self.skipWaiting();
  event.waitUntil(
    caches.open(CACHE_NAME).then((cache) =>
      // Per-URL and best-effort: addAll rejects the whole install when any single URL
      // (e.g. "./" or sitemap.xml, which are not served under /_site/) is not a 2xx.
      Promise.all(PRECACHE_URLS.map((url) => cache.add(url).catch(() => {}))),
    ),
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches.keys().then((keys) => Promise.all(keys.filter((k) => k !== CACHE_NAME).map((k) => caches.delete(k)))).then(() => self.clients.claim()),
  );
});

self.addEventListener("fetch", (event) => {
  if (event.request.method !== "GET") return;
  const isNavigation = event.request.mode === "navigate";
  event.respondWith(
    isNavigation
      ? fetch(event.request).catch(() => caches.match("./index.html"))
      : caches.match(event.request).then((cached) => cached ?? fetch(event.request)),
  );
});
