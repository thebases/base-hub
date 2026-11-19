package models

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
)

// regenerateWebsiteSiteRootScaffold rewrites the site's root-level flat
// scaffold files (index.html, main.js, manifest.json, sitemap.xml, styles.css,
// sw.js) plus vendor/, mirroring the legacy editor's exportImport.ts
// buildWebsiteScaffoldFiles output shape. Deliberately does not touch any
// page's own pages/<Name>/* files -- those are only (re)written by
// writeWebsitePageComponentFiles when that specific page publishes, so
// publishing one page never silently pushes another page's unpublished draft
// content live.
func regenerateWebsiteSiteRootScaffold(ctx context.Context, siteID int64) error {
	siteRow, err := loadWebsiteSiteRow(ctx, siteID)
	if err != nil {
		return err
	}
	siteDeployFolder := stringValue(siteRow["deploy_path"])
	if siteDeployFolder == "" {
		return fmt.Errorf("tbl.website_site %v has no deploy_path", siteID)
	}
	// Unpublish and page delete also regenerate without going through ensureWebsiteSiteDeployFolder, so
	// the scaffold writer checks the root itself instead of writing files into a foreign folder.
	confined, ok := ConfinedWebsiteDeployPath(websiteContextDBName(ctx), siteDeployFolder)
	if !ok {
		return fmt.Errorf("tbl.website_site %v deploy_path is outside the tenant public folder; republish the site", siteID)
	}
	siteDeployFolder = confined

	pageRows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WebsitePageModelName, nil), orm.RecordMap{
		"site_id": siteID,
		"__order": "id asc",
		"__limit": 1000,
	}, nil)
	if err != nil {
		return err
	}
	publishedPages := make([]orm.RecordMap, 0, len(pageRows))
	for _, page := range pageRows {
		if stringValue(page["deploy_path"]) != "" {
			publishedPages = append(publishedPages, page)
		}
	}

	if err := ensureWebsiteSiteVendorAssets(siteDeployFolder); err != nil {
		return err
	}

	componentNames := assignWebsiteComponentNames(publishedPages)
	translationRows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WebsitePageTranslationModelName, nil), orm.RecordMap{
		"site_id": siteID, "is_active": true, "__order": "lang asc", "__limit": 2000,
	}, nil)
	if err != nil {
		return err
	}
	routes, homeRoute := buildWebsiteRouteEntries(siteRow, publishedPages, componentNames, translationRows)
	// Behaviors and the sitemap look at what is served: pages plus their published translations.
	servedContent := append([]orm.RecordMap{}, publishedPages...)
	for _, t := range translationRows {
		if stringValue(t["deploy_path"]) != "" {
			servedContent = append(servedContent, orm.RecordMap{"body_html": t["body_html"]})
		}
	}

	// styles.css = copy of the static module base file + this site's theme CSS.
	stylesCSS, err := buildWebsiteStylesCSS(mapValueOrEmpty(siteRow["theme"]))
	if err != nil {
		return err
	}
	files := map[string]string{
		"index.html":    buildWebsiteIndexHTML(siteRow, websiteBehaviorsUsed(siteRow, servedContent)),
		"main.js":       buildWebsiteMainJS(routes, homeRoute),
		"manifest.json": buildWebsiteManifestJSON(siteRow, homeRoute),
		"sitemap.xml":   buildWebsiteSitemapXML(siteRow, publishedPages, translationRows),
		"styles.css":    stylesCSS,
	}
	for name, content := range files {
		fullPath := filepath.Join(siteDeployFolder, name)
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write website site scaffold file %q: %w", fullPath, err)
		}
	}

	precacheURLs := []string{
		"./index.html", "./styles.css", "./manifest.json", "./sitemap.xml", "./main.js",
		"./vendor/baseui.min.js", "./vendor/bootstrap.min.css",
	}
	for _, route := range routes {
		precacheURLs = append(precacheURLs, fmt.Sprintf("./pages/%s/%s.js", route.ComponentName, route.ComponentName))
		// A protected template needs a bearer token, so the service worker cannot precache it (and a
		// cached copy would outlive the visitor's session).
		if !route.Protected {
			precacheURLs = append(precacheURLs, fmt.Sprintf("./pages/%s/%s.xml", route.ComponentName, route.ComponentName))
		}
		precacheURLs = append(precacheURLs, fmt.Sprintf("./pages/%s/%s.css", route.ComponentName, route.ComponentName))
	}
	offlineSupport := true
	if spaManifest := mapValueOrEmpty(siteRow["spa_manifest"]); spaManifest != nil {
		if value, ok := boolValue(spaManifest["offlineSupport"]); ok {
			offlineSupport = value
		}
	}
	// Deliberately not keyed on siteRow["revision"]: that counter is the
	// editor's optimistic-concurrency CAS field and does not change on
	// publish (ensureWebsiteSiteDeployFolder writes deploy_path via a raw
	// RuntimeRepositoryWrite that bypasses writeWebsiteSite's CAS bump). A
	// republish with no unrelated site edit therefore produced byte-identical
	// sw.js content, so browsers never reinstalled the service worker or
	// refetched cached assets -- previously-broken/blank published pages
	// stayed cached forever even after the content was fixed and republished.
	// Including the publish timestamp guarantees every publish changes the
	// script bytes, forcing a fresh install + old-cache eviction.
	cacheName := fmt.Sprintf("bwe-site-%d-v%d-%d", siteID, anyToInt64(siteRow["revision"]), time.Now().Unix())
	swPath := filepath.Join(siteDeployFolder, "sw.js")
	if err := os.WriteFile(swPath, []byte(buildWebsiteServiceWorkerJS(cacheName, precacheURLs, offlineSupport)), 0o644); err != nil {
		return fmt.Errorf("write website site scaffold file %q: %w", swPath, err)
	}
	return nil
}

