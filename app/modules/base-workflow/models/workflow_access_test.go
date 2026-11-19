package models

import (
	"context"
	"errors"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func stubWorkflowAccess(t *testing.T, groups ...string) {
	t.Helper()
	prevGroups, prevDefault := workflowCallerGroupBIDs, workflowDefaultAccess
	workflowCallerGroupBIDs = func(context.Context) ([]string, error) { return groups, nil }
	// The base.group_user prefix baseline already passes the default ACL for base.*.
	workflowDefaultAccess = func(context.Context, *orm.Environment, *orm.ModelMetadata, string, []string, orm.RecordMap) error {
		return nil
	}
	t.Cleanup(func() { workflowCallerGroupBIDs, workflowDefaultAccess = prevGroups, prevDefault })
}

func workflowRPCEnv(userID int64) *orm.Environment {
	return orm.NewEnvironment(nil, userID, map[string]any{orm.RBACContextAuthModeKey: "bearer"}, false)
}

// Caller group sets as CallerGroupBIDs returns them (implied groups expanded).
var (
	workflowTestPlainUser = []string{"base.group_user", "base.group_multi_company"}
	workflowTestViewer    = []string{"base.group_user", string(WorkflowRoleViewer)}
	workflowTestOperator  = []string{"base.group_user", string(WorkflowRoleOperator), string(WorkflowRoleViewer)}
	workflowTestAuthor    = []string{"base.group_user", string(WorkflowRoleAuthor), string(WorkflowRoleOperator), string(WorkflowRoleViewer)}
	workflowTestSystem    = []string{"base.group_user", "base.group_multi_company", "base.group_system"}
)

// TestWorkflowRoleOperationTable is the role x operation matrix (plan T94). Author membership
// alone grants nothing beyond operator until steps stop running as sudo (T44/D6).
func TestWorkflowRoleOperationTable(t *testing.T) {
	type expect struct{ plain, viewer, operator, author, system bool }
	readOnly := expect{false, true, true, true, true}
	operatorUp := expect{false, false, true, true, true}
	systemOnly := expect{false, false, false, false, true}

	type row struct {
		model, op string
		want      expect
	}
	var rows []row
	for _, name := range workflowAuthoringModelNames {
		rows = append(rows,
			row{name, "read", readOnly},
			row{name, "create", systemOnly},
			row{name, "write", systemOnly},
			row{name, "unlink", systemOnly},
		)
	}
	rows = append(rows,
		row{WorkflowRunModelName, "read", readOnly},
		row{WorkflowRunModelName, "create", systemOnly}, // create is Internal; name_create/copy need author
		row{WorkflowRunModelName, "write", operatorUp},  // replay_run, cancel_run (write itself is Internal)
		row{WorkflowRunModelName, "unlink", systemOnly},
		row{OutboundDeliveryModelName, "read", readOnly},
		row{OutboundDeliveryModelName, "create", systemOnly},
		row{OutboundDeliveryModelName, "write", operatorUp}, // manual_retry
		row{OutboundDeliveryModelName, "unlink", systemOnly},
	)

	callers := []struct {
		name   string
		groups []string
		pick   func(expect) bool
	}{
		{"plain user", workflowTestPlainUser, func(e expect) bool { return e.plain }},
		{"viewer", workflowTestViewer, func(e expect) bool { return e.viewer }},
		{"operator", workflowTestOperator, func(e expect) bool { return e.operator }},
		{"author", workflowTestAuthor, func(e expect) bool { return e.author }},
		{"system", workflowTestSystem, func(e expect) bool { return e.system }},
	}
	for _, r := range rows {
		role := workflowRequiredRole(r.model, r.op)
		for _, caller := range callers {
			if got, want := workflowCallerHasRole(caller.groups, role), caller.pick(r.want); got != want {
				t.Errorf("%s %s %s: allowed = %v, want %v (role %s)", caller.name, r.op, r.model, got, want, role)
			}
		}
	}
}

// A higher workflow group satisfies the lower roles even when the implied groups were not
// expanded (e.g. before tbl_group_implied_rel exists).
func TestWorkflowCallerHasRoleWithoutImpliedExpansion(t *testing.T) {
	if !workflowCallerHasRole([]string{string(WorkflowRoleAuthor)}, WorkflowRoleViewer) {
		t.Fatal("author group must satisfy viewer")
	}
	if !workflowCallerHasRole([]string{string(WorkflowRoleOperator)}, WorkflowRoleViewer) {
		t.Fatal("operator group must satisfy viewer")
	}
	if workflowCallerHasRole([]string{string(WorkflowRoleViewer)}, WorkflowRoleOperator) {
		t.Fatal("viewer group must not satisfy operator")
	}
	if workflowCallerHasRole([]string{string(WorkflowRoleAuthor)}, WorkflowRoleAuthor) {
		t.Fatal("author group alone must not satisfy author until T44/D6")
	}
	if workflowCallerHasRole(nil, WorkflowRole("workflow.unknown")) {
		t.Fatal("unknown role must be refused")
	}
}

func TestWorkflowRoleAccessInternalUserForbidden(t *testing.T) {
	stubWorkflowAccess(t, workflowTestPlainUser...)
	ctx := context.Background()
	for _, name := range workflowGuardedModelNames {
		meta := &orm.ModelMetadata{Name: name}
		for _, op := range []string{"read", "create", "write", "unlink"} {
			err := workflowRoleAccess(ctx, workflowRPCEnv(7), meta, op, nil, orm.RecordMap{"name": "x"})
			var ormErr *orm.Error
			if !errors.As(err, &ormErr) || ormErr.Code != orm.ErrForbidden {
				t.Fatalf("%s %s: err = %v, want forbidden", name, op, err)
			}
		}
	}
}

func TestWorkflowRoleAccessViewerReadsOnly(t *testing.T) {
	stubWorkflowAccess(t, workflowTestViewer...)
	ctx := context.Background()
	meta := &orm.ModelMetadata{Name: FlowModelName}
	if err := workflowRoleAccess(ctx, workflowRPCEnv(7), meta, "read", nil, nil); err != nil {
		t.Fatalf("viewer read: %v", err)
	}
	if err := workflowRoleAccess(ctx, workflowRPCEnv(7), meta, "write", nil, orm.RecordMap{"is_active": false}); err == nil {
		t.Fatal("viewer write must be forbidden")
	}
}

func TestWorkflowRoleAccessSystemSudoAndInternalAllowed(t *testing.T) {
	ctx := context.Background()
	meta := &orm.ModelMetadata{Name: FlowModelName}

	stubWorkflowAccess(t, workflowTestSystem...)
	if err := workflowRoleAccess(ctx, workflowRPCEnv(7), meta, "write", nil, nil); err != nil {
		t.Fatalf("group_system write: %v", err)
	}

	stubWorkflowAccess(t, workflowTestPlainUser...)
	if err := workflowRoleAccess(ctx, orm.NewEnvironment(nil, 7, nil, true), meta, "create", nil, nil); err != nil {
		t.Fatalf("sudo create: %v", err)
	}
	// No auth mode = internal caller (engine, cron, boot reconcile, master-data import).
	if err := workflowRoleAccess(ctx, orm.NewEnvironment(nil, 0, nil, false), meta, "create", nil, nil); err != nil {
		t.Fatalf("internal create: %v", err)
	}
	if err := workflowRoleAccess(ctx, nil, meta, "create", nil, nil); err != nil {
		t.Fatalf("nil env create: %v", err)
	}
}

func TestEnsureWorkflowRole(t *testing.T) {
	stubWorkflowAccess(t, workflowTestViewer...)
	rpcCtx := orm.ContextWithEnvironment(context.Background(), workflowRPCEnv(7))
	if err := EnsureWorkflowRole(rpcCtx, WorkflowRoleViewer); err != nil {
		t.Fatalf("viewer: %v", err)
	}
	err := EnsureWorkflowRole(rpcCtx, WorkflowRoleOperator)
	var ormErr *orm.Error
	if !errors.As(err, &ormErr) || ormErr.Code != orm.ErrForbidden {
		t.Fatalf("viewer asking operator: err = %v, want forbidden", err)
	}
	if err := EnsureWorkflowRole(context.Background(), WorkflowRoleAuthor); err != nil {
		t.Fatalf("no environment: %v", err)
	}
	sudoCtx := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironment(nil, 7, nil, true))
	if err := EnsureWorkflowRole(sudoCtx, WorkflowRoleAuthor); err != nil {
		t.Fatalf("sudo: %v", err)
	}
}

