package orm

// File overview:
// - Responsibility: typed adapter for runtime methods and typed row reads (plan T91), replacing per-module
//   kwargs decoding and hand-written RecordMap -> struct converters. Opt-in per method.
// - Key symbols: `Call`, `Typed`, `DecodeRecord`, `ReadAs`.
// - Pattern: decoding goes through encoding/json by the struct's json tags after a small normalisation
//   step (many2one pairs collapse to the id, `false` on a non-bool field becomes null), so the same
//   struct tags that drive the registry drive the decoding.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"git.thebase.vn/base_hub/internal/utils/anyconv"
)

// Call is what a Typed method body receives: the decoded arguments plus the raw dispatch inputs.
type Call[A any] struct {
	Args   A
	Model  *ModelMetadata
	IDs    []string
	Kwargs map[string]any
}

// ID returns the single record id of a UsesPathID call, or an invalid-params error when the call
// does not carry exactly one valid id.
func (c Call[A]) ID() (int64, error) {
	if len(c.IDs) != 1 {
		return 0, InvalidParamsf("exactly one record id is required, got %d", len(c.IDs))
	}
	id, ok := anyconv.Int64(c.IDs[0])
	if !ok || id <= 0 {
		return 0, InvalidParamsf("invalid record id %q", c.IDs[0])
	}
	return id, nil
}

// Typed sets m.Call to an adapter that decodes the call arguments into A and runs fn. Arguments are
// the kwargs, merged over a positional args[0] object when one is sent (kwargs win), which matches how
// hand-written methods read them today. When m.Params is nil and A is a struct, Params are derived
// from A's scalar fields so the API docs and the Actions popup list them.
func Typed[A, R any](m RuntimeMethod, fn func(ctx context.Context, c Call[A]) (R, error)) RuntimeMethod {
	if m.Params == nil {
		m.Params = runtimeMethodParamsFor(reflect.TypeOf((*A)(nil)).Elem())
	}
	m.Call = typedRuntimeCall(fn)
	return m
}

func typedRuntimeCall[A, R any](fn func(ctx context.Context, c Call[A]) (R, error)) RuntimeMethodFunc {
	return func(ctx context.Context, model *ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
		decoded, err := DecodeRecord[A](typedCallSource(args, kwargs))
		if err != nil {
			return nil, err
		}
		return fn(ctx, Call[A]{Args: decoded, Model: model, IDs: ids, Kwargs: kwargs})
	}
}

// typedCallSource merges a positional object (args[0]) under the kwargs.
func typedCallSource(args []any, kwargs map[string]any) RecordMap {
	source := RecordMap{}
	if len(args) > 0 {
		if positional, err := anyToRecordMap(args[0]); err == nil {
			for key, value := range positional {
				source[key] = value
			}
		}
	}
	for key, value := range kwargs {
		source[key] = value
	}
	return source
}

// DecodeRecord decodes a runtime row or kwargs map into T by json tags. Many2one `[id, name]` pairs
// collapse to the id for integer targets, and `false` (an unset many2one or empty value in runtime
// reads) becomes null on non-bool targets. A value of the wrong type is an invalid-params error.
func DecodeRecord[T any](row RecordMap) (T, error) {
	var out T
	targets := jsonFieldTypes(reflect.TypeOf((*T)(nil)).Elem())
	normalized := make(map[string]any, len(row))
	for key, value := range row {
		normalized[key] = normalizeDecodeValue(value, targets[key])
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return out, InvalidParamsf("cannot encode values: %v", err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) && typeErr.Field != "" {
			return out, InvalidParamsf("%q must be a valid %s value", typeErr.Field, typeErr.Type)
		}
		return out, InvalidParamsf("invalid values: %v", err)
	}
	return out, nil
}

// ReadAs reads ids through RuntimeRepositoryRead (test stub seam included) and decodes each row
// into T. fields follows RuntimeRepositoryRead (nil reads the repository default projection).
func ReadAs[T any](ctx context.Context, model *ModelMetadata, ids []string, fields []string) ([]T, error) {
	rows, err := RuntimeRepositoryRead(ctx, model, ids, fields)
	if err != nil {
		return nil, err
	}
	out := make([]T, 0, len(rows))
	for _, row := range rows {
		decoded, err := DecodeRecord[T](row)
		if err != nil {
			return nil, fmt.Errorf("orm: decode %s row: %w", model.Name, err)
		}
		out = append(out, decoded)
	}
	return out, nil
}

// normalizeDecodeValue rewrites the runtime value shapes encoding/json cannot map onto target.
func normalizeDecodeValue(value any, target reflect.Type) any {
	if target == nil {
		return value
	}
	target = dereferenceType(target)
	if flag, ok := value.(bool); ok && !flag && target.Kind() != reflect.Bool && target.Kind() != reflect.Interface {
		return nil
	}
	if isIntegerKind(target.Kind()) {
		switch value.(type) {
		case [2]any, []any:
			if id, ok := anyconv.Many2OneID(value); ok {
				return id
			}
		}
	}
	return value
}

// jsonFieldTypes maps json keys to field types, flattening embedded structs the way encoding/json does.
func jsonFieldTypes(typ reflect.Type) map[string]reflect.Type {
	out := map[string]reflect.Type{}
	typ = dereferenceType(typ)
	if typ.Kind() != reflect.Struct {
		return out
	}
	for _, field := range reflect.VisibleFields(typ) {
		if !field.IsExported() || (field.Anonymous && dereferenceType(field.Type).Kind() == reflect.Struct && field.Tag.Get("json") == "") {
			continue
		}
		name, _ := parseStructTag(field.Tag.Get("json"))
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		out[name] = field.Type
	}
	return out
}

// runtimeMethodParamsFor derives Params from A's scalar fields (structs, slices and maps have no
// popup input type and are left out). Labels come from `meta:"label:..."` when present.
func runtimeMethodParamsFor(typ reflect.Type) []RuntimeMethodParam {
	typ = dereferenceType(typ)
	if typ.Kind() != reflect.Struct {
		return nil
	}
	var params []RuntimeMethodParam
	for _, field := range reflect.VisibleFields(typ) {
		if !field.IsExported() || field.Anonymous {
			continue
		}
		name, _ := parseStructTag(field.Tag.Get("json"))
		paramType := runtimeMethodParamType(field.Type)
		if name == "-" || paramType == "" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		options := parseFieldMetaOptions(field)
		params = append(params, RuntimeMethodParam{
			Name: name, Label: options["label"], Type: paramType,
			Required: parseBoolOption(options, "required"), Help: options["help"],
		})
	}
	return params
}

var timeType = reflect.TypeOf(time.Time{})

func runtimeMethodParamType(typ reflect.Type) string {
	typ = dereferenceType(typ)
	if typ == timeType {
		return RuntimeMethodParamDatetime
	}
	switch {
	case typ.Kind() == reflect.Bool:
		return RuntimeMethodParamBoolean
	case isIntegerKind(typ.Kind()):
		return RuntimeMethodParamInteger
	case isFloatKind(typ.Kind()):
		return RuntimeMethodParamFloat
	case typ.Kind() == reflect.String:
		return RuntimeMethodParamChar
	}
	return ""
}
