package models

import (
	"context"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

// The converted create/write overrides now live in ModelSpec.Prepare; these cover the DB-free rules.
func TestPrepareWebsiteRedirectCreate(t *testing.T) {
	op := orm.WriteOp{Create: true, Values: orm.RecordMap{"url_from": "old/", "url_to": "/new"}}
	if err := prepareWebsiteRedirect(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	if op.Values["url_from"] != "/old" || op.Values["redirect_type"] != "301" {
		t.Fatalf("not normalised: %v", op.Values)
	}
	bad := orm.WriteOp{Create: true, Values: orm.RecordMap{"url_from": "/a", "url_to": "//evil.com"}}
	if err := prepareWebsiteRedirect(context.Background(), bad); err == nil {
		t.Fatal("protocol-relative target must be rejected")
	}
}

func TestPrepareWebsiteMenuRejectsUnsafeURL(t *testing.T) {
	op := orm.WriteOp{Create: true, Values: orm.RecordMap{"name": "x", "url": "javascript:alert(1)"}}
	if err := prepareWebsiteMenu(context.Background(), op); err == nil {
		t.Fatal("javascript: url must be rejected")
	}
}

func TestPrepareWebsiteSnippetSanitizesDefinition(t *testing.T) {
	op := orm.WriteOp{Create: true, Values: orm.RecordMap{"definition": map[string]any{"html": "<p>a</p><script>x()</script>"}}}
	if err := prepareWebsiteSnippet(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	if h := op.Values["definition"].(map[string]any)["html"].(string); h == "<p>a</p><script>x()</script>" {
		t.Fatalf("script not stripped: %q", h)
	}
}
