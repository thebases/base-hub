package models

import (
	"context"
	"testing"
)

func okStep(key string) workflowDefStep {
	return workflowDefStep{StepKey: key, Code: "transform_data", Version: 1, HasCatalog: true,
		Columns: map[string]any{"template": map[string]any{"a": "b"}}}
}

func activeWebhook() workflowDefTrigger {
	return workflowDefTrigger{ID: 7, TriggerType: "webhook", Topic: "t", IsActive: true}
}

// onErrorStep is okStep with extra_params.on_error_step_key = target (plan T103).
func onErrorStep(key, target string) workflowDefStep {
	step := okStep(key)
	step.Extra = map[string]any{StepOnErrorParam: target}
	return step
}

// approverStep is a wait_approval step with extra_params.approver_group_id = group.
func approverStep(key string, group any) workflowDefStep {
	return workflowDefStep{StepKey: key, Code: WaitApprovalActionCode, Version: 1, HasCatalog: true, Extra: map[string]any{WaitApprovalGroupParam: group}}
}

func TestValidateWorkflowDefinition(t *testing.T) {
	badParams := workflowDefStep{StepKey: "bad", Code: "transform_data", Version: 1, HasCatalog: true, Columns: map[string]any{}}
	tests := []struct {
		name string
		def  workflowDefinition
		want []string // "severity:code:step_key"
	}{
		{"clean sequential", workflowDefinition{Steps: []workflowDefStep{okStep("a")}, Triggers: []workflowDefTrigger{activeWebhook()}}, nil},
		// No trigger row: the implicit webhook serves /wh, so it counts as an active trigger.
		{"empty workflow", workflowDefinition{}, []string{"warning:no_steps:"}},
		{"implicit webhook only", workflowDefinition{Steps: []workflowDefStep{okStep("a")}}, nil},
		// Same rule as /wh: with no other transport row, even an inactive webhook row serves.
		{"inactive webhook row only", workflowDefinition{Steps: []workflowDefStep{okStep("a")}, Triggers: []workflowDefTrigger{{ID: 1, TriggerType: "webhook", Topic: "t"}}}, nil},
		{"inactive trigger only", workflowDefinition{Steps: []workflowDefStep{okStep("a")}, Triggers: []workflowDefTrigger{{ID: 1, TriggerType: "mqtt", Topic: "t"}}}, []string{"warning:no_active_trigger:"}},
		{"inactive mqtt and webhook rows", workflowDefinition{Steps: []workflowDefStep{okStep("a")}, Triggers: []workflowDefTrigger{{ID: 1, TriggerType: "mqtt", Topic: "t"}, {ID: 2, TriggerType: "webhook", Topic: "t"}}}, []string{"warning:no_active_trigger:"}},
		{"duplicate and empty keys", workflowDefinition{Steps: []workflowDefStep{okStep("a"), okStep("a"), okStep("")}, Triggers: []workflowDefTrigger{activeWebhook()}},
			[]string{"error:step_key_duplicate:a", "error:step_key_empty:"}},
		{"unknown action", workflowDefinition{Steps: []workflowDefStep{{StepKey: "x", Code: "nope", Version: 1}}, Triggers: []workflowDefTrigger{activeWebhook()}}, []string{"error:unknown_action:x"}},
		{"catalog row without node def is not an error", workflowDefinition{Steps: []workflowDefStep{{StepKey: "x", Code: "legacy", Version: 1, HasCatalog: true}}, Triggers: []workflowDefTrigger{activeWebhook()}}, nil},
		{"missing required param", workflowDefinition{Steps: []workflowDefStep{badParams}, Triggers: []workflowDefTrigger{activeWebhook()}}, []string{"error:invalid_param:bad"}},
		{"orphan edge", workflowDefinition{Steps: []workflowDefStep{okStep("a"), okStep("b")}, Edges: []workflowDefEdge{{ID: 1, From: "a", To: "b"}, {ID: 2, From: "a", To: "ghost"}}, Triggers: []workflowDefTrigger{activeWebhook()}},
			[]string{"error:edge_missing_step:ghost"}},
		{"cycle", workflowDefinition{Steps: []workflowDefStep{okStep("a"), okStep("b")}, Edges: []workflowDefEdge{{ID: 1, From: "a", To: "b"}, {ID: 2, From: "b", To: "a"}}, Triggers: []workflowDefTrigger{activeWebhook()}},
			[]string{"error:cycle:a", "error:cycle:b"}},
		{"unreachable step", workflowDefinition{Steps: []workflowDefStep{okStep("a"), okStep("b"), okStep("c")}, Edges: []workflowDefEdge{{ID: 1, From: "a", To: "b"}}, Triggers: []workflowDefTrigger{activeWebhook()}},
			[]string{"warning:step_unreachable:c"}},
		{"condition edge without expr", workflowDefinition{Steps: []workflowDefStep{okStep("a"), okStep("b")}, Edges: []workflowDefEdge{{ID: 1, From: "a", To: "b", EdgeType: "condition", ConditionExpr: "  "}}, Triggers: []workflowDefTrigger{activeWebhook()}},
			[]string{"error:condition_expr_empty:a"}},
		{"bad webhook config", workflowDefinition{Steps: []workflowDefStep{okStep("a")}, Triggers: []workflowDefTrigger{{ID: 9, TriggerType: "webhook", Topic: "t", IsActive: true, Config: map[string]any{"hmac_secret": "literal"}}}},
			[]string{"error:trigger_config_invalid:"}},
		{"on_error forward target", workflowDefinition{Steps: []workflowDefStep{onErrorStep("a", "c"), okStep("b"), okStep("c")}, Triggers: []workflowDefTrigger{activeWebhook()}}, nil},
		{"on_error missing and backward targets", workflowDefinition{Steps: []workflowDefStep{okStep("a"), onErrorStep("b", "ghost"), onErrorStep("c", "a"), onErrorStep("d", "d")}, Triggers: []workflowDefTrigger{activeWebhook()}},
			[]string{"error:on_error_target_missing:b", "error:on_error_target_backward:c", "error:on_error_target_backward:d"}},
		{"on_error target without step_key resolves like the engine", workflowDefinition{Steps: []workflowDefStep{onErrorStep("a", "notify_me"), {Name: "Notify Me", Code: "transform_data", Version: 1, HasCatalog: true, Columns: map[string]any{"template": map[string]any{"a": "b"}}}}, Triggers: []workflowDefTrigger{activeWebhook()}},
			[]string{"error:step_key_empty:"}},
		{"wait_approval in a linear workflow", workflowDefinition{Steps: []workflowDefStep{okStep("a"), {StepKey: "w", Code: WaitApprovalActionCode, Version: 1, HasCatalog: true}}, Triggers: []workflowDefTrigger{activeWebhook()}}, nil},
		{"wait_approval in a DAG", workflowDefinition{ExecutionMode: WorkflowExecutionModeDAG, Steps: []workflowDefStep{okStep("a"), {StepKey: "w", Code: WaitApprovalActionCode, Version: 1, HasCatalog: true}}, Edges: []workflowDefEdge{{ID: 1, From: "a", To: "w"}}, Triggers: []workflowDefTrigger{activeWebhook()}},
			[]string{"error:wait_approval_in_dag:w"}},
		{"wait_approval with an existing approver group", workflowDefinition{GroupIDs: map[int64]bool{12: true}, Steps: []workflowDefStep{okStep("a"), approverStep("w", 12.0)}, Triggers: []workflowDefTrigger{activeWebhook()}}, nil},
		{"wait_approval with an unknown approver group", workflowDefinition{GroupIDs: map[int64]bool{12: true}, Steps: []workflowDefStep{okStep("a"), approverStep("w", "13")}, Triggers: []workflowDefTrigger{activeWebhook()}},
			[]string{"error:invalid_param:w"}},
		{"wait_approval with a malformed approver group", workflowDefinition{Steps: []workflowDefStep{approverStep("w", "sales")}, Triggers: []workflowDefTrigger{activeWebhook()}},
			[]string{"error:invalid_param:w"}},
		{"model trigger without model", workflowDefinition{Steps: []workflowDefStep{okStep("a")}, Triggers: []workflowDefTrigger{{ID: 9, TriggerType: "model", IsActive: true}}},
			[]string{"error:trigger_config_invalid:"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := validateWorkflowDefinition(context.Background(), tc.def)
			var keys []string
			for _, issue := range got {
				keys = append(keys, issue.Severity+":"+issue.Code+":"+issue.StepKey)
			}
			if len(keys) != len(tc.want) {
				t.Fatalf("issues = %v, want %v", keys, tc.want)
			}
			for i := range keys {
				if keys[i] != tc.want[i] {
					t.Fatalf("issues = %v, want %v", keys, tc.want)
				}
			}
		})
	}
}

func TestValidateWorkflowIssueIDs(t *testing.T) {
	got := validateWorkflowDefinition(context.Background(), workflowDefinition{
		Steps:    []workflowDefStep{okStep("a")},
		Edges:    []workflowDefEdge{{ID: 5, From: "a", To: "zz"}},
		Triggers: []workflowDefTrigger{{ID: 9, TriggerType: "model", IsActive: true}},
	})
	var edgeID, triggerID int64
	for _, issue := range got {
		edgeID += issue.EdgeID
		triggerID += issue.TriggerID
	}
	if edgeID != 5 || triggerID != 9 {
		t.Fatalf("edge/trigger ids = %d/%d, issues %+v", edgeID, triggerID, got)
	}
}

func TestValidateMethodRegistered(t *testing.T) {
	for _, m := range FlowModelSpec.RuntimeMethods {
		if m.Name == "validate" {
			if !m.Public || !m.ReadOnly || !m.UsesPathID {
				t.Fatalf("validate must be public read-only path-id: %+v", m)
			}
			return
		}
	}
	t.Fatal("validate method not registered")
}
