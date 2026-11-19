package models

// File overview:
// - Responsibility: synthesizes default form/list/kanban/search/graph/calendar arch XML when a model has no stored view.
// - Key symbols: `SynthesizeViewArch`, `ViewLabelForModel`, `synthesizeUIViewArch`, `synthesizeFormArch`.

import (
	"sort"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
)

// SynthesizeViewArch exposes the same fallback arch generator resolveUIView
// uses when no stored ui.view row exists for a model/type, so callers outside
// this package (the MCP `create_ui` scaffold tool) can persist that exact
// baseline arch as a real ui.view row instead of hand-rolling a second
// generator that could drift from what the runtime renders by default.
func SynthesizeViewArch(model *orm.ModelMetadata, viewType string) string {
	return synthesizeUIViewArch(model, viewType)
}

// ViewLabelForModel exposes the display label the built-in view synthesizer
// derives for a model (its registered Label, falling back to Name), so
// scaffolded views/menus default to the same label resolveUIView would use.
func ViewLabelForModel(model *orm.ModelMetadata) string {
	return labelForModel(model)
}

// synthesizeUIViewArch synthesizes the ui view arch handled by this package.
func synthesizeUIViewArch(model *orm.ModelMetadata, viewType string) string {
	switch normalizeViewType(viewType) {
	case "form":
		return synthesizeFormArch(model)
	case "tree":
		return synthesizeListArch(model)
	case "kanban":
		return synthesizeKanbanArch(model)
	case "search":
		return synthesizeSearchArch(model)
	case "pivot":
		return `<pivot string="` + xmlEscape(labelForModel(model)) + `"></pivot>`
	case "graph":
		return synthesizeGraphArch(model)
	case "calendar":
		return synthesizeCalendarArch(model)
	default:
		return synthesizeListArch(model)
	}
}

// synthesizeFormArch synthesizes the form arch handled by this package.
func synthesizeFormArch(model *orm.ModelMetadata) string {
	var builder strings.Builder
	builder.WriteString(`<form string="`)
	builder.WriteString(xmlEscape(labelForModel(model)))
	builder.WriteString(`"><sheet><group>`)
	for _, field := range model.Fields {
		builder.WriteString(`<field name="`)
		builder.WriteString(xmlEscape(field.Name))
		if field.PrimaryKey {
			builder.WriteString(`" invisible="1"/>`)
			continue
		}
		builder.WriteString(`"/>`)
	}
	builder.WriteString(`</group></sheet></form>`)
	return builder.String()
}

// synthesizeListArch synthesizes the list arch handled by this package.
func synthesizeListArch(model *orm.ModelMetadata) string {
	var builder strings.Builder
	builder.WriteString(`<list string="`)
	builder.WriteString(xmlEscape(labelForModel(model)))
	builder.WriteString(`">`)
	for _, field := range model.Fields {
		builder.WriteString(`<field name="`)
		builder.WriteString(xmlEscape(field.Name))
		if field.PrimaryKey {
			builder.WriteString(`" invisible="1"/>`)
			continue
		}
		builder.WriteString(`"/>`)
	}
	builder.WriteString(`</list>`)
	return builder.String()
}

// synthesizeKanbanArch synthesizes the kanban arch handled by this package.
func synthesizeKanbanArch(model *orm.ModelMetadata) string {
	var builder strings.Builder
	builder.WriteString(`<kanban><templates><t t-name="card"><div class="o_kanban_record">`)
	for _, field := range model.Fields {
		builder.WriteString(`<field name="`)
		builder.WriteString(xmlEscape(field.Name))
		if field.PrimaryKey {
			builder.WriteString(`" invisible="1"/>`)
			continue
		}
		builder.WriteString(`"/>`)
	}
	builder.WriteString(`</div></t></templates></kanban>`)
	return builder.String()
}

// synthesizeSearchArch synthesizes the search arch handled by this package.
func synthesizeSearchArch(model *orm.ModelMetadata) string {
	fieldName := preferredFieldName(model)
	return `<search string="` + xmlEscape(labelForModel(model)) + `"><field name="` + xmlEscape(fieldName) + `"/></search>`
}

// synthesizeGraphArch synthesizes the graph arch handled by this package.
func synthesizeGraphArch(model *orm.ModelMetadata) string {
	fieldName := preferredFieldName(model)
	return `<graph string="` + xmlEscape(labelForModel(model)) + `"><field name="` + xmlEscape(fieldName) + `"/></graph>`
}

// synthesizeCalendarArch synthesizes the calendar arch handled by this package.
func synthesizeCalendarArch(model *orm.ModelMetadata) string {
	dateField := firstFieldByName(model, "date", "date_start", "x_date", "x_date_start")
	if dateField == "" {
		return `<calendar string="` + xmlEscape(labelForModel(model)) + `"></calendar>`
	}
	return `<calendar string="` + xmlEscape(labelForModel(model)) + `" date_start="` + xmlEscape(dateField) + `"><field name="` + xmlEscape(preferredFieldName(model)) + `"/></calendar>`
}

