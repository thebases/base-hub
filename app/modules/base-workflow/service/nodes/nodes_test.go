package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

func TestCRUDModelFindOrCreateMergesCreatedIDIntoPayload(t *testing.T) {
	defer RegisterWorkflowActionRuntimeHooks(WorkflowActionRuntimeHooks{})
	RegisterWorkflowActionRuntimeHooks(WorkflowActionRuntimeHooks{
		SearchModelRecords: func(_ context.Context, model string, filter map[string]any, fields []string) ([]map[string]any, error) {
			if model != "tbl.partner" {
				t.Fatalf("model = %q, want tbl.partner", model)
			}
			if filter["ref"] != "BID-001" {
				t.Fatalf("filter ref = %#v, want BID-001", filter["ref"])
			}
			if len(fields) != 2 || fields[0] != "id" || fields[1] != "ref" {
				t.Fatalf("fields = %#v, want [id ref]", fields)
			}
			return nil, nil
		},
		CreateModelRecord: func(_ context.Context, model string, values map[string]any) (map[string]any, error) {
			if model != "tbl.partner" {
				t.Fatalf("create model = %q, want tbl.partner", model)
			}
			if values["name"] != "Acme" || values["ref"] != "BID-001" {
				t.Fatalf("values = %#v", values)
			}
			return map[string]any{"id": int64(42), "name": values["name"], "ref": values["ref"]}, nil
		},
		FlattenJSON: func(input map[string]any, output map[string]any) {
			for key, value := range input {
				output[key] = value
			}
		},
	})

	output, err := CRUDModel(context.Background(), WorkflowActionRequest{
		Step: workflowmodels.Step{Params: map[string]any{
			"model":      "tbl.partner",
			"operation":  "find_or_create",
			"filter":     map[string]any{"ref": "#bid"},
			"fields":     []string{"id", "ref"},
			"values":     map[string]any{"name": "#acc_holder_name", "ref": "#bid"},
			"result_key": "partner",
			"id_field":   "partner_id",
		}},
		Payload: []byte(`{"acc_holder_name":"Acme","bid":"BID-001"}`),
	})
	if err != nil {
		t.Fatalf("CRUDModel() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(output, &decoded); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if decoded["partner_id"] != float64(42) {
		t.Fatalf("partner_id = %#v, want 42", decoded["partner_id"])
	}
	partner, ok := decoded["partner"].(map[string]any)
	if !ok || partner["ref"] != "BID-001" {
		t.Fatalf("partner = %#v", decoded["partner"])
	}
}

func TestPostOutputObjectPersistsOutboundDeliveryViaCRUDModel(t *testing.T) {
	defer RegisterWorkflowActionRuntimeHooks(WorkflowActionRuntimeHooks{})

	var createdValues map[string]any
	var updatedIDs []string
	var updatedValues map[string]any

	RegisterWorkflowActionRuntimeHooks(WorkflowActionRuntimeHooks{
		CreateModelRecord: func(_ context.Context, model string, values map[string]any) (map[string]any, error) {
			if model != workflowmodels.OutboundDeliveryModelName {
				t.Fatalf("create model = %q, want %s", model, workflowmodels.OutboundDeliveryModelName)
			}
			createdValues = cloneJSONObject(values)
			return map[string]any{
				"id": int64(77),
			}, nil
		},
		WriteModelRecords: func(_ context.Context, model string, ids []string, values map[string]any) (bool, error) {
			if model != workflowmodels.OutboundDeliveryModelName {
				t.Fatalf("write model = %q, want %s", model, workflowmodels.OutboundDeliveryModelName)
			}
			updatedIDs = append([]string(nil), ids...)
			updatedValues = cloneJSONObject(values)
			return true, nil
		},
		PostHTTPJSON: func(_ context.Context, rawURL string, headers map[string]string, body map[string]any) (int, string, error) {
			if rawURL != "https://example.com/hook" {
				t.Fatalf("rawURL = %q, want https://example.com/hook", rawURL)
			}
			if headers["Content-Type"] != "application/json" {
				t.Fatalf("headers = %#v", headers)
			}
			if body["ok"] != true {
				t.Fatalf("body = %#v", body)
			}
			return 200, `{"delivered":true}`, nil
		},
		FlattenJSON: func(input map[string]any, output map[string]any) {
			for key, value := range input {
				output[key] = value
			}
		},
	})

	ctx := workflowmodels.ContextWithWorkflowRequestID(context.Background(), "f14df4a4-d175-4fb0-a328-4053743b34d8")
	output, err := PostOutputObject(ctx, WorkflowActionRequest{
		Topic: "ipn.test",
		Step: workflowmodels.Step{Params: map[string]any{
			"url": "https://example.com/hook",
		}},
		Payload: []byte(`{"ok":true}`),
	})
	if err != nil {
		t.Fatalf("PostOutputObject() error = %v", err)
	}
	if string(output) != `{"ok":true}` {
		t.Fatalf("output = %s, want original payload", string(output))
	}
	if createdValues["workflow_request_id"] != "f14df4a4-d175-4fb0-a328-4053743b34d8" {
		t.Fatalf("workflow_request_id = %#v", createdValues["workflow_request_id"])
	}
	createdName, _ := createdValues["name"].(string)
	if !strings.HasPrefix(createdName, "f14df4a4-d175-4fb0-a328-4053743b34d8-") {
		t.Fatalf("name = %#v, want workflow-request-id-prefixed unique name", createdValues["name"])
	}
	if createdValues["status"] != workflowmodels.OutboundDeliveryStatusQueued {
		t.Fatalf("create status = %#v, want queued", createdValues["status"])
	}
	requestBody, ok := createdValues["request_body"].(map[string]any)
	if !ok || requestBody["ok"] != true {
		t.Fatalf("request_body = %#v", createdValues["request_body"])
	}
	if len(updatedIDs) != 1 || updatedIDs[0] != "77" {
		t.Fatalf("updated ids = %#v, want [77]", updatedIDs)
	}
	if updatedValues["status"] != workflowmodels.OutboundDeliveryStatusDelivered {
		t.Fatalf("updated status = %#v, want delivered", updatedValues["status"])
	}
	if got := updatedValues["next_attempt_at"]; got != nil {
		t.Fatalf("next_attempt_at = %#v, want omitted for delivered row", got)
	}
	if updatedValues["response_body"] != `{"delivered":true}` {
		t.Fatalf("response_body = %#v, want response body", updatedValues["response_body"])
	}
}

func TestPostOutputObjectWritesRetryTimestampForRetryableFailures(t *testing.T) {
	defer RegisterWorkflowActionRuntimeHooks(WorkflowActionRuntimeHooks{})

	var updatedValues map[string]any
	RegisterWorkflowActionRuntimeHooks(WorkflowActionRuntimeHooks{
		CreateModelRecord: func(_ context.Context, model string, values map[string]any) (map[string]any, error) {
			if model != workflowmodels.OutboundDeliveryModelName {
				t.Fatalf("create model = %q, want %s", model, workflowmodels.OutboundDeliveryModelName)
			}
			return map[string]any{"id": int64(91)}, nil
		},
		WriteModelRecords: func(_ context.Context, model string, ids []string, values map[string]any) (bool, error) {
			if model != workflowmodels.OutboundDeliveryModelName {
				t.Fatalf("write model = %q, want %s", model, workflowmodels.OutboundDeliveryModelName)
			}
			if len(ids) != 1 || ids[0] != "91" {
				t.Fatalf("ids = %#v, want [91]", ids)
			}
			updatedValues = cloneJSONObject(values)
			return true, nil
		},
		PostHTTPJSON: func(_ context.Context, _ string, _ map[string]string, _ map[string]any) (int, string, error) {
			return 0, "", fmt.Errorf("dial timeout")
		},
		FlattenJSON: func(input map[string]any, output map[string]any) {
			for key, value := range input {
				output[key] = value
			}
		},
	})

	before := time.Now().UTC().Unix()
	_, err := PostOutputObject(context.Background(), WorkflowActionRequest{
		Topic: "ipn.test",
		Step: workflowmodels.Step{Params: map[string]any{
			"url": "https://example.com/hook",
		}},
		Payload: []byte(`{"ok":true}`),
	})
	if err == nil {
		t.Fatalf("PostOutputObject() error = nil, want retryable failure")
	}
	if updatedValues["status"] != workflowmodels.OutboundDeliveryStatusRetryableFailed {
		t.Fatalf("updated status = %#v, want retryable_failed", updatedValues["status"])
	}
	nextAttempt, ok := updatedValues["next_attempt_at"].(int64)
	if !ok || nextAttempt <= before {
		t.Fatalf("next_attempt_at = %#v, want future unix timestamp", updatedValues["next_attempt_at"])
	}
}
