package nodes

import (
	"context"
	"strings"
	"testing"

	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

func TestCallWorkflowActionUsesHook(t *testing.T) {
	saved := CurrentWorkflowActionRuntimeHooks()
	defer RegisterWorkflowActionRuntimeHooks(saved)
	var gotID int64
	hooks := saved
	hooks.CallWorkflow = func(_ context.Context, id int64, payload []byte, label string) ([]byte, error) {
		gotID = id
		return []byte(`{"child":true}`), nil
	}
	RegisterWorkflowActionRuntimeHooks(hooks)
	out, err := CallWorkflowAction(context.Background(), WorkflowActionRequest{
		Step: workflowmodels.Step{Params: map[string]any{"target_workflow_id": float64(7), "wait": "sync"}}, Payload: []byte(`{}`),
	})
	if err != nil || gotID != 7 || string(out) != `{"child":true}` {
		t.Fatalf("out=%s id=%d err=%v", out, gotID, err)
	}
	for _, params := range []map[string]any{
		{},
		{"target_workflow_id": 7, "wait": "async"},
		{"target_workflow_id": 7, "payload_mapping": "template"},
	} {
		if _, err := CallWorkflowAction(context.Background(), WorkflowActionRequest{Step: workflowmodels.Step{Params: params}, Payload: []byte(`{}`)}); err == nil || !strings.Contains(err.Error(), "call_workflow") {
			t.Fatalf("%v: err = %v", params, err)
		}
	}
}
