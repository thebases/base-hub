package routes

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/base"
	"git.thebase.vn/base_hub/internal/config"
	internalmodels "git.thebase.vn/base_hub/internal/models"
	"git.thebase.vn/base_hub/internal/orm"
	internalroutes "git.thebase.vn/base_hub/internal/routes"
	"git.thebase.vn/base_hub/internal/services/etlflow"
	testutil "git.thebase.vn/base_hub/internal/test"
	"git.thebase.vn/base_hub/modules/base-workflow/models"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

func TestWorkflowNodeSpecsRequiresAuthentication(t *testing.T) {
	setRPCTestConfig(t)
	restoreWorkflowRouteHooks(t)

	workflowRequireIdentity = func(fiber.Ctx, bool) (*internalroutes.RPCRequestIdentity, error) {
		return nil, fiber.NewError(fiber.StatusUnauthorized, "rpc authentication is required")
	}

	app := newWorkflowRouteTestApp()
	req := httptest.NewRequest(http.MethodGet, "/wf/node-specs", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("node specs request: %v", err)
	}
	testutil.RequireStatus(t, resp, http.StatusUnauthorized)
}

func TestWorkflowNodeSpecsReturnsBuiltins(t *testing.T) {
	setRPCTestConfig(t)
	restoreWorkflowRouteHooks(t)

	workflowRequireIdentity = func(fiber.Ctx, bool) (*internalroutes.RPCRequestIdentity, error) {
		return &internalroutes.RPCRequestIdentity{UserID: 7, DBName: "bean_ipn"}, nil
	}

	app := newWorkflowRouteTestApp()
	req := httptest.NewRequest(http.MethodGet, "/wf/node-specs", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("node specs request: %v", err)
	}
	testutil.RequireStatus(t, resp, http.StatusOK)

	body := testutil.ReadBody(t, resp)
	if !strings.Contains(body, `"type":"webhook"`) {
		t.Fatalf("expected webhook trigger in payload, got %s", body)
	}
	if !strings.Contains(body, `"type":"kafka"`) {
		t.Fatalf("expected kafka trigger in phase 2 payload, got %s", body)
	}
	if !strings.Contains(body, `"type":"mqtt"`) {
		t.Fatalf("expected mqtt trigger in phase 2 payload, got %s", body)
	}
	if !strings.Contains(body, `"type":"tcp"`) {
		t.Fatalf("expected tcp trigger in payload, got %s", body)
	}
	if !strings.Contains(body, `"code":"parse_payload"`) {
		t.Fatalf("expected parse_payload action in payload, got %s", body)
	}
	if strings.Contains(body, `"code":"save_to_db"`) {
		t.Fatalf("did not expect retired save_to_db action in payload, got %s", body)
	}
	if !strings.Contains(body, `"code":"aes_decrypt_action"`) {
		t.Fatalf("expected aes_decrypt_action action in payload, got %s", body)
	}
	if !strings.Contains(body, `"label":"Model Name","name":"model_name","required":true,"type":"combobox"`) {
		t.Fatalf("expected workflow model_name param to use combobox, got %s", body)
	}
	if !strings.Contains(body, `"code":"transform_data"`) || !strings.Contains(body, `"help":"Optional source model. When selected, the frontend can fetch its JSON template from /wf/sys-models."`) {
		t.Fatalf("expected transform_data model template help in payload, got %s", body)
	}
}

func TestWorkflowSysModelsRequiresAuthentication(t *testing.T) {
	setRPCTestConfig(t)
	restoreWorkflowRouteHooks(t)

	workflowRequireIdentity = func(fiber.Ctx, bool) (*internalroutes.RPCRequestIdentity, error) {
		return nil, fiber.NewError(fiber.StatusUnauthorized, "rpc authentication is required")
	}

	app := newWorkflowRouteTestApp()
	req := httptest.NewRequest(http.MethodGet, "/wf/sys-models", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("sys models request: %v", err)
	}
	testutil.RequireStatus(t, resp, http.StatusUnauthorized)
}

