package routes

import (
	"context"
	"errors"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
	runtest "git.thebase.vn/base_hub/internal/test/runtime"
	"github.com/gofiber/fiber/v3"
)

type reportRouteAccessDocument struct {
	orm.BaseModel
	Name string `bun:"name" json:"name"`
}

// Plan T36 / SEC-D-23: the report route checks the report model's read ACL before any read,
// because orm.ExecuteRuntimeMethod (used by the render and the PDF cache) skips it.
func TestEnsureReportModelReadable(t *testing.T) {
	const modelName = "test.report_route_access"
	runtest.RegisterModelType[reportRouteAccessDocument](t, orm.ModelSpec{
		Name:       modelName,
		Label:      "Report Route Access",
		Definition: orm.ModelDefinition(modelName, (*reportRouteAccessDocument)(nil)),
	}.WithStorageName("test_report_route_access"))
	orm.RegisterRuntimeExecutor(modelName, &runtest.ExecutorStub{})

	allowRead := true
	var seenOperation string
	orm.RegisterAccessHandler(modelName, func(_ context.Context, _ *orm.Environment, _ *orm.ModelMetadata, operation string, _ []string, _ orm.RecordMap) error {
		seenOperation = operation
		if !allowRead {
			return orm.Forbiddenf("no %s", operation)
		}
		return nil
	})
	ctx := orm.ContextWithEnvironment(context.Background(), orm.EnvironmentForIdentity(&orm.RBACIdentity{
		UserID: 9, Login: "u", RoleID: 2, AuthMode: "bearer",
	}))

	if err := ensureReportModelReadable(ctx, modelName, []string{"1"}); err != nil {
		t.Fatalf("readable model refused: %v", err)
	}
	if seenOperation != "read" {
		t.Fatalf("want a read check, got %q", seenOperation)
	}

	allowRead = false
	var fiberErr *fiber.Error
	err := ensureReportModelReadable(ctx, modelName, []string{"1"})
	if !errors.As(err, &fiberErr) || fiberErr.Code != fiber.StatusForbidden {
		t.Fatalf("want 403, got %v", err)
	}

	err = ensureReportModelReadable(ctx, "test.report_route_missing", []string{"1"})
	if !errors.As(err, &fiberErr) || fiberErr.Code != fiber.StatusNotFound {
		t.Fatalf("unknown model: want 404, got %v", err)
	}
}
