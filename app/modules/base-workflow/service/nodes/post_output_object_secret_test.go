package nodes

// Plan T40: the post_output_object audit row stores the credential reference, not the resolved
// Authorization header, and is created as sudo so the outbound guard lets the engine set it.

import (
	"context"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

func TestPostOutputObjectStoresAuthorizationRef(t *testing.T) {
	defer RegisterWorkflowActionRuntimeHooks(WorkflowActionRuntimeHooks{})

	var createdValues map[string]any
	var createdSudo bool
	var sentAuthorization string
	RegisterWorkflowActionRuntimeHooks(WorkflowActionRuntimeHooks{
		CreateModelRecord: func(ctx context.Context, _ string, values map[string]any) (map[string]any, error) {
			createdValues = cloneJSONObject(values)
			env := orm.EnvironmentFromContext(ctx)
			createdSudo = env != nil && env.IsSudo()
			return map[string]any{"id": int64(5)}, nil
		},
		WriteModelRecords: func(context.Context, string, []string, map[string]any) (bool, error) { return true, nil },
		PostHTTPJSON: func(_ context.Context, _ string, headers map[string]string, _ map[string]any) (int, string, error) {
			sentAuthorization = headers["Authorization"]
			return 200, "", nil
		},
		FlattenJSON: func(input map[string]any, output map[string]any) {
			for key, value := range input {
				output[key] = value
			}
		},
	})

	rpcEnv := orm.NewEnvironment(nil, 7, map[string]any{orm.RBACContextAuthModeKey: "bearer"}, false)
	ctx := orm.ContextWithEnvironment(context.Background(), rpcEnv)
	_, err := PostOutputObject(ctx, WorkflowActionRequest{
		Topic: "t",
		Step: workflowmodels.Step{Params: map[string]any{
			"url":           "https://example.com/hook",
			"authorization": "Bearer resolved-secret",
		}},
		CredentialRefs: map[string]string{"authorization": "@syscred:partner"},
		Payload:        []byte(`{"ok":true}`),
	})
	if err != nil {
		t.Fatalf("post_output_object: %v", err)
	}
	if sentAuthorization != "Bearer resolved-secret" {
		t.Fatalf("request must carry the resolved header, got %q", sentAuthorization)
	}
	values, _ := createdValues["values"].(map[string]any)
	if values == nil {
		values = createdValues
	}
	headers, _ := values["headers"].(map[string]any)
	if _, ok := headers["Authorization"]; ok {
		t.Fatalf("stored headers keep the secret: %#v", headers)
	}
	if values["authorization_ref"] != "@syscred:partner" {
		t.Fatalf("authorization_ref = %#v", values["authorization_ref"])
	}
	if !createdSudo {
		t.Fatalf("row with a reference must be created through OutboundEngineContext (sudo)")
	}
}
