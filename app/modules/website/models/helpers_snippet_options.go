package models

// File overview:
// - Responsibility: loads the declarative option schemas modules ship under
//   `<module>/static/options/*.json` (the controls the builder's Customize tab shows for a selected
//   block), validates them, and sanitizes custom blocks saved by users.
// - Key symbols: `loadWebsiteOptionSchemas`, `listWebsiteOptionSchemas`, `validateWebsiteOptionSchema`,
//   `createWebsiteSnippet`, `writeWebsiteSnippet`.

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"

	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/queries"
	"git.thebase.vn/base_hub/internal/services/log"
)

const websiteOptionsSubdir = "static/options"

// Control types the builder knows how to render and apply.
var websiteOptionControlTypes = map[string]bool{"select": true, "toggle": true, "range": true, "color": true, "text": true, "image": true}

var (
	websiteOptionClassRe = regexp.MustCompile(`^[A-Za-z0-9_-]+( [A-Za-z0-9_-]+)*$`)
	websiteOptionStyleRe = regexp.MustCompile(`^[a-z]+(-[a-z]+)*$`)
	websiteOptionAttrRe  = regexp.MustCompile(`^(data-[a-z0-9-]+|title|aria-[a-z-]+)$`)
	websiteOptionUnitRe  = regexp.MustCompile(`^(px|rem|em|%|vh|vw|)$`)
)

type websiteOptionSchema struct {
	Key      string         `json:"key"`
	Module   string         `json:"module"`
	Sequence int            `json:"sequence"`
	Selector string         `json:"selector"`
	Exclude  string         `json:"exclude,omitempty"`
	Groups   []websiteGroup `json:"groups"`
}

type websiteGroup struct {
	Label    string           `json:"label"`
	Controls []map[string]any `json:"controls"`
}

// validateWebsiteOptionSchema rejects schemas whose controls could write something the sanitizer
// would have to catch later: class names, style properties and attributes are all allowlisted by
// pattern, so a schema can only ever produce classes, simple style declarations and data/aria attributes.
func validateWebsiteOptionSchema(s *websiteOptionSchema) error {
	if strings.TrimSpace(s.Selector) == "" {
		return fmt.Errorf("selector is required")
	}
	if len(s.Groups) == 0 {
		return fmt.Errorf("at least one group is required")
	}
	for gi, g := range s.Groups {
		for ci, c := range g.Controls {
			where := fmt.Sprintf("groups[%d].controls[%d]", gi, ci)
			typ := stringValue(c["type"])
			if !websiteOptionControlTypes[typ] {
				return fmt.Errorf("%s: unknown control type %q", where, typ)
			}
			if stringValue(c["id"]) == "" || stringValue(c["label"]) == "" {
				return fmt.Errorf("%s: id and label are required", where)
			}
			if st := stringValue(c["style"]); st != "" && !websiteOptionStyleRe.MatchString(st) {
				return fmt.Errorf("%s: invalid style property %q", where, st)
			}
			if at := stringValue(c["attr"]); at != "" && !websiteOptionAttrRe.MatchString(at) {
				return fmt.Errorf("%s: attribute %q is not allowed", where, at)
			}
			if cl := stringValue(c["class"]); cl != "" && !websiteOptionClassRe.MatchString(cl) {
				return fmt.Errorf("%s: invalid class %q", where, cl)
			}
			if u := stringValue(c["unit"]); !websiteOptionUnitRe.MatchString(u) {
				return fmt.Errorf("%s: invalid unit %q", where, u)
			}
			values, _ := c["values"].([]any)
			if typ == "select" && len(values) == 0 {
				return fmt.Errorf("%s: select needs values", where)
			}
			for vi, raw := range values {
				v, _ := raw.(map[string]any)
				if cl := stringValue(v["class"]); cl != "" && !websiteOptionClassRe.MatchString(cl) {
					return fmt.Errorf("%s.values[%d]: invalid class %q", where, vi, cl)
				}
			}
			targets := 0
			for _, k := range []string{"class", "attr", "style"} {
				if stringValue(c[k]) != "" {
					targets++
				}
			}
			if typ == "select" && targets == 0 {
				continue // class-valued select: each value carries its class
			}
			if typ != "select" && targets != 1 {
				return fmt.Errorf("%s: exactly one of class, attr, style is required", where)
			}
		}
	}
	return nil
}

// loadWebsiteOptionSchemas reads every option schema of every installed module. Invalid files are
// logged and skipped so one bad schema cannot disable the whole Customize tab.
func loadWebsiteOptionSchemas(ctx context.Context) ([]websiteOptionSchema, error) {
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
	var out []websiteOptionSchema
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		module := entry.Name()
		dir := path.Join(module, websiteOptionsSubdir)
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
			if !strings.HasSuffix(strings.ToLower(name), ".json") {
				continue
			}
			raw, err := kernelmodules.ReadModuleResource(path.Join(dir, name))
			if err != nil {
				log.Warnf("website: read option schema %s/%s: %v", dir, name, err)
				continue
			}
			var s websiteOptionSchema
			if err := json.Unmarshal(raw, &s); err != nil {
				log.Warnf("website: parse option schema %s/%s: %v", dir, name, err)
				continue
			}
			if err := validateWebsiteOptionSchema(&s); err != nil {
				log.Warnf("website: option schema %s/%s: %v", dir, name, err)
				continue
			}
			s.Module = module
			s.Key = module + "/" + strings.TrimSuffix(name, path.Ext(name))
			out = append(out, s)
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

// listWebsiteOptionSchemas is the model-level `list_options` RuntimeMethod behind the Customize tab.
func listWebsiteOptionSchemas(ctx context.Context, _ *orm.ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
	schemas, err := loadWebsiteOptionSchemas(ctx)
	if err != nil {
		return nil, err
	}
	return schemas, nil
}

// sanitizeWebsiteSnippetValues sanitizes the html of a custom block's definition so a saved block can
// never carry markup the page sanitizer would strip.
func sanitizeWebsiteSnippetValues(values orm.RecordMap) error {
	def, ok := values["definition"].(map[string]any)
	if !ok {
		return nil
	}
	clean := map[string]any{}
	for k, v := range def {
		clean[k] = v
	}
	if err := sanitizeWebsiteHTMLFields(clean, "html"); err != nil {
		return err
	}
	values["definition"] = clean
	return nil
}

// prepareWebsiteSnippet sanitizes the snippet payload before the guarded create/write persists it.
func prepareWebsiteSnippet(_ context.Context, op orm.WriteOp) error {
	return sanitizeWebsiteSnippetValues(op.Values)
}
