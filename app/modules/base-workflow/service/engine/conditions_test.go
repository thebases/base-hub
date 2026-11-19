package engine

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestEvaluateConditionMatchOperators(t *testing.T) {
	bindings := map[string]any{}
	if err := json.Unmarshal([]byte(`{"amount":150,"limit":100,"name":"Order-42","tags":["vip","new"],"code":"abc","count":"7"}`), &bindings); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		cond map[string]any
		want bool
	}{
		{"gt true", map[string]any{"field": "amount", "gt": 100}, true},
		{"gt false", map[string]any{"field": "amount", "gt": 150.0}, false},
		{"gte equal", map[string]any{"field": "amount", "gte": json.Number("150")}, true},
		{"lt ref", map[string]any{"field": "limit", "lt": "#amount"}, true},
		{"lte false", map[string]any{"field": "amount", "lte": 149}, false},
		{"range", map[string]any{"field": "amount", "gt": 100, "lt": 200}, true},
		{"missing field never matches", map[string]any{"field": "nope", "gt": 1}, false},
		{"contains substring", map[string]any{"field": "name", "contains": "der-4"}, true},
		{"contains array", map[string]any{"field": "tags", "contains": "vip"}, true},
		{"contains array miss", map[string]any{"field": "tags", "contains": "old"}, false},
		{"regex match", map[string]any{"field": "name", "regex": `^Order-\d+$`}, true},
		{"regex miss", map[string]any{"field": "code", "regex": `^\d+$`}, false},
		{"regex number field", map[string]any{"field": "amount", "regex": `^15`}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := evaluateWorkflowCondition(tc.cond, bindings)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEvaluateConditionMatchOperatorErrors(t *testing.T) {
	bindings := map[string]any{"count": "7", "amount": 5.0, "obj": map[string]any{"a": 1.0}}
	cases := []struct {
		name string
		cond map[string]any
		want string
	}{
		{"string field in gt", map[string]any{"field": "count", "gt": 1}, "numeric field value"},
		{"string operand", map[string]any{"field": "amount", "gte": "3"}, "must be a number"},
		{"bad regex", map[string]any{"field": "count", "regex": "("}, "condition.regex"},
		{"long regex", map[string]any{"field": "count", "regex": strings.Repeat("a", maxConditionRegexLen+1)}, "longer than"},
		{"regex non-string", map[string]any{"field": "count", "regex": 5}, "must be a string"},
		{"contains on object", map[string]any{"field": "obj", "contains": "a"}, "string or array"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := evaluateWorkflowCondition(tc.cond, bindings)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
	if _, err := evaluateWorkflowCondition(map[string]any{"field": "amount", "greater": 1}, bindings); !errors.Is(err, ErrUnknownConditionOperator) {
		t.Fatalf("unknown key err = %v", err)
	}
}

func TestCompileConditionRegexCaches(t *testing.T) {
	first, err := compileConditionRegex(`^t103-[a-z]+$`)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := compileConditionRegex(`^t103-[a-z]+$`)
	if first != second {
		t.Fatal("pattern compiled twice")
	}
}
