package models

// File overview:
// - Responsibility: discovers header/footer templates shipped by modules as Owl template files under
//   `<module>/static/chrome/<header|footer>/*.xml` and serves them to the builder's Template picker
//   (header and footer scope). Same layout as the page-template and block loaders.
// - Key symbols: `loadWebsiteChromeTemplates`, `listWebsiteChromeTemplates`.

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

const websiteChromeSubdir = "static/chrome"

var websiteChromeKinds = map[string]bool{"header": true, "footer": true}

type websiteChromeTemplate struct {
	Key         string `json:"key"`
	Module      string `json:"module"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Sequence    int    `json:"sequence"`
	Thumbnail   string `json:"thumbnail,omitempty"`
	HTML        string `json:"html"`
}

// loadWebsiteChromeTemplates reads the templates of `kind` of every installed module. The key is
// "<module>/<file name>" and comes from the file location. Markup is sanitized at load so what the
// picker offers is exactly what would be stored; bad files are logged and skipped.
func loadWebsiteChromeTemplates(ctx context.Context, kind string) ([]websiteChromeTemplate, error) {
	if !websiteChromeKinds[kind] {
		return nil, fmt.Errorf("kind must be header or footer")
	}
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
	var out []websiteChromeTemplate
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		module := entry.Name()
		dir := path.Join(module, websiteChromeSubdir, kind)
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
				log.Warnf("website: read chrome template %s/%s: %v", dir, name, err)
				continue
			}
			parsed, err := parseOwlTemplateFile(raw)
			if err != nil {
				log.Warnf("website: parse chrome template %s/%s: %v", dir, name, err)
				continue
			}
			tpl := websiteChromeTemplate{
				Name: parsed.Meta["name"], Description: parsed.Meta["description"],
				Sequence: parsed.sequence(), Thumbnail: parsed.Meta["thumbnail"], HTML: parsed.HTML,
			}
			clean, err := sanitizeWebsiteHTML(tpl.HTML)
			if err != nil || strings.TrimSpace(clean) == "" {
				log.Warnf("website: chrome template %s/%s has no usable html: %v", dir, name, err)
				continue
			}
			base := strings.TrimSuffix(name, path.Ext(name))
			tpl.HTML, tpl.Module, tpl.Kind, tpl.Key = clean, module, kind, module+"/"+base
			if strings.TrimSpace(tpl.Name) == "" {
				tpl.Name = base
			}
			out = append(out, tpl)
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

// listWebsiteChromeTemplates is the model-level `list_chrome_templates` RuntimeMethod (kwargs `kind`).
func listWebsiteChromeTemplates(ctx context.Context, _ *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	kind := stringValue(kwargs["kind"])
	if kind == "" && len(args) > 0 {
		kind = stringValue(args[0])
	}
	templates, err := loadWebsiteChromeTemplates(ctx, kind)
	if err != nil {
		return nil, err
	}
	return templates, nil
}
