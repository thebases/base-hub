package models

// Workflow roles (plan T4 owner decision D1 = (c), plan T94 WF-U-4).
//
// base.group_user has blanket CRUD on base.* by design (the kernel prefix baseline), so CSV rows
// alone cannot narrow it: the access handler below is what enforces the workflow roles.
//
//   - viewer   (workflow.group_workflow_viewer): read definitions, runs and outbound deliveries.
//   - operator (workflow.group_workflow_operator, implies viewer): start runs (test-run, replay,
//     cancel, approve/reject) and retry outbound deliveries (manual_retry only). Run create is
//     Internal (a client-built run row would be resumed by the sudo cron), so any remaining create
//     path on runs (name_create, find_or_create, copy) needs the author role.
//   - author   (workflow.group_workflow_author, implies operator): create, change or delete
//     definitions. A workflow step still runs as sudo (trigger registry, crud_model node), so
//     authoring is equivalent to holding superadmin: an author could write base.users.group_id
//     or drop a tenant through a crud_model step. Until triggers run under an explicit run-as
//     identity (T44/D6), the author role is therefore satisfied only by base.group_system;
//     membership of the author group alone grants nothing beyond operator.
//
// base.group_system satisfies every role. It does not imply the workflow groups through
// implied_ids because a module must not alter another module's group (no cross-module pattern
// for that exists); the check below treats it as holding every role instead.
//
// Sudo, a nil environment and calls without an RBAC auth mode (engine, trigger registry, cron,
// boot reconcile, master-data import) are never restricted, matching ensureRBACModelAccess. The
// engine writes runs and deliveries through WorkflowEngineContext (sudo), so it is unaffected.

import (
	"context"
	"slices"

	"git.thebase.vn/base_hub/internal/orm"
)

// WorkflowRole is the BID of the tbl.groups row that grants the role (seeded by
// data/19_workflow_groups.json).
type WorkflowRole string

const (
	WorkflowRoleViewer   WorkflowRole = "workflow.group_workflow_viewer"
	WorkflowRoleOperator WorkflowRole = "workflow.group_workflow_operator"
	WorkflowRoleAuthor   WorkflowRole = "workflow.group_workflow_author"
)

const workflowSystemGroupBID = "base.group_system"

// workflowRoleLadder orders the roles from weakest to strongest: a group satisfies its own role
// and every weaker one, so the check holds even where tbl_group_implied_rel is not expanded.
var workflowRoleLadder = []WorkflowRole{WorkflowRoleViewer, WorkflowRoleOperator, WorkflowRoleAuthor}

// workflowAuthoringModelNames are the definition models a workflow is built from.
var workflowAuthoringModelNames = []string{
	FlowModelName,
	WorkflowActionModelName,
	WorkflowTriggerModelName,
	WorkflowEdgeModelName,
	WorkflowCredentialModelName,
	WorkflowActionCatalogModelName,
	// Published snapshots hold the same step params as the action rows (plan T97); their mutations
	// are Internal anyway.
	WorkflowVersionModelName,
}

// workflowGuardedModelNames are the models whose RPC access needs a workflow role: definitions
// plus the run and outbound audit rows (they carry trigger payloads and request bodies).
var workflowGuardedModelNames = append(slices.Clone(workflowAuthoringModelNames), WorkflowRunModelName, OutboundDeliveryModelName)

// Test seams over the kernel helpers.
var (
	workflowCallerGroupBIDs = orm.CallerGroupBIDs
	workflowDefaultAccess   = orm.EnsureDefaultModelAccess
)

func init() {
	for _, name := range workflowGuardedModelNames {
		orm.RegisterAccessHandler(name, workflowRoleAccess)
	}
}

// EnsureWorkflowRole returns a forbidden error when the caller in ctx lacks role. Transports that
// bypass the model ACL (the /wf/* helper routes call runtime methods directly) use it.
func EnsureWorkflowRole(ctx context.Context, role WorkflowRole) error {
	return ensureWorkflowRole(ctx, orm.EnvironmentFromContext(ctx), role, string(role))
}

// workflowCallerHasRole reports whether groups (caller BIDs, implied groups included) satisfy role.
func workflowCallerHasRole(groups []string, role WorkflowRole) bool {
	if slices.Contains(groups, workflowSystemGroupBID) {
		return true
	}
	// Author stays system-only until T44/D6 (see the file comment).
	if role == WorkflowRoleAuthor {
		return false
	}
	index := slices.Index(workflowRoleLadder, role)
	if index < 0 {
		return false
	}
	for _, granting := range workflowRoleLadder[index:] {
		if slices.Contains(groups, string(granting)) {
			return true
		}
	}
	return false
}

// workflowRequiredRole maps one model operation to the role it needs. The dispatcher checks a
// custom mutating method (canvas save, copy, import, manual_retry) as "write" with no values, so
// the decision is per model and operation only.
func workflowRequiredRole(modelName, operation string) WorkflowRole {
	if operation == "read" {
		return WorkflowRoleViewer
	}
	switch {
	case modelName == WorkflowRunModelName && operation == "write":
		// The generic run write is Internal (engine-only), so the only client "write" on a run is a
		// run-control method: replay_run and cancel_run (plans T95/T100) are operator actions.
		return WorkflowRoleOperator
	case modelName == OutboundDeliveryModelName && operation == "write":
		// manual_retry is dispatched as "write"; the generic outbound write itself also requires the
		// author role (writeOutboundDelivery), and T40 (outbound_delivery_access.go) still keeps
		// authorization_ref and the fields bound to it engine-only.
		return WorkflowRoleOperator
	}
	return WorkflowRoleAuthor
}

// workflowRoleAccess keeps the standard ACL check, then requires the workflow role the operation
// needs (workflowRequiredRole).
func workflowRoleAccess(ctx context.Context, env *orm.Environment, model *orm.ModelMetadata, operation string, ids []string, values orm.RecordMap) error {
	if err := workflowDefaultAccess(ctx, env, model, operation, ids, values); err != nil {
		return err
	}
	role := workflowRequiredRole(model.Name, operation)
	// set_active writes only is_active (the run switch), an operator action; every other
	// base.workflow write still needs the author role.
	if model.Name == FlowModelName && orm.RuntimeMethodNameFromContext(ctx) == workflowSetActiveMethod {
		role = WorkflowRoleOperator
	}
	return ensureWorkflowRole(ctx, env, role, operation+" "+model.Name)
}

func ensureWorkflowRole(ctx context.Context, env *orm.Environment, role WorkflowRole, action string) error {
	if env == nil || env.IsSudo() {
		return nil
	}
	if _, ok := env.ContextValue(orm.RBACContextAuthModeKey); !ok {
		return nil
	}
	groups, err := workflowCallerGroupBIDs(orm.ContextWithEnvironment(ctx, env))
	if err != nil {
		return err
	}
	if !workflowCallerHasRole(groups, role) {
		if role == WorkflowRoleAuthor {
			return orm.Forbiddenf("permission denied: only %s can %s", workflowSystemGroupBID, action)
		}
		return orm.Forbiddenf("permission denied: %s requires %s", action, role)
	}
	return nil
}
