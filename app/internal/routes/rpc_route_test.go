package routes

// File overview:
// - Responsibility: verifies HTTP and RPC transport behavior for rpc route.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down request parsing and auth/response contracts so frontend and integration clients keep seeing stable transport behavior.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"git.thebase.vn/base_hub/internal/base"
	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/models"
	"git.thebase.vn/base_hub/internal/orm"
	testutil "git.thebase.vn/base_hub/internal/test"
	runtest "git.thebase.vn/base_hub/internal/test/runtime"
	baseauth "git.thebase.vn/base_hub/modules/base-auth/service"
	"github.com/gofiber/fiber/v3"
	"github.com/uptrace/bun"
)

type rpcRouteTestDocument struct {
	orm.BaseModel
	Name   string `bun:"name" json:"name"`
	Active bool   `bun:"active" json:"active"`
}

type rpcRouteShapeTestDocument struct {
	orm.BaseModel
	Name   string `bun:"name" json:"name"`
	Active bool   `bun:"active" json:"active"`
}

// rpcRouteAccessCheckDocument is a distinct Go type on purpose: the model
// registry rejects registering one Go type under two model names, so the
// access-check test cannot reuse rpcRouteShapeTestDocument when both tests
// run in the same process.
type rpcRouteAccessCheckDocument struct {
	orm.BaseModel
	Name   string `bun:"name" json:"name"`
	Active bool   `bun:"active" json:"active"`
}

type rpcRouteFalseToNullDocument struct {
	orm.BaseModel
	Name   string `bun:"name" json:"name"`
	Active bool   `bun:"active" json:"active"`
}

type rpcRouteUIViewTargetDocument struct {
	orm.BaseModel
	Name   string `bun:"name" json:"name"`
	Email  string `bun:"email" json:"email"`
	Active bool   `bun:"active" json:"active"`
}

type rpcRouteLegacyArgDocument struct {
	orm.BaseModel
	Name string `bun:"name" json:"name"`
}

type rpcRouteAliasDocument struct {
	orm.BaseModel
	Name string `bun:"name" json:"name"`
}

type rpcRouteRESTAliasDocument struct {
	orm.BaseModel
	Name string `bun:"name" json:"name"`
}

type rpcRouteNullableDocument struct {
	orm.BaseModel
	Name *string `bun:"name" json:"name"`
}

// TestRPCRoot only covers GET /rpc: session-info (GET /oauth/me) now lives in
// modules/base-auth/routes, covered by that package's own tests.
func TestRPCRoot(t *testing.T) {
	setRPCTestConfig(t)

	app := newRPCTestApp()

	rootReq := testutil.NewJSONRequest(t, http.MethodGet, "/rpc", "")
	rootResp, err := app.Test(rootReq)
	if err != nil {
		t.Fatalf("root request: %v", err)
	}
	testutil.RequireStatus(t, rootResp, http.StatusOK)
}

