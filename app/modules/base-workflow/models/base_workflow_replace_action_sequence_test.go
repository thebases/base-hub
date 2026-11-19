package models

import "testing"

func TestWorkflowActionValuesListFromAny(t *testing.T) {
	values, err := workflowActionValuesListFromAny([]any{
		map[string]any{
			"action_code": "parse_payload",
			"sequence":    10,
		},
		map[string]any{
			"action_code": "crud_model",
			"sequence":    20,
		},
	})
	if err != nil {
		t.Fatalf("workflowActionValuesListFromAny() error = %v", err)
	}
	if len(values) != 2 {
		t.Fatalf("expected 2 values, got %d", len(values))
	}
	if got := values[1]["action_code"]; got != "crud_model" {
		t.Fatalf("second action_code = %#v, want crud_model", got)
	}
}
