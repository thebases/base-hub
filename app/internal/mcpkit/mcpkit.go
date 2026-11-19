// Package mcpkit is the identity-agnostic MCP tool-dispatch toolkit shared by
// every module that contributes MCP tools (base-mcp's own transport plus
// each module's mcp/ folder -- knowledge, website, base-workflow, base, and
// any future base-* module).
//
// File overview (mcpkit.go):
// - Responsibility: tool-catalog/error types, argument validation, and
//   large-response trimming -- the pieces every tool handler needs
//   regardless of which module owns it.
// - Key symbols: ToolDescriptor, ToolError/NewToolError, RequireArrayArg/
//   RequireObjectArg/RequireIDsArg, TrimLargeResponseFields, StringFromAny,
//   Int64FromArgument.
// - Pattern: extracted verbatim out of modules/base-mcp/routes so that
//   package could stay a thin alias/wrapper layer over this one (see that
//   package's mcp_route.go/mcp_route_fallback.go file-header comments) --
//   moving these here removes the module->module dependency
//   website/base-workflow/knowledge/base would otherwise need on
//   modules/base-mcp just to reuse its dispatch helpers, per code-pattern.md's
//   "Decoupled Plugin Module Pattern": modules receive shared primitives via
//   internal/* packages, never via another module's package.
package mcpkit

import (
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"github.com/gofiber/fiber/v3"
)

// ToolDescriptor is the MCP tool-catalog entry every tool group (base-mcp's
// own builtin tools, and every module's mcp/tools.go) returns from its
// Tools() catalog function.
type ToolDescriptor struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// InputSchema is serialized as "inputSchema" (not "input_schema") because
	// that is the literal field name the MCP tool schema requires; a client
	// validating against the spec's JSON Schema will not recognize a
	// differently-cased key and will treat the tool as having no schema.
	InputSchema map[string]any `json:"inputSchema,omitempty"`
}

// ToolError carries the HTTP status a tool failure should surface as, so the
// transport (modules/base-mcp/routes.HandleJSONRPC) can map errors from deep
// inside the runtime dispatch back to the status codes documented in the SRS
// API contract (section 6.3). Every dispatch helper in this package returns
// one of these instead of a bare error so status/code survive across a
// module boundary.
type ToolError struct {
	Status  int
	Code    string
	Message string
}

func (e *ToolError) Error() string { return e.Message }

// NewToolError builds a *ToolError. Kept as a function (not a bare struct
// literal) so call sites read the same as before this moved out of
// modules/base-mcp/routes.
func NewToolError(status int, code, message string) *ToolError {
	return &ToolError{Status: status, Code: code, Message: message}
}

// StringFromAny type-asserts v as a string, returning "" for any other type
// (including nil) instead of panicking -- the standard "optional string
// argument" accessor every tool handler uses on a raw map[string]any.
func StringFromAny(v any) string {
	s, _ := v.(string)
	return s
}

// Int64FromArgument normalizes one MCP argument value (JSON number or numeric
// string) into an int64, used by every tool handler that accepts a
// caller-supplied id/limit/offset-shaped scalar argument.
func Int64FromArgument(v any) (int64, bool) {
	switch typed := v.(type) {
	case float64:
		return int64(typed), true
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return 0, false
		}
		parsed, err := strconv.ParseInt(trimmed, 10, 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

// idsFromAny normalizes an MCP arguments.ids payload (JSON numbers, strings,
// or a mix) into the string id slice runtime method dispatch expects.
// Unexported: only RequireIDsArg needs to build one, from an already
// array-type-checked value.
func idsFromAny(v any) []string {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		switch typed := item.(type) {
		case string:
			trimmed := strings.TrimSpace(typed)
			if trimmed != "" {
				out = append(out, trimmed)
			}
		case float64:
			out = append(out, strconv.FormatInt(int64(typed), 10))
		case int64:
			out = append(out, strconv.FormatInt(typed, 10))
		case int:
			out = append(out, strconv.Itoa(typed))
		}
	}
	return out
}

// RequireArrayArg returns arguments[key] as a []any, nil (with no error) when
// the key is absent, or an invalid_params ToolError when the key is present
// with a different JSON type -- avoids the silent `v, _ := x.([]any)` pattern
// that turns a caller's type mistake into an empty/ignored value instead of a
// diagnosable error.
func RequireArrayArg(arguments map[string]any, key string) ([]any, error) {
	v, ok := arguments[key]
	if !ok {
		return nil, nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, NewToolError(fiber.StatusUnprocessableEntity, "invalid_params", "arguments."+key+" must be an array")
	}
	return arr, nil
}

// RequireObjectArg is RequireArrayArg's counterpart for JSON object arguments.
func RequireObjectArg(arguments map[string]any, key string) (map[string]any, error) {
	v, ok := arguments[key]
	if !ok {
		return nil, nil
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, NewToolError(fiber.StatusUnprocessableEntity, "invalid_params", "arguments."+key+" must be an object")
	}
	return obj, nil
}

// RequireIDsArg validates arguments.ids is an array (when present) before
// converting it, instead of a silent "not an array? nil" fallback, which
// would make a malformed ids argument look identical to "no ids given."
func RequireIDsArg(arguments map[string]any) ([]string, error) {
	v, ok := arguments["ids"]
	if !ok {
		return nil, nil
	}
	if _, isArray := v.([]any); !isArray {
		return nil, NewToolError(fiber.StatusUnprocessableEntity, "invalid_params", "arguments.ids must be an array")
	}
	return idsFromAny(v), nil
}

// mcpResponseLargeFieldThreshold is the per-field byte size above which
// TrimLargeResponseFields replaces a string value with its byte count instead
// of echoing it back verbatim.
const mcpResponseLargeFieldThreshold = 4096

// TrimLargeResponseFields walks a tool result (RecordMap, slice of RecordMap,
// fiber.Map/map[string]any, or a slice of any of those) and replaces any
// string field larger than mcpResponseLargeFieldThreshold with a
// "<field>_bytes": <length> entry, recursively. Scalars and small strings
// pass through unchanged. Every MCP tool result passes through this before
// JSON-RPC encoding (modules/base-mcp/routes.toolCallResult) -- the caller
// already has whatever content it sent in a create/write payload, so trimming
// a field it already knows the value of loses nothing.
func TrimLargeResponseFields(result any) any {
	switch typed := result.(type) {
	case orm.RecordMap:
		return trimLargeRecordFields(typed)
	case []orm.RecordMap:
		out := make([]any, len(typed))
		for i, record := range typed {
			out[i] = trimLargeRecordFields(record)
		}
		return out
	case fiber.Map:
		out := make(fiber.Map, len(typed))
		for k, v := range typed {
			out[k] = TrimLargeResponseFields(v)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, v := range typed {
			out[k] = TrimLargeResponseFields(v)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, v := range typed {
			out[i] = TrimLargeResponseFields(v)
		}
		return out
	default:
		return result
	}
}

// trimLargeRecordFields is TrimLargeResponseFields's per-field pass over one
// record map: a string field over threshold is replaced by its byte count
// instead of being recursed into (a large string has no further structure to
// trim), everything else recurses normally so nested relations/JSON blobs are
// covered too.
func trimLargeRecordFields(record orm.RecordMap) orm.RecordMap {
	trimmed := make(orm.RecordMap, len(record))
	for k, v := range record {
		if s, ok := v.(string); ok && len(s) > mcpResponseLargeFieldThreshold {
			trimmed[k+"_bytes"] = len(s)
			continue
		}
		trimmed[k] = TrimLargeResponseFields(v)
	}
	return trimmed
}