// generatedViewName builds the synthesized display name for an auto-generated view.
// The label combines the model label with the normalized view type so generated records stay readable in admin tooling.
func generatedViewName(model *orm.ModelMetadata, viewType string) string {
	return strings.TrimSpace(labelForModel(model) + " " + strings.Title(normalizeViewType(viewType)))
}

func labelForModel(model *orm.ModelMetadata) string {
	if model == nil {
		return "Model"
	}
	if strings.TrimSpace(model.Label) != "" {
		return model.Label
	}
	return model.Name
}

func preferredFieldName(model *orm.ModelMetadata) string {
	if model == nil {
		return "id"
	}
	// Walk each string{"display name", "name", "x name", "title" entry in order so validation and side effects stay scoped to the current element.
	for _, candidate := range []string{"display_name", "name", "x_name", "title"} {
		if hasField(model, candidate) {
			return candidate
		}
	}
	for _, field := range model.Fields {
		if field.PrimaryKey {
			continue
		}
		return field.Name
	}
	return "id"
}

// firstFieldByName returns the field by name handled by this package.
func firstFieldByName(model *orm.ModelMetadata, names ...string) string {
	for _, name := range names {
		if hasField(model, name) {
			return name
		}
	}
	return ""
}

// hasField checks the field handled by this package.
func hasField(model *orm.ModelMetadata, name string) bool {
	if model == nil {
		return false
	}
	for _, field := range model.Fields {
		if field.Name == name {
			return true
		}
	}
	return false
}

func uiViewFieldMap(model *orm.ModelMetadata) map[string]map[string]any {
	fields := make(map[string]map[string]any, len(model.Fields))
	for _, field := range model.Fields {
		fields[field.Name] = map[string]any{
			"name":       field.Name,
			"type":       firstNonEmpty(string(field.Kind), field.Type),
			"label":      field.Label,
			"help":       field.Help,
			"required":   field.Required,
			"readonly":   field.ReadOnly,
			"stored":     field.Stored,
			"relation":   field.Relation,
			"selection":  append([]orm.SelectionOption(nil), field.Selection...),
			"primaryKey": field.PrimaryKey,
		}
	}
	return fields
}

func patternFromArch(arch string, model *orm.ModelMetadata) (map[string]any, []string, error) {
	root, err := parseArchXML(arch)
	if err != nil {
		return nil, nil, err
	}
	used := map[string]struct{}{}
	pattern := patternNode(root, model, used)
	usedFields := make([]string, 0, len(used))
	for fieldName := range used {
		usedFields = append(usedFields, fieldName)
	}
	sort.Strings(usedFields)
	return pattern, usedFields, nil
}

func patternNode(node *uiXMLNode, model *orm.ModelMetadata, used map[string]struct{}) map[string]any {
	if node == nil {
		return map[string]any{}
	}
	if node.Name == "" {
		return map[string]any{
			"tag":  "#text",
			"text": strings.TrimSpace(node.Text),
		}
	}

	result := map[string]any{
		"tag": node.Name,
	}
	if len(node.Attr) > 0 {
		attrs := make(map[string]string, len(node.Attr))
		for _, attr := range node.Attr {
			attrs[attr.Name.Local] = attr.Value
		}
		result["attrs"] = attrs
	}
	if node.Name == "field" {
		fieldName := attrValue(node, "name")
		result["field"] = fieldName
		if fieldName != "" {
			used[fieldName] = struct{}{}
			if meta := fieldMetadata(model, fieldName); meta != nil {
				result["field_meta"] = meta
			} else {
				result["field_missing"] = true
			}
		}
	}
	children := make([]map[string]any, 0, len(node.Children))
	for _, child := range node.Children {
		if child.Name == "" && strings.TrimSpace(child.Text) == "" {
			continue
		}
		children = append(children, patternNode(child, model, used))
	}
	if len(children) > 0 {
		result["children"] = children
	}
	return result
}

func fieldMetadata(model *orm.ModelMetadata, name string) map[string]any {
	if model == nil {
		return nil
	}
	for _, field := range model.Fields {
		if field.Name != name {
			continue
		}
		return map[string]any{
			"name":       field.Name,
			"type":       firstNonEmpty(string(field.Kind), field.Type),
			"label":      field.Label,
			"help":       field.Help,
			"required":   field.Required,
			"readonly":   field.ReadOnly,
			"stored":     field.Stored,
			"relation":   field.Relation,
			"selection":  append([]orm.SelectionOption(nil), field.Selection...),
			"primaryKey": field.PrimaryKey,
		}
	}
	return nil
}
