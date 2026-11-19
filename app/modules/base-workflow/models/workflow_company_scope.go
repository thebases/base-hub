package models

// Workflow company scope (plan T41).
//
// Children of a workflow (actions, triggers, edges, workflow-scoped credentials) never carry a
// company of their own: company_id always follows the parent workflow, on create, on write (a
// client-sent company_id is dropped, a workflow_id change re-inherits) and when the workflow's
// own company changes (cascade). The engine binds the running workflow's id and company to the
// step context (ContextWithWorkflowScope) so @cred:<name> lookups only see that workflow's
// credentials and the shared credentials of its company, and outbound rows get its company.

import (
	"context"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
)

// WorkflowScope identifies the running workflow and its company.
type WorkflowScope struct {
	WorkflowID int64
	CompanyID  *int64
}

type workflowScopeContextKey struct{}

// ContextWithWorkflowScope binds the running workflow's id and company to ctx.
func ContextWithWorkflowScope(ctx context.Context, workflowID int64, companyID *int64) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, workflowScopeContextKey{}, WorkflowScope{WorkflowID: workflowID, CompanyID: copyInt64Pointer(companyID)})
}

// WorkflowScopeFromContext returns the scope bound by ContextWithWorkflowScope.
func WorkflowScopeFromContext(ctx context.Context) (WorkflowScope, bool) {
	if ctx == nil {
		return WorkflowScope{}, false
	}
	scope, ok := ctx.Value(workflowScopeContextKey{}).(WorkflowScope)
	return scope, ok
}

func copyInt64Pointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func sameInt64Pointer(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

// pickScopedWorkflowCredential returns the credential row a lookup in scope may use, or nil.
// Without a scope only tenant-wide rows (no workflow, no company) qualify. With one, a row must
// belong to the scope's company (or to none) and to the scope's workflow (or to none); a row of
// the workflow wins over a shared one, and a company row over a company-less one. A scope with
// no workflow (outbound manual retry: the row stores its company, not its workflow, and its
// reference is engine-owned) accepts any workflow's row of that company.
func pickScopedWorkflowCredential(rows []orm.RecordMap, scope WorkflowScope, scoped bool) orm.RecordMap {
	var best orm.RecordMap
	bestRank := -1
	for _, row := range rows {
		workflowID := int64PointerFromAny(row["workflow_id"])
		companyID := int64PointerFromAny(row["company_id"])
		if !scoped {
			if workflowID == nil && companyID == nil && bestRank < 0 {
				best, bestRank = row, 0
			}
			continue
		}
		if workflowID != nil && scope.WorkflowID > 0 && *workflowID != scope.WorkflowID {
			continue
		}
		if companyID != nil && !sameInt64Pointer(companyID, scope.CompanyID) {
			continue
		}
		rank := 0
		if workflowID != nil {
			rank += 2
		}
		if companyID != nil {
			rank++
		}
		if rank > bestRank {
			best, bestRank = row, rank
		}
	}
	return best
}

// applyWorkflowChildCompanyOnWrite drops a client-sent company_id from a child write and, when
// the write (re)states workflow_id, re-inherits that workflow's company.
func applyWorkflowChildCompanyOnWrite(ctx context.Context, normalized orm.RecordMap) error {
	delete(normalized, "company_id")
	raw, exists := normalized["workflow_id"]
	if !exists {
		return nil
	}
	workflowID, ok := int64PointerValue(raw)
	if !ok || workflowID == nil {
		return nil
	}
	companyID, err := workflowCompanyID(ctx, *workflowID)
	if err != nil {
		return err
	}
	normalized["company_id"] = companyIDValue(companyID)
	return nil
}

// workflowCompanyChildModels hold rows whose company follows their workflow.
var workflowCompanyChildModels = []string{
	WorkflowActionModelName,
	WorkflowTriggerModelName,
	WorkflowEdgeModelName,
	WorkflowCredentialModelName,
	WorkflowVersionModelName, // published snapshots (plan T97)
}

// cascadeWorkflowCompany rewrites company_id on every child of workflowID.
func cascadeWorkflowCompany(ctx context.Context, workflowID int64, companyID *int64) error {
	for _, modelName := range workflowCompanyChildModels {
		ref := orm.RuntimeModelRef(modelName, nil)
		rows, err := orm.RuntimeRepositorySearchRead(ctx, ref, orm.RecordMap{"workflow_id": workflowID}, []string{"id", "company_id"})
		if err != nil {
			return fmt.Errorf("%s: read children of workflow %d: %w", modelName, workflowID, err)
		}
		ids := make([]string, 0, len(rows))
		for _, row := range rows {
			if !sameInt64Pointer(int64PointerFromAny(row["company_id"]), companyID) {
				ids = append(ids, fmt.Sprint(int64Value(row["id"])))
			}
		}
		if len(ids) == 0 {
			continue
		}
		var value any
		if companyID != nil {
			value = *companyID
		}
		if _, err := orm.RuntimeRepositoryWrite(ctx, ref, ids, orm.RecordMap{"company_id": value}); err != nil {
			return fmt.Errorf("%s: move children of workflow %d to its company: %w", modelName, workflowID, err)
		}
	}
	return nil
}

// ensureWorkflowCredentialNameFree refuses a second credential with the same name in the same
// (company, workflow) scope; excludeID is the row being written (0 on create).
func ensureWorkflowCredentialNameFree(ctx context.Context, model *orm.ModelMetadata, name string, workflowID, companyID *int64, excludeID int64) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WorkflowCredentialModelName, model), orm.RecordMap{"name": name}, []string{"id", "workflow_id", "company_id"})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if int64Value(row["id"]) == excludeID {
			continue
		}
		if sameInt64Pointer(int64PointerFromAny(row["workflow_id"]), workflowID) && sameInt64Pointer(int64PointerFromAny(row["company_id"]), companyID) {
			return orm.Conflictf("base.workflow.credential %q already exists in this workflow and company", name)
		}
	}
	return nil
}