func TestRPCModelRoutesNormalizeLegacyArgsAndPathIDs(t *testing.T) {
	setRPCTestConfig(t)

	const modelName = "test.rpc_route_model"
	runtest.RegisterModelType[rpcRouteTestDocument](t, orm.ModelSpec{
		Name:       modelName,
		Label:      "RPC Route Test Model",
		Definition: orm.ModelDefinition(modelName, (*rpcRouteTestDocument)(nil)),
	}.WithStorageName("test_rpc_route_model"))

	executor := &runtest.ExecutorStub{
		ReadResult:       []orm.RecordMap{{"id": int64(5), "name": "Five", "active": true}},
		SearchReadResult: []orm.RecordMap{{"id": int64(5), "name": "Five", "active": true}},
		CreateResult:     orm.RecordMap{"id": int64(6), "name": "Created"},
		WriteResult:      true,
		UnlinkResult:     true,
	}
	orm.RegisterRuntimeExecutor(modelName, executor)

	app := newRPCTestApp()

	createReq := testutil.NewJSONRequestWithBearer(t, http.MethodPost, "/rpc/"+modelName+"/create", `{"args":[[],{"name":"Created"}],"kwargs":{}}`, rpcTestBearerToken(t))
	createResp, err := app.Test(createReq)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	testutil.RequireStatus(t, createResp, http.StatusOK)
	if got := executor.LastCreateValues["name"]; got != "Created" {
		t.Fatalf("expected stripped create values, got %#v", executor.LastCreateValues)
	}

	readReq := testutil.NewJSONRequestWithBearer(t, http.MethodPost, "/rpc/"+modelName+"/read/5", `{"args":[],"kwargs":{"fields":["id","name"]}}`, rpcTestBearerToken(t))
	readResp, err := app.Test(readReq)
	if err != nil {
		t.Fatalf("read request: %v", err)
	}
	testutil.RequireStatus(t, readResp, http.StatusOK)

	writeReq := testutil.NewJSONRequestWithBearer(t, http.MethodPost, "/rpc/"+modelName+"/write/5,6", `{"args":[{"active":false}],"kwargs":{}}`, rpcTestBearerToken(t))
	writeResp, err := app.Test(writeReq)
	if err != nil {
		t.Fatalf("write request: %v", err)
	}
	testutil.RequireStatus(t, writeResp, http.StatusOK)
	if len(executor.LastWriteIDs) != 2 || executor.LastWriteIDs[0] != "5" || executor.LastWriteIDs[1] != "6" {
		t.Fatalf("unexpected write ids: %#v", executor.LastWriteIDs)
	}

	searchReq := testutil.NewJSONRequestWithBearer(t, http.MethodPost, "/rpc/"+modelName+"/search_read", `{"args":[[["name","=","Five"]]],"kwargs":{"fields":["id","name"],"limit":10}}`, rpcTestBearerToken(t))
	searchResp, err := app.Test(searchReq)
	if err != nil {
		t.Fatalf("search_read request: %v", err)
	}
	testutil.RequireStatus(t, searchResp, http.StatusOK)
}

func TestRPCModelRoutesConvertNonBooleanFalseInputsToNull(t *testing.T) {
	setRPCTestConfig(t)

	const modelName = "test.rpc_route_false_to_null"
	runtest.RegisterModelType[rpcRouteFalseToNullDocument](t, orm.ModelSpec{
		Name:       modelName,
		Label:      "RPC Route False To Null Model",
		Definition: orm.ModelDefinition(modelName, (*rpcRouteFalseToNullDocument)(nil)),
	}.WithStorageName("test_rpc_route_false_to_null"))

	executor := &runtest.ExecutorStub{
		CreateResult: orm.RecordMap{"id": int64(8)},
		WriteResult:  true,
	}
	orm.RegisterRuntimeExecutor(modelName, executor)

	app := newRPCTestApp()

	createReq := testutil.NewJSONRequestWithBearer(t, http.MethodPost, "/rpc/"+modelName+"/create", `{"args":[[],{"name":false,"active":false}],"kwargs":{}}`, rpcTestBearerToken(t))
	createResp, err := app.Test(createReq)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	testutil.RequireStatus(t, createResp, http.StatusOK)
	if got := executor.LastCreateValues["name"]; got != nil {
		t.Fatalf("create name = %#v, want nil", got)
	}
	if got := executor.LastCreateValues["active"]; got != false {
		t.Fatalf("create active = %#v, want false", got)
	}

	writeReq := testutil.NewJSONRequestWithBearer(t, http.MethodPost, "/rpc/"+modelName+"/write/8", `{"args":[{"name":false,"active":false}],"kwargs":{}}`, rpcTestBearerToken(t))
	writeResp, err := app.Test(writeReq)
	if err != nil {
		t.Fatalf("write request: %v", err)
	}
	testutil.RequireStatus(t, writeResp, http.StatusOK)
	if got := executor.LastWriteValues["name"]; got != nil {
		t.Fatalf("write name = %#v, want nil", got)
	}
	if got := executor.LastWriteValues["active"]; got != false {
		t.Fatalf("write active = %#v, want false", got)
	}
}

func TestWithFrontendRPCContextAddsSourceAndMethod(t *testing.T) {
	kwargs := withFrontendRPCContext(map[string]any{
		"context": map[string]any{
			"lang": "en_US",
		},
	}, "create")

	contextValues, _ := kwargs["context"].(map[string]any)
	if got := contextValues["rpc_source"]; got != "frontend" {
		t.Fatalf("rpc_source = %#v, want %q", got, "frontend")
	}
	if got := contextValues["rpc_method"]; got != "create" {
		t.Fatalf("rpc_method = %#v, want %q", got, "create")
	}
	if got := contextValues["lang"]; got != "en_US" {
		t.Fatalf("lang = %#v, want %q", got, "en_US")
	}
}

