package models

// File overview:
// - Responsibility: decides which public behaviors (`static/js/behaviors/<name>.js`) a published
//   site needs, by scanning the content it serves for each behavior's marker attribute, and renders
//   their script tags. Sites that use no behavior ship none.
// - Key symbols: `websiteBehaviorMarkers`, `websiteBehaviorsUsed`, `renderWebsiteBehaviorScriptTags`.

import (
	"html"
	"sort"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
)

const websiteBehaviorsJSPublicPathPrefix = "/website/static/js/behaviors/"

// websiteBehaviorMarkers maps a marker attribute to the behavior file that handles it. The same
// attribute is what the builder's option schemas and blocks write.
var websiteBehaviorMarkers = map[string]string{
	"data-bh-animate":     "animate",
	"data-bh-countdown":   "countdown",
	"data-bh-parallax":    "parallax",
	"data-bh-lightbox":    "lightbox",
	"data-bh-popup":       "popup",
	"data-bh-form":        "form",
	"data-bh-source":      "datasource",
	"data-bh-lang-switch": "language",
	"data-bh-header":      "chrome",
	"data-bh-scrolltop":   "chrome",
	"data-bh-search":      "chrome",
}

// websiteBehaviorsUsed returns the sorted behavior names whose marker appears in the site chrome or
// in any published page. Order is stable so the generated index.html is deterministic.
func websiteBehaviorsUsed(siteRow orm.RecordMap, pages []orm.RecordMap) []string {
	var content strings.Builder
	content.WriteString(stringValue(siteRow["header_html"]))
	content.WriteString(stringValue(siteRow["footer_html"]))
	for _, page := range pages {
		content.WriteString(stringValue(page["body_html"]))
	}
	text := content.String()
	used := map[string]bool{}
	for marker, name := range websiteBehaviorMarkers {
		if strings.Contains(text, marker+"=") {
			used[name] = true
		}
	}
	names := make([]string, 0, len(used))
	for name := range used {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func renderWebsiteBehaviorScriptTags(names []string) string {
	var b strings.Builder
	for _, name := range names {
		b.WriteString("<script src=\"" + html.EscapeString(websiteBehaviorsJSPublicPathPrefix+name+".js") + "\"></script>\n")
	}
	return b.String()
}
