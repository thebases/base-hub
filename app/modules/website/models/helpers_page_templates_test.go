package models

import (
	"context"
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

// chdirAppRoot points relative module-resource lookups (kernelmodules.DefaultRoot "modules") at
// the app root, as in the running server.
func chdirAppRoot(t *testing.T) { t.Helper(); t.Chdir("../../..") }

func TestApplyWebsitePageTemplateBlank(t *testing.T) {
	chdirAppRoot(t)
	values := orm.RecordMap{"name": "About", "page_template": "website/blank"}
	if err := applyWebsitePageTemplate(context.Background(), values); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if strings.TrimSpace(stringValue(values["body_html"])) == "" {
		t.Fatalf("expected body_html from template")
	}
	seo, _ := values["seo"].(map[string]any)
	if seo["title"] != "About" {
		t.Fatalf("seo title = %v, want page name", seo["title"])
	}
}

func TestApplyWebsitePageTemplateKeepsSuppliedDocument(t *testing.T) {
	chdirAppRoot(t)
	doc := map[string]any{"components": []any{}, "x": 1}
	values := orm.RecordMap{"name": "A", "page_template": "website/landing", "document": doc}
	if err := applyWebsitePageTemplate(context.Background(), values); err != nil {
		t.Fatal(err)
	}
	if values["document"].(map[string]any)["x"] != 1 {
		t.Fatalf("supplied document was overwritten")
	}
}

func TestApplyWebsitePageTemplateUnknown(t *testing.T) {
	chdirAppRoot(t)
	if err := applyWebsitePageTemplate(context.Background(), orm.RecordMap{"page_template": "website/nope"}); err == nil {
		t.Fatalf("expected error for unknown template")
	}
}

func TestLoadWebsitePageTemplatesWebsiteSet(t *testing.T) {
	chdirAppRoot(t)
	tpls, err := loadWebsitePageTemplates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, tpl := range tpls {
		have[tpl.Key] = true
	}
	for _, k := range []string{"website/blank", "website/landing", "website/about", "website/contact", "website/terms", "website/privacy"} {
		if !have[k] {
			t.Errorf("missing template %s", k)
		}
	}
}
