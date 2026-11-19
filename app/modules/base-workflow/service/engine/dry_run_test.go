package engine

// File overview:
// - Responsibility: plan T101 dry run through the real builtin handlers with counting runtime
//   hooks: side-effect steps never reach a hook (no outbound row, no HTTP, no model write, no Lua)
//   and no credential is resolved, pure steps still run, the run row is marked and gets one attempt.

import (
	"context"
	"errors"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
	"git.thebase.vn/base_hub/modules/base-workflow/service/nodes"
)

type dryRunHookCalls struct {
	flatten, search, create, write, remove, queue, post, lua, custom int
}

func registerDryRunHooks(t *testing.T, flatten bool) *dryRunHookCalls {
	t.Helper()
	calls := &dryRunHookCalls{}
	hooks := nodes.WorkflowActionRuntimeHooks{
		SearchModelRecords: func(context.Context, string, map[string]any, []string) ([]map[string]any, error) {
			calls.search++
			return []map[string]any{{"id": int64(9), "name": "Found"}}, nil
		},
		CreateModelRecord: func(context.Context, string, map[string]any) (map[string]any, error) {
			calls.create++
			return map[string]any{"id": int64(1)}, nil
		},
		WriteModelRecords: func(context.Context, string, []string, map[string]any) (bool, error) {
			calls.write++
			return true, nil
		},
		DeleteModelRecords: func(context.Context, string, []string) (bool, error) {
			calls.remove++
			return true, nil
		},
		QueueHTTPJSON: func(context.Context, string, string, map[string]string, string, map[string]any) error {
			calls.queue++
			return nil
		},
		PostHTTPJSON: func(context.Context, string, map[string]string, map[string]any) (int, string, error) {
			calls.post++
			return 200, "", nil
		},
		RunLuaAction: func(context.Context, int64, map[string]any, map[string]any) (map[string]any, error) {
			calls.lua++
			return map[string]any{}, nil
		},
	}
	if flatten {
		hooks.FlattenJSON = func(in map[string]any, out map[string]any) {
			calls.flatten++
			for key, value := range in {
				out[key] = value
			}
		}
	}
	previous := nodes.CurrentWorkflowActionRuntimeHooks()
	nodes.RegisterWorkflowActionRuntimeHooks(hooks)
	t.Cleanup(func() { nodes.RegisterWorkflowActionRuntimeHooks(previous) })
	return calls
}

// dryRunTestService runs the real builtin handlers on in-memory run writes; any other action code
// resolves to a counting handler, standing in for a module-registered action with no NodeDef.
func dryRunTestService(calls *dryRunHookCalls, created *workflowmodels.WorkflowRunRecord, updates *[]orm.RecordMap) *Service {
	return &Service{
		lookupAction: func(code string, version int) (ActionHandler, bool) {
			if handler, ok := resolveWorkflowModuleAction(code, version); ok {
				return handler, true
			}
			return func(_ context.Context, req ActionRequest) ([]byte, error) {
				calls.custom++
				return req.Payload, nil
			}, true
		},
		createRun: func(_ context.Context, record workflowmodels.WorkflowRunRecord) (*orm.InsertResult, error) {
			*created = record
			return &orm.InsertResult{InsertedID: int64(8), Acknowledged: true}, nil
		},
		updateRun: func(_ context.Context, _ orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			*updates = append(*updates, cloneRecordMap(values))
			return &orm.UpdateResult{Acknowledged: true, MatchedCount: 1, ModifiedCount: 1}, nil
		},
		now:   func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		sleep: func(context.Context, time.Duration) error { return nil },
	}
}

