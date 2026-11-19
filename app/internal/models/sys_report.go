package models

// File overview:
// - Responsibility: defines built-in runtime model metadata and server-side behavior for
//   sys.report (SRS-F-020 report rendering) inside the kernel model catalog.
// - Key symbols: `SysReportRecord`, `SysReportModelSpec`, `validateSysReportValues`.
// - Design guard (SRS-F-020): no QWeb port — templates are Go html/template sources owned
//   by modules (`modules/{mod}/reports/…`); PDF conversion is delegated to an external
//   engine, so this model stores only the report definition metadata.

import (
	"context"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// SysReportRecord stores one record-bound report definition (SRS-F-020). The
// row maps a public report name onto a module-owned Go template plus the
// rendering options the /report transport and `render_report` method honor.
type SysReportRecord struct {
	orm.BaseModel
	Name string `bun:"name" json:"name" meta:"index,label:Name,help:Stores the human readable name for this report."`
	// ReportName is the public lookup key used by GET /report/{report_name}/{ids}
	// and render_report. Unique so a URL can never resolve ambiguously.
	ReportName string `bun:"report_name" json:"report_name" meta:"unique,label:Report Name,help:Stores the unique technical name used by the /report route and render_report."`
	// Model is the target runtime model whose records feed the template. The
	// caller must hold read access to every rendered id (ACL + record rules +
	// company scope) — enforced by the render path, not here.
	Model string `bun:"model" json:"model" meta:"index,label:Model,help:Stores the target model whose records are rendered by this report."`
	// TemplateRef must resolve to a module template file under
	// modules/{mod}/reports/… (embedded or disk), validated on create/write so a
	// broken reference fails at definition time instead of render time.
	TemplateRef string `bun:"template_ref" json:"template_ref" meta:"label:Template,help:Stores the module template path such as base/reports/demo.html."`
	// PaperFormat drives the PDF engine page size; HTML output ignores it.
	PaperFormat string `bun:"paper_format" json:"paper_format" selection:"(A4,A4),(A5,A5),(letter,Letter),(legal,Legal)" meta:"label:Paper Format,help:Stores the PDF paper format. HTML rendering ignores it."`
	// Attach persists rendered PDFs as sys.attachment rows bound to the target
	// record; re-renders of an unchanged record return the stored attachment
	// (idempotent per record write-generation, SRS-F-020 acceptance).
	Attach bool `bun:"attach" json:"attach" meta:"label:Attach Result,help:Stores whether rendered PDFs persist as attachments on the target record."`
}

const SysReportModelName = "sys.report"

// sysReportPaperFormats is the closed set accepted by the PDF engine mapping;
// an unknown format would silently render on a default page size.
var sysReportPaperFormats = map[string]struct{}{
	"A4": {}, "A5": {}, "letter": {}, "legal": {},
}

var SysReportModel = orm.Define[SysReportRecord](&Models, 325, orm.ModelSpec{
	Name:        SysReportModelName,
	Label:       "Reports",
	Description: "Record-bound report definitions rendered to HTML/PDF from module templates (SRS-F-020)",
	RuntimeMethods: []orm.RuntimeMethod{
		// render_report is ReadOnly by contract (SRS-F-020 §4): rendering must
		// never mutate state; the renderer enforces this with a read-only
		// environment as well.
		{Name: "render_report", Public: true, ModelOnly: true, ReadOnly: true, Call: renderSysReport},
	},
})

// SysReportTemplateValidator is injected by the report service at bootstrap so
// definition validation can parse the referenced template without the models
// package importing the render service (keeps the dependency one-directional).
var SysReportTemplateValidator func(templateRef string) error

// SysReportRenderer is injected by the report service (same one-directional
// wiring as SysReportTemplateValidator) and produces the render_report payload
// for one definition row.
var SysReportRenderer func(ctx context.Context, definition orm.RecordMap, ids []string, output string) (map[string]any, error)

// Indirection breaks the SysReportModelSpec -> renderSysReport ->
// SysReportModel initialization cycle, mirroring the sys_sequence.go pattern.

// FindSysReportDefinition resolves one report definition by its public
// report_name. Definitions are catalog metadata, so the lookup runs sudo — the
// real access control happens on the target records at render time.
func FindSysReportDefinition(ctx context.Context, reportName string) (orm.RecordMap, error) {
	reportName = strings.TrimSpace(reportName)
	if reportName == "" {
		log.Errorf("sys.report: report_name is required")
		return nil, fmt.Errorf("sys.report: report_name is required")
	}
	sudoCtx := ctx
	if env := orm.EnvironmentFromContext(ctx); env != nil {
		sudoCtx = orm.ContextWithEnvironment(ctx, env.WithUser(env.UserID(), true))
	}
	rows, err := orm.RuntimeRepositorySearchRead(sudoCtx, orm.RuntimeModelRef(SysReportModelName, nil), orm.RecordMap{"report_name": reportName, "__limit": 1}, nil)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// renderSysReport implements the render_report runtime method:
//
//	args:   [report_name, ids] or kwargs {report_name, ids, type}
//	type:   "html" (default) or "pdf" (base64 payload)
func renderSysReport(ctx context.Context, _ *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	if SysReportRenderer == nil {
		log.Errorf("sys.report: renderer is not initialized")
		return nil, fmt.Errorf("sys.report: renderer is not initialized")
	}
	reportName := strings.TrimSpace(stringArg(args, 0))
	if reportName == "" {
		reportName = strings.TrimSpace(stringValue(kwargs["report_name"]))
	}
	definition, err := FindSysReportDefinition(ctx, reportName)
	if err != nil {
		return nil, err
	}
	if definition == nil {
		log.Errorf("sys.report: unknown report %q", reportName)
		return nil, fmt.Errorf("sys.report: unknown report %q", reportName)
	}

	var rawIDs any
	if len(args) > 1 {
		rawIDs = args[1]
	} else if kwargs != nil {
		rawIDs = kwargs["ids"]
	}
	ids := idStringsFromAny(rawIDs)
	if len(ids) == 0 {
		log.Errorf("sys.report: render_report requires record ids")
		return nil, fmt.Errorf("sys.report: render_report requires record ids")
	}

	output := strings.ToLower(strings.TrimSpace(stringValue(kwargs["type"])))
	if output == "" {
		output = "html"
	}
	switch output {
	case "html", "pdf":
	default:
		log.Errorf("sys.report: unsupported render type %q", output)
		return nil, fmt.Errorf("sys.report: unsupported render type %q", output)
	}
	return SysReportRenderer(ctx, definition, ids, output)
}

// idStringsFromAny normalizes RPC-shaped id payloads (single value, list, CSV)
// into the runtime's string id convention.
func idStringsFromAny(raw any) []string {
	switch typed := raw.(type) {
	case nil:
		return nil
	case string:
		parts := strings.Split(typed, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := strings.TrimSpace(fmt.Sprint(item)); text != "" && text != "<nil>" {
				out = append(out, text)
			}
		}
		return out
	default:
		if text := strings.TrimSpace(fmt.Sprint(typed)); text != "" {
			return []string{text}
		}
		return nil
	}
}

func init() {
	// Definition rows must be render-ready: a bad template_ref or unknown model
	// discovered at request time would surface as a 500 to end users, so the
	// constraint rejects it at create/write instead.
	orm.RegisterConstraint(SysReportModelName, []string{"report_name", "model", "template_ref", "paper_format"}, validateSysReportValues)
}

// validateSysReportValues enforces the SRS-F-020 definition contract on every
// create/write that touches the render-critical fields.
func validateSysReportValues(_ context.Context, _ orm.RecordSet, changed orm.RecordMap) error {
	if raw, ok := changed["report_name"]; ok {
		if strings.TrimSpace(stringValue(raw)) == "" {
			log.Errorf("sys.report: report_name is required")
			return fmt.Errorf("sys.report: report_name is required")
		}
	}
	if raw, ok := changed["model"]; ok {
		modelName := strings.TrimSpace(stringValue(raw))
		if modelName == "" {
			log.Errorf("sys.report: model is required")
			return fmt.Errorf("sys.report: model is required")
		}
		// The target model must be registered or every render would 500.
		if _, registered := orm.LookupModel(modelName); !registered {
			log.Errorf("sys.report: model %q is not registered", modelName)
			return fmt.Errorf("sys.report: model %q is not registered", modelName)
		}
	}
	if raw, ok := changed["template_ref"]; ok {
		templateRef := strings.TrimSpace(stringValue(raw))
		if templateRef == "" {
			log.Errorf("sys.report: template_ref is required")
			return fmt.Errorf("sys.report: template_ref is required")
		}
		// Templates live under modules/{mod}/reports/ only — the render service
		// re-checks this, but rejecting here keeps bad rows out of the catalog.
		if !strings.Contains(templateRef, "/reports/") {
			log.Errorf("sys.report: template_ref %q must point into a module reports directory", templateRef)
			return fmt.Errorf("sys.report: template_ref %q must point into a module reports directory", templateRef)
		}
		// Parse the template at definition time when the validator is wired.
		if SysReportTemplateValidator != nil {
			if err := SysReportTemplateValidator(templateRef); err != nil {
				log.Errorf("sys.report: template %q is invalid: %v", templateRef, err)
				return fmt.Errorf("sys.report: template %q is invalid: %w", templateRef, err)
			}
		}
	}
	if raw, ok := changed["paper_format"]; ok {
		format := strings.TrimSpace(stringValue(raw))
		if format != "" {
			if _, valid := sysReportPaperFormats[format]; !valid {
				log.Errorf("sys.report: unsupported paper_format %q", format)
				return fmt.Errorf("sys.report: unsupported paper_format %q", format)
			}
		}
	}
	return nil
}
