package models

// File overview:
// - Responsibility: guards privileged base.users and tbl.groups mutations (plan T5). Owner decision
//   D1 keeps the blanket base.group_user CRUD grant on tbl.*/base.*, so these two models protect
//   themselves: role, company scope, account state and credentials of other users are
//   base.group_system business, and anything that reaches base.group_no_one (superadmin) is
//   sudo-only.
// - Key symbols: `resUsersAccess`, `ensureResUsersMutationAllowed`, `resGroupsAccess`,
//   `resGroupsGrantSuperadmin`, `toggleActiveResUsers`.
// - Pattern: registered with orm.RegisterAccessHandler and chained through
//   orm.EnsureDefaultModelAccess, so the standard ACL still runs first. The repository create/write
//   paths pass the final values to the handler, which covers createResUsers/writeResUsers and the
//   generic create paths (copy, name_create, find_or_create) alike. Sudo never reaches a handler.

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"github.com/uptrace/bun"
)

const (
	resGroupSystemBID = "base.group_system"
	resGroupNoOneBID  = "base.group_no_one"
	// resAuthModeClientCredentials is base-auth's auth mode for client_credentials tokens
	// (modules/base-auth/service/builtin_provider.go authModeClientCredentials).
	resAuthModeClientCredentials = "oauth_client_credentials"
)

type resCallerTier int

const (
	resCallerUser resCallerTier = iota
	resCallerSystem
	resCallerSudo
)

// resUsersPrivilegedFields widen what a login can reach (role, company scope, account state,
// legacy access flags) or hold auth material; only base.group_system may set them. company_id is
// absent on purpose: ensureResUsersCompanyScopeConsistent keeps it inside company_ids, so it
// cannot widen access and the preferences page may switch it.
var resUsersPrivilegedFields = []string{
	"group_id", "company_ids", "active", "alldevicesavailable", "allconfigavailable",
	"passwordreset", "passwordresettoken", "authdata", "twofactorsecret", "twofactoraccepted",
}

// resUsersCredentialFields may be changed on the caller's own row; on another user's row they
// require base.group_system (and sudo when that user is a superadmin).
var resUsersCredentialFields = []string{"password", "api_key"}

// resGroupsSudoFields decide who is superadmin (superadmin flag, implied chain).
var resGroupsSudoFields = []string{"implied_ids", "implied_by_ids", "superadmin"}

func init() {
	orm.RegisterAccessHandler(ResUserModelName, resUsersAccess)
	orm.RegisterAccessHandler(ResGroupModelName, resGroupsAccess)
}

func resUsersAccess(ctx context.Context, env *orm.Environment, model *orm.ModelMetadata, operation string, ids []string, values orm.RecordMap) error {
	if err := orm.EnsureDefaultModelAccess(ctx, env, model, operation, ids, values); err != nil {
		return err
	}
	return ensureResUsersMutationAllowed(ctx, env, operation, ids, values)
}

// ensureResUsersMutationAllowed applies the base.users field policy for one operation. values
// are the values about to be persisted (password/api_key already hashed, read masks dropped).
func ensureResUsersMutationAllowed(ctx context.Context, env *orm.Environment, operation string, ids []string, values orm.RecordMap) error {
	if operation == "read" || env.IsSudo() {
		return nil
	}
	// The dispatcher pre-check has no values; the field check runs at the repository write
	// (and in toggleActiveResUsers, the one standard write that never passes values).
	if operation == "write" && values == nil {
		return nil
	}
	privileged := presentFields(values, resUsersPrivilegedFields)
	credential := len(presentFields(values, resUsersCredentialFields)) > 0
	others := resUsersOtherIDs(ids, resCallerSelfID(env))
	// Plain edits of the caller's own row (name, lang, tz, signature, ...) skip the group lookup.
	if operation == "write" && len(privileged) == 0 && !credential && len(others) == 0 {
		return nil
	}
	tier, err := resCallerTierOf(ctx, env)
	if err != nil {
		return err
	}
	switch operation {
	case "create":
		// A new login always carries role, company scope and active state, so creating one is
		// user management, not a self-service action.
		if tier < resCallerSystem {
			return orm.Forbiddenf("base.users: creating users requires %s", resGroupSystemBID)
		}
		return ensureResUsersGroupGrantAllowed(ctx, values)
	case "unlink":
		if tier < resCallerSystem {
			return orm.Forbiddenf("base.users: deleting users requires %s", resGroupSystemBID)
		}
		return ensureResUsersTargetsNotSuperadmin(ctx, ids)
	}
	if tier < resCallerSystem && len(privileged) > 0 {
		return orm.Forbiddenf("base.users: only %s may change %s", resGroupSystemBID, strings.Join(privileged, ", "))
	}
	// Any field of another user's row (login, email, partner_id, password, ...) is user
	// management: renaming the admin's login locks it out, repointing email/partner_id
	// redirects its mail.
	if tier < resCallerSystem && len(others) > 0 {
		return orm.Forbiddenf("base.users: changing another user requires %s", resGroupSystemBID)
	}
	if err := ensureResUsersGroupGrantAllowed(ctx, values); err != nil {
		return err
	}
	// group_system is not superadmin: changing a superadmin's row (e.g. resetting its password
	// or renaming its login) would hand the caller superadmin or lock it out.
	return ensureResUsersTargetsNotSuperadmin(ctx, others)
}

