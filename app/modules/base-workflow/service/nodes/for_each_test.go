package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

// withCallHook swaps the CallWorkflow hook for the duration of a test.
func withCallHook(t *testing.T, call func(context.Context, int64, []byte, string) ([]byte, error)) {
	t.Helper()
	saved := CurrentWorkflowActionRuntimeHooks()
	hooks := saved
	hooks.CallWorkflow = call
	RegisterWorkflowActionRuntimeHooks(hooks)
	t.Cleanup(func() { RegisterWorkflowActionRuntimeHooks(saved) })
}

func forEachRequest(params map[string]any, payload string) WorkflowActionRequest {
	base := map[string]any{"target_workflow_id": 3, "items_path": "lines"}
	for k, v := range params {
		base[k] = v
	}
	return WorkflowActionRequest{Step: workflowmodels.Step{Key: "each", Params: base}, Payload: []byte(payload)}
}

func TestForEachKeepsInputOrderUnderConcurrency(t *testing.T) {
	var running, peak atomic.Int32
	withCallHook(t, func(_ context.Context, id int64, payload []byte, label string) ([]byte, error) {
		n := running.Add(1)
		defer running.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		var in map[string]any
		_ = json.Unmarshal(payload, &in)
		// Later items finish first, so order comes from the index, not completion.
		v, _ := in["item"].(float64)
		time.Sleep(time.Duration(10-int(v)) * time.Millisecond)
		return json.Marshal(map[string]any{"double": v * 2, "label": label})
	})
	out, err := ForEachAction(context.Background(), forEachRequest(map[string]any{"max_concurrency": 3}, `{"lines":[1,2,3,4,5,6]}`))
	if err != nil {
		t.Fatalf("for_each: %v", err)
	}
	var decoded map[string]any
	_ = json.Unmarshal(out, &decoded)
	results := decoded["results"].([]any)
	for i, r := range results {
		obj := r.(map[string]any)
		if obj["double"] != float64((i+1)*2) || obj["label"] != fmt.Sprintf("item%d", i) {
			t.Fatalf("results[%d] = %v", i, obj)
		}
	}
	if peak.Load() > 3 || peak.Load() < 2 {
		t.Fatalf("peak concurrency = %d, want 2..3", peak.Load())
	}
	if meta := decoded["_for_each"].(map[string]any); meta["total"] != 6.0 || meta["failed"] != 0.0 {
		t.Fatalf("_for_each = %v", meta)
	}
}

func TestForEachFailFastAndCollect(t *testing.T) {
	var calls atomic.Int32
	withCallHook(t, func(_ context.Context, _ int64, payload []byte, _ string) ([]byte, error) {
		calls.Add(1)
		if strings.Contains(string(payload), `"bad"`) {
			return nil, errors.New("child broke")
		}
		return payload, nil
	})
	payload := `{"lines":[{"v":"ok"},{"v":"bad"},{"v":"ok"},{"v":"ok"}]}`
	if _, err := ForEachAction(context.Background(), forEachRequest(nil, payload)); err == nil || !strings.Contains(err.Error(), "item 1") {
		t.Fatalf("fail fast err = %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("fail fast ran %d items, want 2 (sequential stop)", calls.Load())
	}
	out, err := ForEachAction(context.Background(), forEachRequest(map[string]any{"fail_fast": false, "output_field": "res"}, payload))
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	var decoded map[string]any
	_ = json.Unmarshal(out, &decoded)
	res := decoded["res"].([]any)
	if len(res) != 4 || res[1].(map[string]any)["_error"] == nil || res[0].(map[string]any)["v"] != "ok" {
		t.Fatalf("collected = %v", res)
	}
	if decoded["_for_each"].(map[string]any)["failed"] != 1.0 {
		t.Fatalf("_for_each = %v", decoded["_for_each"])
	}
}

func TestForEachLimitsAndStop(t *testing.T) {
	withCallHook(t, func(ctx context.Context, _ int64, payload []byte, _ string) ([]byte, error) {
		<-ctx.Done()
		return nil, context.Cause(ctx)
	})
	_, err := ForEachAction(context.Background(), forEachRequest(map[string]any{"max_items": 2}, `{"lines":[1,2,3]}`))
	if !errors.Is(err, workflowmodels.ErrWorkflowStepNonRetryable) {
		t.Fatalf("max_items err = %v", err)
	}
	if _, err := ForEachAction(context.Background(), forEachRequest(nil, `{"lines":"x"}`)); err == nil {
		t.Fatal("a non-array path must fail")
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	stopErr := errors.New("run cancelled")
	time.AfterFunc(10*time.Millisecond, func() { cancel(stopErr) })
	_, err = ForEachAction(ctx, forEachRequest(map[string]any{"fail_fast": false, "max_concurrency": 2}, `{"lines":[1,2,3,4]}`))
	if !errors.Is(err, stopErr) {
		t.Fatalf("stop err = %v", err)
	}
	issues := workflowmodels.ValidateStepParams("for_each", 1, nil, map[string]any{"items_path": "a", "target_workflow_id": 1, "max_concurrency": 9, "max_items": 0})
	if len(issues) != 2 {
		t.Fatalf("issues = %v", issues)
	}
}
