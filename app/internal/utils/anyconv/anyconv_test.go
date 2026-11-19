package anyconv

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestInt64(t *testing.T) {
	good := map[string]struct {
		in   any
		want int64
	}{
		"int":         {7, 7},
		"float whole": {float64(12), 12},
		"string":      {" 42 ", 42},
		"json number": {json.Number("9"), 9},
		"negative":    {int64(-3), -3},
	}
	for name, c := range good {
		if got, ok := Int64(c.in); !ok || got != c.want {
			t.Errorf("%s: got %d,%v want %d", name, got, ok, c.want)
		}
	}
	for name, in := range map[string]any{"fraction": 1.5, "text": "abc", "bool": true, "nil": nil, "huge": 1e30} {
		if _, ok := Int64(in); ok {
			t.Errorf("%s must not convert", name)
		}
	}
}

func TestArgAndRequiredArg(t *testing.T) {
	args := []any{" name ", float64(5), "x", map[string]any{"a": 1}}
	if v, ok := Arg[string](args, 0); !ok || v != "name" {
		t.Fatalf("string arg: %q %v", v, ok)
	}
	if v, ok := Arg[int64](args, 1); !ok || v != 5 {
		t.Fatalf("int arg: %d %v", v, ok)
	}
	if _, ok := Arg[string](args, 1); ok {
		t.Fatal("a number must not decode as string")
	}
	if _, ok := Arg[int64](args, 9); ok {
		t.Fatal("out of range must report missing")
	}
	if v, ok := Arg[map[string]any](args, 3); !ok || v["a"] != 1 {
		t.Fatalf("map arg: %v %v", v, ok)
	}
	if _, err := RequiredArg[int64](args, 2, "id"); !errors.Is(err, ErrArgType) {
		t.Fatalf("want ErrArgType, got %v", err)
	}
}

func TestMany2OneID(t *testing.T) {
	cases := []struct {
		raw  any
		want int64
		ok   bool
	}{
		{[2]any{int64(7), "Name"}, 7, true},
		{[]any{float64(8), "Name"}, 8, true},
		{map[string]any{"id": float64(9)}, 9, true},
		{int64(10), 10, true},
		{float64(11), 11, true},
		{"12", 12, true},
		{false, 0, false},
		{nil, 0, false},
		{[]any{}, 0, false},
		{[2]any{int64(0), ""}, 0, false},
		{int64(-1), 0, false},
	}
	for _, tc := range cases {
		got, ok := Many2OneID(tc.raw)
		if got != tc.want || ok != tc.ok {
			t.Errorf("Many2OneID(%#v) = %d,%v want %d,%v", tc.raw, got, ok, tc.want, tc.ok)
		}
	}
}

func TestTime(t *testing.T) {
	ref := time.Date(2026, 11, 15, 8, 30, 0, 0, time.UTC)
	cases := []struct {
		raw  any
		want time.Time
		ok   bool
	}{
		{ref, ref, true},
		{&ref, ref, true},
		{"2026-11-15T08:30:00Z", ref, true},
		{"2026-11-15 08:30:00", ref, true},
		{"2026-11-15T08:30:00", ref, true},
		{"2026-11-15", time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC), true},
		{"", time.Time{}, false},
		{time.Time{}, time.Time{}, false},
		{false, time.Time{}, false},
		{nil, time.Time{}, false},
		{"not a date", time.Time{}, false},
	}
	for _, tc := range cases {
		got, ok := Time(tc.raw)
		if !got.Equal(tc.want) || ok != tc.ok {
			t.Errorf("Time(%#v) = %v,%v want %v,%v", tc.raw, got, ok, tc.want, tc.ok)
		}
	}
}