func TestWorkflowSysModelsReturnsOptions(t *testing.T) {
	setRPCTestConfig(t)
	restoreWorkflowRouteHooks(t)
	stubWorkflowRouteRole(t, nil)

	workflowRequireIdentity = func(fiber.Ctx, bool) (*internalroutes.RPCRequestIdentity, error) {
		return &internalroutes.RPCRequestIdentity{UserID: 7, DBName: "bean_ipn"}, nil
	}
	workflowRouteSearchSysModels = func(_ context.Context, userID int64, query string, limit int) ([]workflowSysModelSummary, error) {
		if userID != 7 {
			t.Fatalf("userID = %d, want 7", userID)
		}
		if query != "partner" {
			t.Fatalf("query = %q, want partner", query)
		}
		if limit != 5 {
			t.Fatalf("limit = %d, want 5", limit)
		}
		return []workflowSysModelSummary{
			{Name: "tbl.partner", Label: "Contacts"},
		}, nil
	}

	app := newWorkflowRouteTestApp()
	req := httptest.NewRequest(http.MethodGet, "/wf/sys-models?q=partner&limit=5", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("sys models request: %v", err)
	}
	testutil.RequireStatus(t, resp, http.StatusOK)

	body := testutil.ReadBody(t, resp)
	if !strings.Contains(body, `"name":"tbl.partner"`) || !strings.Contains(body, `"label":"Contacts"`) {
		t.Fatalf("expected sys model options in payload, got %s", body)
	}
}

func TestWorkflowSysModelsReturnsModelTemplate(t *testing.T) {
	setRPCTestConfig(t)
	restoreWorkflowRouteHooks(t)
	stubWorkflowRouteRole(t, nil)

	workflowRequireIdentity = func(fiber.Ctx, bool) (*internalroutes.RPCRequestIdentity, error) {
		return &internalroutes.RPCRequestIdentity{UserID: 7, DBName: "bean_ipn"}, nil
	}
	workflowRouteLookupSysModel = func(_ context.Context, userID int64, modelName string) (*workflowSysModelTemplate, error) {
		if userID != 7 {
			t.Fatalf("userID = %d, want 7", userID)
		}
		if modelName != "tbl.partner" {
			t.Fatalf("modelName = %q, want tbl.partner", modelName)
		}
		return &workflowSysModelTemplate{
			Name:  "tbl.partner",
			Label: "Contacts",
			JSONTemplate: map[string]any{
				"name":  "string",
				"email": "string",
			},
		}, nil
	}

	app := newWorkflowRouteTestApp()
	req := httptest.NewRequest(http.MethodGet, "/wf/sys-models?model_name=tbl.partner", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("sys model template request: %v", err)
	}
	testutil.RequireStatus(t, resp, http.StatusOK)

	body := testutil.ReadBody(t, resp)
	if !strings.Contains(body, `"json_template":{"email":"string","name":"string"}`) {
		t.Fatalf("expected sys model json template in payload, got %s", body)
	}
}

func TestWorkflowRouteReturnsNoWorkflowWhenModuleDisabled(t *testing.T) {
	setRPCTestConfig(t)
	restoreWorkflowRouteHooks(t)

	workflowRouteModuleEnabled = func(context.Context, string) (bool, error) { return false, nil }
	workflowRouteModelRegistered = func() bool { return true }

	app := newWorkflowRouteTestApp()
	req := testutil.NewJSONRequest(t, http.MethodPost, "/wh/demo.topic", `{"ok":true}`)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("workflow request: %v", err)
	}
	testutil.RequireStatus(t, resp, http.StatusNotFound)
}

func TestWorkflowRouteReturnsNoWorkflowWhenTopicMissing(t *testing.T) {
	setRPCTestConfig(t)
	restoreWorkflowRouteHooks(t)

	workflowRouteModuleEnabled = func(context.Context, string) (bool, error) { return true, nil }
	workflowRouteModelRegistered = func() bool { return true }
	workflowRouteLookupFlow = func(context.Context, string) (*internalmodels.FlowRecord, error) {
		return nil, nil
	}

	app := newWorkflowRouteTestApp()
	req := testutil.NewJSONRequest(t, http.MethodPost, "/wh", `{"ok":true}`)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("workflow request: %v", err)
	}
	testutil.RequireStatus(t, resp, http.StatusNotFound)
}

