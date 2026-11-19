package models

import (
	"strings"
	"testing"
)

func TestSanitizeBlogHTMLStripsActiveContent(t *testing.T) {
	in := `<p onclick="x()">Hi <script>alert(1)</script><b>there</b></p><a href="javascript:alert(1)">bad</a><a href="https://ok.test" target="_blank">ok</a><img src="x.png" onerror="y()"><iframe src="//evil"></iframe><style>p{}</style>`
	out := SanitizeBlogHTML(in)
	for _, banned := range []string{"script", "onclick", "onerror", "javascript:", "iframe", "alert", "style", "p{}"} {
		if strings.Contains(out, banned) {
			t.Fatalf("output still contains %q: %s", banned, out)
		}
	}
	for _, want := range []string{"<b>there</b>", `href="https://ok.test"`, `rel="noopener noreferrer"`, `<img src="x.png">`} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q: %s", want, out)
		}
	}
}

func TestSanitizeBlogHTMLEscapesText(t *testing.T) {
	if got := SanitizeBlogHTML(`a &lt;script&gt; b`); strings.Contains(got, "<script") {
		t.Fatalf("escaped text must stay escaped: %s", got)
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Hello, World!":          "hello-world",
		"Chào mừng đến Việt Nam": "chao-mung-den-viet-nam",
		"   ":                    "post",
		"日本語":                    "post",
	}
	for in, want := range cases {
		if got := slugify(in, "post"); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBlogReadMinutes(t *testing.T) {
	if got := blogReadMinutes(""); got != 0 {
		t.Fatalf("empty body read time = %d", got)
	}
	if got := blogReadMinutes("<p>one two three</p>"); got != 1 {
		t.Fatalf("short body read time = %d", got)
	}
	long := "<p>" + strings.Repeat("word ", 450) + "</p>"
	if got := blogReadMinutes(long); got != 3 {
		t.Fatalf("450 words read time = %d, want 3", got)
	}
}

func TestBlogPlainTextDropsScript(t *testing.T) {
	if got := BlogPlainText(`<p>a</p><script>bad()</script><p>b</p>`); got != "a b" {
		t.Fatalf("plain text = %q", got)
	}
}