func TestRPCModelRoutesRejectWrongPathShape(t *testing.T) {
	setRPCTestConfig(t)

	const modelName = "test.rpc_route_shape_model"
	runtest.RegisterModelType[rpcRouteShapeTestDocument](t, orm.ModelSpec{
		Name:       modelName,
		Label:      "RPC Route Shape Model",
		Definition: orm.ModelDefinition(modelName, (*rpcRouteShapeTestDocument)(nil)),
	}.WithStorageName("test_rpc_route_shape_model"))
	orm.RegisterRuntimeExecutor(modelName, &runtest.ExecutorStub{})

	app := newRPCTestApp()

	readReq := testutil.NewJSONRequestWithBearer(t, http.MethodPost, "/rpc/"+modelName+"/read", `{"args":[["5"]],"kwargs":{}}`, rpcTestBearerToken(t))
	readResp, err := app.Test(readReq)
	if err != nil {
		t.Fatalf("read request: %v", err)
	}
	testutil.RequireStatus(t, readResp, http.StatusBadRequest)

	createReq := testutil.NewJSONRequestWithBearer(t, http.MethodPost, "/rpc/"+modelName+"/create/5", `{"args":[{"name":"bad"}],"kwargs":{}}`, rpcTestBearerToken(t))
	createResp, err := app.Test(createReq)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	testutil.RequireStatus(t, createResp, http.StatusBadRequest)
}

func TestRPCModelRoutesAllowZeroPathIDForAccessChecks(t *testing.T) {
	setRPCTestConfig(t)

	const modelName = "test.rpc_route_access_check_model"
	runtest.RegisterModelType[rpcRouteAccessCheckDocument](t, orm.ModelSpec{
		Name:       modelName,
		Label:      "RPC Route Access Check Model",
		Definition: orm.ModelDefinition(modelName, (*rpcRouteAccessCheckDocument)(nil)),
	}.WithStorageName("test_rpc_route_access_check_model"))

	app := newRPCTestApp()

	req := testutil.NewJSONRequestWithBearer(
		t,
		http.MethodPost,
		"/rpc/"+modelName+"/check_access_rights/0",
		`{"args":["unlink"],"kwargs":{"raise_exception":false}}`,
		rpcTestBearerToken(t),
	)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("check_access_rights request: %v", err)
	}
	testutil.RequireStatus(t, resp, http.StatusOK)
}

func TestRPCModelRoutesKeepNonEmptyLegacyArgsForNonRecordMethod(t *testing.T) {
	setRPCTestConfig(t)

	const modelName = "test.rpc_route_legacy_args"
	runtest.RegisterModelType[rpcRouteLegacyArgDocument](t, orm.ModelSpec{
		Name:       modelName,
		Label:      "RPC Route Legacy Args",
		Definition: orm.ModelDefinition(modelName, (*rpcRouteLegacyArgDocument)(nil)),
		RuntimeMethods: []orm.RuntimeMethod{
			{
				Name:      "get_combined_arch",
				Public:    true,
				ModelOnly: true,
				ReadOnly:  true,
				Call: func(_ context.Context, _ *orm.ModelMetadata, _ []string, args []any, _ map[string]any) (any, error) {
					if len(args) != 1 {
						return nil, fiber.NewError(http.StatusBadRequest, "expected positional args to remain intact")
					}
					list, ok := args[0].([]any)
					if !ok || len(list) != 1 {
						return nil, fiber.NewError(http.StatusBadRequest, "expected one legacy id list argument")
					}
					return list[0], nil
				},
			},
		},
	}.WithStorageName("test_rpc_route_legacy_args"))

	app := newRPCTestApp()

	req := testutil.NewJSONRequestWithBearer(t, http.MethodPost, "/rpc/"+modelName+"/get_combined_arch", `{"args":[[115]],"kwargs":{}}`, rpcTestBearerToken(t))
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("get_combined_arch request: %v", err)
	}
	testutil.RequireStatus(t, resp, http.StatusOK)

	payload := testutil.DecodeJSON[map[string]any](t, resp.Body)
	if got := payload["result"]; got != float64(115) {
		t.Fatalf("result = %#v, want 115", got)
	}
}

