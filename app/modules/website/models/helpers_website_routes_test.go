package models

import (
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestWebsiteLangRoute(t *testing.T) {
	if websiteLangRoute("vi", "/") != "/vi" || websiteLangRoute("vi", "/about") != "/vi/about" {
		t.Fatal("unexpected language routes")
	}
}

func routeSite() orm.RecordMap {
	return orm.RecordMap{"name": "Acme", "default_canonical_base": "https://acme.test/", "default_language": "en", "supported_languages": []any{"en", "vi"}}
}

func TestBuildWebsiteRouteEntriesHeadData(t *testing.T) {
	pages := []orm.RecordMap{
		{"id": int64(1), "name": "Home", "route_path": "/", "is_homepage": true, "is_indexed": true},
		{"id": int64(2), "name": "About", "route_path": "/about", "is_indexed": false, "seo": map[string]any{"description": "About us"}},
	}
	trs := []orm.RecordMap{
		{"page_id": int64(2), "lang": "vi", "deploy_path": "/x", "seo": map[string]any{"title": "Giới thiệu"}},
		{"page_id": int64(2), "lang": "fr", "deploy_path": ""},
	}
	routes, home := buildWebsiteRouteEntries(routeSite(), pages, map[int64]string{1: "Home", 2: "About"}, trs)
	if home != "/" || len(routes) != 3 {
		t.Fatalf("home=%q routes=%d", home, len(routes))
	}
	about := routes[1].SEO
	if about["robots"] != "noindex,follow" || about["title"] != "About | Acme" || about["canonical"] != "https://acme.test/about" || about["lang"] != "en" {
		t.Fatalf("about head data: %v", about)
	}
	if _, ok := routes[0].SEO["langs"]; ok {
		t.Fatal("a page without translations has no language links")
	}
	vi := routes[2]
	if vi.Route != "/vi/about" || vi.ComponentName != "About_vi" || vi.SEO["lang"] != "vi" || vi.SEO["title"] != "Giới thiệu | Acme" || vi.SEO["description"] != "About us" {
		t.Fatalf("translation entry: %+v", vi)
	}
	alts, _ := vi.SEO["alternates"].([]map[string]string)
	if len(alts) != 3 || alts[0]["lang"] != "en" || alts[1]["lang"] != "x-default" || alts[2]["href"] != "https://acme.test/vi/about" {
		t.Fatalf("alternates: %v", alts)
	}
	if langs, _ := vi.SEO["langs"].([]map[string]string); len(langs) != 2 || langs[1]["path"] != "/vi/about" {
		t.Fatalf("langs: %v", vi.SEO["langs"])
	}
}

func TestWebsiteSitemapListsTranslationsWithAlternates(t *testing.T) {
	pages := []orm.RecordMap{{"id": int64(2), "name": "About", "route_path": "/about", "is_indexed": true}, {"id": int64(3), "name": "Hidden", "route_path": "/hidden", "is_indexed": false}}
	trs := []orm.RecordMap{{"page_id": int64(2), "lang": "vi", "deploy_path": "/x"}}
	xml := buildWebsiteSitemapXML(routeSite(), pages, trs)
	for _, want := range []string{"<loc>https://acme.test/about</loc>", "<loc>https://acme.test/vi/about</loc>", `hreflang="vi" href="https://acme.test/vi/about"`, `hreflang="en" href="https://acme.test/about"`, "xmlns:xhtml"} {
		if !strings.Contains(xml, want) {
			t.Errorf("sitemap missing %q:\n%s", want, xml)
		}
	}
	if strings.Contains(xml, "hidden") {
		t.Error("non-indexed page must not be listed")
	}
}

func TestIndexHTMLCarriesCustomCodeAndLanguage(t *testing.T) {
	site := routeSite()
	site["custom_head_html"] = `<meta name="x-verify" content="1">`
	site["custom_footer_html"] = `<script>window.tracked=1</script>`
	out := buildWebsiteIndexHTML(site, nil)
	head, foot := strings.Index(out, "x-verify"), strings.Index(out, "window.tracked")
	if !strings.Contains(out, `<html lang="en">`) || head < 0 || head > strings.Index(out, "</head>") || foot < strings.Index(out, "main.js") {
		t.Fatalf("custom code misplaced:\n%s", out)
	}
}

func TestMainJSCarriesRouteHeadData(t *testing.T) {
	js := buildWebsiteMainJS([]websiteSiteRouteEntry{{Route: "/", ComponentName: "Home", SEO: map[string]any{"title": "Home", "lang": "en"}}}, "/")
	for _, want := range []string{`"seo"`, "function applySeo", "applySeo(entry.seo)", "data-bh-langs"} {
		if !strings.Contains(js, want) {
			t.Errorf("main.js missing %q", want)
		}
	}
}
