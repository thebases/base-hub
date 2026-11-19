package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
	internalroutes "git.thebase.vn/base_hub/internal/routes"
	testutil "git.thebase.vn/base_hub/internal/test"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
	"github.com/gofiber/fiber/v3"
)

// TestWorkflowHelperRoutesReadAsCaller: /wf/runs/latest, /wf/credentials and /wf/sys-credentials
// must read with the caller's environment (ACL + record rules), not the identity-less DB context
// that let any signed-in user list run payloads and credential names of every company (T39).
func TestWorkflowHelperRoutesReadAsCaller(t *testing.T) {
	for _, path := range []string{"/wf/runs/latest?topic=demo", "/wf/credentials", "/wf/sys-credentials"} {
		t.Run(path, func(t *testing.T) {
			setRPCTestConfig(t)
			restoreWorkflowRouteHooks(t)
			stubWorkflowRouteRole(t, nil)
			workflowContextForDB = func(ctx context.Context, dbName string) context.Context {
				return orm.ContextWithEnvironment(ctx, orm.NewEnvironmentWithDBName(nil, dbName, 0, nil, false))
			}
			workflowRequireIdentity = func(fiber.Ctx, bool) (*internalroutes.RPCRequestIdentity, error) {
				return &internalroutes.RPCRequestIdentity{UserID: 42, DBName: "bean_ipn", CompanyID: 3, CompanyIDs: []int64{3}}, nil
			}
			var seenUID int64 = -1
			var seenSudo bool
			workflowRouteSearchRead = func(ctx context.Context, _ string, _ orm.RecordMap, _ []string) ([]orm.RecordMap, error) {
				if env := orm.EnvironmentFromContext(ctx); env != nil {
					seenUID, seenSudo = env.UserID(), env.IsSudo()
				}
				return nil, nil
			}

			resp, err := newWorkflowRouteTestApp().Test(httptest.NewRequest(http.MethodGet, path, nil))
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			testutil.RequireStatus(t, resp, http.StatusOK)
			if seenUID != 42 || seenSudo {
				t.Fatalf("read ran as uid=%d sudo=%v, want caller uid 42 non-sudo", seenUID, seenSudo)
			}
		})
	}
}

// stubWorkflowRouteRole replaces the workflow role check (it reads the caller's groups from the DB).
func stubWorkflowRouteRole(t *testing.T, err error) {
	t.Helper()
	original := workflowEnsureRole
	workflowEnsureRole = func(context.Context, workflowmodels.WorkflowRole) error { return err }
	t.Cleanup(func() { workflowEnsureRole = original })
}

// TestWorkflowHelperRoutesRequireWorkflowRole: the /wf/* helper routes call runtime methods past
// the RPC model ACL, so each refuses a caller without its workflow role (plan T94).
func TestWorkflowHelperRoutesRequireWorkflowRole(t *testing.T) {
	cases := []struct {
		method, path string
		want         workflowmodels.WorkflowRole
	}{
		{http.MethodGet, "/wf/runs/latest?topic=demo", workflowmodels.WorkflowRoleViewer},
		{http.MethodPost, "/wf/workflows/5/test-run", workflowmodels.WorkflowRoleOperator},
		{http.MethodPost, "/wf/workflows/5/validate-graph", workflowmodels.WorkflowRoleViewer},
		{http.MethodGet, "/wf/workflows/5/export", workflowmodels.WorkflowRoleViewer},
		{http.MethodGet, "/wf/credentials", workflowmodels.WorkflowRoleViewer},
		{http.MethodGet, "/wf/sys-credentials", workflowmodels.WorkflowRoleViewer},
		{http.MethodGet, "/wf/sys-models?q=partner", workflowmodels.WorkflowRoleViewer},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			setRPCTestConfig(t)
			restoreWorkflowRouteHooks(t)
			workflowRequireIdentity = func(fiber.Ctx, bool) (*internalroutes.RPCRequestIdentity, error) {
				return &internalroutes.RPCRequestIdentity{UserID: 42, DBName: "bean_ipn"}, nil
			}
			var asked workflowmodels.WorkflowRole
			original := workflowEnsureRole
			workflowEnsureRole = func(_ context.Context, role workflowmodels.WorkflowRole) error {
				asked = role
				return orm.Forbiddenf("permission denied")
			}
			t.Cleanup(func() { workflowEnsureRole = original })

			resp, err := newWorkflowRouteTestApp().Test(httptest.NewRequest(tc.method, tc.path, nil))
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			testutil.RequireStatus(t, resp, http.StatusForbidden)
			if asked != tc.want {
				t.Fatalf("role checked = %q, want %q", asked, tc.want)
			}
		})
	}
}
