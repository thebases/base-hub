package models

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Allowlist sanitizer for builder-authored HTML (body_html, header_html,
// footer_html). HTML is the stored page format, so every write path (RPC, MCP,
// import) must pass through sanitizeWebsiteHTML before the value is persisted.
// Anything not on the allowlist is dropped, never escaped into the output.

var websiteAllowedTags = map[atom.Atom]bool{
	atom.A: true, atom.Abbr: true, atom.Address: true, atom.Article: true, atom.Aside: true,
	atom.B: true, atom.Blockquote: true, atom.Br: true, atom.Button: true, atom.Caption: true,
	atom.Cite: true, atom.Code: true, atom.Col: true, atom.Colgroup: true, atom.Dd: true,
	atom.Details: true, atom.Div: true, atom.Dl: true, atom.Dt: true, atom.Em: true,
	atom.Figcaption: true, atom.Figure: true, atom.Footer: true, atom.H1: true, atom.H2: true,
	atom.H3: true, atom.H4: true, atom.H5: true, atom.H6: true, atom.Header: true,
	atom.Hr: true, atom.I: true, atom.Img: true, atom.Label: true, atom.Li: true,
	atom.Main: true, atom.Mark: true, atom.Nav: true, atom.Ol: true, atom.P: true,
	atom.Picture: true, atom.Pre: true, atom.Q: true, atom.S: true, atom.Section: true,
	atom.Small: true, atom.Source: true, atom.Span: true, atom.Strong: true, atom.Sub: true,
	atom.Summary: true, atom.Sup: true, atom.Table: true, atom.Tbody: true, atom.Td: true,
	atom.Tfoot: true, atom.Th: true, atom.Thead: true, atom.Time: true, atom.Tr: true,
	atom.U: true, atom.Ul: true, atom.Video: true,
	// Form controls: a form has no action/method, so it can only be handled by the data-bh-form behavior
	// (JSON to this site's own form endpoint). Password and file inputs are not allowed.
	atom.Form: true, atom.Input: true, atom.Textarea: true, atom.Select: true, atom.Option: true,
}

// websiteDroppedWithContent tags are removed together with their children,
// because their content is code or markup that must not surface as text.
var websiteDroppedWithContent = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Iframe: true, atom.Object: true,
	atom.Embed: true, atom.Applet: true, atom.Template: true, atom.Noscript: true,
	atom.Link: true, atom.Meta: true, atom.Base: true, atom.Svg: true, atom.Math: true,
}

var websiteAllowedGlobalAttrs = map[string]bool{
	"id": true, "class": true, "title": true, "lang": true, "dir": true, "role": true,
	"style": true, "tabindex": true,
}

var websiteAllowedTagAttrs = map[atom.Atom]map[string]bool{
	atom.A:        {"href": true, "target": true, "rel": true, "download": true},
	atom.Img:      {"src": true, "alt": true, "width": true, "height": true, "loading": true, "srcset": true, "sizes": true},
	atom.Source:   {"src": true, "srcset": true, "type": true, "media": true, "sizes": true},
	atom.Video:    {"src": true, "poster": true, "controls": true, "loop": true, "muted": true, "playsinline": true, "width": true, "height": true},
	atom.Td:       {"colspan": true, "rowspan": true, "headers": true},
	atom.Th:       {"colspan": true, "rowspan": true, "scope": true, "headers": true},
	atom.Col:      {"span": true},
	atom.Colgroup: {"span": true},
	atom.Time:     {"datetime": true},
	atom.Ol:       {"start": true, "reversed": true, "type": true},
	atom.Button:   {"type": true, "disabled": true},
	atom.Form:     {"novalidate": true},
	atom.Input:    {"autocomplete": true, "type": true, "name": true, "placeholder": true, "required": true, "maxlength": true, "value": true, "min": true, "max": true, "checked": true},
	atom.Textarea: {"autocomplete": true, "name": true, "placeholder": true, "required": true, "maxlength": true, "rows": true},
	atom.Select:   {"name": true, "required": true},
	atom.Option:   {"value": true, "selected": true},
	atom.Label:    {"for": true},
	atom.Details:  {"open": true},
}