func TestWorkflowRouteReturnsNoWorkflowWhenFlowMissing(t *testing.T) {
	setRPCTestConfig(t)
	restoreWorkflowRouteHooks(t)

	workflowRouteModuleEnabled = func(context.Context, string) (bool, error) { return true, nil }
	workflowRouteModelRegistered = func() bool { return true }
	workflowRouteLookupFlow = func(context.Context, string) (*internalmodels.FlowRecord, error) {
		return nil, errors.New("pq: lookup failed on tbl_internal")
	}

	app := newWorkflowRouteTestApp()
	req := testutil.NewJSONRequest(t, http.MethodPost, "/wh/demo.topic", `{"ok":true}`)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("workflow request: %v", err)
	}
	// A lookup failure is a server error; its text must not reach the caller (T43).
	testutil.RequireStatus(t, resp, http.StatusServiceUnavailable)
	if body := testutil.ReadBody(t, resp); strings.Contains(body, "tbl_internal") {
		t.Fatalf("lookup error leaked: %s", body)
	}
}

func TestWorkflowRouteRejectsUnauthorizedGroup(t *testing.T) {
	setRPCTestConfig(t)
	restoreWorkflowRouteHooks(t)

	groupID := int64(88)
	workflowRouteModuleEnabled = func(context.Context, string) (bool, error) { return true, nil }
	workflowRouteModelRegistered = func() bool { return true }
	workflowRouteLookupFlow = func(context.Context, string) (*internalmodels.FlowRecord, error) {
		return &internalmodels.FlowRecord{Name: "Protected", Topic: "demo.topic", GroupID: &groupID}, nil
	}
	workflowRequireIdentity = func(fiber.Ctx, bool) (*internalroutes.RPCRequestIdentity, error) {
		return &internalroutes.RPCRequestIdentity{UserID: 7, GroupIDs: []int64{3, 4}, DBName: "bean_ipn"}, nil
	}

	app := newWorkflowRouteTestApp()
	req := testutil.NewJSONRequest(t, http.MethodPost, "/wh/demo.topic", `{"ok":true}`)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("workflow request: %v", err)
	}
	testutil.RequireStatus(t, resp, http.StatusForbidden)
}

func TestWorkflowRouteRejectsProtectedFlowWithoutAuthorization(t *testing.T) {
	setRPCTestConfig(t)
	restoreWorkflowRouteHooks(t)

	groupID := int64(88)
	workflowRouteModuleEnabled = func(context.Context, string) (bool, error) { return true, nil }
	workflowRouteModelRegistered = func() bool { return true }
	workflowRouteLookupFlow = func(context.Context, string) (*internalmodels.FlowRecord, error) {
		return &internalmodels.FlowRecord{Name: "Protected", Topic: "demo.topic", GroupID: &groupID}, nil
	}
	workflowRequireIdentity = func(fiber.Ctx, bool) (*internalroutes.RPCRequestIdentity, error) {
		return nil, fiber.NewError(fiber.StatusUnauthorized, "rpc authentication is required")
	}

	app := newWorkflowRouteTestApp()
	req := testutil.NewJSONRequest(t, http.MethodPost, "/wh/demo.topic", `{"ok":true}`)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("workflow request: %v", err)
	}
	testutil.RequireStatus(t, resp, http.StatusUnauthorized)
}

func TestWorkflowRouteExecutesPublicTopicWithoutAuthorization(t *testing.T) {
	setRPCTestConfig(t)
	restoreWorkflowRouteHooks(t)
	workflowContextForDB = func(ctx context.Context, dbName string) context.Context {
		return orm.ContextWithEnvironment(ctx, orm.NewEnvironmentWithDBName(nil, dbName, 0, nil, false))
	}

	var executedTopic string
	var executedBody string

	workflowRouteModuleEnabled = func(context.Context, string) (bool, error) { return true, nil }
	workflowRouteModelRegistered = func() bool { return true }
	workflowRouteLookupFlow = func(context.Context, string) (*internalmodels.FlowRecord, error) {
		return &internalmodels.FlowRecord{Name: "Public", Topic: "demo.topic"}, nil
	}
	workflowRouteCurrentIdentity = func(fiber.Ctx) (*internalroutes.RPCRequestIdentity, error) {
		return nil, nil
	}
	workflowRouteExecute = func(ctx context.Context, topic string, input []byte) error {
		executedTopic = topic
		executedBody = string(input)
		env := orm.EnvironmentFromContext(ctx)
		if env == nil || env.UserID() != 0 {
			return errors.New("expected anonymous environment")
		}
		requestID := models.WorkflowRequestIDFromContext(ctx)
		if _, err := uuid.Parse(requestID); err != nil {
			return err
		}
		return nil
	}

	app := newWorkflowRouteTestApp()
	req := testutil.NewJSONRequest(t, http.MethodPost, "/wh/demo.topic", `{"ok":true}`)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("workflow request: %v", err)
	}
	testutil.RequireStatus(t, resp, http.StatusOK)
	if executedTopic != "demo.topic" {
		t.Fatalf("executed topic = %q, want demo.topic", executedTopic)
	}
	if strings.TrimSpace(executedBody) != `{"ok":true}` {
		t.Fatalf("executed body = %q", executedBody)
	}
}

