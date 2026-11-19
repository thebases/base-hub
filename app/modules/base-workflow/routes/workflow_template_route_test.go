package routes

import (
	"context"
	"net/http"
	"testing"

	internalmodels "git.thebase.vn/base_hub/internal/models"
	testutil "git.thebase.vn/base_hub/internal/test"
)

// A gallery template (plan T101) is never started by /wh, even when it is active.
func TestWorkflowRouteNeverRunsTemplate(t *testing.T) {
	setRPCTestConfig(t)
	restoreWorkflowRouteHooks(t)

	workflowRouteModuleEnabled = func(context.Context, string) (bool, error) { return true, nil }
	workflowRouteModelRegistered = func() bool { return true }
	workflowRouteLookupFlow = func(context.Context, string) (*internalmodels.FlowRecord, error) {
		return &internalmodels.FlowRecord{Name: "Demo", Topic: "demo", IsActive: true, IsTemplate: true}, nil
	}
	workflowRouteExecute = func(context.Context, string, []byte) error {
		t.Fatalf("a template must not execute")
		return nil
	}

	app := newWorkflowRouteTestApp()
	resp, err := app.Test(testutil.NewJSONRequest(t, http.MethodPost, "/wh/demo", `{"ok":true}`))
	if err != nil {
		t.Fatalf("workflow request: %v", err)
	}
	testutil.RequireStatus(t, resp, http.StatusNotFound)
}