// websiteAllowedDataAttrs are the builder/behavior contract attributes kept in
// published markup (stable block key, options state, behavior hooks).
func websiteAllowedAttrName(tag atom.Atom, name string) bool {
	name = strings.ToLower(name)
	if strings.HasPrefix(name, "on") {
		return false
	}
	if strings.HasPrefix(name, "data-bhe-") {
		// Editor-only state never survives a save.
		return false
	}
	if strings.HasPrefix(name, "data-") || strings.HasPrefix(name, "aria-") {
		return true
	}
	if websiteAllowedGlobalAttrs[name] {
		return true
	}
	return websiteAllowedTagAttrs[tag][name]
}

// websiteAllowedAutocomplete are the autocomplete hints a form field may carry.
var websiteAllowedAutocomplete = map[string]bool{"off": true, "on": true, "name": true, "email": true, "tel": true, "username": false}

// websiteAllowedControlType limits `type` on inputs and buttons. Anything else (password, file, image,
// hidden, ...) is dropped, which turns the control into a plain text input or a plain button.
func websiteAllowedControlType(tag atom.Atom, v string) bool {
	switch tag {
	case atom.Input:
		switch v {
		case "text", "email", "tel", "number", "checkbox", "radio", "date", "url":
			return true
		}
		return false
	case atom.Button:
		return v == "button" || v == "submit" || v == "reset"
	}
	return true
}

// websiteSafeURL reports whether a URL attribute value uses a permitted scheme.
func websiteSafeURL(raw string) bool {
	v := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, raw)
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return true
	}
	i := strings.IndexAny(v, ":/?#")
	if i < 0 || v[i] != ':' {
		return true // relative reference
	}
	switch v[:i] {
	case "http", "https", "mailto", "tel":
		return true
	}
	return false
}

// websiteSafeStyle rejects inline style values that can load resources or run
// script-like expressions.
func websiteSafeStyle(v string) bool {
	l := strings.ToLower(v)
	for _, bad := range []string{"expression(", "javascript:", "@import", "behavior:", "-moz-binding"} {
		if strings.Contains(l, bad) {
			return false
		}
	}
	// url(...) is needed for background images; only permitted schemes pass.
	rest := l
	for {
		i := strings.Index(rest, "url(")
		if i < 0 {
			return true
		}
		rest = rest[i+4:]
		end := strings.IndexByte(rest, ')')
		if end < 0 {
			return false
		}
		target := strings.Trim(strings.TrimSpace(rest[:end]), `"'`)
		if !websiteSafeURL(target) {
			return false
		}
		rest = rest[end+1:]
	}
}

// editorOnlyClass reports builder-only classes that are stripped on save.
func websiteEditorOnlyClass(c string) bool {
	return strings.HasPrefix(c, "bhe-")
}

