package models

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChromeTemplatesShipFullSets(t *testing.T) {
	chdirAppRoot(t)
	want := map[string][]string{
		"header": {"default", "hamburger", "boxed", "stretch", "vertical", "search", "sales_one", "sales_two", "sales_three", "sales_four", "sidebar"},
		"footer": {"default", "descriptive", "centered", "links", "minimalist", "contact", "call_to_action", "headline", "mega", "mega_columns", "mega_links", "mega_cards"},
	}
	for kind, names := range want {
		tpls, err := loadWebsiteChromeTemplates(context.Background(), kind)
		if err != nil {
			t.Fatal(err)
		}
		have := map[string]websiteChromeTemplate{}
		for _, tpl := range tpls {
			have[tpl.Key] = tpl
		}
		for _, n := range names {
			tpl, ok := have["website/"+n]
			if !ok {
				t.Errorf("%s template %q missing", kind, n)
				continue
			}
			// Every header/footer must carry its region id and marker, or options and behaviors cannot find it.
			id, marker := `id="site-header"`, `data-bh-header="`+n+`"`
			if kind == "footer" {
				id, marker = `id="site-footer"`, `data-bh-footer="`+n+`"`
			}
			if !strings.Contains(tpl.HTML, id) || !strings.Contains(tpl.HTML, marker) {
				t.Errorf("%s/%s lacks %s or %s", kind, n, id, marker)
			}
			if kind == "header" && !strings.Contains(tpl.HTML, `id="site-nav-menu"`) {
				t.Errorf("header/%s has no ul#site-nav-menu slot for the menu", n)
			}
		}
	}
}

// Shipped markup must already be what the sanitizer would store, or saving a template would silently change it.
func TestChromeTemplatesStableUnderSanitizer(t *testing.T) {
	chdirAppRoot(t)
	files, _ := filepath.Glob("modules/website/static/chrome/*/*.xml")
	if len(files) == 0 {
		t.Fatal("no chrome templates found")
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parseOwlTemplateFile(raw)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		tpl := websiteChromeTemplate{HTML: parsed.HTML}
		once, err := sanitizeWebsiteHTML(tpl.HTML)
		if err != nil {
			t.Fatal(err)
		}
		twice, _ := sanitizeWebsiteHTML(once)
		if once != twice {
			t.Errorf("%s: sanitizer is not idempotent on this template", f)
		}
		if strings.Count(once, "<") < strings.Count(tpl.HTML, "<")-2 {
			t.Errorf("%s: sanitizer dropped markup", f)
		}
	}
}

func TestChromeTemplatesRejectUnknownKind(t *testing.T) {
	chdirAppRoot(t)
	if _, err := loadWebsiteChromeTemplates(context.Background(), "sidebar"); err == nil {
		t.Fatal("expected error for unknown kind")
	}
}

func TestDefaultChromeUsesTemplates(t *testing.T) {
	chdirAppRoot(t)
	out, err := defaultWebsiteChrome(context.Background(), nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if !strings.Contains(m["header_html"].(string), `data-bh-header="default"`) || !strings.Contains(m["footer_html"].(string), `data-bh-footer="default"`) {
		t.Fatalf("default chrome does not come from the default templates: %v", m)
	}
}

func TestHeaderFooterOptionSchemasValid(t *testing.T) {
	chdirAppRoot(t)
	schemas, err := loadWebsiteOptionSchemas(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, s := range schemas {
		have[s.Key] = true
	}
	for _, k := range []string{"website/header", "website/header_sidebar", "website/footer"} {
		if !have[k] {
			t.Errorf("option schema %s missing or invalid (invalid schemas are skipped at load)", k)
		}
	}
}
