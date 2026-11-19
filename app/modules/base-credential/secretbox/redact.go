package secretbox

// File overview:
// - Responsibility: model-level read redaction for secret columns until the kernel has a generic
//   `secret` field meta (plan T19).
// - Key symbols: `RedactedReadMethods`, `Redact`.
// - Pattern: wraps the kernel's default read-shaped methods (same flags, ACL and record rules) and
//   deletes the secret keys from whatever they return, so no transport (/rpc, alias routes, MCP)
//   ever serializes them. Server code that needs the value reads through the repository directly.

import (
	"context"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
)

// redactedMethodNames are the default methods whose results carry stored field values.
var redactedMethodNames = []string{"read", "search_read", "search_fetch", "read_group", "copy_data", "copy"}

// RedactedReadMethods returns overrides for the default read-shaped methods that strip fields
// (and read_group aggregates over them) from every result, plus a find_or_create that refuses
// secret fields in its domain or values (the default would store them unencrypted).
func RedactedReadMethods(fields ...string) []orm.RuntimeMethod {
	methods := make([]orm.RuntimeMethod, 0, len(redactedMethodNames)+1)
	for _, name := range redactedMethodNames {
		base, ok := orm.LookupRuntimeMethod(name)
		if !ok || base.Call == nil {
			panic("secretbox: default runtime method " + name + " is not registered")
		}
		call := base.Call
		base.Call = func(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
			result, err := call(ctx, model, ids, args, kwargs)
			if err != nil {
				return result, err
			}
			return Redact(result, fields...), nil
		}
		methods = append(methods, base)
	}
	return append(methods, guardedFindOrCreate(fields))
}

func guardedFindOrCreate(fields []string) orm.RuntimeMethod {
	base, ok := orm.LookupRuntimeMethod("find_or_create")
	if !ok || base.Call == nil {
		panic("secretbox: default runtime method find_or_create is not registered")
	}
	call := base.Call
	base.Call = func(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
		if mentionsField(args, fields) || mentionsField(kwargs, fields) {
			return nil, orm.InvalidParamsf("find_or_create cannot match or set secret fields; use create or write")
		}
		result, err := call(ctx, model, ids, args, kwargs)
		if err != nil {
			return result, err
		}
		return Redact(result, fields...), nil
	}
	return base
}

// mentionsField reports whether any map key or string anywhere in value names one of fields.
func mentionsField(value any, fields []string) bool {
	switch typed := value.(type) {
	case string:
		for _, field := range fields {
			if typed == field {
				return true
			}
		}
	case map[string]any:
		for key, item := range typed {
			if mentionsField(key, fields) || mentionsField(item, fields) {
				return true
			}
		}
	case orm.RecordMap:
		return mentionsField(map[string]any(typed), fields)
	case []any:
		for _, item := range typed {
			if mentionsField(item, fields) {
				return true
			}
		}
	}
	return false
}

// Redact deletes fields (and "<field>:<aggregate>" keys) from a record or list of records in place
// and returns it.
func Redact(result any, fields ...string) any {
	switch typed := result.(type) {
	case orm.RecordMap:
		redactRecord(typed, fields)
	case map[string]any:
		redactRecord(typed, fields)
	case []orm.RecordMap:
		for _, record := range typed {
			redactRecord(record, fields)
		}
	case []map[string]any:
		for _, record := range typed {
			redactRecord(record, fields)
		}
	case []any:
		for _, item := range typed {
			Redact(item, fields...)
		}
	}
	return result
}

func redactRecord(record map[string]any, fields []string) {
	for key := range record {
		for _, field := range fields {
			if key == field || strings.HasPrefix(key, field+":") {
				delete(record, key)
				break
			}
		}
	}
}
