package report

// File overview:
// - Responsibility: HTML rendering for sys.report definitions (SRS-F-020) — template
//   loading from module-owned sources, a restricted func map (formatting + i18n, no
//   arbitrary code), and read-only record loading through the runtime.
// - Key symbols: `Definition`, `RenderHTML`, `ValidateTemplateRef`, `ReadOnlyContext`.
// - Design guard: no QWeb port. Templates are Go html/template files under
//   modules/{mod}/reports/…; records reach the template with the same serialization
//   contract as SRS-F-004 (many2one as [id, name] tuples, false-normalization).

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/i18n"
	"git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// Definition carries the render-relevant fields of one sys.report row.
type Definition struct {
	Name        string
	ReportName  string
	Model       string
	TemplateRef string
	PaperFormat string
	Attach      bool
}

// ReadOnlyContext rebinds the environment with the runtime read-only flag so a
// template can never trigger writes (SRS-F-020 §4): every runtime mutation
// checks this flag before opening its transaction.
func ReadOnlyContext(ctx context.Context) context.Context {
	env := orm.EnvironmentFromContext(ctx)
	if env == nil {
		return ctx
	}
	return orm.ContextWithEnvironment(ctx, env.WithContext(map[string]any{
		orm.ReadOnlyEnvContextKey: true,
	}))
}

// ValidateTemplateRef parses the referenced template so sys.report definitions
// fail at create/write time instead of surfacing a 500 on first render.
func ValidateTemplateRef(templateRef string) error {
	_, err := loadTemplate(templateRef)
	return err
}

// loadTemplate resolves and parses one module-owned template. The path shape
// {mod}/reports/… is enforced so a definition can only reach files modules
// explicitly publish as report sources.
func loadTemplate(templateRef string) (*template.Template, error) {
	templateRef = strings.TrimSpace(templateRef)
	parts := strings.Split(strings.TrimPrefix(templateRef, "/"), "/")
	if len(parts) < 3 || parts[1] != "reports" {
		log.Errorf("report: template_ref %q must match {module}/reports/{file}", templateRef)
		return nil, fmt.Errorf("report: template_ref %q must match {module}/reports/{file}", templateRef)
	}
	source, err := modules.ReadModuleResource(templateRef)
	if err != nil {
		log.Errorf("report: template %q not found: %v", templateRef, err)
		return nil, fmt.Errorf("report: template %q not found: %w", templateRef, err)
	}
	// The func map is bound with a neutral language at parse time; execution
	// re-binds it with the request language so `t` translates correctly.
	parsed, err := template.New(parts[len(parts)-1]).Funcs(renderFuncMap("en", time.UTC)).Parse(string(source))
	if err != nil {
		log.Errorf("report: parse template %q: %v", templateRef, err)
		return nil, fmt.Errorf("report: parse template %q: %w", templateRef, err)
	}
	return parsed, nil
}

// renderFuncMap builds the restricted template function set (SRS-F-020 §1):
// formatting helpers and i18n only — no arbitrary code execution paths.
func renderFuncMap(lang string, loc *time.Location) template.FuncMap {
	return template.FuncMap{
		// t translates one UI term through the i18n registry (tbl.lang terms),
		// echoing the source string when no translation exists.
		"t": func(term string) string {
			return i18n.DefaultRegistry().Translate(lang, term)
		},
		// displayName collapses a many2one [id, name] tuple to its label.
		"displayName": func(value any) string {
			switch typed := value.(type) {
			case [2]any:
				return fmt.Sprint(typed[1])
			case []any:
				if len(typed) == 2 {
					return fmt.Sprint(typed[1])
				}
			case string:
				return typed
			}
			// False-normalized empties render as blank, not "false".
			if b, ok := value.(bool); ok && !b {
				return ""
			}
			if value == nil {
				return ""
			}
			return fmt.Sprint(value)
		},
		// formatFloat renders a numeric value with fixed decimals.
		"formatFloat": func(value any, decimals int) string {
			switch typed := value.(type) {
			case float64:
				return fmt.Sprintf("%.*f", decimals, typed)
			case float32:
				return fmt.Sprintf("%.*f", decimals, float64(typed))
			case int64:
				return fmt.Sprintf("%.*f", decimals, float64(typed))
			case int:
				return fmt.Sprintf("%.*f", decimals, float64(typed))
			}
			return fmt.Sprint(value)
		},
		// formatDate/formatDateTime normalize runtime timestamp payloads for
		// print output; unparseable values pass through unchanged.
		"formatDate": func(value any) string {
			if parsed, ok := parseRenderTime(value, loc); ok {
				return parsed.Format("2006-01-02")
			}
			return fmt.Sprint(value)
		},
		"formatDateTime": func(value any) string {
			if parsed, ok := parseRenderTime(value, loc); ok {
				return parsed.Format("2006-01-02 15:04:05")
			}
			return fmt.Sprint(value)
		},
	}
}