// websiteFloatValue extracts a numeric SEO field (e.g. seo.priority) from a
// jsonb-decoded value, which may surface as float64, int, or int64 depending
// on the read path.
func websiteFloatValue(raw any) (float64, bool) {
	switch typed := raw.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	default:
		return 0, false
	}
}

func mapValueOrEmpty(raw any) map[string]any {
	if m, ok := raw.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func boolFromAny(raw any) bool {
	value, _ := boolValue(raw)
	return value
}

// timestampValue formats a RecordMap timestamp field for sitemap lastmod:
// runtime reads may hand back either a time.Time (base model columns like
// updated_at) or an already-serialized RFC3339 string, depending on the
// driver layer, so both shapes are handled here rather than at each call site.
func timestampValue(raw any) string {
	switch typed := raw.(type) {
	case time.Time:
		return typed.UTC().Format(time.RFC3339)
	case string:
		return typed
	default:
		return ""
	}
}

// buildWebsiteIndexHTML is the Go port of exportImport.ts's buildIndexHtml:
// the single SPA shell document every route resolves to.
func buildWebsiteIndexHTML(siteRow orm.RecordMap, behaviors []string) string {
	title := firstNonEmpty(stringValue(siteRow["default_seo_name"]), stringValue(siteRow["name"]), "Website")
	return "<!doctype html>\n<html lang=\"" + html.EscapeString(websiteSiteDefaultLanguage(siteRow)) + "\">\n<head>\n<meta charset=\"utf-8\">\n" +
		"<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n" +
		"<title>" + html.EscapeString(title) + "</title>\n" +
		"<link rel=\"icon\" href=\"data:,\">\n" +
		"<link rel=\"manifest\" href=\"./manifest.json\">\n" +
		"<link rel=\"sitemap\" type=\"application/xml\" href=\"./sitemap.xml\">\n" +
		"<link rel=\"stylesheet\" href=\"./vendor/bootstrap.min.css\">\n" +
		"<link rel=\"stylesheet\" href=\"./styles.css\">\n" +
		stringValue(siteRow["custom_head_html"]) + "\n" +
		"</head>\n<body>\n<div id=\"app\"></div>\n" +
		"<script src=\"./vendor/baseui.min.js\"></script>\n" +
		renderWebsiteStaticScriptTags() +
		renderWebsiteBehaviorScriptTags(behaviors) +
		"<script src=\"./main.js\" type=\"module\"></script>\n" +
		stringValue(siteRow["custom_footer_html"]) + "\n" +
		"</body>\n</html>\n"
}

// buildWebsiteMainJS is the Go port of exportImport.ts's buildMainJs: a
// minimal client-side router resolving the initial route (falling back to
// the homepage) and intercepting same-site link clicks via pushState.
func buildWebsiteMainJS(routes []websiteSiteRouteEntry, homeRoute string) string {
	routeTable := make(map[string]map[string]any, len(routes))
	for _, route := range routes {
		routeTable[route.Route] = map[string]any{
			"name":   route.ComponentName,
			"module": fmt.Sprintf("./pages/%s/%s.js", route.ComponentName, route.ComponentName),
			"css":    fmt.Sprintf("./pages/%s/%s.css", route.ComponentName, route.ComponentName),
		}
		if route.Protected {
			routeTable[route.Route]["protected"] = true
		}
		if route.SEO != nil {
			routeTable[route.Route]["seo"] = route.SEO
		}
	}
	routeTableJSON, _ := json.MarshalIndent(routeTable, "", "  ")
	homeRouteJSON, _ := json.Marshal(homeRoute)

	return fmt.Sprintf(`const ROUTES = %s;
const HOME_ROUTE = %s;

let currentApp = null;
let cssLink = null;

// Per-route head data: title, description, robots, canonical, hreflang alternates, <html lang>.
// Language links are exposed as <link rel="alternate" data-bh-seo> for the language selector.
function setMeta(name, content) {
  let el = document.head.querySelector('meta[name="' + name + '"][data-bh-seo]');
  if (!content) { if (el) el.remove(); return; }
  if (!el) { el = document.createElement("meta"); el.setAttribute("name", name); el.setAttribute("data-bh-seo", ""); document.head.appendChild(el); }
  el.setAttribute("content", content);
}

function applySeo(seo) {
  if (!seo) return;
  document.title = seo.title || document.title;
  document.documentElement.setAttribute("lang", seo.lang || "en");
  setMeta("description", seo.description);
  setMeta("robots", seo.robots);
  document.head.querySelectorAll("link[data-bh-seo]").forEach((el) => el.remove());
  const add = (rel, attrs) => {
    const el = document.createElement("link");
    el.setAttribute("rel", rel);
    el.setAttribute("data-bh-seo", "");
    Object.keys(attrs).forEach((k) => el.setAttribute(k, attrs[k]));
    document.head.appendChild(el);
  };
  if (seo.canonical) add("canonical", { href: seo.canonical });
  (seo.alternates || []).forEach((a) => add("alternate", { hreflang: a.lang, href: a.href }));
  document.documentElement.setAttribute("data-bh-langs", JSON.stringify(seo.langs || []));
}

async function mountRoute(path) {
  const entry = ROUTES[path] ?? ROUTES[HOME_ROUTE];
  if (!entry) return;
  if (!cssLink) {
    cssLink = document.createElement("link");
    cssLink.rel = "stylesheet";
    cssLink.id = "page-css";
    document.head.appendChild(cssLink);
  }
  cssLink.href = entry.css;
  const mod = await import(entry.module);
  const PageComponent = mod[entry.name];
  if (currentApp) {
    currentApp.destroy();
    currentApp = null;
  }
  const root = document.getElementById("app");
  root.innerHTML = "";
  // owl.mount resolves to the root component, not the app, so keep the App to be able to destroy it on navigation.
  const app = new owl.App(PageComponent);
  await app.mount(root);
  currentApp = app;
  applySeo(entry.seo);
}

function navigate(path) {
  if (!(path in ROUTES)) return;
  history.pushState({}, "", path);
  void mountRoute(path);
}

document.body.addEventListener("click", (ev) => {
  const link = ev.target.closest ? ev.target.closest("a[href]") : null;
  if (!link) return;
  const href = link.getAttribute("href");
  if (!href || !(href in ROUTES) || link.target === "_blank") return;
  ev.preventDefault();
  navigate(href);
});

window.addEventListener("popstate", () => mountRoute(location.pathname));

if ("serviceWorker" in navigator) {
  navigator.serviceWorker.register("./sw.js").catch(() => {});
}

void mountRoute(location.pathname);
`, routeTableJSON, homeRouteJSON)
}

// websiteSpaManifestDefaults mirrors spaService.ts's DEFAULT_SPA_MANIFEST.
var websiteSpaManifestDefaults = map[string]string{
	"name":            "My Website",
	"shortName":       "My Site",
	"description":     "",
	"themeColor":      "#0d6efd",
	"backgroundColor": "#ffffff",
	"display":         "standalone",
	"orientation":     "any",
}

func websiteSpaManifestField(spaManifest map[string]any, key string) string {
	if str, ok := spaManifest[key].(string); ok && str != "" {
		return str
	}
	return websiteSpaManifestDefaults[key]
}

// buildWebsiteManifestJSON is the Go port of spaService.ts's
// resolveSpaManifest + buildManifestJson. Icon entries are left empty --
// favicon/icon embedding is a follow-up (no attachment-content-read helper
// exists in this module yet).
func buildWebsiteManifestJSON(siteRow orm.RecordMap, startURL string) string {
	spaManifest := mapValueOrEmpty(siteRow["spa_manifest"])
	name := websiteSpaManifestField(spaManifest, "name")
	shortName := websiteSpaManifestField(spaManifest, "shortName")
	if shortName == "" {
		shortName = name
	}
	manifest := map[string]any{
		"name":             name,
		"short_name":       shortName,
		"start_url":        startURL,
		"scope":            "./",
		"display":          websiteSpaManifestField(spaManifest, "display"),
		"orientation":      websiteSpaManifestField(spaManifest, "orientation"),
		"theme_color":      websiteSpaManifestField(spaManifest, "themeColor"),
		"background_color": websiteSpaManifestField(spaManifest, "backgroundColor"),
		"icons":            []any{},
	}
	if description := websiteSpaManifestField(spaManifest, "description"); description != "" {
		manifest["description"] = description
	}
	encoded, _ := json.MarshalIndent(manifest, "", "  ")
	return string(encoded)
}

// buildWebsiteServiceWorkerJS is the Go port of spaService.ts's
// buildServiceWorkerJs (both the pass-through and full-precache variants).
func buildWebsiteServiceWorkerJS(cacheName string, precacheURLs []string, offlineSupport bool) string {
	cacheNameJSON, _ := json.Marshal(cacheName)
	if !offlineSupport {
		return fmt.Sprintf(`const CACHE_NAME = %s;
self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (event) => event.waitUntil(self.clients.claim()));
self.addEventListener("fetch", () => {});
`, cacheNameJSON)
	}
	precacheJSON, _ := json.MarshalIndent(precacheURLs, "", "  ")
	return fmt.Sprintf(`const CACHE_NAME = %s;
const PRECACHE_URLS = %s;

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
`, cacheNameJSON, precacheJSON)
}

// buildWebsiteSitemapXML renders sitemap.xml: one entry per published page that is indexed and not
// opted out via seo.includeInSitemap, plus one per published translation, each group cross-linked with
// hreflang alternates. The origin is tbl.website_site.default_canonical_base (the server has no
// browser origin to fall back on).
func buildWebsiteSitemapXML(siteRow orm.RecordMap, pages []orm.RecordMap, translations []orm.RecordMap) string {
	base := strings.TrimRight(stringValue(siteRow["default_canonical_base"]), "/")
	defLang := websiteSiteDefaultLanguage(siteRow)
	byPage := map[int64][]orm.RecordMap{}
	for _, t := range translations {
		if stringValue(t["deploy_path"]) != "" {
			byPage[anyToInt64(t["page_id"])] = append(byPage[anyToInt64(t["page_id"])], t)
		}
	}
	var urls strings.Builder
	n := 0
	for _, page := range pages {
		if websitePageIsProtected(page) {
			continue
		}
		if v, ok := page["is_indexed"]; ok && v != nil && !boolFromAny(v) {
			continue
		}
		seo := websitePageSEO(page)
		if includeInSitemap, ok := boolValue(seo["includeInSitemap"]); ok && !includeInSitemap {
			continue
		}
		route := normalizeRoutePath(stringValue(page["route_path"]), boolFromAny(page["is_homepage"]))
		changefreq := websiteSEOStringField(seo, "changeFrequency")
		if changefreq == "" {
			changefreq = "weekly"
		}
		priority := "0.5"
		if boolFromAny(page["is_homepage"]) {
			priority = "1"
		}
		if priorityValue, ok := websiteFloatValue(seo["priority"]); ok {
			priority = strconv.FormatFloat(priorityValue, 'g', -1, 64)
		}
		lastmod := timestampValue(page["updated_at"])
		if len(lastmod) >= 10 {
			lastmod = lastmod[:10]
		}
		trs := byPage[anyToInt64(page["id"])]
		var alternates strings.Builder
		if len(trs) > 0 {
			alternates.WriteString("    <xhtml:link rel=\"alternate\" hreflang=\"" + html.EscapeString(defLang) + "\" href=\"" + html.EscapeString(base+route) + "\"/>\n")
			for _, t := range trs {
				lang := stringValue(t["lang"])
				alternates.WriteString("    <xhtml:link rel=\"alternate\" hreflang=\"" + html.EscapeString(lang) + "\" href=\"" + html.EscapeString(base+websiteLangRoute(lang, route)) + "\"/>\n")
			}
		}
		entry := func(path string) string {
			return "  <url>\n    <loc>" + html.EscapeString(base+path) + "</loc>\n" +
				"    <lastmod>" + html.EscapeString(lastmod) + "</lastmod>\n" +
				"    <changefreq>" + html.EscapeString(changefreq) + "</changefreq>\n" +
				"    <priority>" + priority + "</priority>\n" + alternates.String() + "  </url>"
		}
		for _, path := range append([]string{route}, translatedPaths(trs, route)...) {
			if n > 0 {
				urls.WriteByte('\n')
			}
			urls.WriteString(entry(path))
			n++
		}
	}
	return `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:xhtml="http://www.w3.org/1999/xhtml">` + "\n" +
		urls.String() + "\n</urlset>\n"
}

func translatedPaths(trs []orm.RecordMap, route string) []string {
	out := make([]string, 0, len(trs))
	for _, t := range trs {
		out = append(out, websiteLangRoute(stringValue(t["lang"]), route))
	}
	return out
}