func TestRPCModelRoutesMountAliasRoute(t *testing.T) {
	setRPCTestConfig(t)

	const modelName = "test.rpc_route_alias"
	runtest.RegisterModelType[rpcRouteAliasDocument](t, orm.ModelSpec{
		Name:       modelName,
		Label:      "RPC Route Alias",
		Definition: orm.ModelDefinition(modelName, (*rpcRouteAliasDocument)(nil)),
		RuntimeMethods: []orm.RuntimeMethod{
			{
				Name:       "get_combined_arch",
				Public:     true,
				ModelOnly:  true,
				ReadOnly:   true,
				AliasRoute: "/rpc-alias/combined-arch/",
				Call: func(_ context.Context, _ *orm.ModelMetadata, _ []string, args []any, _ map[string]any) (any, error) {
					return "alias-ok", nil
				},
			},
		},
	}.WithStorageName("test_rpc_route_alias"))

	// Registering after the model exists mirrors real startup order: modules
	// register models via init(), then RegisterRPCRoutes walks the registry.
	app := newRPCTestApp()

	req := testutil.NewJSONRequestWithBearer(t, http.MethodPost, "/base/rpc-alias/combined-arch", `{"args":[],"kwargs":{}}`, rpcTestBearerToken(t))
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("alias route request: %v", err)
	}
	testutil.RequireStatus(t, resp, http.StatusOK)

	payload := testutil.DecodeJSON[map[string]any](t, resp.Body)
	if got := payload["result"]; got != "alias-ok" {
		t.Fatalf("result = %#v, want %q", got, "alias-ok")
	}

	// The canonical /rpc path must keep working alongside the alias.
	canonicalReq := testutil.NewJSONRequestWithBearer(t, http.MethodPost, "/rpc/"+modelName+"/get_combined_arch", `{"args":[],"kwargs":{}}`, rpcTestBearerToken(t))
	canonicalResp, err := app.Test(canonicalReq)
	if err != nil {
		t.Fatalf("canonical route request: %v", err)
	}
	testutil.RequireStatus(t, canonicalResp, http.StatusOK)
}

// TestRPCAnonymousAliasRoutePassesRawRequest covers RuntimeMethod.AliasAnonymous: no bearer token,
// a raw text/plain body reaches the method as *orm.AliasRequest (credentials stripped, lowercase
// header keys), and an orm.AliasResponse is written verbatim with an any-origin CORS header.
func TestRPCAnonymousAliasRoutePassesRawRequest(t *testing.T) {
	setRPCTestConfig(t)

	const modelName = "test.rpc_route_anonymous_alias"
	var got *orm.AliasRequest
	runtest.RegisterModelType[rpcRouteAliasDocument](t, orm.ModelSpec{
		Name:       modelName,
		Label:      "RPC Route Anonymous Alias",
		Definition: orm.ModelDefinition(modelName, (*rpcRouteAliasDocument)(nil)),
		RuntimeMethods: []orm.RuntimeMethod{
			{
				Name:           "ingest",
				ModelOnly:      true,
				AliasRoute:     "rpc-anonymous/event",
				AliasAnonymous: true,
				Call: func(_ context.Context, _ *orm.ModelMetadata, _ []string, _ []any, kwargs map[string]any) (any, error) {
					got, _ = orm.AliasRequestFromKwargs(kwargs)
					return orm.AliasResponse{Status: http.StatusAccepted, Headers: map[string]string{"X-Test": "1"}, Body: "ok"}, nil
				},
			},
		},
	}.WithStorageName("test_rpc_route_anonymous_alias"))

	app := newRPCTestApp()

	req := httptest.NewRequest(http.MethodPost, "/base/rpc-anonymous/event", strings.NewReader(`{"n":"pageview"}`))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("CF-IPCountry", "VN")
	req.Header.Set("Cookie", "session=secret")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("anonymous alias request: %v", err)
	}
	testutil.RequireStatus(t, resp, http.StatusAccepted)
	if origin := resp.Header.Get("Access-Control-Allow-Origin"); origin != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want *", origin)
	}
	if resp.Header.Get("X-Test") != "1" {
		t.Fatalf("AliasResponse headers were not written")
	}
	if got == nil || string(got.Body) != `{"n":"pageview"}` {
		t.Fatalf("method did not receive the raw body: %#v", got)
	}
	if got.Headers["cf-ipcountry"] != "VN" {
		t.Fatalf("headers = %#v, want lowercase cf-ipcountry", got.Headers)
	}
	if _, leaked := got.Headers["cookie"]; leaked {
		t.Fatalf("cookie header must not reach an anonymous method")
	}
}

