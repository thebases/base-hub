package report

// File overview:
// - Responsibility: wires the render service into the sys.report model surface —
//   template validation on definition writes and the render_report runtime method
//   payload — without the models package importing this service (one-directional
//   dependency, injected the same way as the DatabaseProvisioner pattern).
// - Key symbols: `init`, `DefinitionFromRecord`, `renderDefinitionPayload`.

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/models"
	"git.thebase.vn/base_hub/internal/orm"
)

// init registers this package runtime during bootstrap so sys.report rows are
// validated and renderable as soon as the transport packages link this service.
func init() {
	models.SysReportTemplateValidator = ValidateTemplateRef
	models.SysReportRenderer = renderDefinitionPayload
}

// DefinitionFromRecord projects one sys.report runtime row into the render
// definition, tolerating the runtime's false-normalization on empty fields.
func DefinitionFromRecord(row orm.RecordMap) Definition {
	return Definition{
		Name:        recordString(row["name"]),
		ReportName:  recordString(row["report_name"]),
		Model:       recordString(row["model"]),
		TemplateRef: recordString(row["template_ref"]),
		PaperFormat: recordString(row["paper_format"]),
		Attach:      recordBool(row["attach"]),
	}
}

// renderDefinitionPayload backs the render_report runtime method: HTML output
// returns the markup directly, PDF output returns base64 so the JSON-RPC
// envelope stays valid UTF-8.
func renderDefinitionPayload(ctx context.Context, definition orm.RecordMap, ids []string, output string) (map[string]any, error) {
	def := DefinitionFromRecord(definition)
	html, _, err := RenderHTML(ctx, def, ids)
	if err != nil {
		return nil, err
	}
	if output == "pdf" {
		pdf, err := ConvertHTMLToPDF(ctx, EngineURL(ctx), html, def.PaperFormat)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"report_name": def.ReportName,
			"type":        "pdf",
			"mimetype":    "application/pdf",
			"content":     base64.StdEncoding.EncodeToString(pdf),
		}, nil
	}
	return map[string]any{
		"report_name": def.ReportName,
		"type":        "html",
		"mimetype":    "text/html; charset=utf-8",
		"content":     string(html),
	}, nil
}

// recordString extracts a string field tolerating false-normalized empties.
func recordString(raw any) string {
	if text, ok := raw.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}

// recordBool extracts a boolean field from runtime row payloads.
func recordBool(raw any) bool {
	if value, ok := raw.(bool); ok {
		return value
	}
	return false
}

// AttachmentFileName builds the deterministic per-write-generation file name
// used to make attach=true renders idempotent (SRS-F-020 acceptance): the name
// embeds the record's update marker, so an unchanged record maps to the same
// attachment and a changed record naturally produces a new one.
func AttachmentFileName(def Definition, recordID string, generation string) string {
	sanitized := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		default:
			return '-'
		}
	}, fmt.Sprintf("%s-%s-g%s", def.ReportName, recordID, generation))
	return sanitized + ".pdf"
}
