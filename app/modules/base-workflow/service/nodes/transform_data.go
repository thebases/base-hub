package nodes

// File overview:
// - Responsibility: the transform_data@v1 node — renders a JSON template against the current
//   payload using {{ field }} / #field expression syntax. Also owns the shared template/
//   expression engine (`replaceTemplateAny`, `ResolveStringParams`) reused by crud_model's
//   filter/values rendering and by the engine's per-step string-param resolution.
// - Key symbols: `TransformDataAction`, `renderTemplateObject`, `ResolveStringParams`.

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"git.thebase.vn/base_hub/internal/services/log"
)

// init registers this node's single definition (plan T93): canvas spec, catalog entry and
// param validation all derive from it.
func init() {
	registerNode(nodeDef{
		Code:        "transform_data",
		Version:     1,
		Sequence:    50,
		Label:       "Transform Data",
		Description: "Transform the current JSON object into a DTO or custom JSON document using inline templates.",
		Category:    "Transform",
		Params: []paramDef{
			{
				Name:   "model_name",
				Label:  "Model Name",
				Type:   "combobox",
				Help:   "Optional source model. When selected, the frontend can fetch its JSON template from /wf/sys-models.",
				Column: "model_name",
			},
			{Name: "template", Label: "Template", Type: "json", Required: true, Column: "template"},
			{Name: "flatten", Label: "Flatten Input", Type: "boolean", Column: "flatten_input"},
		},
	}, TransformDataAction)
}

// doublebraceRe matches {{ field }} and {{ nested.field }} expression tokens used by the canvas param editor.
var doublebraceRe = regexp.MustCompile(`\{\{\s*(\w[\w.]*)\s*\}\}`)

// TransformDataAction transforms the data action handled by this package.
func TransformDataAction(_ context.Context, req WorkflowActionRequest) ([]byte, error) {
	input, err := decodeJSONObject(req.Payload)
	if err != nil {
		log.Errorf("transform_data: %v", err)
		return nil, fmt.Errorf("transform_data: %w", err)
	}

	if template, ok, err := actionObjectParam(req.Step.Params, "template", "schema", "output_schema", "dto"); err != nil {
		log.Errorf("transform_data: %v", err)
		return nil, fmt.Errorf("transform_data: %w", err)
	} else if ok {
		output, err := renderTemplateObject(template, input, boolParamDefault(req.Step.Params, "flatten", true))
		if err != nil {
			log.Errorf("transform_data: %v", err)
			return nil, fmt.Errorf("transform_data: %w", err)
		}
		return json.Marshal(output)
	}

	log.Errorf("transform_data: params.template is required")
	return nil, fmt.Errorf("transform_data: params.template is required")
}

func renderTemplateObject(template map[string]any, input map[string]any, flatten bool) (map[string]any, error) {
	cloned := cloneJSONObject(template)
	bindings := cloneJSONObject(input)
	if flatten {
		if workflowActionRuntimeHooks.FlattenJSON == nil {
			log.Errorf("flatten_json: flatten hook is not initialized")
			return nil, fmt.Errorf("flatten_json: flatten hook is not initialized")
		}
		bindings = map[string]any{}
		workflowActionRuntimeHooks.FlattenJSON(input, bindings)
	}
	replaced, err := replaceTemplateAny(cloned, bindings)
	if err != nil {
		return nil, err
	}
	output, ok := replaced.(map[string]any)
	if !ok {
		log.Errorf("template must render to a JSON object")
		return nil, fmt.Errorf("template must render to a JSON object")
	}
	return output, nil
}

func renderedObjectParam(params map[string]any, input map[string]any, keys ...string) (map[string]any, error) {
	object, ok, err := actionObjectParam(params, keys...)
	if err != nil || !ok {
		return map[string]any{}, err
	}
	return renderTemplateObject(object, input, boolParamDefault(params, "flatten", true))
}

func renderedObjectListParam(params map[string]any, input map[string]any, keys ...string) ([]map[string]any, error) {
	for _, key := range keys {
		raw, exists := params[key]
		if !exists || raw == nil {
			continue
		}
		items, err := objectListFromAny(key, raw)
		if err != nil {
			return nil, err
		}
		out := make([]map[string]any, 0, len(items))
		for _, item := range items {
			rendered, err := renderTemplateObject(item, input, boolParamDefault(params, "flatten", true))
			if err != nil {
				return nil, err
			}
			out = append(out, rendered)
		}
		return out, nil
	}
	return nil, nil
}

