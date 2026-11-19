package models

// File overview:
// - Responsibility: pure helpers for blog post bodies — allowlist HTML sanitizing, plain-text
//   extraction, teaser generation and read-time estimation. No ORM or I/O.
// - Key symbols: `SanitizeBlogHTML`, `BlogPlainText`, `blogTeaser`, `blogReadMinutes`.

import (
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

var blogAllowedTags = map[string]bool{
	"p": true, "br": true, "hr": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"ul": true, "ol": true, "li": true, "blockquote": true, "pre": true, "code": true,
	"em": true, "strong": true, "b": true, "i": true, "u": true, "s": true, "sub": true, "sup": true, "mark": true,
	"a": true, "img": true, "figure": true, "figcaption": true, "span": true, "div": true,
	"table": true, "thead": true, "tbody": true, "tfoot": true, "tr": true, "th": true, "td": true,
}

// blogDroppedWithContent lists tags whose inner text must disappear too (it is code, not prose).
var blogDroppedWithContent = map[string]bool{"script": true, "style": true, "iframe": true, "object": true, "embed": true, "noscript": true, "template": true}

var blogAllowedAttrs = map[string]map[string]bool{
	"a":   {"href": true, "title": true, "target": true, "rel": true},
	"img": {"src": true, "alt": true, "title": true, "width": true, "height": true, "loading": true},
	"td":  {"colspan": true, "rowspan": true},
	"th":  {"colspan": true, "rowspan": true},
}

// SanitizeBlogHTML keeps an editorial subset of HTML: unknown tags are unwrapped (text kept),
// script-like tags are removed with their content, `on*`/`style` attributes are never emitted and
// URLs must be http(s), mailto or relative. Stored content is trusted-author input, but public
// pages render it unauthenticated, so the render path always passes through here.
func SanitizeBlogHTML(raw string) string {
	tokenizer := html.NewTokenizer(strings.NewReader(raw))
	var out strings.Builder
	skipDepth := 0
	skipTag := ""
	for {
		tokenType := tokenizer.Next()
		if tokenType == html.ErrorToken {
			break // io.EOF, or malformed input: keep what was sanitized so far
		}
		token := tokenizer.Token()
		name := strings.ToLower(token.Data)
		if skipDepth > 0 {
			switch tokenType {
			case html.StartTagToken:
				if name == skipTag {
					skipDepth++
				}
			case html.EndTagToken:
				if name == skipTag {
					skipDepth--
				}
			}
			continue
		}
		switch tokenType {
		case html.TextToken:
			out.WriteString(html.EscapeString(token.Data))
		case html.StartTagToken, html.SelfClosingTagToken:
			if blogDroppedWithContent[name] {
				if tokenType == html.StartTagToken {
					skipDepth, skipTag = 1, name
				}
				continue
			}
			if !blogAllowedTags[name] {
				continue
			}
			out.WriteString("<" + name)
			for _, attr := range token.Attr {
				key := strings.ToLower(attr.Key)
				if !blogAllowedAttrs[name][key] && key != "class" {
					continue
				}
				value := attr.Val
				switch key {
				case "href", "src":
					if !blogSafeURL(value) {
						continue
					}
				case "class":
					value = blogSafeClass(value)
					if value == "" {
						continue
					}
				case "target":
					if value != "_blank" && value != "_self" {
						continue
					}
				}
				out.WriteString(" " + key + `="` + html.EscapeString(value) + `"`)
			}
			if name == "a" {
				// External links open safely regardless of what the author typed.
				out.WriteString(` rel="noopener noreferrer"`)
			}
			out.WriteString(">")
		case html.EndTagToken:
			if blogAllowedTags[name] && name != "br" && name != "hr" && name != "img" {
				out.WriteString("</" + name + ">")
			}
		}
	}
	return out.String()
}

func blogSafeURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "", "http", "https", "mailto":
		return true
	default:
		return false
	}
}

// blogSafeClass keeps only Bootstrap-style class tokens.
func blogSafeClass(raw string) string {
	var kept []string
	for _, token := range strings.Fields(raw) {
		valid := true
		for _, r := range token {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
				valid = false
				break
			}
		}
		if valid {
			kept = append(kept, token)
		}
	}
	return strings.Join(kept, " ")
}

// BlogPlainText flattens HTML into whitespace-normalized visible text (script/style removed).
func BlogPlainText(raw string) string {
	tokenizer := html.NewTokenizer(strings.NewReader(raw))
	var out strings.Builder
	skipTag := ""
	for {
		tokenType := tokenizer.Next()
		if tokenType == html.ErrorToken {
			break
		}
		token := tokenizer.Token()
		name := strings.ToLower(token.Data)
		switch tokenType {
		case html.StartTagToken:
			if blogDroppedWithContent[name] && skipTag == "" {
				skipTag = name
			}
			out.WriteByte(' ')
		case html.EndTagToken:
			if name == skipTag {
				skipTag = ""
			}
			out.WriteByte(' ')
		case html.SelfClosingTagToken:
			out.WriteByte(' ')
		case html.TextToken:
			if skipTag == "" {
				out.WriteString(token.Data)
			}
		}
	}
	return strings.Join(strings.Fields(out.String()), " ")
}

// blogReadMinutes estimates reading time at 200 words/minute, minimum 1 for any non-empty body.
func blogReadMinutes(contentHTML string) int {
	words := len(strings.Fields(BlogPlainText(contentHTML)))
	if words == 0 {
		return 0
	}
	minutes := (words + 199) / 200
	if minutes < 1 {
		minutes = 1
	}
	return minutes
}
