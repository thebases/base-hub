package models

// Access guard for tbl.discuss_mail_server (plan T4, owner decision D1 = (c)).
//
// base.group_user has blanket CRUD on tbl.* by design, so without this guard any
// internal user could read the SMTP password (stored in clear until T23) or point
// outbound mail at a host they control. The model has no menu or screen for regular
// users and the sender reads it through the repository (no ACL), so every RPC
// operation, reads included, is limited to base.group_system.

import (
	"context"
	"slices"

	"git.thebase.vn/base_hub/internal/orm"
)

const mailServerManagerGroupBID = "base.group_system"

// Test seams over kernel helpers that need the auth database.
var (
	mailServerCallerGroupBIDs = orm.CallerGroupBIDs
	mailServerDefaultAccess   = orm.EnsureDefaultModelAccess
)

func init() {
	orm.RegisterAccessHandler(DiscussMailServerModelName, discussMailServerAccess)
}

// discussMailServerAccess keeps the standard ACL check, then requires
// base.group_system for every operation by an RPC-authenticated caller. Sudo, nil
// env and callers without an auth mode (seed import, boot, the mail sender) keep
// the kernel default, matching ensureRBACModelAccess.
func discussMailServerAccess(ctx context.Context, env *orm.Environment, model *orm.ModelMetadata, operation string, ids []string, values orm.RecordMap) error {
	if err := mailServerDefaultAccess(ctx, env, model, operation, ids, values); err != nil {
		return err
	}
	if env == nil || env.IsSudo() {
		return nil
	}
	if _, ok := env.ContextValue(orm.RBACContextAuthModeKey); !ok {
		return nil
	}
	groups, err := mailServerCallerGroupBIDs(orm.ContextWithEnvironment(ctx, env))
	if err != nil {
		return err
	}
	if !slices.Contains(groups, mailServerManagerGroupBID) {
		return orm.Forbiddenf("permission denied: only %s can %s %s", mailServerManagerGroupBID, operation, DiscussMailServerModelName)
	}
	return nil
}
