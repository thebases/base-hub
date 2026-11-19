package models

// File overview:
// - Responsibility: defines the node spec response types served by GET /wf/node-specs for the visual
//   canvas frontend, and the single-source node definition (`NodeDef`) every builtin action node
//   registers from its own file under service/nodes (plan T93). The canvas spec, the catalog entry
//   (`BuiltinETLActions`) and the param validation (`ValidateStepParams`) are all derived from it.
// - Key symbols: `NodeParamSpec`, `ActionNodeSpec`, `NodeDef`, `ParamDef`, `ValidateStepParams`.
// - Pattern: The types live here (not in service/nodes) because models and routes both read the
//   registry and service/nodes already imports models; nodes registers into it, never the reverse.

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// NodeParamSpec is one canvas field. For an action node it also says where the value is stored
// (Column or ExtraKey), so the SPA maps params generically instead of switching on names.
type NodeParamSpec struct {
	Name      string            `json:"name"`
	Label     string            `json:"label"`
	Type      string            `json:"type"`
	Required  bool              `json:"required"`
	Default   any               `json:"default,omitempty"`
	Help      string            `json:"help,omitempty"`
	Options   []NodeParamOption `json:"options,omitempty"`
	Selection []NodeParamOption `json:"selection,omitempty"`
	// Column is the base.workflow.action column holding the value; ExtraKey the extra_params key
	// when there is no column. Exactly one is set on an action param, neither on a trigger param.
	Column   string `json:"column,omitempty"`
	ExtraKey string `json:"extra_key,omitempty"`
	// Secret marks a param that only accepts an @cred:/@syscred: reference (see
	// workflow_action_secret_refs.go); the value names the picker the canvas offers.
	Secret string `json:"secret,omitempty"`
	// ValueType "int" stores a select's value as an integer id (lua_script automation_action_id).
	ValueType string `json:"value_type,omitempty"`
}

// ParamDef is the definition side of NodeParamSpec: a node declares its params once and the
// /wf/node-specs response serves them as they are.
type ParamDef = NodeParamSpec

const (
	// ParamSecretCredential: an @cred:<name> base.workflow.credential reference whose
	// credential_type matches the param name (or "other").
	ParamSecretCredential = "credential"
	// ParamSecretSysCredential: an @syscred:<name> sys.credential reference.
	ParamSecretSysCredential = "sys_credential"
	// ParamValueInt stores the value as a positive integer id.
	ParamValueInt = "int"
)

type NodeParamOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type ActionNodeSpec struct {
	Code string `json:"code"`
	// CatalogID is the backing base.workflow.action.catalog row id (0 for the static
	// builtin-list fallback, which has no live catalog row). The canvas uses it to default
	// a new node's Action Catalog field to the row that matches the dragged node type.
	CatalogID int64           `json:"catalog_id,omitempty"`
	Label     string          `json:"label"`
	Category  string          `json:"category"`
	Icon      string          `json:"icon,omitempty"`
	Params    []NodeParamSpec `json:"params"`
}

type TriggerNodeSpec struct {
	Type   string          `json:"type"`
	Label  string          `json:"label"`
	Icon   string          `json:"icon,omitempty"`
	Params []NodeParamSpec `json:"params"`
}

type NodeSpecResponse struct {
	Triggers []TriggerNodeSpec `json:"triggers"`
	Actions  []ActionNodeSpec  `json:"actions"`
}

// ParamIssue is one problem found by ValidateStepParams. Param is empty for a node-level issue.
type ParamIssue struct {
	Param   string `json:"param"`
	Message string `json:"message"`
}

// NodeDef is the single definition of a builtin action node (code@version). The node file under
// service/nodes registers it together with its handler; the canvas spec, the catalog entry and
// the validation below are derived from it.
type NodeDef struct {
	Code        string
	Version     int
	Label       string
	Description string
	Category    string
	// Sequence orders the builtin list (the static canvas fallback and the catalog seed order).
	Sequence int
	Params   []ParamDef
	// ExtraDefaults are catalog default_params with no canvas default of their own (an empty
	// condition expression, lua data): putting them on the param would prefill the canvas field.
	ExtraDefaults map[string]any
	// Validate adds node-specific checks over the values keyed by param name (optional).
	Validate func(values map[string]any) []ParamIssue
	// SideEffect is the kind of external or persistent effect the node has (NodeEffect*); empty
	// means the node is pure. A dry run (plan T101) never calls the handler of a side-effect node:
	// the engine records the intended effect instead.
	SideEffect string
}

// Node side-effect kinds (NodeDef.SideEffect).
const (
	NodeEffectHTTP   = "http"   // sends an HTTP request now (post_output_object)
	NodeEffectOutbox = "outbox" // writes an outbound row that a worker delivers (notify)
	NodeEffectWrite  = "write"  // writes model records (crud_model)
	NodeEffectScript = "script" // runs stored code that may call any runtime method (lua_script)
)

// DefaultParams is the catalog default_params: every param default keyed by param name (the
// runtime param key) plus ExtraDefaults. Built fresh on each call so callers may mutate it.
func (d NodeDef) DefaultParams() map[string]any {
	out := make(map[string]any, len(d.Params)+len(d.ExtraDefaults))
	for _, param := range d.Params {
		if param.Default != nil {
			out[param.Name] = cloneDefaultValue(param.Default)
		}
	}
	for key, value := range d.ExtraDefaults {
		out[key] = cloneDefaultValue(value)
	}
	return out
}