// sanitizeWebsiteHTML parses a fragment leniently, keeps only allowlisted
// tags/attributes/URL schemes, strips editor-only (bhe-, data-bhe-) state, and
// re-serializes. The result is HTML; use normalizeWebsiteHTMLToXMLFragment for
// the strict XML needed by the Owl scaffold.
func sanitizeWebsiteHTML(input string) (string, error) {
	ctxNode := &xhtml.Node{Type: xhtml.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := xhtml.ParseFragment(strings.NewReader(input), ctxNode)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	for _, n := range nodes {
		sanitizeWebsiteNode(&buf, n)
	}
	return buf.String(), nil
}

func sanitizeWebsiteNode(buf *bytes.Buffer, n *xhtml.Node) {
	switch n.Type {
	case xhtml.TextNode:
		buf.WriteString(xhtml.EscapeString(n.Data))
	case xhtml.ElementNode:
		if websiteDroppedWithContent[n.DataAtom] {
			return
		}
		if !websiteAllowedTags[n.DataAtom] {
			// Unknown wrapper: unwrap, keep sanitized children.
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				sanitizeWebsiteNode(buf, c)
			}
			return
		}
		buf.WriteByte('<')
		buf.WriteString(n.Data)
		seen := map[string]bool{}
		for _, a := range n.Attr {
			key := strings.ToLower(a.Key)
			if a.Namespace != "" || seen[key] || !websiteAllowedAttrName(n.DataAtom, key) {
				continue
			}
			val := a.Val
			switch key {
			case "href", "src", "poster":
				if !websiteSafeURL(val) {
					continue
				}
			case "srcset":
				ok := true
				for _, part := range strings.Split(val, ",") {
					fields := strings.Fields(part)
					if len(fields) > 0 && !websiteSafeURL(fields[0]) {
						ok = false
					}
				}
				if !ok {
					continue
				}
			case "style":
				if !websiteSafeStyle(val) {
					continue
				}
			case "class":
				kept := make([]string, 0, 4)
				for _, c := range strings.Fields(val) {
					if !websiteEditorOnlyClass(c) {
						kept = append(kept, c)
					}
				}
				if len(kept) == 0 {
					continue
				}
				val = strings.Join(kept, " ")
			case "target":
				if val != "_blank" && val != "_self" {
					continue
				}
			case "tabindex":
				if val != "-1" && val != "0" {
					continue
				}
			case "autocomplete":
				if !websiteAllowedAutocomplete[strings.ToLower(val)] {
					continue
				}
			case "type":
				if !websiteAllowedControlType(n.DataAtom, strings.ToLower(val)) {
					continue
				}
			}
			seen[key] = true
			buf.WriteByte(' ')
			buf.WriteString(key)
			buf.WriteString(`="`)
			buf.WriteString(xhtml.EscapeString(val))
			buf.WriteByte('"')
		}
		if n.DataAtom == atom.A && seenTargetBlank(n) {
			// Prevent reverse-tabnabbing for links opening a new window.
			if !seen["rel"] {
				buf.WriteString(` rel="noopener noreferrer"`)
			}
		}
		if websiteVoidElements[n.DataAtom] {
			buf.WriteString("/>")
			return
		}
		buf.WriteByte('>')
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			sanitizeWebsiteNode(buf, c)
		}
		buf.WriteString("</")
		buf.WriteString(n.Data)
		buf.WriteByte('>')
	default:
		// Comments, doctype and document nodes carry no content we keep, but a
		// document wrapper may hold children.
		if n.Type == xhtml.DocumentNode {
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				sanitizeWebsiteNode(buf, c)
			}
		}
	}
}

func seenTargetBlank(n *xhtml.Node) bool {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, "target") && a.Val == "_blank" {
			return true
		}
	}
	return false
}

// sanitizeWebsiteHTMLFields sanitizes the named HTML fields in place when the
// payload carries them; absent keys are left alone so content-only writes
// keep working.
func sanitizeWebsiteHTMLFields(values map[string]any, fields ...string) error {
	for _, field := range fields {
		raw, ok := values[field]
		if !ok || raw == nil {
			continue
		}
		clean, err := sanitizeWebsiteHTML(stringValue(raw))
		if err != nil {
			return fmt.Errorf("%s: %w", field, err)
		}
		values[field] = clean
	}
	return nil
}

// websitePublishHTMLKey carries the composed (header + body + footer) HTML from
// publishWebsitePage to writeWebsitePageComponentFiles; it is never persisted.
const websitePublishHTMLKey = "__publish_html"

