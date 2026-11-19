package models

// File overview:
// - Responsibility: guards tbl.discuss_alias, the record that decides which
//   model an anonymous inbound-mail webhook call creates rows on (SEC-D-3).
//   Ownership: discuss module. Audit-sensitivity: high (an unrestricted
//   alias lets an unauthenticated POST create rows on any model).
// - Key symbols: `discussAliasAccess`, `validateAliasTargetModel`,
//   `modelDeclaresChatter`.
// - Pattern: access handler chained through orm.EnsureDefaultModelAccess
//   (see internal/models/sys_admin_company_access.go) plus an
//   orm.RegisterConstraint on alias_model_id. The same target check runs
//   again at routing time so alias rows saved before this guard existed
//   cannot be used either.

import (
	"context"
	"errors"
	"slices"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"github.com/uptrace/bun"
)

// aliasManagerGroupBID is the group allowed to create/write/unlink aliases.
// The base.group_user blanket CRUD grant on tbl.* is kept on purpose (owner
// decision D1 = c), so the restriction has to be explicit here.
const aliasManagerGroupBID = "base.group_system"

// aliasMutatingOperations mirrors the kernel's normalizeRBACOperation: every
// spelling that maps to create/write/unlink. Anything else is a read.
var aliasMutatingOperations = []string{"create", "new", "name_create", "write", "update", "toggle_active", "unlink", "delete", "remove"}

// aliasDeniedModels and aliasDeniedPrefixes are refused as alias targets even
// if someone flags one of their views use_chatter: identity, group and
// system/UI configuration models must never be creatable from an email.
var (
	aliasDeniedModels   = []string{"base.users", "tbl.groups"}
	aliasDeniedPrefixes = []string{"sys.", "ui."}
)

// aliasCallerGroupBIDs and aliasDefaultAccess are test seams over kernel
// helpers that need the auth database (implied groups, sys_model_access).
var (
	aliasCallerGroupBIDs = orm.CallerGroupBIDs
	aliasDefaultAccess   = orm.EnsureDefaultModelAccess
)

func init() {
	orm.RegisterAccessHandler(DiscussAliasModelName, discussAliasAccess)
	orm.RegisterConstraint(DiscussAliasModelName, []string{"alias_model_id"}, validateAliasModelConstraint)
}

// discussAliasAccess keeps the standard ACL check, then limits alias
// mutations by RPC-authenticated callers to base.group_system. Reads stay on
// standard ACL. Internal callers (nil env, no auth mode: seed import, boot)
// and sudo keep the kernel's default semantics, matching ensureRBACModelAccess.
func discussAliasAccess(ctx context.Context, env *orm.Environment, model *orm.ModelMetadata, operation string, ids []string, values orm.RecordMap) error {
	if err := aliasDefaultAccess(ctx, env, model, operation, ids, values); err != nil {
		return err
	}
	if !slices.Contains(aliasMutatingOperations, strings.ToLower(strings.TrimSpace(operation))) {
		return nil
	}
	if env == nil || env.IsSudo() {
		return nil
	}
	if _, ok := env.ContextValue(orm.RBACContextAuthModeKey); !ok {
		return nil
	}
	groups, err := aliasCallerGroupBIDs(orm.ContextWithEnvironment(ctx, env))
	if err != nil {
		return err
	}
	if !slices.Contains(groups, aliasManagerGroupBID) {
		return orm.Forbiddenf("permission denied: only %s can %s %s", aliasManagerGroupBID, operation, DiscussAliasModelName)
	}
	return nil
}

// validateAliasModelConstraint rejects an alias whose alias_model_id is not
// an allowed chatter target, on every create and on writes touching the field.
func validateAliasModelConstraint(ctx context.Context, _ orm.RecordSet, values orm.RecordMap) error {
	modelName, _ := values["alias_model_id"].(string)
	return validateAliasTargetModel(ctx, modelName)
}

// validateAliasTargetModel allows only registered, non-sensitive models that
// declare a chatter on one of their views: those are the models built to
// receive messages, which is exactly what an inbound alias produces.
func validateAliasTargetModel(ctx context.Context, modelName string) error {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return orm.InvalidParamsf("%s: alias_model_id is required", DiscussAliasModelName)
	}
	if aliasModelDenied(modelName) {
		return orm.InvalidParamsf("%s: model %q cannot be an alias target", DiscussAliasModelName, modelName)
	}
	if _, ok := orm.LookupModel(modelName); !ok {
		return orm.InvalidParamsf("%s: model %q is not registered", DiscussAliasModelName, modelName)
	}
	hasChatter, err := modelDeclaresChatter(ctx, orm.BunDBFromContext(ctx), modelName)
	if err != nil {
		return err
	}
	if !hasChatter {
		return orm.InvalidParamsf("%s: model %q has no chatter view and cannot be an alias target", DiscussAliasModelName, modelName)
	}
	return nil
}

func aliasModelDenied(modelName string) bool {
	if slices.Contains(aliasDeniedModels, modelName) {
		return true
	}
	for _, prefix := range aliasDeniedPrefixes {
		if strings.HasPrefix(modelName, prefix) {
			return true
		}
	}
	return false
}

// modelDeclaresChatter reports whether any active ui.view of modelName opts
// into the chatter, either via the use_chatter flag or a literal <chatter
// node in its arch (the two ways resolveStoredUIView docks the panel).
// Flipping use_chatter is itself a ui.* write, i.e. group_system-only.
func modelDeclaresChatter(ctx context.Context, db bun.IDB, modelName string) (bool, error) {
	if db == nil {
		return false, errors.New("tbl.discuss_alias: database is not initialized")
	}
	var found bool
	err := db.NewRaw(`SELECT EXISTS (
		SELECT 1 FROM ui_view
		WHERE model = ? AND active IS NOT FALSE
		  AND (use_chatter OR COALESCE(arch_db, '') LIKE '%<chatter%' OR COALESCE(arch, '') LIKE '%<chatter%'))`, modelName).
		Scan(ctx, &found)
	return found, err
}
