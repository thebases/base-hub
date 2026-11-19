package models

// File overview:
// - Responsibility: builds the route table of a published site, including the per-route head data
//   (title, description, robots, canonical, hreflang alternates, language links) that main.js applies
//   on every navigation, and the translated routes (`/<lang><route>`).
// - Key symbols: `buildWebsiteRouteEntries`, `websiteRouteSEO`.

import (
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
)

// websiteSiteRouteEntry is one route's slot in the site-wide main.js route table.
type websiteSiteRouteEntry struct {
	Route         string
	ComponentName string
	SEO           map[string]any
	Protected     bool // logged-in-only page: template is gated, no precache, sign-in required
}

// websiteLangRoute prefixes a page route with a language code: "/" -> "/vi", "/about" -> "/vi/about".
func websiteLangRoute(lang, route string) string {
	if route == "/" || route == "" {
		return "/" + lang
	}
	return "/" + lang + route
}

// websiteRouteSEO is the head data of one route. `alternates` are absolute hreflang links (only when
// the site has a canonical base); `langs` are the same links as site paths, for the language selector.
func websiteRouteSEO(siteName, base, route, lang, name string, seo map[string]any, isIndexed bool, fallback map[string]any) map[string]any {
	pick := func(key string) string {
		if v := strings.TrimSpace(stringValue(seo[key])); v != "" {
			return v
		}
		return strings.TrimSpace(stringValue(fallback[key]))
	}
	title := pick("title")
	if title == "" {
		title = name
	}
	if siteName != "" && title != siteName && route != "/" {
		title += " | " + siteName
	}
	out := map[string]any{"title": title, "lang": lang}
	if d := pick("description"); d != "" {
		out["description"] = d
	}
	robots := pick("robots")
	if robots == "" && !isIndexed {
		robots = "noindex,follow"
	}
	if robots != "" {
		out["robots"] = robots
	}
	canonical := pick("canonicalUrl")
	if canonical == "" && base != "" {
		canonical = base + route
	}
	if canonical != "" {
		out["canonical"] = canonical
	}
	return out
}

// buildWebsiteRouteEntries builds the route table for the published pages and their published
// translations, and returns the home route.
func buildWebsiteRouteEntries(siteRow orm.RecordMap, pages []orm.RecordMap, names map[int64]string, translations []orm.RecordMap) ([]websiteSiteRouteEntry, string) {
	siteName := firstNonEmpty(stringValue(siteRow["default_seo_name"]), stringValue(siteRow["name"]))
	base := strings.TrimRight(stringValue(siteRow["default_canonical_base"]), "/")
	defLang := websiteSiteDefaultLanguage(siteRow)
	byPage := map[int64][]orm.RecordMap{}
	for _, t := range translations {
		if stringValue(t["deploy_path"]) != "" {
			pid := anyToInt64(t["page_id"])
			byPage[pid] = append(byPage[pid], t)
		}
	}
	routes := make([]websiteSiteRouteEntry, 0, len(pages))
	homeRoute := "/"
	for _, page := range pages {
		id := anyToInt64(page["id"])
		route := normalizeRoutePath(stringValue(page["route_path"]), boolFromAny(page["is_homepage"]))
		if boolFromAny(page["is_homepage"]) {
			homeRoute = route
		}
		indexed := true
		if v, ok := page["is_indexed"]; ok && v != nil {
			indexed = boolFromAny(v)
		}
		pageSEO := websitePageSEO(page)
		protected := websitePageIsProtected(page)
		// A page that needs a login must not be indexed either.
		entry := websiteRouteSEO(siteName, base, route, defLang, stringValue(page["name"]), pageSEO, indexed && !protected, nil)
		trs := byPage[id]
		var langs []map[string]string
		var alternates []map[string]string
		if len(trs) > 0 {
			langs = append(langs, map[string]string{"lang": defLang, "path": route})
			if base != "" {
				alternates = append(alternates, map[string]string{"lang": defLang, "href": base + route}, map[string]string{"lang": "x-default", "href": base + route})
			}
			for _, t := range trs {
				lang := stringValue(t["lang"])
				langs = append(langs, map[string]string{"lang": lang, "path": websiteLangRoute(lang, route)})
				if base != "" {
					alternates = append(alternates, map[string]string{"lang": lang, "href": base + websiteLangRoute(lang, route)})
				}
			}
			entry["langs"] = langs
			if len(alternates) > 0 {
				entry["alternates"] = alternates
			}
		}
		routes = append(routes, websiteSiteRouteEntry{Route: route, ComponentName: names[id], SEO: entry, Protected: protected})
		for _, t := range trs {
			lang := stringValue(t["lang"])
			troute := websiteLangRoute(lang, route)
			tseo := mapValueOrEmpty(t["seo"])
			te := websiteRouteSEO(siteName, base, troute, lang, stringValue(page["name"]), tseo, indexed && !protected, pageSEO)
			if canonical := strings.TrimSpace(stringValue(tseo["canonicalUrl"])); canonical == "" && base != "" {
				te["canonical"] = base + troute
			} else if canonical == "" {
				delete(te, "canonical")
			}
			te["langs"] = langs
			if len(alternates) > 0 {
				te["alternates"] = alternates
			}
			routes = append(routes, websiteSiteRouteEntry{Route: troute, ComponentName: websiteTranslationComponentName(names[id], lang), SEO: te, Protected: protected})
		}
	}
	return routes, homeRoute
}