// TestRPCAnonymousRESTAliasRoute covers AliasAnonymous on a REST-form route: no bearer token, the
// exact verb + absolute path (no /api prefix), the raw body reaching the method, and a
// path-ID method being refused.
func TestRPCAnonymousRESTAliasRoute(t *testing.T) {
	setRPCTestConfig(t)

	const modelName = "test.rpc_route_anonymous_rest_alias"
	var got *orm.AliasRequest
	runtest.RegisterModelType[rpcRouteAliasDocument](t, orm.ModelSpec{
		Name:       modelName,
		Label:      "RPC Route Anonymous REST Alias",
		Definition: orm.ModelDefinition(modelName, (*rpcRouteAliasDocument)(nil)),
		RuntimeMethods: []orm.RuntimeMethod{
			{
				Name:           "bootstrap",
				ModelOnly:      true,
				AliasRoute:     "POST /test-anon-rest/bootstrap",
				AliasAnonymous: true,
				Call: func(_ context.Context, _ *orm.ModelMetadata, _ []string, _ []any, kwargs map[string]any) (any, error) {
					got, _ = orm.AliasRequestFromKwargs(kwargs)
					return orm.AliasResponse{Status: http.StatusCreated, Body: "ok"}, nil
				},
			},
			{
				Name:           "by_id",
				UsesPathID:     true,
				AliasRoute:     "POST /test-anon-rest/by-id/:id",
				AliasAnonymous: true,
				Call: func(context.Context, *orm.ModelMetadata, []string, []any, map[string]any) (any, error) {
					return "never", nil
				},
			},
		},
	}.WithStorageName("test_rpc_route_anonymous_rest_alias"))

	app := newRPCTestApp()

	req := httptest.NewRequest(http.MethodPost, "/test-anon-rest/bootstrap", strings.NewReader(`{"a":1}`))
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("anonymous REST alias request: %v", err)
	}
	testutil.RequireStatus(t, resp, http.StatusCreated)
	if got == nil || string(got.Body) != `{"a":1}` {
		t.Fatalf("method did not receive the raw body: %#v", got)
	}

	idResp, err := app.Test(httptest.NewRequest(http.MethodPost, "/test-anon-rest/by-id/1", nil))
	if err != nil {
		t.Fatalf("path-id request: %v", err)
	}
	testutil.RequireStatus(t, idResp, http.StatusNotFound)
}

