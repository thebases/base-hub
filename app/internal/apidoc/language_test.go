package apidoc

import (
	"testing"
	"testing/fstest"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestReadDocumentLanguageFallback(t *testing.T) {
	orm.SetDescriptionFS(fstest.MapFS{
		"m/en/a.md": {Data: []byte("english a")},
		"m/vn/a.md": {Data: []byte("tiếng việt a")},
		"m/en/b.md": {Data: []byte("english b")},
	})
	t.Cleanup(func() { orm.SetDescriptionFS(nil) })

	cases := []struct{ lang, ref, want string }{
		{"vn", "m/a.md", "tiếng việt a"},
		{"vi-VN", "m/a.md", "tiếng việt a"},
		{"en", "m/a.md", "english a"},
		{"fr", "m/a.md", "english a"},
		{"vn", "m/b.md", "english b"},
	}
	for _, c := range cases {
		if got, ok := ReadDocument(c.lang, c.ref); !ok || got != c.want {
			t.Errorf("ReadDocument(%q,%q) = %q,%v want %q", c.lang, c.ref, got, ok, c.want)
		}
	}
	if _, ok := ReadDocument("en", "missing.md"); ok {
		t.Error("missing document must not resolve")
	}
}
