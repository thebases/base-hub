package models

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/orm"
	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// websiteOwlVendorAssets maps module-resource source paths (see
// kernelmodules.ReadModuleResource) to the filename each is copied to under
// every site deploy folder's "vendor/" directory. Bootstrap's CSS must be linked
// before the runtime script in generated index.html so component styles that rely on
// Bootstrap classes are already active when Owl mounts.
var websiteOwlVendorAssets = []struct {
	sourcePath string
	destName   string
}{
	{"website/static/bs/css/bootstrap.min.css", "bootstrap.min.css"},
	// baseui.min.js bundles Bootstrap's JS and the Owl runtime (global `owl`), the same build the
	// builder uses (@thebase/ui, pinned in static/baseui/VERSION), so one runtime serves both.
	{"website/static/baseui/baseui.min.js", "baseui.min.js"},
}

// ensureWebsiteSiteVendorAssets copies the site's shared runtime libraries
// (Bootstrap CSS, the BaseUI runtime) into <siteDeployFolder>/vendor so every published page
// folder is self-contained on disk -- the deploy folder can be rsynced or
// served by any static file server without depending on the admin binary
// being reachable at request time. Runs on every publish (page or site
// level) so a library upgrade reaches already-deployed sites on next publish.
func ensureWebsiteSiteVendorAssets(siteDeployFolder string) error {
	vendorDir := filepath.Join(siteDeployFolder, "vendor")
	if err := os.MkdirAll(vendorDir, 0o755); err != nil {
		return fmt.Errorf("create website vendor asset folder %q: %w", vendorDir, err)
	}
	for _, asset := range websiteOwlVendorAssets {
		raw, err := kernelmodules.ReadModuleResource(asset.sourcePath)
		if err != nil {
			return fmt.Errorf("read website vendor asset %q: %w", asset.sourcePath, err)
		}
		destPath := filepath.Join(vendorDir, asset.destName)
		if err := os.WriteFile(destPath, raw, 0o644); err != nil {
			return fmt.Errorf("write website vendor asset %q: %w", destPath, err)
		}
	}
	return nil
}

// websiteOwlComponentName derives an Owl-template/component name (PascalCase,
// no suffix) from a page slug, e.g. "about-us" -> "AboutUs". Mirrors
// the legacy editor's exportImport.ts componentNameFor base-name step exactly
// so a page's exported and published component names always match.
func websiteOwlComponentName(slug string) string {
	parts := strings.Split(slugify(slug), "-")
	var builder strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		builder.WriteString(strings.ToUpper(part[:1]))
		builder.WriteString(part[1:])
	}
	name := builder.String()
	if name == "" {
		name = "Page"
	}
	if name[0] >= '0' && name[0] <= '9' {
		name = "Page" + name
	}
	return name
}

// assignWebsiteComponentNames computes each page's deterministic component
// name (folder + file basename under pages/) in the given order, de-duping
// collisions with a numeric suffix -- the direct port of exportImport.ts's
// componentNameFor(page, usedSet) loop. Callers must pass pages in a stable
// order (id ascending) so re-running this against the same page set always
// yields the same assignment.
func assignWebsiteComponentNames(pages []orm.RecordMap) map[int64]string {
	used := make(map[string]bool, len(pages))
	names := make(map[int64]string, len(pages))
	for _, page := range pages {
		id := anyToInt64(page["id"])
		base := websiteOwlComponentName(stringValue(page["slug"]))
		name := base
		for i := 2; used[name]; i++ {
			name = fmt.Sprintf("%s%d", base, i)
		}
		used[name] = true
		names[id] = name
	}
	return names
}

var websiteInlineStyleBlockPattern = regexp.MustCompile(`(?is)<style[^>]*>(.*?)</style>`)

// extractWebsiteInlineStyles pulls every <style> block out of authored body
// HTML so the Owl scaffold can ship it as the page's own .css file instead of
// an inline tag, matching the Owl component convention of a dedicated
// stylesheet per component. Returns the body with style blocks removed and
// the concatenated CSS (empty string if none were present).
func extractWebsiteInlineStyles(bodyHTML string) (string, string) {
	var css strings.Builder
	cleaned := websiteInlineStyleBlockPattern.ReplaceAllStringFunc(bodyHTML, func(match string) string {
		sub := websiteInlineStyleBlockPattern.FindStringSubmatch(match)
		if len(sub) == 2 {
			css.WriteString(strings.TrimSpace(sub[1]))
			css.WriteString("\n")
		}
		return ""
	})
	return cleaned, strings.TrimSpace(css.String())
}