// TestWorkflowSetActiveNeedsOperatorOnly: the run switch (set_active) is an operator action, while
// a generic write of the same row still needs the author role (base.group_system).
// ormCode returns the typed orm error code of err (0 when err is not one).
func ormCode(err error) int {
	var ormErr *orm.Error
	if errors.As(err, &ormErr) {
		return ormErr.Code
	}
	return 0
}

func TestWorkflowSetActiveNeedsOperatorOnly(t *testing.T) {
	meta := &orm.ModelMetadata{Name: FlowModelName}
	call := func(groups []string, method string) error {
		stubWorkflowAccess(t, groups...)
		ctx := orm.ContextWithEnvironment(context.Background(), workflowRPCEnv(7))
		return orm.EnsureRuntimeModelAccess(ctx, meta, orm.RuntimeMethod{Name: method}, []string{"3"})
	}
	if err := call(workflowTestOperator, workflowSetActiveMethod); err != nil {
		t.Fatalf("operator set_active: %v", err)
	}
	if err := call(workflowTestOperator, "write"); ormCode(err) != orm.ErrForbidden {
		t.Fatalf("operator generic write must stay forbidden, got %v", err)
	}
	if err := call(workflowTestViewer, workflowSetActiveMethod); ormCode(err) != orm.ErrForbidden {
		t.Fatalf("viewer set_active must be forbidden, got %v", err)
	}
	if err := call(workflowTestSystem, workflowSetActiveMethod); err != nil {
		t.Fatalf("system set_active: %v", err)
	}
}

func TestSetFlowActiveValidatesArguments(t *testing.T) {
	cases := []struct {
		name   string
		ids    []string
		kwargs map[string]any
	}{
		{"no id", nil, map[string]any{"active": true}},
		{"two ids", []string{"1", "2"}, map[string]any{"active": true}},
		{"missing active", []string{"1"}, map[string]any{}},
		{"non-bool active", []string{"1"}, map[string]any{"active": "yes"}},
	}
	for _, tc := range cases {
		if _, err := setFlowActive(context.Background(), &orm.ModelMetadata{Name: FlowModelName}, tc.ids, nil, tc.kwargs); ormCode(err) != orm.ErrInvalidParams {
			t.Fatalf("%s: want invalid params, got %v", tc.name, err)
		}
	}
}
