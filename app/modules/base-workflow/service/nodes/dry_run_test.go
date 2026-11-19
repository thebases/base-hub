package nodes

import (
	"encoding/json"
	"testing"

	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

// Every builtin with external or persistent effects is declared as such (plan T101): a new node
// must decide, so a dry run can never run an effect by omission.
func TestNodeDefsDeclareSideEffects(t *testing.T) {
	want := map[string]string{
		"post_output_object": workflowmodels.NodeEffectHTTP,
		"notify":             workflowmodels.NodeEffectOutbox,
		"crud_model":         workflowmodels.NodeEffectWrite,
		"lua_script":         workflowmodels.NodeEffectScript,
		"call_workflow":      workflowmodels.NodeEffectScript,
		"for_each":           workflowmodels.NodeEffectScript,
	}
	for _, def := range workflowmodels.NodeDefs() {
		if def.SideEffect != want[def.Code] {
			t.Errorf("%s side effect = %q, want %q", def.Code, def.SideEffect, want[def.Code])
		}
	}
}

func dryRunIntentFor(t *testing.T, code string, params map[string]any, refs map[string]string, payload string) (map[string]any, bool) {
	t.Helper()
	output, stubbed, err := DryRunStepOutput(code, 1, "k", params, refs, []byte(payload))
	if err != nil {
		t.Fatalf("%s: %v", code, err)
	}
	if !stubbed {
		return nil, false
	}
	var decoded map[string]any
	if err := json.Unmarshal(output, &decoded); err != nil {
		t.Fatalf("%s output: %v", code, err)
	}
	intended := decoded[DryRunIntendedKey].([]any)
	return intended[len(intended)-1].(map[string]any), true
}

func TestDryRunStepOutputRedactsAndKeepsRefs(t *testing.T) {
	intent, stubbed := dryRunIntentFor(t, "post_output_object", map[string]any{
		"target_ref":    "https://partner.example/post",
		"authorization": "@syscred:partner_token",
		"headers":       map[string]any{"Authorization": "Bearer raw", "X-Api-Key": "k", "X-Trace": "1"},
	}, map[string]string{"authorization": "@syscred:partner_token"}, `{"a":1}`)
	if !stubbed {
		t.Fatal("post_output_object was not stubbed")
	}
	params := intent["params"].(map[string]any)
	headers := params["headers"].(map[string]any)
	if params["authorization"] != "@syscred:partner_token" || headers["Authorization"] != dryRunRedacted || headers["X-Api-Key"] != dryRunRedacted || headers["X-Trace"] != "1" {
		t.Fatalf("intent params = %#v", params)
	}
	if intent["method"] != "POST" || intent["url"] != "https://partner.example/post" || intent["effect"] != workflowmodels.NodeEffectHTTP {
		t.Fatalf("intent = %#v", intent)
	}

	crud, _ := dryRunIntentFor(t, "crud_model", map[string]any{"model": "res.partner", "operation": "Create", "result_key": "_r", "values": map[string]any{"password": "p", "name": "n"}}, nil, `{}`)
	values := crud["params"].(map[string]any)["values"].(map[string]any)
	if crud["operation"] != "create" || values["password"] != dryRunRedacted || values["name"] != "n" || crud["params"].(map[string]any)["result_key"] != "_r" {
		t.Fatalf("crud intent = %#v", crud)
	}
}

func TestDryRunStepOutputLetsPureStepsRun(t *testing.T) {
	cases := []struct {
		code   string
		params map[string]any
		refs   map[string]string
	}{
		{code: "parse_payload"},
		{code: "flatten_json"},
		{code: "transform_data", params: map[string]any{"template": map[string]any{"a": "b"}}},
		{code: "condition", params: map[string]any{"expression": map[string]any{"field": "a", "equals": 1}}},
		{code: "aes_action"}, // no key: generates its own, nothing to decrypt
		{code: "crud_model", params: map[string]any{"model": "res.partner", "operation": "search_read"}},
	}
	for _, tc := range cases {
		if _, stubbed := dryRunIntentFor(t, tc.code, tc.params, tc.refs, `{}`); stubbed {
			t.Errorf("%s was stubbed, want it to run", tc.code)
		}
	}
	if intent, stubbed := dryRunIntentFor(t, "hmac_action", nil, map[string]string{"hmac_key": "@cred:k"}, `{}`); !stubbed || intent["reason"] != dryRunReasonCredential {
		t.Fatalf("hmac with a credential ref: stubbed=%v intent=%#v", stubbed, intent)
	}
}

func TestDryRunPassThroughAccumulatesIntents(t *testing.T) {
	first, _, _ := DryRunStepOutput("notify", 1, "a", map[string]any{"target_ref": "https://x"}, nil, []byte(`{"order":7}`))
	second, _, _ := DryRunStepOutput("lua_script", 1, "b", map[string]any{"automation_action_id": 3}, nil, first)
	var decoded map[string]any
	if err := json.Unmarshal(second, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["order"] != float64(7) || decoded[DryRunMarkerKey] != true || len(decoded[DryRunIntendedKey].([]any)) != 2 {
		t.Fatalf("output = %#v", decoded)
	}
}
