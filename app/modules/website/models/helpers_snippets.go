package models

// File overview:
// - Responsibility: discovers builder blocks shipped by modules as Owl template files under
//   `<module>/static/snippets/*.xml` and lists them for the builder's Blocks tab and MCP.
// - Key symbols: `loadWebsiteSnippets`, `listWebsiteSnippetCatalog`.

import (
	"context"
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

// websiteSnippetsSubdir is where every module keeps its blocks, relative to the module.
const websiteSnippetsSubdir = "static/snippets"

// websiteCatalogSnippet is one block file. `Key` is "<module>/<file name without .xml>",
// derived from the file location so a copied file cannot collide with another module's block.
// `Html` is the block markup; it is sanitized at load so the editor never inserts unsafe markup.
type websiteCatalogSnippet struct {
	Key       string   `json:"key"`
	Module    string   `json:"module"`
	Name      string   `json:"name"`
	Group     string   `json:"group"`
	Keywords  []string `json:"keywords"`
	Sequence  int      `json:"sequence"`
	Thumbnail string   `json:"thumbnail,omitempty"`
	Class     string   `json:"class,omitempty"`
	Html      string   `json:"html"`
}

// loadWebsiteSnippets reads every block of every installed module. Blocks of modules that are not
// installed in the current database are skipped; unreadable or unsafe files are logged and
// skipped so one bad file cannot empty the library.
func loadWebsiteSnippets(ctx context.Context) ([]websiteCatalogSnippet, error) {
	tree := kernelmodules.ModuleTreeFS()
	if tree == nil {
		return nil, nil
	}
	entries, err := fs.ReadDir(tree, ".")
	if err != nil {
		return nil, err
	}
	pool := orm.ResolvePGXPool("")
	if env := orm.EnvironmentFromContext(ctx); env != nil {
		pool = orm.ResolvePGXPool(env.DBName())
	}
	var out []websiteCatalogSnippet
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		module := entry.Name()
		dir := path.Join(module, websiteSnippetsSubdir)
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
				log.Warnf("website: read snippet %s/%s: %v", dir, name, err)
				continue
			}
			tpl, err := parseOwlTemplateFile(raw)
			if err != nil {
				log.Warnf("website: parse snippet %s/%s: %v", dir, name, err)
				continue
			}
			snip := websiteCatalogSnippet{
				Name: tpl.Meta["name"], Group: tpl.Meta["group"], Keywords: tpl.keywords(),
				Sequence: tpl.sequence(), Thumbnail: tpl.Meta["thumbnail"], Class: tpl.Meta["class"],
			}
			clean, err := sanitizeWebsiteHTML(tpl.HTML)
			if err != nil || strings.TrimSpace(clean) == "" {
				log.Warnf("website: snippet %s/%s has unusable html: %v", dir, name, err)
				continue
			}
			base := strings.TrimSuffix(name, path.Ext(name))
			snip.Html = clean
			snip.Module = module
			snip.Key = module + "/" + base
			if strings.TrimSpace(snip.Name) == "" {
				snip.Name = base
			}
			if strings.TrimSpace(snip.Group) == "" {
				snip.Group = "content"
			}
			out = append(out, snip)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Sequence != out[j].Sequence {
			return out[i].Sequence < out[j].Sequence
		}
		return out[i].Key < out[j].Key
	})
	return out, nil
}

// listWebsiteSnippetCatalog is the model-level `list_catalog` RuntimeMethod behind the Blocks tab.
// Optional kwargs: site_id (adds that site's saved custom blocks), group (exact match) and search (case-insensitive over name, group, keywords).
func listWebsiteSnippetCatalog(ctx context.Context, _ *orm.ModelMetadata, _ []string, _ []any, kwargs map[string]any) (any, error) {
	snippets, err := loadWebsiteSnippets(ctx)
	if err != nil {
		return nil, err
	}
	if siteID, ok := int64FromAny(kwargs["site_id"]); ok && siteID > 0 {
		custom, err := loadWebsiteCustomSnippets(ctx, siteID)
		if err != nil {
			return nil, err
		}
		snippets = append(snippets, custom...)
	}
	group := strings.ToLower(strings.TrimSpace(stringValue(kwargs["group"])))
	search := strings.ToLower(strings.TrimSpace(stringValue(kwargs["search"])))
	out := make([]websiteCatalogSnippet, 0, len(snippets))
	for _, s := range snippets {
		if group != "" && strings.ToLower(s.Group) != group {
			continue
		}
		if search != "" && !websiteSnippetMatches(s, search) {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

func websiteSnippetMatches(s websiteCatalogSnippet, needle string) bool {
	if strings.Contains(strings.ToLower(s.Name), needle) || strings.Contains(strings.ToLower(s.Group), needle) {
		return true
	}
	for _, k := range s.Keywords {
		if strings.Contains(strings.ToLower(k), needle) {
			return true
		}
	}
	return false
}

// loadWebsiteCustomSnippets returns the blocks users saved from the canvas for one site. Their html is
// sanitized again on read: rows can also be written through paths that predate the write hook.
func loadWebsiteCustomSnippets(ctx context.Context, siteID int64) ([]websiteCatalogSnippet, error) {
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WebsiteSnippetModelName, nil), orm.RecordMap{
		"site_id": siteID, "__order": "id asc", "__limit": 200,
	}, []string{"id", "name", "definition"})
	if err != nil {
		return nil, err
	}
	var out []websiteCatalogSnippet
	for _, row := range rows {
		def, _ := row["definition"].(map[string]any)
		clean, err := sanitizeWebsiteHTML(stringValue(def["html"]))
		if err != nil || strings.TrimSpace(clean) == "" {
			continue
		}
		id, _ := int64FromAny(row["id"])
		out = append(out, websiteCatalogSnippet{
			Key: fmt.Sprintf("custom/%d", id), Module: "custom", Name: stringValue(row["name"]),
			Group: "custom", Sequence: 1000 + int(id), Html: clean,
		})
	}
	return out, nil
}