func TestDryRunStubsSideEffectStepsAndRunsPureOnes(t *testing.T) {
	calls := registerDryRunHooks(t, true)
	var created workflowmodels.WorkflowRunRecord
	var updates []orm.RecordMap
	svc := dryRunTestService(calls, &created, &updates)

	steps := []workflowmodels.Step{
		{Key: "flat", Action: "flatten_json", ActionVersion: 1},
		{Key: "lookup", Action: "crud_model", ActionVersion: 1, Params: map[string]any{"model": "res.partner", "operation": "search_one", "filter": map[string]any{"name": "Found"}}},
		{Key: "save", Action: "crud_model", ActionVersion: 1, Params: map[string]any{"model": "res.partner", "operation": "create", "values": map[string]any{"name": "{{ customer }}", "api_key": "plain-secret"}}},
		{Key: "notify", Action: "notify", ActionVersion: 1, Params: map[string]any{"target_ref": "https://partner.example/hook", "authorization": "@syscred:partner_token"}},
		{Key: "post", Action: "post_output_object", ActionVersion: 1, Params: map[string]any{"target_ref": "https://partner.example/post", "headers": map[string]any{"X-Api-Key": "literal", "X-Trace": "1"}}},
		{Key: "script", Action: "lua_script", ActionVersion: 1, Params: map[string]any{"automation_action_id": int64(7)}},
		{Key: "sign", Action: "hmac_action", ActionVersion: 1, Params: map[string]any{"hmac_key": "@cred:signing_key"}},
		{Key: "custom", Action: "module_specific_action", ActionVersion: 1},
	}
	ctx := workflowmodels.ContextWithWorkflowDryRun(context.Background())
	err := svc.executeFlow(ctx, flowRuntime{
		flow:  workflowmodels.FlowRecord{BaseModel: orm.BaseModel{ID: 1}, Topic: "orders", Version: 1, IsActive: true},
		steps: steps,
	}, []byte(`{"customer":"Ann"}`))
	if err != nil {
		t.Fatalf("dry run: %v (a credential lookup or a side effect was attempted)", err)
	}

	if calls.flatten == 0 || calls.search != 1 {
		t.Fatalf("pure steps did not run: flatten=%d search=%d", calls.flatten, calls.search)
	}
	if calls.create+calls.write+calls.remove+calls.queue+calls.post+calls.lua+calls.custom != 0 {
		t.Fatalf("side effects in a dry run: %+v", *calls)
	}
	if !created.DryRun || !created.CallerBound {
		t.Fatalf("run row dry_run=%v caller_bound=%v, want both true", created.DryRun, created.CallerBound)
	}

	final := updates[len(updates)-1]
	if final["status"] != workflowmodels.WorkflowRunStatusSucceeded {
		t.Fatalf("final status = %#v", final["status"])
	}
	output, _ := final["output_payload"].(map[string]any)
	if output["customer"] != "Ann" || output[nodes.DryRunMarkerKey] != true {
		t.Fatalf("payload not passed through: %#v", output)
	}
	intended, _ := output[nodes.DryRunIntendedKey].([]any)
	wantKeys := []string{"save", "notify", "post", "script", "sign", "custom"}
	if len(intended) != len(wantKeys) {
		t.Fatalf("intents = %#v, want %v", intended, wantKeys)
	}
	byKey := map[string]map[string]any{}
	for i, raw := range intended {
		intent, _ := raw.(map[string]any)
		if intent["step"] != wantKeys[i] {
			t.Fatalf("intent %d step = %#v, want %s", i, intent["step"], wantKeys[i])
		}
		byKey[wantKeys[i]] = intent
	}
	save := byKey["save"]
	if save["model"] != "res.partner" || save["operation"] != "create" || save["reason"] != "side_effect" {
		t.Fatalf("crud intent = %#v", save)
	}
	// The stored run payload also passes the audit policy, so the intent's own redaction is
	// asserted in service/nodes (dry_run_test.go); here only the rendering is checked.
	if values := save["values"].(map[string]any); values["name"] != "Ann" {
		t.Fatalf("crud intent values = %#v, want the rendered name", values)
	}
	notify := byKey["notify"]
	if notify["method"] != "POST" || notify["url"] != "https://partner.example/hook" || notify["effect"] != workflowmodels.NodeEffectOutbox {
		t.Fatalf("notify intent = %#v", notify)
	}
	if byKey["script"]["automation_action_id"] == nil || byKey["sign"]["reason"] != "credential" || byKey["custom"]["reason"] != "unknown_node" {
		t.Fatalf("script/sign/custom intents = %#v %#v %#v", byKey["script"], byKey["sign"], byKey["custom"])
	}
}

func TestDryRunGetsOneAttempt(t *testing.T) {
	calls := registerDryRunHooks(t, false) // flatten_json fails: its hook is missing
	var created workflowmodels.WorkflowRunRecord
	var updates []orm.RecordMap
	svc := dryRunTestService(calls, &created, &updates)

	err := svc.executeFlow(workflowmodels.ContextWithWorkflowDryRun(context.Background()), flowRuntime{
		flow:  workflowmodels.FlowRecord{BaseModel: orm.BaseModel{ID: 1}, Topic: "orders", Version: 1, IsActive: true},
		steps: []workflowmodels.Step{{Key: "flat", Action: "flatten_json", ActionVersion: 1, RetryPolicy: workflowmodels.RetryPolicy{MaxAttempts: 3}}},
	}, []byte(`{}`))
	if err == nil || errors.Is(err, ErrRunRetryScheduled) {
		t.Fatalf("err = %v, want the step failure without a scheduled retry", err)
	}
	for _, update := range updates {
		if update["status"] == workflowmodels.WorkflowRunStatusRetryWaiting {
			t.Fatalf("dry run was parked as waiting_retry")
		}
	}
	if rows, _ := updates[len(updates)-1]["step_runs"].([]workflowmodels.WorkflowStepRun); len(rows) != 1 {
		t.Fatalf("step runs = %d, want 1 attempt", len(rows))
	}
}

func TestNonDryRunStillRunsSideEffects(t *testing.T) {
	calls := registerDryRunHooks(t, true)
	var created workflowmodels.WorkflowRunRecord
	var updates []orm.RecordMap
	svc := dryRunTestService(calls, &created, &updates)
	err := svc.executeFlow(context.Background(), flowRuntime{
		flow:  workflowmodels.FlowRecord{BaseModel: orm.BaseModel{ID: 1}, Topic: "orders", Version: 1, IsActive: true},
		steps: []workflowmodels.Step{{Key: "notify", Action: "notify", ActionVersion: 1, Params: map[string]any{"target_ref": "https://partner.example/hook"}}},
	}, []byte(`{}`))
	if err != nil || calls.queue != 1 || created.DryRun {
		t.Fatalf("err=%v queue=%d dry_run=%v, want a normal delivery", err, calls.queue, created.DryRun)
	}
}