func TestWorkflowRouteReturnsNotFoundForUnknownTopic(t *testing.T) {
	setRPCTestConfig(t)
	restoreWorkflowRouteHooks(t)

	workflowRouteModuleEnabled = func(context.Context, string) (bool, error) { return true, nil }
	workflowRouteModelRegistered = func() bool { return true }
	workflowRouteLookupFlow = func(context.Context, string) (*internalmodels.FlowRecord, error) {
		return nil, fmt.Errorf("%w: topic=ghost", etlflow.ErrFlowNotFound)
	}

	app := newWorkflowRouteTestApp()
	req := testutil.NewJSONRequest(t, http.MethodPost, "/wh/ghost", `{"ok":true}`)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("workflow request: %v", err)
	}
	testutil.RequireStatus(t, resp, http.StatusNotFound)
}

func TestWorkflowRouteRefusesNonWebhookWorkflow(t *testing.T) {
	setRPCTestConfig(t)
	restoreWorkflowRouteHooks(t)

	executed := false
	var checkedWorkflowID int64
	workflowRouteModuleEnabled = func(context.Context, string) (bool, error) { return true, nil }
	workflowRouteModelRegistered = func() bool { return true }
	workflowRouteLookupFlow = func(context.Context, string) (*internalmodels.FlowRecord, error) {
		flow := &internalmodels.FlowRecord{Name: "MQTT only", Topic: "device.events"}
		flow.ID = 42
		return flow, nil
	}
	workflowRouteServesWebhook = func(_ context.Context, workflowID int64) (workflowWebhookSettings, error) {
		checkedWorkflowID = workflowID
		return workflowWebhookSettings{}, nil
	}
	workflowRouteCurrentIdentity = func(fiber.Ctx) (*internalroutes.RPCRequestIdentity, error) { return nil, nil }
	workflowRouteExecute = func(context.Context, string, []byte) error {
		executed = true
		return nil
	}

	app := newWorkflowRouteTestApp()
	req := testutil.NewJSONRequest(t, http.MethodPost, "/wh/device.events", `{"ok":true}`)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("workflow request: %v", err)
	}
	testutil.RequireStatus(t, resp, http.StatusNotFound)
	if executed {
		t.Fatal("non-webhook workflow must not execute over /wh")
	}
	if checkedWorkflowID != 42 {
		t.Fatalf("trigger check workflow id = %d, want 42", checkedWorkflowID)
	}
}

// The request-DB allowlist itself is the bindRequestDatabase middleware (internal/cmd, T12),
// which this route-only app does not mount; here the route must still run nothing for a DB
// it cannot open.
func TestWorkflowRouteDoesNotRunOnUnknownDatabase(t *testing.T) {
	setRPCTestConfig(t)
	restoreWorkflowRouteHooks(t)

	executed := false
	workflowRouteExecute = func(context.Context, string, []byte) error {
		executed = true
		return nil
	}

	app := newWorkflowRouteTestApp()
	req := testutil.NewJSONRequest(t, http.MethodPost, "/wh/demo.topic", `{"ok":true}`)
	req.Header.Set(internalroutes.RPCHeaderDatabaseName, "ghost_db")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("workflow request: %v", err)
	}
	testutil.RequireStatus(t, resp, http.StatusNotFound)
	if executed {
		t.Fatal("workflow must not run on an unknown database")
	}
}

