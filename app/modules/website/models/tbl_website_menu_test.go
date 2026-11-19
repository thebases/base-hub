package models

import (
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestRenderWebsiteMenuHTMLNestsAndEscapes(t *testing.T) {
	items := []orm.RecordMap{
		{"id": int64(1), "name": "Home", "__href": "/", "is_visible": true, "sequence": int64(1)},
		{"id": int64(2), "name": "A&B", "__href": "/ab", "is_visible": true, "sequence": int64(2)},
		{"id": int64(3), "name": "Child", "__href": "/child", "parent_id": int64(2), "is_visible": true, "sequence": int64(1), "new_window": true},
		{"id": int64(4), "name": "Bad", "__href": "javascript:alert(1)", "is_visible": true, "sequence": int64(3)},
	}
	out := renderWebsiteMenuHTML(items)
	for _, want := range []string{`A&amp;B`, `class="dropdown-menu"`, `href="/child"`, `rel="noopener noreferrer"`} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in %s", want, out)
		}
	}
	if strings.Contains(out, "javascript:") {
		t.Errorf("unsafe href leaked: %s", out)
	}
}

func TestRenderWebsiteMenuHidesChildrenOfHiddenParent(t *testing.T) {
	items := []orm.RecordMap{
		{"id": int64(1), "name": "Parent", "__href": "/p", "is_visible": false, "sequence": int64(1)},
		{"id": int64(2), "name": "Kid", "__href": "/k", "parent_id": int64(1), "is_visible": true, "sequence": int64(1)},
		{"id": int64(3), "name": "Stray", "__href": "/s", "parent_id": int64(99), "is_visible": true, "sequence": int64(2)},
	}
	if out := renderWebsiteMenuHTML(items); out != "" {
		t.Fatalf("hidden parent and orphan must not render, got %s", out)
	}
}

func TestInjectWebsiteMenuKeepsModuleItems(t *testing.T) {
	header := `<header><ul id="site-nav-menu"><li>old</li><li data-nav-key="blog"><a href="/blog">Blog</a></li></ul></header>`
	out, err := injectWebsiteMenuIntoHeader(header, `<li class="nav-item"><a href="/">Home</a></li>`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "old") || !strings.Contains(out, "Home") || !strings.Contains(out, `data-nav-key="blog"`) {
		t.Fatalf("unexpected: %s", out)
	}
	if strings.Index(out, "Home") > strings.Index(out, "blog") {
		t.Errorf("menu entries must precede module items: %s", out)
	}
}

func TestComposeWebsitePublishHTML(t *testing.T) {
	site := map[string]any{"header_html": `<header><ul id="site-nav-menu"></ul></header>`, "footer_html": `<footer>f</footer>`}
	got, err := composeWebsitePublishHTML(site, map[string]any{"body_html": `<main>b<script>x</script></main>`}, `<li><a href="/">H</a></li>`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "<main>b</main>") || !strings.Contains(got, ">H<") || !strings.HasSuffix(got, "<footer>f</footer>") || strings.Contains(got, "script") {
		t.Fatalf("unexpected: %s", got)
	}
	if empty, _ := composeWebsitePublishHTML(site, map[string]any{}, ""); empty != "" {
		t.Fatalf("legacy pages must not be composed, got %q", empty)
	}
}

func TestNormalizeWebsiteRedirectValues(t *testing.T) {
	bad := []orm.RecordMap{
		{"url_from": "/a", "redirect_type": "301", "url_to": "//evil.com"},
		{"url_from": "/a", "redirect_type": "301", "url_to": "javascript:x"},
		{"url_from": "/a", "redirect_type": "301"},
		{"url_from": "/", "redirect_type": "410"},
		{"url_from": "/a", "redirect_type": "500"},
		{"url_from": "/a", "redirect_type": "301", "url_to": "/\t/evil.com"},
		{"url_from": "/a", "redirect_type": "301", "url_to": "/\\evil.com"},
		{"url_from": "/a", "redirect_type": "301", "url_to": "/a"},
		{"url_from": "/a?x=1", "redirect_type": "410"},
	}
	for i, v := range bad {
		if err := normalizeWebsiteRedirectValues(v, true); err == nil {
			t.Errorf("case %d should fail: %v", i, v)
		}
	}
	ok := orm.RecordMap{"url_from": "old/", "url_to": "/new"}
	if err := normalizeWebsiteRedirectValues(ok, true); err != nil || ok["url_from"] != "/old" || ok["redirect_type"] != "301" {
		t.Fatalf("got %v err %v", ok, err)
	}
	if err := normalizeWebsiteRedirectValues(orm.RecordMap{"url_from": "/g", "redirect_type": "410"}, true); err != nil {
		t.Fatal(err)
	}
}
