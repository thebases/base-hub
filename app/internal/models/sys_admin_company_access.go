package models

// File overview:
// - Responsibility: restricts the global system-configuration models surfaced
//   under the "Admin" menu (see ui_menu.go's allowUIMenuRowForRootCompany) to
//   the root company, so hiding the sidebar entry isn't the only boundary.
// - Key symbols: `adminOnlyModelNames`, `adminSharedModelNames`, `init`.
// - Pattern: chains through orm.EnsureDefaultModelAccess first so standard
//   ACL/group semantics are preserved, then adds a company gate on top.

import (
	"context"
	"fmt"

	"git.thebase.vn/base_hub/internal/orm"
)

// adminOnlyModelNames are pure system-configuration screens with no other
// caller in the app: only the root company's operators ever need to reach
// them, so every operation (read/write/create/unlink) is gated.
var adminOnlyModelNames = []string{
	SysModelModelName,
	SysModelAccessModelName,
	SysModelFieldModelName,
	SysModelDataModelName,
	SysModelRelationModelName,
	SysModelOperationModelName,
	SysModelConstraintModelName,
	SysRuleModelName,
	SysModuleCategoryModelName,
	SysModuleModelName,
	SysConfigModelName,
	SysModelLogModelName,
}

// adminSharedModelNames back objects that every company's users read
// constantly during normal operation (menu bootstrap, view rendering, action
// resolution) but that have no per-company isolation of their own. Only
// mutating them is root-company-only; reads stay open so other companies keep
// working normally.
var adminSharedModelNames = []string{
	UIMenuModelName,
	UIActionClientModelName,
	UIActionWindowModelName,
	UIViewModelName,
	UIViewCustomModelName,
}

func init() {
	for _, name := range adminOnlyModelNames {
		orm.RegisterAccessHandler(name, adminOnlyCompanyAccess)
	}
	for _, name := range adminSharedModelNames {
		orm.RegisterAccessHandler(name, adminSharedCompanyAccess)
	}
}

// adminOnlyCompanyAccess denies every operation for callers outside the root
// company, after the standard ACL/group check has already passed.
func adminOnlyCompanyAccess(ctx context.Context, env *orm.Environment, model *orm.ModelMetadata, operation string, ids []string, values orm.RecordMap) error {
	if err := orm.EnsureDefaultModelAccess(ctx, env, model, operation, ids, values); err != nil {
		return err
	}
	return ensureRootCompanyAccess(env, model)
}

// adminSharedCompanyAccess only denies mutating operations for callers
// outside the root company; reads stay on standard ACL semantics since these
// models are read by every company during normal page/menu bootstrap.
func adminSharedCompanyAccess(ctx context.Context, env *orm.Environment, model *orm.ModelMetadata, operation string, ids []string, values orm.RecordMap) error {
	if err := orm.EnsureDefaultModelAccess(ctx, env, model, operation, ids, values); err != nil {
		return err
	}
	if operation == "read" {
		return nil
	}
	return ensureRootCompanyAccess(env, model)
}

// ensureRootCompanyAccess denies when a non-sudo, RPC-authenticated caller's
// active company isn't rootCompanyID. A nil env (internal/non-RPC caller)
// keeps existing default semantics, mirroring ensureRBACModelAccess's own
// env==nil bypass. A CompanyID lookup error means the caller's active company
// isn't even in their own allowed set (a spoofed/invalid context), so it fails
// closed rather than silently allowing the request through.
func ensureRootCompanyAccess(env *orm.Environment, model *orm.ModelMetadata) error {
	if env == nil {
		return nil
	}
	companyID, err := env.CompanyID()
	if err != nil {
		return fmt.Errorf("permission denied: model %q is restricted to the root company: %w", model.Name, err)
	}
	if companyID != rootCompanyID {
		return fmt.Errorf("permission denied: model %q is restricted to the root company", model.Name)
	}
	return nil
}