// TestRPCRESTAliasRoutes covers the REST form of AliasRoute/Aliases: exact verb + absolute path,
// path/query/body binding, reserved-kwarg stripping, static-before-param mounting, custom
// Render status, the {error, message} failure shape and the 401 for a missing token.
func TestRPCRESTAliasRoutes(t *testing.T) {
	setRPCTestConfig(t)

	const modelName = "test.rpc_route_rest_alias"
	echo := func(_ context.Context, _ *orm.ModelMetadata, ids []string, _ []any, kwargs map[string]any) (any, error) {
		// Echo what the method actually saw, including whether a reserved key leaked.
		_, sudo := kwargs["sudo"]
		_, uid := kwargs["uid"]
		contextValues, _ := kwargs["context"].(map[string]any)
		return map[string]any{
			"ids": ids, "code": kwargs["code"], "limit": kwargs["limit"], "name": kwargs["name"],
			"sudo": sudo, "uid": uid, "active_test": contextValues["active_test"],
		}, nil
	}
	runtest.RegisterModelType[rpcRouteRESTAliasDocument](t, orm.ModelSpec{
		Name:       modelName,
		Label:      "RPC Route REST Alias",
		Definition: orm.ModelDefinition(modelName, (*rpcRouteRESTAliasDocument)(nil)),
		RuntimeMethods: []orm.RuntimeMethod{
			{
				Name: "echo", Public: true, ModelOnly: true, Call: echo,
				// Default REST contract: params + query + body as kwargs, 200 {"result"}.
				AliasRoute: "get /test-rest/items/:code/",
				Aliases: []orm.MethodAlias{{
					Route: "PUT /test-rest/items/:code",
					Bind: func(call *orm.AliasCall) error {
						body, err := call.JSONObject()
						if err != nil {
							return err
						}
						// Copy the whole body on purpose: the transport must still strip sudo/uid/context.
						call.Kwargs = body
						call.Kwargs["code"] = call.Params["code"]
						call.Context = map[string]any{"active_test": false}
						return nil
					},
					Render: func(_ *orm.AliasCall, result any) (int, any, error) { return http.StatusCreated, result, nil },
				}, {
					Route: "POST /test-rest/items/:code/fail",
					Bind: func(*orm.AliasCall) error {
						return &orm.AliasError{Status: http.StatusConflict, Code: "duplicate_code", Message: "code exists"}
					},
				}},
			},
			{
				Name: "touch", Public: true, UsesPathID: true, Call: echo,
				AliasRoute: "DELETE /test-rest/items/:id",
			},
			{
				Name: "versions", Public: true, ModelOnly: true, ReadOnly: true,
				AliasRoute: "GET /test-rest/items/versions",
				Call: func(context.Context, *orm.ModelMetadata, []string, []any, map[string]any) (any, error) {
					return "static", nil
				},
			},
		},
	}.WithStorageName("test_rpc_route_rest_alias"))

	app := newRPCTestApp()
	token := rpcTestBearerToken(t)
	do := func(method, target, body, bearer string) *http.Response {
		t.Helper()
		resp, err := app.Test(testutil.NewJSONRequestWithBearer(t, method, target, body, bearer))
		if err != nil {
			t.Fatalf("%s %s: %v", method, target, err)
		}
		return resp
	}

	resp := do(http.MethodGet, "/test-rest/items/SN01?limit=5", "", token)
	testutil.RequireStatus(t, resp, http.StatusOK)
	got := testutil.DecodeJSON[map[string]any](t, resp.Body)["result"].(map[string]any)
	if got["code"] != "SN01" || got["limit"] != "5" {
		t.Fatalf("default bind = %#v", got)
	}

	resp = do(http.MethodPut, "/test-rest/items/SN01", `{"name":"n1","sudo":true,"uid":7,"context":{"active_test":true}}`, token)
	testutil.RequireStatus(t, resp, http.StatusCreated)
	got = testutil.DecodeJSON[map[string]any](t, resp.Body)
	if got["code"] != "SN01" || got["name"] != "n1" || got["sudo"] != false || got["uid"] != false {
		t.Fatalf("custom bind = %#v (reserved kwargs must not reach the method)", got)
	}
	if got["active_test"] != false {
		t.Fatalf("context = %#v, want only the server-built active_test=false", got["active_test"])
	}

	resp = do(http.MethodDelete, "/test-rest/items/42", "", token)
	testutil.RequireStatus(t, resp, http.StatusOK)
	if ids := testutil.DecodeJSON[map[string]any](t, resp.Body)["result"].(map[string]any)["ids"].([]any); len(ids) != 1 || ids[0] != "42" {
		t.Fatalf(":id did not feed ids: %#v", ids)
	}

	resp = do(http.MethodGet, "/test-rest/items/versions", "", token)
	testutil.RequireStatus(t, resp, http.StatusOK)
	if got := testutil.DecodeJSON[map[string]any](t, resp.Body)["result"]; got != "static" {
		t.Fatalf("static route shadowed by :code, got %#v", got)
	}

	resp = do(http.MethodPost, "/test-rest/items/SN01/fail", "{}", token)
	testutil.RequireStatus(t, resp, http.StatusConflict)
	if got := testutil.DecodeJSON[map[string]any](t, resp.Body); restErrorCode(got) != float64(orm.ErrConflict) || restErrorMessage(got) != "code exists" {
		t.Fatalf("error body = %#v", got)
	}

	resp = do(http.MethodPut, "/test-rest/items/SN01", "{", token)
	testutil.RequireStatus(t, resp, http.StatusBadRequest)
	if got := testutil.DecodeJSON[map[string]any](t, resp.Body); restErrorCode(got) != float64(orm.ErrParse) {
		t.Fatalf("malformed body = %#v", got)
	}

	resp = do(http.MethodGet, "/test-rest/items/SN01", "", "")
	testutil.RequireStatus(t, resp, http.StatusUnauthorized)
	if got := testutil.DecodeJSON[map[string]any](t, resp.Body); restErrorCode(got) != float64(orm.ErrUnauthorized) {
		t.Fatalf("401 body = %#v", got)
	}
}

// restErrorCode and restErrorMessage read the standard {"id","error":{"code","message"}} body.
func restErrorCode(body map[string]any) any {
	detail, _ := body["error"].(map[string]any)
	return detail["code"]
}

func restErrorMessage(body map[string]any) any {
	detail, _ := body["error"].(map[string]any)
	return detail["message"]
}

