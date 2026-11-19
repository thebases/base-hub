package orm

// File overview:
// - Responsibility: declares the input parameters a RuntimeMethod reads from kwargs, so callers
//   (e.g. the sys.model.action popup) can render an input form, and enforces the declared
//   required/type contract before the method body runs.
// - Key symbols: `RuntimeMethodParam`, `normalizeRuntimeMethodParams`, `ValidateRuntimeMethodKwargs`.
// - Pattern: the declaration lives next to the method in code (single source of truth); methods
//   without Params keep their old free-form kwargs behavior untouched.

import (
	"fmt"
	"math"
	"strings"

	"git.thebase.vn/base_hub/internal/utils/anyconv"
)

// Supported RuntimeMethodParam.Type values; empty normalizes to char.
const (
	RuntimeMethodParamChar      = "char"
	RuntimeMethodParamText      = "text"
	RuntimeMethodParamInteger   = "integer"
	RuntimeMethodParamFloat     = "float"
	RuntimeMethodParamBoolean   = "boolean"
	RuntimeMethodParamDate      = "date"
	RuntimeMethodParamDatetime  = "datetime"
	RuntimeMethodParamSelection = "selection"
	// RuntimeMethodParamPassword is a string the UI masks; validated like char.
	RuntimeMethodParamPassword = "password"
)

var runtimeMethodParamTypes = map[string]struct{}{
	RuntimeMethodParamChar: {}, RuntimeMethodParamText: {}, RuntimeMethodParamInteger: {}, RuntimeMethodParamFloat: {},
	RuntimeMethodParamBoolean: {}, RuntimeMethodParamDate: {}, RuntimeMethodParamDatetime: {}, RuntimeMethodParamSelection: {}, RuntimeMethodParamPassword: {},
}

// RuntimeMethodParam is one kwarg a RuntimeMethod reads. Name is the kwargs key the value is
// sent under; Default pre-fills the input form only (it is never injected server-side).
type RuntimeMethodParam struct {
	Name      string            `json:"name"`
	Label     string            `json:"label,omitempty"`
	Type      string            `json:"type,omitempty"`
	Required  bool              `json:"required,omitempty"`
	Help      string            `json:"help,omitempty"`
	Default   any               `json:"default,omitempty"`
	Selection []SelectionOption `json:"selection,omitempty"`
}

// normalizeRuntimeMethodParams trims names, defaults Type to char and Label to Name, and rejects
// empty/duplicate names, unknown types and option-less selections at registration time, so a
// bad declaration fails boot instead of rendering a broken popup.
func normalizeRuntimeMethodParams(methodName string, params []RuntimeMethodParam) ([]RuntimeMethodParam, error) {
	if len(params) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(params))
	normalized := make([]RuntimeMethodParam, 0, len(params))
	for _, param := range params {
		param.Name = strings.TrimSpace(param.Name)
		param.Type = strings.ToLower(strings.TrimSpace(param.Type))
		if param.Name == "" {
			return nil, fmt.Errorf("orm: runtime method %q declares a param without a name", methodName)
		}
		if _, dup := seen[param.Name]; dup {
			return nil, fmt.Errorf("orm: runtime method %q declares param %q twice", methodName, param.Name)
		}
		seen[param.Name] = struct{}{}
		if param.Type == "" {
			param.Type = RuntimeMethodParamChar
		}
		if _, ok := runtimeMethodParamTypes[param.Type]; !ok {
			return nil, fmt.Errorf("orm: runtime method %q param %q has unsupported type %q", methodName, param.Name, param.Type)
		}
		if param.Type == RuntimeMethodParamSelection && len(param.Selection) == 0 {
			return nil, fmt.Errorf("orm: runtime method %q selection param %q has no options", methodName, param.Name)
		}
		if strings.TrimSpace(param.Label) == "" {
			param.Label = param.Name
		}
		normalized = append(normalized, param)
	}
	return normalized, nil
}

// ValidateRuntimeMethodKwargs enforces the declared params against kwargs: required params must
// be present and non-blank, and present values must match the declared type. Undeclared kwargs
// pass through (context keys etc.), and the method body still owns business validation.
// A positional call (len(args) > 0) skips the required check: many methods accept the same input
// as args[0] or a kwarg, so declaring Params must not break existing positional callers.
func ValidateRuntimeMethodKwargs(method RuntimeMethod, args []any, kwargs map[string]any) error {
	positional := len(args) > 0
	for _, param := range method.Params {
		value, present := kwargs[param.Name]
		if !present || runtimeMethodParamBlank(value) {
			if param.Required && !positional {
				return fmt.Errorf("%s: %q is required", method.Name, param.Label)
			}
			continue
		}
		if !runtimeMethodParamTypeMatches(param, value) {
			return fmt.Errorf("%s: %q must be a valid %s value", method.Name, param.Label, param.Type)
		}
	}
	return nil
}

// runtimeMethodParamBlank treats nil and whitespace-only strings as "not provided"; false and 0
// are real values (a required boolean is satisfied by false).
func runtimeMethodParamBlank(value any) bool {
	if value == nil {
		return true
	}
	text, ok := value.(string)
	return ok && strings.TrimSpace(text) == ""
}

// runtimeMethodParamTypeMatches checks the JSON-decoded value shape (numbers arrive as float64).
// Date/datetime stay strings; the method parses them with its own layout.
func runtimeMethodParamTypeMatches(param RuntimeMethodParam, value any) bool {
	switch param.Type {
	case RuntimeMethodParamBoolean:
		_, ok := value.(bool)
		return ok
	case RuntimeMethodParamInteger:
		// A many2one [id, name] pair is accepted for an integer param: the SPA
		// echoes relation values in read shape, and Typed collapses it to the id.
		if _, ok := runtimeMethodParamMany2OnePair(value); ok {
			return true
		}
		number, ok := runtimeMethodParamNumber(value)
		return ok && number == math.Trunc(number)
	case RuntimeMethodParamFloat:
		_, ok := runtimeMethodParamNumber(value)
		return ok
	case RuntimeMethodParamSelection:
		text, ok := value.(string)
		if !ok {
			return false
		}
		for _, option := range param.Selection {
			if option.Value == text {
				return true
			}
		}
		return false
	default:
		_, ok := value.(string)
		return ok
	}
}

// runtimeMethodParamMany2OnePair reports whether value is a many2one read pair with an integer id.
func runtimeMethodParamMany2OnePair(value any) (int64, bool) {
	switch pair := value.(type) {
	case [2]any:
		return anyconv.Int64(pair[0])
	case []any:
		if len(pair) == 2 {
			return anyconv.Int64(pair[0])
		}
	}
	return 0, false
}

// runtimeMethodParamNumber accepts the numeric shapes a JSON decoder or a Go caller produces.
func runtimeMethodParamNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case int32:
		return float64(typed), true
	}
	return 0, false
}
