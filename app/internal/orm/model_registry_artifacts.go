package orm

import (
	"encoding/json"
	"html"
	"strings"
)

// BuildModelHelperHTML renders an HTML list from field helper text so admin
// tooling can show the field-level guidance captured in model metadata.
func BuildModelHelperHTML(meta ModelMetadata) string {
	items := make([]string, 0, len(meta.Fields))
	for _, field := range meta.Fields {
		help := strings.TrimSpace(field.Help)
		if help == "" {
			continue
		}

		title := strings.TrimSpace(field.Label)
		if title == "" {
			title = strings.TrimSpace(field.Name)
		}
		if title == "" {
			continue
		}

		items = append(items, "<li><strong>"+html.EscapeString(title)+"</strong>: "+html.EscapeString(help)+"</li>")
	}
	if len(items) == 0 {
		return ""
	}
	return "<ul>" + strings.Join(items, "") + "</ul>"
}

// BuildModelJSONDTO projects model metadata into a JSON-friendly map for
// storage in sys.model without exposing internal reflection state.
func BuildModelJSONDTO(meta ModelMetadata) map[string]any {
	payload, err := json.Marshal(meta)
	if err != nil {
		return nil
	}

	dto := make(map[string]any)
	if err := json.Unmarshal(payload, &dto); err != nil {
		return nil
	}
	return dto
}
