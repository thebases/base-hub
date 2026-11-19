package models

// File overview:
// - Responsibility: keeps sys.oauth_client from being a base.group_system -> superadmin path
//   (plan T17a). A client token runs with the client's role, or bypasses RBAC entirely when
//   is_superadmin is set, so whoever can create, edit or re-key a superadmin-capable client
//   holds superadmin.
// - Policy for non-sudo callers (the standard ACL already limits the model to group_system):
//   is_superadmin may not be set, role_id may not be (or imply) a superadmin group, and a
//   client that is superadmin-capable may not be written, re-keyed or deleted.
// - Pattern: orm.RegisterAccessHandler chained through orm.EnsureDefaultModelAccess. The
//   repository create/write/unlink inside createSysOauthClient, writeSysOauthClient,
//   unlinkSysOauthClient and generate_client_secret pass ids and final values to the handler.

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	basemodels "git.thebase.vn/base_hub/modules/base/models"
	"github.com/uptrace/bun"
)

// Test seams over helpers that need the database.
var (
	oauthClientDefaultAccess       = orm.EnsureDefaultModelAccess
	oauthClientGroupsGrantSuper    = basemodels.GroupsGrantSuperadmin
	oauthClientTargetsSuperCapable = sysOauthClientTargetsSuperadminCapable
)

func init() {
	orm.RegisterAccessHandler(SysOauthClientModelName, sysOauthClientAccess)
}

func sysOauthClientAccess(ctx context.Context, env *orm.Environment, model *orm.ModelMetadata, operation string, ids []string, values orm.RecordMap) error {
	if err := oauthClientDefaultAccess(ctx, env, model, operation, ids, values); err != nil {
		return err
	}
	if operation == "read" || env == nil || env.IsSudo() {
		return nil
	}
	if raw, ok := values["is_superadmin"]; ok && oauthClientTruthy(raw) {
		return orm.Forbiddenf("sys.oauth_client: only a superadmin may set is_superadmin")
	}
	if raw, ok := values["role_id"]; ok {
		roleID, ok := oauthClientIDValue(raw)
		if !ok {
			return orm.InvalidParamsf("sys.oauth_client: invalid role_id %v", raw)
		}
		if roleID > 0 {
			super, err := oauthClientGroupsGrantSuper(ctx, []int64{roleID})
			if err != nil {
				return err
			}
			if super {
				return orm.Forbiddenf("sys.oauth_client: only a superadmin may bind a client to a superadmin role")
			}
		}
	}
	if (operation == "write" || operation == "unlink") && len(ids) > 0 {
		super, err := oauthClientTargetsSuperCapable(ctx, env, ids)
		if err != nil {
			return err
		}
		if super {
			return orm.Forbiddenf("sys.oauth_client: only a superadmin may change, re-key or delete a superadmin client")
		}
	}
	return nil
}

// sysOauthClientTargetsSuperadminCapable reports whether any target client has is_superadmin
// set or a role that grants superadmin. Reads through the caller's DB handle so a transaction
// sees its own rows.
func sysOauthClientTargetsSuperadminCapable(ctx context.Context, env *orm.Environment, ids []string) (bool, error) {
	clientIDs := make([]int64, 0, len(ids))
	for _, id := range ids {
		parsed, err := strconv.ParseInt(strings.TrimSpace(id), 10, 64)
		if err != nil {
			// An id the guard cannot read must not skip the check while the repository resolves it.
			return false, orm.InvalidParamsf("invalid record id %q", id)
		}
		clientIDs = append(clientIDs, parsed)
	}
	db := env.DB()
	if db == nil {
		db = orm.GetBunDB()
	}
	var rows []struct {
		RoleID       int64 `bun:"role_id"`
		IsSuperadmin bool  `bun:"is_superadmin"`
	}
	if err := db.NewSelect().
		TableExpr("sys_oauth_client").
		ColumnExpr("COALESCE(role_id, 0) AS role_id").
		ColumnExpr("COALESCE(is_superadmin, false) AS is_superadmin").
		Where("id IN (?)", bun.In(clientIDs)).
		Scan(ctx, &rows); err != nil {
		return false, fmt.Errorf("sys.oauth_client: load target clients: %w", err)
	}
	roleIDs := make([]int64, 0, len(rows))
	for _, row := range rows {
		if row.IsSuperadmin {
			return true, nil
		}
		if row.RoleID > 0 {
			roleIDs = append(roleIDs, row.RoleID)
		}
	}
	if len(roleIDs) == 0 {
		return false, nil
	}
	return oauthClientGroupsGrantSuper(ctx, roleIDs)
}

func oauthClientTruthy(raw any) bool {
	switch typed := raw.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		value := strings.TrimSpace(strings.ToLower(typed))
		return value != "" && value != "false" && value != "0"
	default:
		// Numbers and anything else: treat non-zero as true rather than let an odd type slip by.
		return fmt.Sprint(raw) != "0"
	}
}

// oauthClientIDValue reads a many2one value: an id, a numeric string, [id, name], or empty.
func oauthClientIDValue(raw any) (int64, bool) {
	switch typed := raw.(type) {
	case nil, bool:
		return 0, true
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		return int64(typed), float64(int64(typed)) == typed
	case []any:
		if len(typed) == 0 {
			return 0, true
		}
		return oauthClientIDValue(typed[0])
	}
	text := strings.TrimSpace(fmt.Sprint(raw))
	if text == "" {
		return 0, true
	}
	id, err := strconv.ParseInt(text, 10, 64)
	return id, err == nil
}