func newWorkflowRouteTestApp() *fiber.App {
	app := fiber.New(fiber.Config{JSONEncoder: base.JSONMarshal})
	RegisterWorkflowRoutes(app)
	return app
}

func restoreWorkflowRouteHooks(t *testing.T) {
	t.Helper()
	originalModuleEnabled := workflowRouteModuleEnabled
	originalLookupFlow := workflowRouteLookupFlow
	originalServesWebhook := workflowRouteServesWebhook
	originalExecute := workflowRouteExecute
	originalResolveDBName := workflowResolveDBName
	originalContextForDB := workflowContextForDB
	originalCurrentIdentity := workflowRouteCurrentIdentity
	originalRequireIdentity := workflowRequireIdentity
	originalModelRegistered := workflowRouteModelRegistered
	originalBuildNodeSpecs := workflowRouteBuildNodeSpecs
	originalSearchSysModels := workflowRouteSearchSysModels
	originalLookupSysModel := workflowRouteLookupSysModel
	originalSearchRead := workflowRouteSearchRead
	// Default: every looked-up flow is a webhook workflow; tests override it to cover the gate.
	workflowRouteServesWebhook = func(context.Context, int64) (workflowWebhookSettings, error) {
		return workflowWebhookSettings{Serves: true}, nil
	}
	originalResolveSecret := workflowRouteResolveWebhookSecret
	originalRunExists := workflowRouteRunExists
	originalRunIDByName := workflowRouteRunIDByName
	originalNow := webhookNow
	workflowRouteRunExists = func(context.Context, string) (bool, error) { return false, nil }
	workflowRouteRunIDByName = func(context.Context, string) (any, error) { return int64(77), nil }
	t.Cleanup(func() {
		workflowRouteModuleEnabled = originalModuleEnabled
		workflowRouteLookupFlow = originalLookupFlow
		workflowRouteServesWebhook = originalServesWebhook
		workflowRouteExecute = originalExecute
		workflowResolveDBName = originalResolveDBName
		workflowContextForDB = originalContextForDB
		workflowRouteCurrentIdentity = originalCurrentIdentity
		workflowRequireIdentity = originalRequireIdentity
		workflowRouteModelRegistered = originalModelRegistered
		workflowRouteBuildNodeSpecs = originalBuildNodeSpecs
		workflowRouteSearchSysModels = originalSearchSysModels
		workflowRouteLookupSysModel = originalLookupSysModel
		workflowRouteSearchRead = originalSearchRead
		workflowRouteResolveWebhookSecret = originalResolveSecret
		workflowRouteRunExists = originalRunExists
		workflowRouteRunIDByName = originalRunIDByName
		webhookNow = originalNow
	})
}

func setRPCTestConfig(t *testing.T) {
	t.Helper()
	testutil.RestoreConfig(t, func(cfg *config.AppConfig) {
		// bean_ipn is the logical home DB these tests register and log into;
		// db_name no longer exists as a config key, so the home selector must
		// name it explicitly.
		cfg.PostgresqlDb.HomeDBName = "bean_ipn"
		cfg.PostgresqlDb.Password = "db-password"
	})
}

// T48: a step failure with attempts left is not a failed delivery; the run is stored and the
// resume cron retries it, so /wh answers 202 retry_scheduled.
func TestWorkflowRouteAcceptsScheduledRetry(t *testing.T) {
	setRPCTestConfig(t)
	restoreWorkflowRouteHooks(t)

	workflowRouteModuleEnabled = func(context.Context, string) (bool, error) { return true, nil }
	workflowRouteModelRegistered = func() bool { return true }
	workflowRouteLookupFlow = func(context.Context, string) (*internalmodels.FlowRecord, error) {
		flow := &internalmodels.FlowRecord{Name: "Retry", Topic: "retry.topic"}
		flow.ID = 43
		return flow, nil
	}
	workflowRouteServesWebhook = func(context.Context, int64) (workflowWebhookSettings, error) {
		return workflowWebhookSettings{Serves: true}, nil
	}
	workflowRouteCurrentIdentity = func(fiber.Ctx) (*internalroutes.RPCRequestIdentity, error) { return nil, nil }
	workflowRouteExecute = func(context.Context, string, []byte) error {
		return fmt.Errorf("%w (run 1, step s, next attempt at 0): boom", etlflow.ErrRunRetryScheduled)
	}

	app := newWorkflowRouteTestApp()
	resp, err := app.Test(testutil.NewJSONRequest(t, http.MethodPost, "/wh/retry.topic", `{"ok":true}`))
	if err != nil {
		t.Fatalf("workflow request: %v", err)
	}
	testutil.RequireStatus(t, resp, http.StatusAccepted)
}

