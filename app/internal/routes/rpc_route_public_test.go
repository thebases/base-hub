package routes

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
	testutil "git.thebase.vn/base_hub/internal/test"
	runtest "git.thebase.vn/base_hub/internal/test/runtime"
	baseauth "git.thebase.vn/base_hub/modules/base-auth/service"
)

type rpcRoutePublicFlagDocument struct {
	orm.BaseModel
	Name string `bun:"name" json:"name"`
}

// Plan T36 / SEC-T-20: /rpc refuses a method that is not Public (the `_`-prefixed helpers) the
// same way it refuses an Internal or unknown one; Public methods still run.
func TestRPCModelRoutesRefuseNonPublicMethod(t *testing.T) {
	setRPCTestConfig(t)

	const modelName = "test.rpc_route_public_flag"
	called := map[string]bool{}
	call := func(name string) orm.RuntimeMethodFunc {
		return func(context.Context, *orm.ModelMetadata, []string, []any, map[string]any) (any, error) {
			called[name] = true
			return name, nil
		}
	}
	runtest.RegisterModelType[rpcRoutePublicFlagDocument](t, orm.ModelSpec{
		Name:       modelName,
		Label:      "RPC Route Public Flag",
		Definition: orm.ModelDefinition(modelName, (*rpcRoutePublicFlagDocument)(nil)),
		RuntimeMethods: []orm.RuntimeMethod{
			{Name: "_private_helper", ModelOnly: true, ReadOnly: true, Call: call("_private_helper")},
			{Name: "public_action", Public: true, ModelOnly: true, ReadOnly: true, Call: call("public_action")},
		},
	}.WithStorageName("test_rpc_route_public_flag"))
	orm.RegisterRuntimeExecutor(modelName, &runtest.ExecutorStub{})

	app := newRPCTestApp()
	token := rpcTestBearerToken(t)

	resp, err := app.Test(testutil.NewJSONRequestWithBearer(t, http.MethodPost, "/rpc/"+modelName+"/_private_helper", `{"args":[],"kwargs":{}}`, token))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "is not registered") || called["_private_helper"] {
		t.Fatalf("non-public method must be refused as not found; status %d body %s called %v", resp.StatusCode, body, called)
	}

	resp, err = app.Test(testutil.NewJSONRequestWithBearer(t, http.MethodPost, "/rpc/"+modelName+"/public_action", `{"args":[],"kwargs":{}}`, token))
	if err != nil {
		t.Fatal(err)
	}
	testutil.RequireStatus(t, resp, http.StatusOK)
	if !called["public_action"] {
		t.Fatal("public method must still run")
	}
}

// Plan T36 / SEC-T-21: /rpc/db/list names every tenant DB, so it needs a superadmin.
func TestRPCDatabaseListNeedsSuperadmin(t *testing.T) {
	setRPCTestConfig(t)
	rpcTestBearerToken(t) // installs the signing key

	token, err := baseauth.MintAccessTokenForTests(&orm.RBACIdentity{
		UserID: 7, Login: "plain", RoleName: "user",
	}, "test")
	if err != nil {
		t.Fatal(err)
	}
	app := newRPCTestApp()
	resp, err := app.Test(testutil.NewJSONRequestWithBearer(t, http.MethodPost, "/rpc/db/list", `{"id":1,"params":{}}`, token))
	if err != nil {
		t.Fatal(err)
	}
	testutil.RequireStatus(t, resp, http.StatusForbidden)
}
