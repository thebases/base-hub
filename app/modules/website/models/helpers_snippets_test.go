package models

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every shipped block must survive the sanitizer unchanged in meaning: non-empty, keeps its
// data-snippet key and bh- class, and carries no reference-implementation vocabulary.
func TestShippedSnippetsAreSafeAndComplete(t *testing.T) {
	files, err := filepath.Glob("../static/snippets/*.xml")
	if err != nil || len(files) < 25 {
		t.Fatalf("expected at least 25 snippets, got %d (%v)", len(files), err)
	}
	for _, f := range files {
		raw, _ := os.ReadFile(f)
		tpl, err := parseOwlTemplateFile(raw)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		s := websiteCatalogSnippet{Name: tpl.Meta["name"], Group: tpl.Meta["group"], Thumbnail: tpl.Meta["thumbnail"]}
		key := strings.TrimSuffix(filepath.Base(f), ".xml")
		clean, err := sanitizeWebsiteHTML(tpl.HTML)
		if err != nil || !strings.Contains(clean, `data-snippet="`+key+`"`) || !strings.Contains(clean, "bh-"+key) {
			t.Errorf("%s: sanitized html lost its block contract: %q (%v)", f, clean, err)
		}
		if !strings.HasPrefix(s.Thumbnail, "data:image/svg+xml") {
			t.Errorf("%s: missing wireframe thumbnail", f)
		}
		if s.Name == "" || s.Group == "" {
			t.Errorf("%s: missing name or group", f)
		}
	}
}

func TestWebsiteSnippetMatches(t *testing.T) {
	s := websiteCatalogSnippet{Name: "Banner", Group: "intro", Keywords: []string{"hero"}}
	for _, q := range []string{"ban", "intro", "her"} {
		if !websiteSnippetMatches(s, q) {
			t.Errorf("%q should match", q)
		}
	}
	if websiteSnippetMatches(s, "zzz") {
		t.Error("zzz should not match")
	}
}
