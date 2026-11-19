package models

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const WebsiteMenuModelName = "tbl.website_menu"

// websiteNavMenuID is the id of the header list the publish step fills from
// tbl.website_menu. Module-contributed items keep the data-nav-key convention.
const websiteNavMenuID = "site-nav-menu"

type WebsiteMenuRecord struct {
	orm.BaseModel
	SiteID          *int64 `bun:"site_id" json:"site_id" meta:"kind:many2one,model:tbl.website_site,ondelete:CASCADE,index,required,label:Site,help:Site this menu entry belongs to."`
	ParentID        *int64 `bun:"parent_id" json:"parent_id" meta:"kind:many2one,model:tbl.website_menu,ondelete:CASCADE,index,label:Parent,help:Parent entry; empty means a top-level entry."`
	Name            string `bun:"name" json:"name" meta:"required,label:Name,help:Label shown in the navigation."`
	PageID          *int64 `bun:"page_id" json:"page_id" meta:"kind:many2one,model:tbl.website_page,ondelete:SET NULL,index,label:Page,help:Page this entry links to. Takes precedence over URL."`
	URL             string `bun:"url" json:"url" meta:"label:URL,help:Link used when no page is selected (relative path or http/https/mailto/tel)."`
	Sequence        int64  `bun:"sequence,notnull,default:10" json:"sequence" meta:"label:Sequence,help:Ordering among siblings."`
	NewWindow       bool   `bun:"new_window,notnull,default:false" json:"new_window" meta:"label:New Window,help:Open the link in a new tab."`
	IsVisible       bool   `bun:"is_visible,notnull,default:true" json:"is_visible" meta:"label:Visible,help:Hidden entries are not rendered."`
	IsMegaMenu      bool   `bun:"is_mega_menu,notnull,default:false" json:"is_mega_menu" meta:"label:Mega Menu,help:Render this entry's dropdown from the mega menu content instead of its child entries."`
	MegaContentHTML string `bun:"mega_content_html" json:"mega_content_html" meta:"label:Mega Menu Content,help:Sanitized HTML shown in the mega menu panel."`
}

var WebsiteMenuModel = orm.Define[WebsiteMenuRecord](&Models, 440, orm.ModelSpec{
	Name:        WebsiteMenuModelName,
	Label:       "Website Menu",
	Description: "Nested navigation entries rendered into the site header at publish",
	Prepare:     prepareWebsiteMenu,
})

func normalizeWebsiteMenuValues(values orm.RecordMap) error {
	if err := sanitizeWebsiteHTMLFields(values, "mega_content_html"); err != nil {
		return err
	}
	if raw, ok := values["url"]; ok && raw != nil {
		u := strings.TrimSpace(stringValue(raw))
		if !websiteSafeURL(u) {
			return fmt.Errorf("tbl.website_menu url %q uses an unsupported scheme", u)
		}
		values["url"] = u
	}
	return nil
}

// ensureWebsiteMenuParentSite requires the parent entry to belong to the same site.
func ensureWebsiteMenuParentSite(ctx context.Context, parentID, siteID int64) error {
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(WebsiteMenuModelName, nil), []string{fmt.Sprint(parentID)}, []string{"site_id"})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return fmt.Errorf("tbl.website_menu parent_id %d not found", parentID)
	}
	if ps := anyToInt64(rows[0]["site_id"]); ps != siteID {
		return fmt.Errorf("tbl.website_menu parent must belong to the same site")
	}
	return nil
}

// prepareWebsiteMenu sanitizes a menu payload and enforces the same-site parent and no-cycle rules
// before the guarded create/write persists it.
func prepareWebsiteMenu(ctx context.Context, op orm.WriteOp) error {
	working := op.Values
	if err := normalizeWebsiteMenuValues(working); err != nil {
		return err
	}
	parent, ok := int64FromAny(working["parent_id"])
	if !ok || parent <= 0 {
		return nil
	}
	if op.Create {
		site, _ := int64FromAny(working["site_id"])
		return ensureWebsiteMenuParentSite(ctx, parent, site)
	}
	// A cycle (entry under its own descendant) would make the tree unrenderable.
	if err := ensureWebsiteMenuNoCycle(ctx, op.IDs, parent); err != nil {
		return err
	}
	site, hasSite := int64FromAny(working["site_id"])
	if !hasSite && len(op.IDs) > 0 {
		rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(WebsiteMenuModelName, op.Model), op.IDs[:1], []string{"site_id"})
		if err != nil || len(rows) == 0 {
			return fmt.Errorf("tbl.website_menu entry not found")
		}
		site = anyToInt64(rows[0]["site_id"])
	}
	return ensureWebsiteMenuParentSite(ctx, parent, site)
}