// T103: a run parked at wait_approval is accepted: 202 with its run id and the resume token.
func TestWorkflowRouteAcceptsApprovalWait(t *testing.T) {
	setRPCTestConfig(t)
	restoreWorkflowRouteHooks(t)
	workflowRouteModuleEnabled = func(context.Context, string) (bool, error) { return true, nil }
	workflowRouteModelRegistered = func() bool { return true }
	workflowRouteLookupFlow = func(context.Context, string) (*internalmodels.FlowRecord, error) {
		flow := &internalmodels.FlowRecord{Name: "Approve", Topic: "approve.topic"}
		flow.ID = 45
		return flow, nil
	}
	workflowRouteServesWebhook = func(context.Context, int64) (workflowWebhookSettings, error) {
		return workflowWebhookSettings{Serves: true}, nil
	}
	workflowRouteCurrentIdentity = func(fiber.Ctx) (*internalroutes.RPCRequestIdentity, error) { return nil, nil }
	workflowRouteExecute = func(context.Context, string, []byte) error {
		pending := &models.WorkflowApprovalPending{RunID: 91, StepKey: "wait", Token: "tok", ExpiresAt: 123}
		// The engine's error also matches ErrRunRetryScheduled; the approval answer must win.
		return fmt.Errorf("%w: %w", etlflow.ErrRunRetryScheduled, pending)
	}
	resp, err := newWorkflowRouteTestApp().Test(testutil.NewJSONRequest(t, http.MethodPost, "/wh/approve.topic", `{"ok":true}`))
	if err != nil {
		t.Fatalf("workflow request: %v", err)
	}
	testutil.RequireStatus(t, resp, http.StatusAccepted)
	body := testutil.ReadBody(t, resp)
	for _, want := range []string{`"status":"waiting_approval"`, `"run_id":91`, `"resume_token":"tok"`, `"expires_at":123`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %s, want %s", body, want)
		}
	}
}

// T95/T100: /wh answers carry the run id; a workflow at max_concurrent_runs answers 409.
func TestWorkflowRouteRunIDAndConcurrencyConflict(t *testing.T) {
	setRPCTestConfig(t)
	restoreWorkflowRouteHooks(t)
	workflowRouteModuleEnabled = func(context.Context, string) (bool, error) { return true, nil }
	workflowRouteModelRegistered = func() bool { return true }
	workflowRouteLookupFlow = func(context.Context, string) (*internalmodels.FlowRecord, error) {
		flow := &internalmodels.FlowRecord{Name: "Limited", Topic: "limited.topic"}
		flow.ID = 44
		return flow, nil
	}
	workflowRouteCurrentIdentity = func(fiber.Ctx) (*internalroutes.RPCRequestIdentity, error) { return nil, nil }
	post := func() *http.Response {
		resp, err := newWorkflowRouteTestApp().Test(testutil.NewJSONRequest(t, http.MethodPost, "/wh/limited.topic", `{"ok":true}`))
		if err != nil {
			t.Fatalf("workflow request: %v", err)
		}
		return resp
	}

	workflowRouteExecute = func(context.Context, string, []byte) error { return nil }
	resp := post()
	testutil.RequireStatus(t, resp, http.StatusOK)
	if body := testutil.ReadBody(t, resp); !strings.Contains(body, `"run_id":77`) {
		t.Fatalf("ok body = %s, want run_id", body)
	}

	workflowRouteExecute = func(context.Context, string, []byte) error {
		return fmt.Errorf("refused: %w", models.ErrWorkflowRunConcurrencyLimit)
	}
	testutil.RequireStatus(t, post(), http.StatusConflict)
}