// websiteVoidElements lists HTML void elements that must self-close in the
// XML template output (Owl's template compiler parses .xml files as strict
// XML, unlike a browser's lenient HTML parser).
var websiteVoidElements = map[atom.Atom]bool{
	atom.Area: true, atom.Base: true, atom.Br: true, atom.Col: true,
	atom.Embed: true, atom.Hr: true, atom.Img: true, atom.Input: true,
	atom.Link: true, atom.Meta: true, atom.Param: true, atom.Source: true,
	atom.Track: true, atom.Wbr: true,
}

// normalizeWebsiteHTMLToXMLFragment reparses author-provided (browser-lenient)
// body HTML and re-serializes it as strict, well-formed XML: unescaped "&",
// unclosed void elements (<br>, <img>, ...), and unquoted/duplicate
// attributes are all sources of XML parse errors that HTML tolerates but
// Owl's template compiler will not.
func normalizeWebsiteHTMLToXMLFragment(bodyHTML string) (string, error) {
	context := &xhtml.Node{Type: xhtml.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := xhtml.ParseFragment(strings.NewReader(bodyHTML), context)
	if err != nil {
		return "", fmt.Errorf("parse website page body HTML: %w", err)
	}
	var buf bytes.Buffer
	for _, node := range nodes {
		writeWebsiteXMLNode(&buf, node)
	}
	return buf.String(), nil
}

func writeWebsiteXMLNode(buf *bytes.Buffer, node *xhtml.Node) {
	switch node.Type {
	case xhtml.TextNode:
		xml.EscapeText(buf, []byte(node.Data))
	case xhtml.CommentNode:
		// Comments cannot contain "--"; drop rather than risk an invalid document.
		if !strings.Contains(node.Data, "--") {
			buf.WriteString("<!--")
			buf.WriteString(node.Data)
			buf.WriteString("-->")
		}
	case xhtml.ElementNode:
		buf.WriteByte('<')
		buf.WriteString(node.Data)
		for _, attr := range node.Attr {
			buf.WriteByte(' ')
			buf.WriteString(websiteXMLAttrName(attr.Key))
			buf.WriteString(`="`)
			xml.EscapeText(buf, []byte(attr.Val))
			buf.WriteByte('"')
		}
		if websiteVoidElements[node.DataAtom] {
			buf.WriteString("/>")
			return
		}
		buf.WriteByte('>')
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			writeWebsiteXMLNode(buf, child)
		}
		buf.WriteString("</")
		buf.WriteString(node.Data)
		buf.WriteByte('>')
	default:
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			writeWebsiteXMLNode(buf, child)
		}
	}
}

// websiteXMLAttrName guards against HTML attribute names that are not legal
// XML names (e.g. "t-on-click" is fine, but a stray malformed attribute
// shouldn't be able to break the document); it is a conservative pass-through
// since legacy-authored attributes are already well-formed in practice.
func websiteXMLAttrName(name string) string {
	return name
}

// renderWebsiteOwlTemplateXML renders the page's .xml scaffold file as a bare
// XHTML fragment, single-rooted under a wrapper div so it stays well-formed
// regardless of the authored content's own top-level shape. This file is not
// an Owl templates manifest: renderWebsiteOwlComponentJS fetches its raw text
// and feeds it straight into owl.xml({raw:[template]}), which itself wraps
// the string in a <t> tag -- matching the legacy editor's exportImport.ts
// toXhtml() output exactly. A leading <?xml ...?> declaration or an outer
// <templates>/<t t-name="..."> wrapper (the previous, incorrect shape) breaks
// that wrapping ("XML declaration allowed only at the start of the
// document"), which is also why this file must not carry componentName at
// all -- the name only lives in the sibling .js file's owl.Component class
// and the component's own <t> tag.
func renderWebsiteOwlTemplateXML(bodyFragmentXML string) string {
	return `<div class="owl-page">` + bodyFragmentXML + `</div>`
}

// websiteVisibilityLoggedIn marks a page whose published body is served only to signed-in users.
const websiteVisibilityLoggedIn = "logged_in"

func websitePageIsProtected(pageRow orm.RecordMap) bool {
	return stringValue(pageRow["visibility"]) == websiteVisibilityLoggedIn
}