func ensureWebsiteMenuNoCycle(ctx context.Context, ids []string, parentID int64) error {
	self := map[int64]bool{}
	for _, id := range ids {
		if n, ok := int64FromAny(id); ok {
			self[n] = true
		}
	}
	ref := orm.RuntimeModelRef(WebsiteMenuModelName, nil)
	cur := parentID
	for depth := 0; cur > 0 && depth < 64; depth++ {
		if self[cur] {
			return fmt.Errorf("tbl.website_menu parent_id would create a cycle")
		}
		rows, err := orm.RuntimeRepositoryRead(ctx, ref, []string{fmt.Sprint(cur)}, []string{"parent_id"})
		if err != nil || len(rows) == 0 {
			return err
		}
		cur, _ = int64FromAny(rows[0]["parent_id"])
	}
	return nil
}

// loadWebsiteMenuItems returns the site's visible entries, resolving page links
// to route paths so rendering stays pure.
func loadWebsiteMenuItems(ctx context.Context, siteID int64) ([]orm.RecordMap, error) {
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WebsiteMenuModelName, nil), orm.RecordMap{
		"site_id": siteID, "__order": "sequence asc, id asc", "__limit": 1000,
	}, nil)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	pages, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WebsitePageModelName, nil), orm.RecordMap{
		"site_id": siteID, "__limit": 1000,
	}, []string{"id", "route_path", "is_homepage"})
	if err != nil {
		return nil, err
	}
	routes := map[int64]string{}
	for _, p := range pages {
		routes[anyToInt64(p["id"])] = normalizeRoutePath(stringValue(p["route_path"]), boolFromAny(p["is_homepage"]))
	}
	for _, r := range rows {
		if pid, ok := int64FromAny(r["page_id"]); ok && pid > 0 {
			if route, found := routes[pid]; found {
				r["__href"] = route
			}
		}
		if _, set := r["__href"]; !set {
			r["__href"] = stringValue(r["url"])
		}
	}
	return rows, nil
}