// ActionSpec renders the definition as its /wf/node-specs entry. A node with no params keeps
// `params: null`, which is what the endpoint has always served for them.
func (d NodeDef) ActionSpec() ActionNodeSpec {
	var params []NodeParamSpec
	if len(d.Params) > 0 {
		params = make([]NodeParamSpec, len(d.Params))
		for i, param := range d.Params {
			param.Options = append([]NodeParamOption(nil), param.Options...)
			params[i] = param
		}
	}
	return ActionNodeSpec{Code: d.Code, Label: d.Label, Category: d.Category, Params: params}
}

// catalogEntry is the derived ETLActionCatalogEntry; builtins always run their own handler.
func (d NodeDef) catalogEntry() ETLActionCatalogEntry {
	return ETLActionCatalogEntry{
		Label:          d.Label,
		Name:           d.Code,
		Version:        d.Version,
		Description:    d.Description,
		HandlerKey:     d.Code,
		HandlerVersion: d.Version,
		DefaultParams:  d.DefaultParams(),
	}
}

// ValidateStepParams checks one action's values against its node definition: required params
// (a param with a default is never missing, the default applies), value types, secret refs and the
// node's own Validate. columns holds base.workflow.action column values, extra its extra_params.
// It never mutates its inputs. Not wired into any save path yet (plan T98 serves it).
func ValidateStepParams(code string, version int, columns map[string]any, extra map[string]any) []ParamIssue {
	def, ok := LookupNodeDef(code, version)
	if !ok {
		return []ParamIssue{{Message: fmt.Sprintf("unknown action %s@%d", strings.TrimSpace(code), normalizedNodeVersion(version))}}
	}
	values := make(map[string]any, len(def.Params))
	var issues []ParamIssue
	for _, param := range def.Params {
		value, present := paramStoredValue(param, columns, extra)
		if !present || isEmptyParamValue(value) {
			if param.Required && param.Default == nil {
				issues = append(issues, ParamIssue{Param: param.Name, Message: param.Label + " is required"})
			}
			continue
		}
		values[param.Name] = value
		if message := paramTypeIssue(param, value); message != "" {
			issues = append(issues, ParamIssue{Param: param.Name, Message: message})
		}
	}
	if def.Validate != nil {
		issues = append(issues, def.Validate(values)...)
	}
	return issues
}

func paramStoredValue(param ParamDef, columns map[string]any, extra map[string]any) (any, bool) {
	if param.Column != "" {
		value, ok := columns[param.Column]
		return value, ok
	}
	value, ok := extra[param.ExtraKey]
	return value, ok
}

// isEmptyParamValue matches the canvas required check: nil, blank text, an empty object or list.
func isEmptyParamValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	case map[string]any:
		return len(typed) == 0
	case []any:
		return len(typed) == 0
	}
	return false
}

// paramTypeIssue returns the type problem of a non-empty value, or "".
func paramTypeIssue(param ParamDef, value any) string {
	if param.Secret != "" {
		if text, ok := value.(string); !ok || !IsWorkflowCredentialRef(text) {
			return param.Label + " must be a @cred:<name> or @syscred:<name> reference"
		}
		return ""
	}
	if param.ValueType == ParamValueInt {
		if id, ok := positiveIntValue(value); !ok || id <= 0 {
			return param.Label + " must be a positive integer id"
		}
		return ""
	}
	switch param.Type {
	case "boolean":
		if _, ok := value.(bool); !ok {
			return param.Label + " must be true or false"
		}
	case "number":
		switch value.(type) {
		case int, int64, float64, json.Number:
		default:
			return param.Label + " must be a number"
		}
	case "json":
		if !isJSONObjectValue(value) {
			return param.Label + " must be a JSON object"
		}
	case "select":
		text, ok := value.(string)
		if !ok {
			return param.Label + " must be text"
		}
		if len(param.Options) > 0 && !hasParamOption(param.Options, text) {
			return param.Label + " has an unknown value " + strconv.Quote(text)
		}
	default:
		if _, ok := value.(string); !ok {
			return param.Label + " must be text"
		}
	}
	return ""
}

func hasParamOption(options []NodeParamOption, value string) bool {
	for _, option := range options {
		if option.Value == value {
			return true
		}
	}
	return false
}

// positiveIntValue reads an integer from the JSON shapes an id arrives in (number or digits).
func positiveIntValue(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		if typed == float64(int64(typed)) {
			return int64(typed), true
		}
	case json.Number:
		n, err := typed.Int64()
		return n, err == nil
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return n, err == nil
	}
	return 0, false
}

func isJSONObjectValue(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		return true
	case string:
		var decoded map[string]any
		return json.Unmarshal([]byte(typed), &decoded) == nil && decoded != nil
	}
	return false
}

// cloneDefaultValue copies map/slice defaults so a caller mutating DefaultParams never changes
// the registered definition.
func cloneDefaultValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = cloneDefaultValue(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = cloneDefaultValue(item)
		}
		return out
	}
	return value
}
