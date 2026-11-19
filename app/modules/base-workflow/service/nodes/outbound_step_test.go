package nodes

// Plans T53/T56: post_output_object fails on non-2xx, reuses the step's outbound row across
// attempts, and outbound nodes send the step idempotency key; lua_script exposes it.

import (
	"context"
	"strconv"
	"strings"
	"testing"

	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

func postHooks(t *testing.T, status int, body string, existing map[string]any, created, written *[]map[string]any, sentHeaders *map[string]string) {
	t.Helper()
	RegisterWorkflowActionRuntimeHooks(WorkflowActionRuntimeHooks{
		SearchModelRecords: func(_ context.Context, _ string, filter map[string]any, _ []string) ([]map[string]any, error) {
			if existing == nil {
				return nil, nil
			}
			return []map[string]any{existing}, nil
		},
		CreateModelRecord: func(_ context.Context, _ string, values map[string]any) (map[string]any, error) {
			*created = append(*created, cloneJSONObject(values))
			return map[string]any{"id": int64(31)}, nil
		},
		WriteModelRecords: func(_ context.Context, _ string, _ []string, values map[string]any) (bool, error) {
			*written = append(*written, cloneJSONObject(values))
			return true, nil
		},
		PostHTTPJSON: func(_ context.Context, _ string, headers map[string]string, _ map[string]any) (int, string, error) {
			*sentHeaders = headers
			return status, body, nil
		},
		FlattenJSON: func(input map[string]any, output map[string]any) {
			for key, value := range input {
				output[key] = value
			}
		},
	})
	t.Cleanup(func() { RegisterWorkflowActionRuntimeHooks(WorkflowActionRuntimeHooks{}) })
}

func postRequest() WorkflowActionRequest {
	return WorkflowActionRequest{
		Topic:   "t",
		Step:    workflowmodels.Step{Key: "post_partner", Params: map[string]any{"url": "https://example.com/hook"}},
		Payload: []byte(`{"ok":true}`),
	}
}

func TestPostOutputObjectFailsOnNon2xx(t *testing.T) {
	cases := []struct {
		status     int
		wantStatus string
	}{
		{500, workflowmodels.OutboundDeliveryStatusRetryableFailed},
		{404, workflowmodels.OutboundDeliveryStatusPermanentFailed},
		{302, workflowmodels.OutboundDeliveryStatusRetryableFailed},
	}
	for _, tc := range cases {
		var created, written []map[string]any
		var headers map[string]string
		postHooks(t, tc.status, strings.Repeat("e", 2000), nil, &created, &written, &headers)
		_, err := PostOutputObject(context.Background(), postRequest())
		if err == nil {
			t.Fatalf("status %d must fail the step", tc.status)
		}
		if !strings.Contains(err.Error(), "http status "+strconv.Itoa(tc.status)) || len(err.Error()) > postOutputErrorBodyLimit+100 {
			t.Fatalf("status %d: error %q must name the status and truncate the body", tc.status, err.Error())
		}
		last := written[len(written)-1]
		if last["status"] != tc.wantStatus {
			t.Fatalf("status %d: row status %v, want %s", tc.status, last["status"], tc.wantStatus)
		}
	}
}

// A retry of the step reuses its row (no second create on the unique name) and sends the same
// Idempotency-Key.
func TestPostOutputObjectReusesStepRow(t *testing.T) {
	ctx := workflowmodels.ContextWithWorkflowRequestID(context.Background(), "run-9")
	var created, written []map[string]any
	var headers map[string]string

	postHooks(t, 200, "ok", nil, &created, &written, &headers)
	if _, err := PostOutputObject(ctx, postRequest()); err != nil {
		t.Fatalf("first attempt: %v", err)
	}
	if len(created) != 1 || created[0]["name"] != "run-9:post_partner" || created[0]["delivery_type"] != workflowmodels.OutboundDeliveryTypeHTTPJSONSync {
		t.Fatalf("created %#v, want one row named after the step key", created)
	}
	if headers["Idempotency-Key"] != "run-9:post_partner" {
		t.Fatalf("headers %#v, want the step key as Idempotency-Key", headers)
	}

	created, written = nil, nil
	postHooks(t, 200, "ok", map[string]any{"id": int64(31), "attempt_count": float64(1)}, &created, &written, &headers)
	if _, err := PostOutputObject(ctx, postRequest()); err != nil {
		t.Fatalf("second attempt: %v", err)
	}
	if len(created) != 0 {
		t.Fatalf("a retry must reuse the row, created %#v", created)
	}
	if written[0]["attempt_count"] != int64(2) || written[0]["max_attempts"] != int64(2) {
		t.Fatalf("reuse write %#v, want attempt 2 (never claimable by the sweeper)", written[0])
	}
	if headers["Idempotency-Key"] != "run-9:post_partner" {
		t.Fatalf("retry headers %#v, want the same key", headers)
	}
}

func TestIdempotencyKeyHeaderKeepsAuthorValue(t *testing.T) {
	headers := withIdempotencyKeyHeader(map[string]string{"idempotency-key": "mine"}, "run:step")
	if len(headers) != 1 || headers["idempotency-key"] != "mine" {
		t.Fatalf("headers = %#v, the author's header wins", headers)
	}
	ctx := workflowmodels.ContextWithWorkflowStepIdempotencyKey(context.Background(), "engine:key")
	if got := stepIdempotencyKey(ctx, postRequest()); got != "engine:key" {
		t.Fatalf("key = %q, the engine's key wins", got)
	}
}

func TestNotifyPassesStepKey(t *testing.T) {
	var gotKey string
	var gotHeaders map[string]string
	RegisterWorkflowActionRuntimeHooks(WorkflowActionRuntimeHooks{
		QueueHTTPJSON: func(ctx context.Context, _ string, _ string, headers map[string]string, _ string, _ map[string]any) error {
			gotKey = workflowmodels.WorkflowStepIdempotencyKeyFromContext(ctx)
			gotHeaders = headers
			return nil
		},
	})
	t.Cleanup(func() { RegisterWorkflowActionRuntimeHooks(WorkflowActionRuntimeHooks{}) })
	ctx := workflowmodels.ContextWithWorkflowRequestID(context.Background(), "run-3")
	req := postRequest()
	req.Step.Key = "notify_a"
	if _, err := Notify(ctx, req); err != nil {
		t.Fatal(err)
	}
	if gotKey != "run-3:notify_a" || gotHeaders["Idempotency-Key"] != "run-3:notify_a" {
		t.Fatalf("key=%q headers=%#v", gotKey, gotHeaders)
	}
}

func TestLuaScriptGetsIdempotencyKey(t *testing.T) {
	var globals map[string]any
	RegisterWorkflowActionRuntimeHooks(WorkflowActionRuntimeHooks{
		RunLuaAction: func(_ context.Context, _ int64, _ map[string]any, g map[string]any) (map[string]any, error) {
			globals = g
			return map[string]any{}, nil
		},
	})
	t.Cleanup(func() { RegisterWorkflowActionRuntimeHooks(WorkflowActionRuntimeHooks{}) })
	ctx := workflowmodels.ContextWithWorkflowStepIdempotencyKey(context.Background(), "run-4:lua")
	_, err := LuaScriptAction(ctx, WorkflowActionRequest{Step: workflowmodels.Step{Key: "lua", Params: map[string]any{"automation_action_id": 5}}, Payload: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if globals["idempotency_key"] != "run-4:lua" {
		t.Fatalf("globals = %#v", globals)
	}
}