// TestRPCDatabaseListIgnoresUnresolvedSelector covers the current DB-loading
// contract: an X-Database-Name header is trusted as a literal selector (no
// upfront allowlist — see resolveRPCDBName), so a name that names no
// registered database is not itself a request error. /rpc/db/list never
// touches the selected database at all, so it succeeds regardless of the
// header's validity; login-time unknown-DB rejection (POST /oauth/token) now
// lives in modules/base-auth/routes.
func TestRPCDatabaseListIgnoresUnresolvedSelector(t *testing.T) {
	setRPCTestConfig(t)
	registerRPCTestDB(t, "bean_ipn")

	app := newRPCTestApp()

	rpcReq := testutil.NewJSONRequestWithBearer(t, http.MethodPost, "/rpc/db/list", `{"id":1,"params":{}}`, rpcTestBearerToken(t))
	rpcReq.Header.Set(rpcHeaderDatabaseName, "ghost_db")
	rpcResp, err := app.Test(rpcReq)
	if err != nil {
		t.Fatalf("rpc request: %v", err)
	}
	testutil.RequireStatus(t, rpcResp, http.StatusOK)
}

func TestRPCModelRoutesConvertNestedNullsToFalse(t *testing.T) {
	setRPCTestConfig(t)

	const modelName = "test.rpc_route_nullable"
	runtest.RegisterModelType[rpcRouteNullableDocument](t, orm.ModelSpec{
		Name:       modelName,
		Label:      "RPC Route Nullable Model",
		Definition: orm.ModelDefinition(modelName, (*rpcRouteNullableDocument)(nil)),
	}.WithStorageName("test_rpc_route_nullable"))
	orm.RegisterRuntimeExecutor(modelName, &runtest.ExecutorStub{
		ReadResult: []orm.RecordMap{{
			"id":      int64(7),
			"name":    nil,
			"profile": map[string]any{"nickname": nil},
			"items":   []any{nil, map[string]any{"flag": nil}},
		}},
	})

	app := newRPCTestApp()

	req := testutil.NewJSONRequestWithBearer(t, http.MethodPost, "/rpc/"+modelName+"/read/7", `{"args":[],"kwargs":{"fields":["id","name"]}}`, rpcTestBearerToken(t))
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("read request: %v", err)
	}
	testutil.RequireStatus(t, resp, http.StatusOK)

	payload := testutil.DecodeJSON[map[string]any](t, resp.Body)
	resultList, ok := payload["result"].([]any)
	if !ok || len(resultList) != 1 {
		t.Fatalf("expected one result row, got %#v", payload["result"])
	}
	row, ok := resultList[0].(map[string]any)
	if !ok {
		t.Fatalf("expected row map, got %#v", resultList[0])
	}
	if got := row["name"]; got != false {
		t.Fatalf("name = %#v, want false", got)
	}
	profile, ok := row["profile"].(map[string]any)
	if !ok {
		t.Fatalf("expected profile map, got %#v", row["profile"])
	}
	if got := profile["nickname"]; got != false {
		t.Fatalf("profile.nickname = %#v, want false", got)
	}
	items, ok := row["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("expected items slice, got %#v", row["items"])
	}
	if got := items[0]; got != false {
		t.Fatalf("items[0] = %#v, want false", got)
	}
	itemMap, ok := items[1].(map[string]any)
	if !ok {
		t.Fatalf("expected nested item map, got %#v", items[1])
	}
	if got := itemMap["flag"]; got != false {
		t.Fatalf("items[1].flag = %#v, want false", got)
	}
}

func TestRPCDatabaseAndMetaRoutes(t *testing.T) {
	setRPCTestConfig(t)

	app := newRPCTestApp()

	dbReq := testutil.NewJSONRequestWithBearer(t, http.MethodPost, "/rpc/db/list", `{"id":1,"params":{}}`, rpcTestBearerToken(t))
	dbResp, err := app.Test(dbReq)
	if err != nil {
		t.Fatalf("db list request: %v", err)
	}
	testutil.RequireStatus(t, dbResp, http.StatusOK)

	metaReq := testutil.NewJSONRequestWithBearer(t, http.MethodPost, "/rpc_meta/model/create", `{"id":2,"params":{}}`, rpcTestBearerToken(t))
	metaResp, err := app.Test(metaReq)
	if err != nil {
		t.Fatalf("meta request: %v", err)
	}
	testutil.RequireStatus(t, metaResp, http.StatusBadRequest)
}

