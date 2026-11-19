package models

// File overview:
// - Responsibility: discovers page templates shipped by modules as Owl template files under
//   `<module>/static/templates/*.xml`, lists them for the "New page" popup and applies one to a
//   newly created page.
// - Key symbols: `loadWebsitePageTemplates`, `applyWebsitePageTemplate`, `listWebsitePageTemplates`.

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/queries"
	"git.thebase.vn/base_hub/internal/services/log"
)

// websiteTemplatesSubdir is where every module keeps its page templates, relative to the module.
const websiteTemplatesSubdir = "static/templates"

// websitePageTemplate is one template file. `Key` is "<module>/<file name without .xml>" and is
// what tbl.website_page.page_template stores; it is derived from the file location, never read
// from the file, so a copied file cannot collide with another module's template.
type websitePageTemplate struct {
	Key         string `json:"key"`
	Module      string `json:"module"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	Sequence    int    `json:"sequence"`
	// HTML is the starter body markup (sanitized again when applied).
	HTML string         `json:"html,omitempty"`
	Seo  map[string]any `json:"seo,omitempty"`
}

// loadWebsitePageTemplates reads every template of every installed module. Templates of modules
// that are not installed in the current database are skipped, so uninstalling website-blog
// removes its blog templates from the popup. Unreadable files are logged and skipped: one bad
// file must not block creating pages.
func loadWebsitePageTemplates(ctx context.Context) ([]websitePageTemplate, error) {
	tree := kernelmodules.ModuleTreeFS()
	if tree == nil {
		return nil, nil
	}
	entries, err := fs.ReadDir(tree, ".")
	if err != nil {
		return nil, err
	}
	var pool = orm.ResolvePGXPool("")
	if env := orm.EnvironmentFromContext(ctx); env != nil {
		pool = orm.ResolvePGXPool(env.DBName())
	}
	var templates []websitePageTemplate
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		module := entry.Name()
		dir := path.Join(module, websiteTemplatesSubdir)
		names, err := kernelmodules.ListModuleResourceDir(dir)
		if err != nil || len(names) == 0 {
			continue
		}
		if module != "website" {
			installed, err := queries.ModuleInstalled(ctx, pool, module)
			if err != nil {
				return nil, err
			}
			if !installed {
				continue
			}
		}
		for _, name := range names {
			if !strings.HasSuffix(strings.ToLower(name), ".xml") {
				continue
			}
			raw, err := kernelmodules.ReadModuleResource(path.Join(dir, name))
			if err != nil {
				log.Warnf("website: read page template %s/%s: %v", dir, name, err)
				continue
			}
			parsed, err := parseOwlTemplateFile(raw)
			if err != nil {
				log.Warnf("website: parse page template %s/%s: %v", dir, name, err)
				continue
			}
			tpl := websitePageTemplate{
				Name: parsed.Meta["name"], Description: parsed.Meta["description"], Icon: parsed.Meta["icon"],
				Sequence: parsed.sequence(), HTML: parsed.HTML,
			}
			if rawSeo := strings.TrimSpace(parsed.Meta["seo"]); rawSeo != "" {
				if err := json.Unmarshal([]byte(rawSeo), &tpl.Seo); err != nil {
					log.Warnf("website: page template %s/%s seo: %v", dir, name, err)
				}
			}
			tpl.Module = module
			tpl.Key = module + "/" + strings.TrimSuffix(name, path.Ext(name))
			if strings.TrimSpace(tpl.Name) == "" {
				tpl.Name = strings.TrimSuffix(name, path.Ext(name))
			}
			templates = append(templates, tpl)
		}
	}
	sort.SliceStable(templates, func(i, j int) bool {
		if templates[i].Sequence != templates[j].Sequence {
			return templates[i].Sequence < templates[j].Sequence
		}
		return templates[i].Key < templates[j].Key
	})
	return templates, nil
}

// listWebsitePageTemplates is the model-level `list_page_templates` RuntimeMethod behind the
// template popup. It returns summaries only; documents stay server-side.
func listWebsitePageTemplates(ctx context.Context, _ *orm.ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
	templates, err := loadWebsitePageTemplates(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(templates))
	for _, tpl := range templates {
		out = append(out, map[string]any{
			"key":         tpl.Key,
			"module":      tpl.Module,
			"name":        tpl.Name,
			"description": tpl.Description,
			"icon":        tpl.Icon,
			"sequence":    tpl.Sequence,
		})
	}
	return out, nil
}

// applyWebsitePageTemplate fills body_html and seo of a page being created from the chosen template.
// It never overwrites a document the caller supplied (MCP/editor creates), and an unknown key is
// an error rather than a silent blank page.
func applyWebsitePageTemplate(ctx context.Context, values orm.RecordMap) error {
	key := stringValue(values["page_template"])
	if key == "" || strings.TrimSpace(stringValue(values["body_html"])) != "" {
		return nil
	}
	templates, err := loadWebsitePageTemplates(ctx)
	if err != nil {
		return err
	}
	for _, tpl := range templates {
		if tpl.Key != key {
			continue
		}
		if strings.TrimSpace(tpl.HTML) != "" {
			clean, err := sanitizeWebsiteHTML(tpl.HTML)
			if err != nil {
				return fmt.Errorf("page template %q: %w", key, err)
			}
			values["body_html"] = clean
		}
		if !hasNonEmptyDocument(values["seo"]) {
			seo := map[string]any{}
			for k, v := range tpl.Seo {
				seo[k] = v
			}
			if _, ok := seo["title"]; !ok {
				seo["title"] = stringValue(values["name"])
			}
			values["seo"] = seo
		}
		return nil
	}
	return fmt.Errorf("tbl.website_page page_template %q is not available", key)
}

func hasNonEmptyDocument(value any) bool {
	m, ok := value.(map[string]any)
	return ok && len(m) > 0
}