// replaceTemplateAny replaces the template any handled by this package.
func replaceTemplateAny(value any, bindings map[string]any) (any, error) {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, nested := range typed {
			replaced, err := replaceTemplateAny(nested, bindings)
			if err != nil {
				return nil, err
			}
			out[key] = replaced
		}
		return out, nil
	case []any:
		out := make([]any, 0, len(typed))
		for _, nested := range typed {
			replaced, err := replaceTemplateAny(nested, bindings)
			if err != nil {
				return nil, err
			}
			out = append(out, replaced)
		}
		return out, nil
	case string:
		trimmed := strings.TrimSpace(typed)
		// A lone {{ field }} or {{ nested.field }} token: return the typed binding value.
		if m := doublebraceRe.FindStringSubmatch(trimmed); len(m) >= 2 && m[0] == trimmed {
			if replacement, ok := lookupNestedBinding(bindings, strings.TrimSpace(m[1])); ok {
				return replacement, nil
			}
			return typed, nil
		}
		// A lone #key token (no other # chars) is replaced with the typed binding value.
		if strings.HasPrefix(trimmed, "#") && !strings.Contains(trimmed[1:], "#") {
			key := strings.TrimPrefix(trimmed, "#")
			if replacement, ok := bindings[key]; ok {
				return replacement, nil
			}
			return typed, nil
		}
		// Inline {{ field }} substitutions.
		result := doublebraceRe.ReplaceAllStringFunc(typed, func(match string) string {
			m := doublebraceRe.FindStringSubmatch(match)
			if len(m) < 2 {
				return match
			}
			if replacement, ok := lookupNestedBinding(bindings, strings.TrimSpace(m[1])); ok {
				return fmt.Sprint(replacement)
			}
			return match
		})
		// Inline #field substitutions.
		re := regexp.MustCompile(`#(\w+)`)
		return re.ReplaceAllStringFunc(result, func(match string) string {
			key := strings.TrimPrefix(match, "#")
			if replacement, ok := bindings[key]; ok {
				return fmt.Sprint(replacement)
			}
			return match
		}), nil
	default:
		return value, nil
	}
}

// ResolveStringParams resolves {{ field }} and #field expressions in the top-level string
// values of a step params map against the given payload bindings.
// Only string-typed values are resolved; object and array values are passed through unchanged
// so actions that perform their own deep template processing (e.g. transform_data) are not pre-empted.
func ResolveStringParams(params map[string]any, bindings map[string]any) map[string]any {
	if len(params) == 0 || len(bindings) == 0 {
		return params
	}
	resolved := make(map[string]any, len(params))
	for k, v := range params {
		if s, ok := v.(string); ok && (strings.Contains(s, "#") || strings.Contains(s, "{{")) {
			resolved[k] = resolveStringExpr(s, bindings)
		} else {
			resolved[k] = v
		}
	}
	return resolved
}

// resolveStringExpr resolves a single string param value against payload bindings.
// A lone {{ field }} or #field token returns the typed binding value (preserving int, bool, etc.).
// Mixed strings with inline tokens are string-substituted in place.
func resolveStringExpr(s string, bindings map[string]any) any {
	trimmed := strings.TrimSpace(s)
	// Lone {{ field }} or {{ nested.field }} → typed value.
	if m := doublebraceRe.FindStringSubmatch(trimmed); len(m) >= 2 && m[0] == trimmed {
		if val, ok := lookupNestedBinding(bindings, strings.TrimSpace(m[1])); ok {
			return val
		}
		return s
	}
	// Lone #field → typed value (backward compat).
	if strings.HasPrefix(trimmed, "#") && !strings.Contains(trimmed[1:], "#") {
		key := strings.TrimPrefix(trimmed, "#")
		if val, ok := bindings[key]; ok {
			return val
		}
		return s
	}
	// Inline {{ field }} substitutions.
	result := doublebraceRe.ReplaceAllStringFunc(s, func(match string) string {
		m := doublebraceRe.FindStringSubmatch(match)
		if len(m) < 2 {
			return match
		}
		if val, ok := lookupNestedBinding(bindings, strings.TrimSpace(m[1])); ok {
			return fmt.Sprint(val)
		}
		return match
	})
	// Inline #field substitutions (backward compat).
	result = regexp.MustCompile(`#(\w+)`).ReplaceAllStringFunc(result, func(match string) string {
		key := strings.TrimPrefix(match, "#")
		if val, ok := bindings[key]; ok {
			return fmt.Sprint(val)
		}
		return match
	})
	return result
}

// lookupNestedBinding resolves a dot-notation field path (e.g. "order.id") against bindings.
func lookupNestedBinding(bindings map[string]any, field string) (any, bool) {
	if bindings == nil {
		return nil, false
	}
	// Direct lookup first.
	if val, ok := bindings[field]; ok {
		return val, true
	}
	// Dot-notation traversal.
	current := any(bindings)
	for _, part := range strings.Split(field, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		val, exists := object[part]
		if !exists {
			return nil, false
		}
		current = val
	}
	return current, true
}