// renderWebsiteMenuHTML renders the menu tree as <li> items (the content of
// ul#site-nav-menu). Names and hrefs are escaped; mega content was sanitized on
// write and is sanitized again here.
func renderWebsiteMenuHTML(items []orm.RecordMap) string {
	children := map[int64][]orm.RecordMap{}
	byID := map[int64]orm.RecordMap{}
	for _, it := range items {
		byID[anyToInt64(it["id"])] = it
	}
	// An entry shows only when it and every ancestor are visible; hiding a dropdown hides its
	// children. Entries whose parent is missing (other site or deleted) are dropped, not promoted.
	var shown func(it orm.RecordMap, depth int) bool
	shown = func(it orm.RecordMap, depth int) bool {
		if !boolFromAny(it["is_visible"]) || depth > 64 {
			return false
		}
		parent, _ := int64FromAny(it["parent_id"])
		if parent <= 0 {
			return true
		}
		p, ok := byID[parent]
		return ok && shown(p, depth+1)
	}
	for _, it := range items {
		if !shown(it, 0) {
			continue
		}
		parent, _ := int64FromAny(it["parent_id"])
		children[parent] = append(children[parent], it)
	}
	for k := range children {
		sort.SliceStable(children[k], func(i, j int) bool {
			return anyToInt64(children[k][i]["sequence"]) < anyToInt64(children[k][j]["sequence"])
		})
	}
	var b bytes.Buffer
	var walk func(parent int64, depth int)
	walk = func(parent int64, depth int) {
		for _, it := range children[parent] {
			id := anyToInt64(it["id"])
			kids := children[id]
			mega := boolFromAny(it["is_mega_menu"]) && strings.TrimSpace(stringValue(it["mega_content_html"])) != ""
			dropdown := len(kids) > 0 || mega
			liClass := "nav-item"
			if dropdown {
				liClass += " dropdown"
			}
			if depth > 0 {
				liClass = "dropdown-item-wrap"
			}
			fmt.Fprintf(&b, `<li class="%s" data-menu-id="%d">`, liClass, id)
			href := stringValue(it["__href"])
			if !websiteSafeURL(href) {
				href = ""
			}
			linkClass := "nav-link"
			if depth > 0 {
				linkClass = "dropdown-item"
			}
			attrs := ""
			if boolFromAny(it["new_window"]) {
				attrs = ` target="_blank" rel="noopener noreferrer"`
			}
			if dropdown {
				fmt.Fprintf(&b, `<a class="%s dropdown-toggle" href="%s" data-bs-toggle="dropdown" aria-expanded="false"%s>%s</a>`,
					linkClass, xhtml.EscapeString(firstNonEmpty(href, "#")), attrs, xhtml.EscapeString(stringValue(it["name"])))
				if mega {
					clean, _ := sanitizeWebsiteHTML(stringValue(it["mega_content_html"]))
					fmt.Fprintf(&b, `<div class="dropdown-menu bh-mega-menu">%s</div>`, clean)
				} else {
					b.WriteString(`<ul class="dropdown-menu">`)
					walk(id, depth+1)
					b.WriteString(`</ul>`)
				}
			} else {
				fmt.Fprintf(&b, `<a class="%s" href="%s"%s>%s</a>`, linkClass, xhtml.EscapeString(firstNonEmpty(href, "#")), attrs, xhtml.EscapeString(stringValue(it["name"])))
			}
			b.WriteString(`</li>`)
		}
	}
	walk(0, 0)
	return b.String()
}

// injectWebsiteMenuIntoHeader replaces the <li> children of ul#site-nav-menu in
// the header markup with menuHTML, keeping any child carrying data-nav-key
// (module-contributed entries such as blog) that the menu does not duplicate.
// Returns the header unchanged when the list is absent or the menu is empty.
func injectWebsiteMenuIntoHeader(headerHTML, menuHTML string) (string, error) {
	if strings.TrimSpace(menuHTML) == "" || strings.TrimSpace(headerHTML) == "" {
		return headerHTML, nil
	}
	ctxNode := &xhtml.Node{Type: xhtml.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := xhtml.ParseFragment(strings.NewReader(headerHTML), ctxNode)
	if err != nil {
		return "", err
	}
	menuNodes, err := xhtml.ParseFragment(strings.NewReader("<ul>"+menuHTML+"</ul>"), ctxNode)
	if err != nil || len(menuNodes) == 0 {
		return headerHTML, err
	}
	var target *xhtml.Node
	var find func(*xhtml.Node)
	find = func(n *xhtml.Node) {
		if target != nil {
			return
		}
		if n.Type == xhtml.ElementNode && n.DataAtom == atom.Ul {
			for _, a := range n.Attr {
				if a.Key == "id" && a.Val == websiteNavMenuID {
					target = n
					return
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			find(c)
		}
	}
	for _, n := range nodes {
		find(n)
	}
	if target == nil {
		return headerHTML, nil
	}
	var kept []*xhtml.Node
	for c := target.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == xhtml.ElementNode {
			for _, a := range c.Attr {
				if a.Key == "data-nav-key" {
					kept = append(kept, c)
					break
				}
			}
		}
	}
	for target.FirstChild != nil {
		target.RemoveChild(target.FirstChild)
	}
	for c := menuNodes[0].FirstChild; c != nil; {
		next := c.NextSibling
		menuNodes[0].RemoveChild(c)
		target.AppendChild(c)
		c = next
	}
	for _, k := range kept {
		if k.Parent != nil {
			k.Parent.RemoveChild(k)
		}
		target.AppendChild(k)
	}
	var out bytes.Buffer
	for _, n := range nodes {
		if err := xhtml.Render(&out, n); err != nil {
			return "", err
		}
	}
	return out.String(), nil
}
