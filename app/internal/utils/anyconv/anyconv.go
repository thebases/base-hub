// Package anyconv decodes loosely typed RPC arguments (JSON values and Go
// numerics held in `any`) into concrete types. It replaces the per-module
// stringValue/int64FromAny/argAt copies. Conversions never guess: a value of
// the wrong type reports ok=false (or an ErrArgType from RequiredArg) instead
// of silently becoming "" or 0.
package anyconv

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// ErrArgType is returned by RequiredArg when an argument is missing or has the
// wrong type. Callers map it to a validation error.
var ErrArgType = errors.New("anyconv: invalid argument")

// At returns args[index], or nil when the index is out of range.
func At(args []any, index int) any {
	if index < 0 || index >= len(args) {
		return nil
	}
	return args[index]
}

// String returns the whitespace-trimmed string and true when raw is a string.
func String(raw any) (string, bool) {
	s, ok := raw.(string)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(s), true
}

// Int64 converts integer-valued numerics and decimal strings. Floats with a
// fractional part, NaN/Inf and out-of-range values are rejected. Zero and
// negative values are returned as-is; callers that need a positive id check
// that themselves.
func Int64(raw any) (int64, bool) {
	switch v := raw.(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case int32:
		return int64(v), true
	case uint32:
		return int64(v), true
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || v != math.Trunc(v) || v < math.MinInt64 || v >= math.MaxInt64 {
			return 0, false
		}
		return int64(v), true
	case json.Number:
		n, err := strconv.ParseInt(v.String(), 10, 64)
		return n, err == nil
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		return n, err == nil
	default:
		return 0, false
	}
}

// Bool accepts bool values and the strings "true"/"false" (case-insensitive).
func Bool(raw any) (bool, bool) {
	switch v := raw.(type) {
	case bool:
		return v, true
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true":
			return true, true
		case "false":
			return false, true
		}
	}
	return false, false
}

// Arg decodes args[index] as T using the converters above. T is one of string,
// int64, bool or any exact type already held in the slot (map[string]any,
// []any, ...). The second result is false when the slot is missing or does not
// convert.
func Arg[T any](args []any, index int) (T, bool) {
	var zero T
	raw := At(args, index)
	if raw == nil {
		return zero, false
	}
	switch any(zero).(type) {
	case string:
		v, ok := String(raw)
		if !ok {
			return zero, false
		}
		return any(v).(T), true
	case int64:
		v, ok := Int64(raw)
		if !ok {
			return zero, false
		}
		return any(v).(T), true
	case bool:
		v, ok := Bool(raw)
		if !ok {
			return zero, false
		}
		return any(v).(T), true
	}
	v, ok := raw.(T)
	return v, ok
}

// RequiredArg is Arg that reports a missing or wrongly typed argument as an
// error naming the argument, so RPC handlers return a validation error rather
// than continuing with a zero value.
func RequiredArg[T any](args []any, index int, name string) (T, error) {
	v, ok := Arg[T](args, index)
	if !ok {
		var zero T
		return zero, fmt.Errorf("%w: %q (position %d) must be %T", ErrArgType, name, index, zero)
	}
	return v, nil
}

// Many2OneID returns the record id held in a many2one value. Runtime reads
// return many2one fields as an [id, display_name] tuple ([2]any from
// RuntimeRead/RuntimeSearchRead, []any after a JSON round trip); writes and
// RPC arguments carry a bare id; some callers pass {"id": n}. All of them
// decode here. A missing value, false (unset many2one) or a non-positive id
// reports ok=false.
func Many2OneID(raw any) (int64, bool) {
	var id int64
	var ok bool
	switch v := raw.(type) {
	case [2]any:
		id, ok = Int64(v[0])
	case []any:
		if len(v) == 0 {
			return 0, false
		}
		id, ok = Int64(v[0])
	case map[string]any:
		id, ok = Int64(v["id"])
	default:
		id, ok = Int64(raw)
	}
	if !ok || id <= 0 {
		return 0, false
	}
	return id, true
}

// timeLayouts are the string shapes date/datetime values take on the wire:
// RFC 3339 from JSON clients, Postgres text output, and plain dates.
var timeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999-07",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02",
}

// Time decodes a date/datetime value. Values read from the database arrive as
// time.Time (or *time.Time); values from RPC clients arrive as strings. Strings
// without an offset are read as UTC. The zero time, nil, false and empty
// strings report ok=false.
func Time(raw any) (time.Time, bool) {
	switch v := raw.(type) {
	case time.Time:
		return v, !v.IsZero()
	case *time.Time:
		if v == nil || v.IsZero() {
			return time.Time{}, false
		}
		return *v, true
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return time.Time{}, false
		}
		for _, layout := range timeLayouts {
			if t, err := time.Parse(layout, s); err == nil {
				return t, !t.IsZero()
			}
		}
	}
	return time.Time{}, false
}