// parseRenderTime accepts the timestamp shapes the runtime serializes. Values
// that carry an instant (time.Time, RFC3339 with an offset) are shown in loc,
// the caller's time zone, never the server's. Naive strings (date-only, or a
// datetime without offset) carry no instant and are shown as written: moving a
// bare date to another zone could shift it by a day.
func parseRenderTime(value any, loc *time.Location) (time.Time, bool) {
	switch typed := value.(type) {
	case time.Time:
		return typed.In(loc), true
	case string:
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
			if parsed, err := time.Parse(layout, typed); err == nil {
				return parsed.In(loc), true
			}
		}
		for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02"} {
			if parsed, err := time.Parse(layout, typed); err == nil {
				return parsed, true
			}
		}
	}
	return time.Time{}, false
}

// renderLocation is the caller's time zone from the environment context key
// "tz" (the convention sys.sequence date placeholders use), UTC when unset or
// invalid, so output never depends on the server's local zone.
func renderLocation(ctx context.Context) *time.Location {
	env := orm.EnvironmentFromContext(ctx)
	if env == nil {
		return time.UTC
	}
	raw, _ := env.ContextValue("tz")
	name, _ := raw.(string)
	if strings.TrimSpace(name) == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(strings.TrimSpace(name))
	if err != nil {
		log.Warnf("report: invalid environment timezone %q, using UTC", name)
		return time.UTC
	}
	return loc
}

// RenderHTML loads the target records through the runtime under a read-only
// environment and executes the report template, returning the HTML plus the
// loaded records (the attach path needs their write-generation). Record
// loading uses the caller's environment, so ACL + record rules + company scope
// apply to every id — one denied record fails the whole request (SRS-F-020
// acceptance: no partial redaction surprises).
func RenderHTML(ctx context.Context, def Definition, ids []string) ([]byte, []orm.RecordMap, error) {
	if len(ids) == 0 {
		log.Errorf("report: render requires at least one record id")
		return nil, nil, fmt.Errorf("report: render requires at least one record id")
	}
	// The whole render runs read-only: record loading, template execution, and
	// any runtime call a helper might make can never mutate state.
	renderCtx := ReadOnlyContext(ctx)

	tmpl, err := loadTemplate(def.TemplateRef)
	if err != nil {
		return nil, nil, err
	}

	// Records load through the standard runtime read so templates see the exact
	// SRS-F-004 serialization contract ([id, name] relations, false-normalized
	// empties) and access checks run per id.
	raw, err := orm.ExecuteRuntimeMethod(renderCtx, def.Model, "read", ids, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	records := recordsFromRuntimeResult(raw)
	// A silent shortfall means some ids were filtered rather than denied with an
	// explicit error; treat it as denial so output never omits requested rows.
	if len(records) != len(ids) {
		log.Errorf("report: %d of %d records unavailable for %s", len(ids)-len(records), len(ids), def.ReportName)
		return nil, nil, fmt.Errorf("report: some requested records are not readable")
	}

	lang := "en"
	if env := orm.EnvironmentFromContext(ctx); env != nil {
		if rawLang, ok := env.ContextValue("lang"); ok {
			if text, ok := rawLang.(string); ok && strings.TrimSpace(text) != "" {
				lang = strings.TrimSpace(text)
			}
		}
	}

	var buffer bytes.Buffer
	// Execute with the request language bound into the func map so `t` calls
	// translate against the caller's locale.
	loc := renderLocation(ctx)
	if err := tmpl.Funcs(renderFuncMap(lang, loc)).Execute(&buffer, map[string]any{
		"docs":   records,
		"report": def,
		"lang":   lang,
		"now":    time.Now().In(loc),
	}); err != nil {
		// Log template identity per the SRS error contract (template name +
		// failure); the transport maps this to a 500.
		log.Errorf("report: execute template %q for %s: %v", def.TemplateRef, def.ReportName, err)
		return nil, nil, fmt.Errorf("report: execute template %q: %w", def.TemplateRef, err)
	}
	return buffer.Bytes(), records, nil
}

// recordsFromRuntimeResult normalizes the runtime read result shapes into rows.
func recordsFromRuntimeResult(raw any) []orm.RecordMap {
	switch typed := raw.(type) {
	case []orm.RecordMap:
		return typed
	case orm.RecordMap:
		return []orm.RecordMap{typed}
	case []any:
		out := make([]orm.RecordMap, 0, len(typed))
		for _, item := range typed {
			switch record := item.(type) {
			case orm.RecordMap:
				out = append(out, record)
			case map[string]any:
				out = append(out, orm.RecordMap(record))
			}
		}
		return out
	case map[string]any:
		return []orm.RecordMap{orm.RecordMap(typed)}
	}
	return nil
}