func TestRPCUIViewGetViewReturnsGeneratedPayload(t *testing.T) {
	setRPCTestConfig(t)

	bunDB, err := orm.NewBunDB(testutil.EnvPostgresConfig())
	if err != nil {
		t.Skipf("open postgres fixture: %v", err)
	}
	defer bunDB.Close()

	testutil.SwapBunDB(t, bunDB)

	// sys.model.log must be registered before migration: catalog sync writes
	// change logs into sys_model_log, so on a fresh scratch database the table
	// has to be created by this same migrate pass or AutoMigrate fails.
	models.SysModelLogModel.InitDefault()
	defer models.SysModelLogModel.Shutdown()
	models.UIViewModel.InitDefault()
	defer models.UIViewModel.Shutdown()
	if err := orm.AutoMigrateRegisteredModelsToDB(t.Context(), bunDB.DB, orm.DefaultModelRegistry()); err != nil {
		t.Fatalf("auto-migrate ui view fixture: %v", err)
	}

	const targetModelName = "test.rpc_ui_view_target"
	runtest.RegisterModelType[rpcRouteUIViewTargetDocument](t, orm.ModelSpec{
		Name:       targetModelName,
		Label:      "RPC UI View Target",
		Definition: orm.ModelDefinition(targetModelName, (*rpcRouteUIViewTargetDocument)(nil)),
	}.WithStorageName("test_rpc_ui_view_target"))

	app := newRPCTestApp()

	req := testutil.NewJSONRequestWithBearer(t, http.MethodPost, "/rpc/"+models.UIViewModelName+"/get_view", `{"args":[[],"`+targetModelName+`","form"],"kwargs":{}}`, rpcTestBearerToken(t))
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("get_view request: %v", err)
	}
	testutil.RequireStatus(t, resp, http.StatusOK)

	payload := testutil.DecodeJSON[map[string]any](t, resp.Body)
	result, ok := payload["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result map, got %#v", payload["result"])
	}
	if got := result["model"]; got != targetModelName {
		t.Fatalf("model = %#v, want %q", got, targetModelName)
	}
	if got := result["type"]; got != "form" {
		t.Fatalf("type = %#v, want form", got)
	}
	meta, ok := result["meta"].(map[string]any)
	if !ok {
		t.Fatalf("expected meta map, got %#v", result["meta"])
	}
	if got := meta["source"]; got != "generated" {
		t.Fatalf("source = %#v, want generated", got)
	}
}

func newRPCTestApp() *fiber.App {
	app := fiber.New(fiber.Config{JSONEncoder: base.JSONMarshal, ErrorHandler: APIErrorHandler})
	RegisterRPCRoutes(app)
	return app
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

func registerRPCTestDB(t *testing.T, name string) {
	t.Helper()
	orm.SetNamedBunDB(name, &orm.BunDB{DB: &bun.DB{}})
	orm.SetDefaultBunDBName(name)
	t.Cleanup(func() {
		orm.SetNamedBunDB(name, nil)
	})
}

// rpcTestSigningKeyOnce swaps in one ephemeral, process-local RSA signing key
// the first time a DB-free test needs a bearer token, so these unit tests
// never need a live Postgres connection just to sign/validate a JWT (the
// production LoadOrCreateSigningKey path persists its key to
// sys_config_param on the home DB). Real-Postgres tests in this same package
// (e.g. session_info_oauth_test.go) are unaffected in what they assert: they
// mint and validate within one process run and never check key persistence.
var rpcTestSigningKeyOnce sync.Once

// rpcTestBearerToken mints a superadmin OAuth bearer token accepted by
// currentRPCIdentity, replacing the removed X-API-Key/config.Auth.APIKey
// credential these tests used to authenticate with (RBAC/OAuth backend plan
// §1/§7.4 test-update requirement).
func rpcTestBearerToken(t *testing.T) string {
	t.Helper()
	rpcTestSigningKeyOnce.Do(func() {
		key, err := baseauth.NewEphemeralSigningKeyForTests()
		if err != nil {
			t.Fatalf("generate test signing key: %v", err)
		}
		baseauth.SetSigningKeyForTests(key)
	})
	token, err := baseauth.MintAccessTokenForTests(&orm.RBACIdentity{
		UserID:       1,
		Login:        "system",
		RoleName:     "system",
		IsSuperadmin: true,
	}, "test")
	if err != nil {
		t.Fatalf("mint test bearer token: %v", err)
	}
	return token
}