// ensureResUsersGroupGrantAllowed rejects a group_id that is, or implies, a superadmin group.
func ensureResUsersGroupGrantAllowed(ctx context.Context, values orm.RecordMap) error {
	raw, ok := values["group_id"]
	if !ok {
		return nil
	}
	groupID, ok := resGroupIDValue(raw)
	if !ok {
		// An unparseable value could still resolve to a group downstream; refuse rather than
		// let it skip the superadmin check.
		return orm.InvalidParamsf("base.users: invalid group_id %v", raw)
	}
	if groupID <= 0 {
		return nil
	}
	superadmin, err := resGroupsGrantSuperadmin(ctx, []int64{groupID})
	if err != nil {
		return err
	}
	if superadmin {
		return orm.Forbiddenf("base.users: only a superadmin may grant %s", resGroupNoOneBID)
	}
	return nil
}

// ensureResUsersTargetsNotSuperadmin rejects a change to any user whose role reaches superadmin.
func ensureResUsersTargetsNotSuperadmin(ctx context.Context, ids []string) error {
	userIDs, err := resParseIDs(ids)
	if err != nil || len(userIDs) == 0 {
		return err
	}
	superadmin, err := resSuperadminClosure(ctx,
		"SELECT g.id FROM tbl_groups AS g JOIN base_users AS u ON u.group_id::text = g.id::text WHERE u.id IN (?)", userIDs)
	if err != nil {
		return err
	}
	if superadmin {
		return orm.Forbiddenf("base.users: only a superadmin may change a superadmin user's role, state or credentials")
	}
	return nil
}

// resGroupsGrantSuperadmin reports whether any of groupIDs is, or transitively implies, a group
// that makes its members superadmin (the superadmin flag, or base.group_no_one by BID). Effective
// membership is group_id plus the implied chain, the same expansion enrichRBACUserRole uses.
func resGroupsGrantSuperadmin(ctx context.Context, groupIDs []int64) (bool, error) {
	if len(groupIDs) == 0 {
		return false, nil
	}
	return resSuperadminClosure(ctx, "SELECT id FROM tbl_groups WHERE id IN (?)", groupIDs)
}

// GroupsGrantSuperadmin is resGroupsGrantSuperadmin for other modules (base-auth guards
// sys.oauth_client role_id with it), so "which groups make a caller superadmin" has one owner.
func GroupsGrantSuperadmin(ctx context.Context, groupIDs []int64) (bool, error) {
	return resGroupsGrantSuperadmin(ctx, groupIDs)
}

// resSuperadminClosure expands the seed group set through tbl_group_implied_rel (gid implies hid)
// and checks it for a superadmin group. seedSQL is a package constant, never caller input.
func resSuperadminClosure(ctx context.Context, seedSQL string, seedIDs []int64) (bool, error) {
	query := `WITH RECURSIVE implied AS (` + seedSQL + `
		UNION
		SELECT r.hid FROM tbl_group_implied_rel AS r JOIN implied AS i ON r.gid = i.id
	)
	SELECT EXISTS (SELECT 1 FROM tbl_groups AS t JOIN implied AS i ON i.id = t.id WHERE t.superadmin OR t.bid = ?)`
	db, err := runtimeDBForModel(ctx)
	if err != nil {
		return false, err
	}
	var found bool
	if err := db.NewRaw(query, bun.In(seedIDs), resGroupNoOneBID).Scan(ctx, &found); err != nil {
		return false, fmt.Errorf("resolve superadmin groups: %w", err)
	}
	return found, nil
}

// resCallerTierOf classifies the caller. Group membership honours implied groups, so a group that
// implies base.group_system counts. A caller without an RBAC identity holds no group: it is a
// plain user here, so internal writers must run sudo (boot seeds, CLI and provisioning already do).
func resCallerTierOf(ctx context.Context, env *orm.Environment) (resCallerTier, error) {
	if env.IsSudo() {
		return resCallerSudo, nil
	}
	bids, err := orm.CallerGroupBIDs(ctx)
	if err != nil {
		return resCallerUser, err
	}
	if slices.Contains(bids, resGroupSystemBID) {
		return resCallerSystem, nil
	}
	return resCallerUser, nil
}

