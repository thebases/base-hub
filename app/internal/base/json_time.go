package base

// File overview:
// - Responsibility: rewrites every time.Time inside an API payload into a UTC Unix-seconds integer before JSON encoding.
// - Key symbols: `TimesToUnix`, `convertTimeValue`, `typeHasTime`.
// - Contract: a zero time becomes `false` (the app-wide "unset" sentinel, same as null); values whose type cannot contain a time are returned untouched.

import (
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"time"
)

var (
	timeType       = reflect.TypeOf(time.Time{})
	jsonMarshaler  = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	hasTimeCache   sync.Map // reflect.Type -> bool
	structFieldMap sync.Map // reflect.Type -> []timeStructField
)

// timeStructField is one JSON-visible struct field, resolved once per type.
type timeStructField struct {
	index     []int
	name      string
	omitEmpty bool
}

// TimesToUnix returns v with every time.Time replaced by its UTC Unix-seconds value.
// Structs that contain times are flattened into map[string]any following their json tags;
// everything else is passed through so encoding behaviour is unchanged.
func TimesToUnix(v any) any {
	switch v.(type) {
	case nil, string, bool, float64, float32, int, int64, int32, json.RawMessage, []byte:
		return v
	}
	return convertTimeValue(reflect.ValueOf(v))
}

// customMarshaler reports whether a type owns its JSON form (other than time.Time itself).
func customMarshaler(t reflect.Type) bool {
	if t == timeType || (t.Kind() == reflect.Ptr && t.Elem() == timeType) {
		return false
	}
	return t.Implements(jsonMarshaler) || reflect.PointerTo(t).Implements(jsonMarshaler)
}

// typeHasTime reports whether a value of type t can hold a time.Time at any depth.
// Interfaces answer true because their dynamic content is only known at runtime.
func typeHasTime(t reflect.Type) bool {
	if cached, ok := hasTimeCache.Load(t); ok {
		return cached.(bool)
	}
	// Seed false first so self-referential types terminate.
	hasTimeCache.Store(t, false)
	result := computeHasTime(t)
	hasTimeCache.Store(t, result)
	return result
}

func computeHasTime(t reflect.Type) bool {
	if t == timeType {
		return true
	}
	if customMarshaler(t) {
		return false
	}
	switch t.Kind() {
	case reflect.Interface:
		return true
	case reflect.Ptr, reflect.Slice, reflect.Array:
		return typeHasTime(t.Elem())
	case reflect.Map:
		return t.Key().Kind() == reflect.String && typeHasTime(t.Elem())
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if field.PkgPath != "" && !field.Anonymous {
				continue
			}
			if typeHasTime(field.Type) {
				return true
			}
		}
	}
	return false
}

// convertTimeValue walks rv and returns its time-converted JSON-ready form.
func convertTimeValue(rv reflect.Value) any {
	if !rv.IsValid() {
		return nil
	}
	t := rv.Type()
	if t == timeType {
		ts := rv.Interface().(time.Time)
		if ts.IsZero() {
			return false
		}
		return ts.UTC().Unix()
	}
	if !typeHasTime(t) {
		return rv.Interface()
	}
	switch rv.Kind() {
	case reflect.Ptr, reflect.Interface:
		if rv.IsNil() {
			return nil
		}
		return convertTimeValue(rv.Elem())
	case reflect.Map:
		if rv.IsNil() {
			return nil
		}
		out := make(map[string]any, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			out[iter.Key().String()] = convertTimeValue(iter.Value())
		}
		return out
	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice && rv.IsNil() {
			return nil
		}
		out := make([]any, rv.Len())
		for i := range out {
			out[i] = convertTimeValue(rv.Index(i))
		}
		return out
	case reflect.Struct:
		out := map[string]any{}
		for _, field := range structFields(t) {
			fv, ok := fieldByIndexSafe(rv, field.index)
			if !ok {
				continue
			}
			if field.omitEmpty && fv.IsZero() {
				continue
			}
			out[field.name] = convertTimeValue(fv)
		}
		return out
	}
	return rv.Interface()
}

// fieldByIndexSafe reads a nested field, skipping paths through nil embedded pointers.
func fieldByIndexSafe(rv reflect.Value, index []int) (reflect.Value, bool) {
	for i, idx := range index {
		if i > 0 && rv.Kind() == reflect.Ptr {
			if rv.IsNil() {
				return reflect.Value{}, false
			}
			rv = rv.Elem()
		}
		rv = rv.Field(idx)
	}
	return rv, true
}

// structFields resolves the JSON-visible fields of a struct, flattening untagged embedded structs.
func structFields(t reflect.Type) []timeStructField {
	if cached, ok := structFieldMap.Load(t); ok {
		return cached.([]timeStructField)
	}
	var fields []timeStructField
	collectStructFields(t, nil, &fields)
	structFieldMap.Store(t, fields)
	return fields
}

func collectStructFields(t reflect.Type, prefix []int, out *[]timeStructField) {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		index := append(append([]int{}, prefix...), i)
		if field.Anonymous && name == "" {
			ft := field.Type
			if ft.Kind() == reflect.Ptr {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct && !customMarshaler(field.Type) && ft != timeType {
				collectStructFields(ft, index, out)
				continue
			}
		}
		if field.PkgPath != "" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		*out = append(*out, timeStructField{
			index:     index,
			name:      name,
			omitEmpty: strings.Contains(opts, "omitempty"),
		})
	}
}