// composeWebsitePublishHTML builds the published markup for builder pages:
// site-level header and footer around the page body. Returns "" when the page
// has no body_html (legacy document pages keep their own chrome).
func composeWebsitePublishHTML(siteRow, pageRow map[string]any, menuHTML string) (string, error) {
	body := stringValue(pageRow["body_html"])
	if strings.TrimSpace(body) == "" {
		return "", nil
	}
	// Re-sanitize at publish: rows written before the sanitizer, or by raw
	// repository writes, must not reach the public site unchecked.
	parts := make([]string, 0, 3)
	for i, chunk := range []string{stringValue(siteRow["header_html"]), body, stringValue(siteRow["footer_html"])} {
		if strings.TrimSpace(chunk) == "" {
			continue
		}
		// A page that carries its own header/footer (migrated pages, starter templates) keeps it; the
		// site-level one would only duplicate it.
		if (i == 0 && websiteBodyHasChrome(body, "site-header")) || (i == 2 && websiteBodyHasChrome(body, "site-footer")) {
			continue
		}
		// The builder's "None" template stores a marker region: the site header/footer is hidden.
		if (i == 0 && strings.Contains(chunk, `data-bh-header="none"`)) || (i == 2 && strings.Contains(chunk, `data-bh-footer="none"`)) {
			continue
		}
		clean, err := sanitizeWebsiteHTML(chunk)
		if err != nil {
			return "", err
		}
		if i == 0 {
			// Menu tree fills ul#site-nav-menu after sanitizing, because the
			// rendered dropdown markup carries data-bs-* hooks only we emit.
			if clean, err = injectWebsiteMenuIntoHeader(clean, menuHTML); err != nil {
				return "", err
			}
		}
		parts = append(parts, clean)
	}
	return strings.Join(parts, "\n"), nil
}

// websiteBodyHasChrome reports whether the page markup already contains the site header or footer
// element (id="site-header" / id="site-footer").
func websiteBodyHasChrome(body, id string) bool {
	return strings.Contains(body, `id="`+id+`"`)
}

const websiteCustomCSSMaxBytes = 100 * 1024

var websiteCSSBannedRe = regexp.MustCompile(`(?i)@import|@charset|expression\s*\(|javascript:|vbscript:|behavior\s*:|-moz-binding|</|<!--|\\0|\\u|\\[0-9a-f]{2,6}`)
var websiteCSSURLRe = regexp.MustCompile(`(?i)url\(\s*["']?([^"')]*)["']?\s*\)`)

// sanitizeWebsiteCustomCSS validates a page stylesheet. It cannot rewrite CSS safely, so it either
// returns the text unchanged or rejects it: no imports, no script-capable constructs, no CSS
// escapes (which could hide the banned ones), balanced braces, and urls only to same-site paths,
// https addresses or data:image. Page CSS is admin-authored, but it ships to every visitor.
func sanitizeWebsiteCustomCSS(css string) (string, error) {
	css = strings.TrimSpace(css)
	if css == "" {
		return "", nil
	}
	if len(css) > websiteCustomCSSMaxBytes {
		return "", fmt.Errorf("custom css is larger than %d KB", websiteCustomCSSMaxBytes/1024)
	}
	if websiteCSSBannedRe.MatchString(css) {
		return "", fmt.Errorf("custom css contains a construct that is not allowed (@import, escapes, script urls, expressions)")
	}
	depth := 0
	for _, r := range css {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth < 0 {
				return "", fmt.Errorf("custom css has an unbalanced closing brace")
			}
		}
	}
	if depth != 0 {
		return "", fmt.Errorf("custom css has an unclosed block")
	}
	for _, m := range websiteCSSURLRe.FindAllStringSubmatch(css, -1) {
		target := strings.TrimSpace(m[1])
		ok := strings.HasPrefix(target, "/") && !strings.HasPrefix(target, "//") ||
			strings.HasPrefix(strings.ToLower(target), "https://") ||
			strings.HasPrefix(strings.ToLower(target), "data:image/") && !strings.Contains(strings.ToLower(target), "svg")
		if !ok {
			return "", fmt.Errorf("custom css url %q is not allowed", target)
		}
	}
	return css, nil
}

// sanitizeWebsiteCustomCSSField validates values["custom_css"] when the payload carries it.
func sanitizeWebsiteCustomCSSField(values map[string]any) error {
	raw, ok := values["custom_css"]
	if !ok {
		return nil
	}
	if raw == nil {
		values["custom_css"] = ""
		return nil
	}
	clean, err := sanitizeWebsiteCustomCSS(stringValue(raw))
	if err != nil {
		return err
	}
	values["custom_css"] = clean
	return nil
}