func resGroupsAccess(ctx context.Context, env *orm.Environment, model *orm.ModelMetadata, operation string, ids []string, values orm.RecordMap) error {
	if err := orm.EnsureDefaultModelAccess(ctx, env, model, operation, ids, values); err != nil {
		return err
	}
	if operation == "read" || env.IsSudo() {
		return nil
	}
	tier, err := resCallerTierOf(ctx, env)
	if err != nil {
		return err
	}
	if tier < resCallerSystem {
		return orm.Forbiddenf("tbl.groups: changing groups requires %s", resGroupSystemBID)
	}
	// Renaming, re-bidding or deleting a group that grants superadmin (base.group_no_one, or one
	// implying it) would strip superadmin from every member, so only a superadmin may touch it.
	if operation == "write" || operation == "unlink" {
		groupIDs, err := resParseIDs(ids)
		if err != nil {
			return err
		}
		superadmin, err := resGroupsGrantSuperadmin(ctx, groupIDs)
		if err != nil {
			return err
		}
		if superadmin {
			return orm.Forbiddenf("tbl.groups: only a superadmin may change a group that grants superadmin")
		}
	}
	// The implied chain and the superadmin flag decide who is superadmin, so only a superadmin
	// may change them. A create carrying empty values (a form in create mode) changes nothing.
	for _, field := range resGroupsSudoFields {
		raw, ok := values[field]
		if !ok || (operation == "create" && resGroupValueEmpty(raw)) {
			continue
		}
		return orm.Forbiddenf("tbl.groups: only a superadmin may change %s", field)
	}
	return nil
}

// toggleActiveResUsers guards the standard toggle_active, which persists `active` without handing
// the values to the access hook, then delegates to it.
func toggleActiveResUsers(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	targets := resStandardTargetIDs(ids, args, kwargs)
	if len(targets) == 0 {
		// The standard method would fail too; refusing here also means no id source can be
		// missed by the guard and still reach the toggle.
		return nil, orm.InvalidParamsf("base.users.toggle_active requires target ids")
	}
	if err := ensureResUsersMutationAllowed(ctx, orm.EnvironmentFromContext(ctx), "write", targets, orm.RecordMap{"active": true}); err != nil {
		return nil, err
	}
	standard, ok := orm.LookupRuntimeMethod("toggle_active")
	if !ok || standard.Call == nil {
		return nil, fmt.Errorf("base.users.toggle_active: standard method is not registered")
	}
	return standard.Call(ctx, model, ids, args, kwargs)
}

func presentFields(values orm.RecordMap, fields []string) []string {
	var out []string
	for _, field := range fields {
		if _, ok := values[field]; ok {
			out = append(out, field)
		}
	}
	return out
}

// resCallerSelfID is the base.users id the caller owns, 0 when none. A client_credentials token
// runs with uid = its sys.oauth_client id, which is not a base.users id, so it owns no row.
func resCallerSelfID(env *orm.Environment) int64 {
	if mode, _ := env.ContextValue(orm.RBACContextAuthModeKey); mode == resAuthModeClientCredentials {
		return 0
	}
	return env.UserID()
}

// resUsersOtherIDs returns the ids that are not the caller's own row (all of them when selfID <= 0).
func resUsersOtherIDs(ids []string, selfID int64) []string {
	var out []string
	for _, id := range ids {
		if parsed, err := strconv.ParseInt(strings.TrimSpace(id), 10, 64); err != nil || parsed != selfID || selfID <= 0 {
			out = append(out, id)
		}
	}
	return out
}

// resParseIDs parses record ids strictly: an id the guard cannot read must not skip the
// superadmin check while the repository still resolves it.
func resParseIDs(ids []string) ([]int64, error) {
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		parsed, err := strconv.ParseInt(strings.TrimSpace(id), 10, 64)
		if err != nil {
			return nil, orm.InvalidParamsf("invalid record id %q", id)
		}
		out = append(out, parsed)
	}
	return out, nil
}

// resStandardTargetIDs mirrors orm's runtimeIDsArg (path ids, then args[0], then kwargs["ids"])
// so toggleActiveResUsers guards exactly the rows the standard toggle_active will touch. It also
// accepts float64 JSON numbers, which only ever widens what gets checked.
func resStandardTargetIDs(ids []string, args []any, kwargs map[string]any) []string {
	if len(ids) > 0 {
		return ids
	}
	if parsed := resIDStrings(argAt(args, 0)); len(parsed) > 0 {
		return parsed
	}
	return resIDStrings(kwargs["ids"])
}

func resIDStrings(raw any) []string {
	switch typed := raw.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			switch v := item.(type) {
			case string:
				if strings.TrimSpace(v) != "" {
					out = append(out, v)
				}
			case fmt.Stringer:
				out = append(out, v.String())
			case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
				out = append(out, fmt.Sprint(v))
			case float64:
				out = append(out, strconv.FormatInt(int64(v), 10))
			}
		}
		return out
	}
	return nil
}

// resGroupIDValue parses a many2one group value: id, numeric string, json.Number, [id, name]
// tuple; nil/false mean "no group".
func resGroupIDValue(raw any) (int64, bool) {
	switch typed := raw.(type) {
	case nil, bool:
		return 0, true
	case []any:
		if len(typed) == 0 {
			return 0, true
		}
		return resGroupIDValue(typed[0])
	}
	if id, ok := int64FromAny(raw); ok {
		return id, true
	}
	id, err := strconv.ParseInt(strings.TrimSpace(fmt.Sprint(raw)), 10, 64)
	return id, err == nil
}

func resGroupValueEmpty(raw any) bool {
	switch typed := raw.(type) {
	case nil:
		return true
	case bool:
		return !typed
	}
	return len(int64SliceFromAny(raw)) == 0
}
