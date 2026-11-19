package nodes

import (
	"context"
	"testing"
)

func TestLuaScriptActionRunsSelectedActionWithDataGlobal(t *testing.T) {
	prev := workflowActionRuntimeHooks
	defer func() { workflowActionRuntimeHooks = prev }()

	var gotID int64
	var gotPayload, gotGlobals map[string]any
	workflowActionRuntimeHooks.RunLuaAction = func(_ context.Context, id int64, payload map[string]any, globals map[string]any) (map[string]any, error) {
		gotID, gotPayload, gotGlobals = id, payload, globals
		return map[string]any{"ok": true}, nil
	}

	req := WorkflowActionRequest{Payload: []byte(`{"prev":1}`), Topic: "t"}
	req.Step.Params = map[string]any{"automation_action_id": float64(7), "data": map[string]any{"limit": float64(5)}}
	out, err := LuaScriptAction(context.Background(), req)
	if err != nil || gotID != 7 || string(out) != `{"ok":true}` {
		t.Fatalf("out=%s id=%d err=%v", out, gotID, err)
	}
	if gotPayload["prev"] != float64(1) {
		t.Fatalf("payload = %#v, want previous node output", gotPayload)
	}
	if data, _ := gotGlobals["data"].(map[string]any); data["limit"] != float64(5) || gotGlobals["raw"] != `{"prev":1}` {
		t.Fatalf("globals = %#v", gotGlobals)
	}

	// No data configured still binds an empty object.
	req.Step.Params = map[string]any{"automation_action_id": 7}
	if _, err = LuaScriptAction(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if data, ok := gotGlobals["data"].(map[string]any); !ok || len(data) != 0 {
		t.Fatalf("data global = %#v, want empty object", gotGlobals["data"])
	}

	// Inline code is no longer a thing: without an action the step fails.
	req.Step.Params = map[string]any{"code": "return payload"}
	if _, err = LuaScriptAction(context.Background(), req); err == nil {
		t.Fatal("expected error when no automation action is selected")
	}
}

func TestLuaScriptActionResolvesSeedReference(t *testing.T) {
	prev := workflowActionRuntimeHooks
	defer func() { workflowActionRuntimeHooks = prev }()

	var gotRef string
	workflowActionRuntimeHooks.RunLuaActionByRef = func(_ context.Context, ref string, _ map[string]any, _ map[string]any) (map[string]any, error) {
		gotRef = ref
		return map[string]any{"ok": true}, nil
	}
	req := WorkflowActionRequest{Payload: []byte(`{}`)}
	req.Step.Params = map[string]any{"automation_action": " edc.automation.action.mqtt_accept "}
	if _, err := LuaScriptAction(context.Background(), req); err != nil || gotRef != "edc.automation.action.mqtt_accept" {
		t.Fatalf("ref=%q err=%v", gotRef, err)
	}
}