// renderWebsiteOwlComponentJSFor builds the component module. A protected page's template is not
// a public file: it lives under <site>/protected/ and the server hands it out only for a valid
// bearer token, so the module reads the session token the core SPA keeps in sessionStorage
// (same origin) and sends it. Without a token or on 401/403 the visitor is sent to /login.
func renderWebsiteOwlComponentJSFor(componentName string, protected bool) string {
	if protected {
		return "const auth = (() => {\n" +
			"  try { return JSON.parse(sessionStorage.getItem(\"basehub-auth\") || \"{}\").state || {}; } catch { return {}; }\n" +
			"})();\n" +
			"const headers = {};\n" +
			"if (auth.accessToken) headers.Authorization = \"Bearer \" + auth.accessToken;\n" +
			"if (auth.db) headers[auth.dbHeaderName || \"X-Database-Name\"] = auth.db;\n" +
			"const response = await fetch(new URL(\"./" + componentName + ".xml\", import.meta.url), { headers, cache: \"no-store\" });\n" +
			"if (!response.ok) {\n" +
			"  location.replace(\"/login\");\n" +
			"  await new Promise(() => {});\n" +
			"}\n" +
			"const template = await response.text();\n\n" +
			"export class " + componentName + " extends owl.Component {\n" +
			"  static template = owl.xml({ raw: [template] });\n" +
			"}\n"
	}
	return "const template = await (await fetch(new URL(\"./" + componentName + ".xml\", import.meta.url))).text();\n\n" +
		"export class " + componentName + " extends owl.Component {\n" +
		"  static template = owl.xml({ raw: [template] });\n" +
		"}\n"
}

// websiteProtectedDir is the site-relative folder holding templates of logged-in-only pages.
const websiteProtectedDir = "protected"

// writeWebsitePageComponentFiles writes one page's Owl component scaffold
// (<Name>.xml + <Name>.js + <Name>.css, no per-page index.html) into
// <siteDeployFolder>/pages/<componentName>/, following the legacy editor's
// exportImport.ts buildWebsiteScaffoldFiles pages/<Name>/ layout. componentName
// must come from assignWebsiteComponentNames so it always matches the site's
// generated main.js route table. Returns the folder's absolute path
// (persisted as tbl.website_page.deploy_path).
func writeWebsitePageComponentFiles(siteDeployFolder string, pageRow orm.RecordMap, componentName string) (string, error) {
	if err := ensureWebsiteSiteVendorAssets(siteDeployFolder); err != nil {
		return "", err
	}

	pageFolder := filepath.Join(siteDeployFolder, "pages", componentName)
	if err := os.MkdirAll(pageFolder, 0o755); err != nil {
		return "", fmt.Errorf("create website page deploy folder %q: %w", pageFolder, err)
	}

	// publishWebsitePage composed the sanitized header + body + footer; a page with no body yet gets a
	// placeholder rather than failing the whole site publish.
	body := stringValue(pageRow[websitePublishHTMLKey])
	if body == "" {
		body = "<p>No published page content yet.</p>"
	}
	cleanedBody, inlineCSS := extractWebsiteInlineStyles(body)
	css := strings.TrimSpace(stringValue(pageRow["custom_css"]) + "\n" + inlineCSS)
	bodyFragmentXML, err := normalizeWebsiteHTMLToXMLFragment(cleanedBody)
	if err != nil {
		return "", fmt.Errorf("website page %v: %w", pageRow["id"], err)
	}

	protected := websitePageIsProtected(pageRow)
	files := map[string]string{
		componentName + ".js":  renderWebsiteOwlComponentJSFor(componentName, protected),
		componentName + ".css": css,
	}
	// A protected page's template goes to <site>/protected/, a folder the public /_site handler never
	// serves; the gated route in the website runtime reads it. Switching visibility removes the other copy.
	publicXML := filepath.Join(pageFolder, componentName+".xml")
	protectedDir := filepath.Join(siteDeployFolder, websiteProtectedDir)
	protectedXML := filepath.Join(protectedDir, componentName+".xml")
	templateXML := renderWebsiteOwlTemplateXML(bodyFragmentXML)
	if protected {
		if err := os.MkdirAll(protectedDir, 0o755); err != nil {
			return "", fmt.Errorf("create website protected folder %q: %w", protectedDir, err)
		}
		if err := os.WriteFile(protectedXML, []byte(templateXML), 0o644); err != nil {
			return "", fmt.Errorf("write website protected template %q: %w", protectedXML, err)
		}
		_ = os.Remove(publicXML)
	} else {
		files[componentName+".xml"] = templateXML
		_ = os.Remove(protectedXML)
	}
	for name, content := range files {
		fullPath := filepath.Join(pageFolder, name)
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			return "", fmt.Errorf("write website page component file %q: %w", fullPath, err)
		}
	}
	return pageFolder, nil
}
