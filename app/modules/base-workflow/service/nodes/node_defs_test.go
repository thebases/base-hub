package nodes

// File overview:
// - Responsibility: checks the single-source node registry (plan T93): every definition has a
//   handler, and ValidateStepParams derives required/type/secret checks from the definitions.
// - Key symbols: `TestEveryNodeDefHasHandler`, `TestValidateStepParams`.

import (
	"reflect"
	"testing"

	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

func TestEveryNodeDefHasHandler(t *testing.T) {
	defs := workflowmodels.NodeDefs()
	if len(defs) != len(workflowActionHandlers) {
		t.Fatalf("%d definitions but %d handlers", len(defs), len(workflowActionHandlers))
	}
	for _, def := range defs {
		if _, ok := ResolveWorkflowActionHandler(def.Code, def.Version); !ok {
			t.Fatalf("%s@%d has no handler", def.Code, def.Version)
		}
	}
	if _, ok := ResolveWorkflowActionHandler("crud_model", 2); ok {
		t.Fatal("crud_model@2 must not resolve")
	}
}

func TestValidateStepParams(t *testing.T) {
	cases := []struct {
		name    string
		code    string
		columns map[string]any
		extra   map[string]any
		want    []workflowmodels.ParamIssue
	}{
		{
			name: "crud_model operation falls back to its default",
			code: "crud_model", columns: map[string]any{"model_name": "tbl.partner"},
		},
		{
			name: "crud_model missing model and unknown operation",
			code: "crud_model", extra: map[string]any{"operation": "drop", "merge_result": "yes"},
			want: []workflowmodels.ParamIssue{
				{Param: "model_name", Message: "Model Name is required"},
				{Param: "operation", Message: `Operation has an unknown value "drop"`},
				{Param: "merge_result", Message: "Merge Result must be true or false"},
			},
		},
		{
			name: "post_output_object literal authorization refused",
			code: "post_output_object", columns: map[string]any{"target_ref": "https://x.test", "authorization": "Bearer abc", "headers": map[string]any{"A": "b"}},
			want: []workflowmodels.ParamIssue{{Param: "authorization", Message: "Authorization must be a @cred:<name> or @syscred:<name> reference"}},
		},
		{
			name: "hmac credential ref accepted",
			code: "hmac_action", columns: map[string]any{"hmac_key": "@cred:signing", "include_key": true},
		},
		{
			name: "condition empty expression is missing",
			code: "condition", extra: map[string]any{"expression": map[string]any{}},
			want: []workflowmodels.ParamIssue{{Param: "expression", Message: "Condition is required"}},
		},
		{
			name: "transform template must be an object",
			code: "transform_data", columns: map[string]any{"template": "[1]"},
			want: []workflowmodels.ParamIssue{{Param: "template", Message: "Template must be a JSON object"}},
		},
		{
			name: "lua_script needs a positive action id",
			code: "lua_script", extra: map[string]any{"automation_action_id": float64(0)},
			want: []workflowmodels.ParamIssue{{Param: "automation_action_id", Message: "Custom Action must be a positive integer id"}},
		},
		{
			name: "lua_script id as JSON number",
			code: "lua_script", extra: map[string]any{"automation_action_id": float64(12), "data": map[string]any{"k": 1}},
		},
		{
			name: "unknown code",
			code: "nope",
			want: []workflowmodels.ParamIssue{{Message: "unknown action nope@1"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := workflowmodels.ValidateStepParams(tc.code, 1, tc.columns, tc.extra)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

// TestDefaultParamsAreCopies guards the registry: a caller mutating the derived default_params
// (MergeETLActionParams does) must not change the next resolution.
func TestDefaultParamsAreCopies(t *testing.T) {
	entry, _ := workflowmodels.ResolveBuiltinWorkflowAction("condition", 1)
	entry.DefaultParams["expression"].(map[string]any)["field"] = "x"
	again, _ := workflowmodels.ResolveBuiltinWorkflowAction("condition", 1)
	if len(again.DefaultParams["expression"].(map[string]any)) != 0 {
		t.Fatal("registered default leaked a mutation")
	}
}
