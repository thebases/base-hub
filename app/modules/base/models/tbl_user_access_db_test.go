package models

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	testutil "git.thebase.vn/base_hub/internal/test"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

// Fixture groups: 1 user <- 2 multi_company <- 3 system (implied chain), 4 no_one (superadmin
// flag), 5 a plain-looking group that implies no_one. Users: 10 internal, 11 system, 12 superadmin.
const (
	accessGroupUser  = 1
	accessGroupSys   = 3
	accessGroupNoOne = 4
	accessGroupWraps = 5
	accessUserPlain  = 10
	accessUserSystem = 11
	accessUserAdmin  = 12
)

// setupUserAccessDB pins a pool to a throwaway schema holding just the tables the T5 guard reads
// (tbl_groups, tbl_group_implied_rel, base_users).
func setupUserAccessDB(t *testing.T) context.Context {
	t.Helper()
	cfg := testutil.EnvPostgresConfig()
	ctx := context.Background()
	admin, err := testutil.OpenAdminConn(ctx, cfg)
	if err != nil {
		t.Skipf("open postgres fixture: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close(ctx) })
	schema := fmt.Sprintf("user_access_test_%d", time.Now().UnixNano())
	for _, stmt := range []string{
		"CREATE SCHEMA " + schema,
		"CREATE TABLE " + schema + ".tbl_groups (id BIGINT PRIMARY KEY, bid TEXT, name TEXT, superadmin BOOLEAN NOT NULL DEFAULT false)",
		"CREATE TABLE " + schema + ".tbl_group_implied_rel (gid BIGINT NOT NULL, hid BIGINT NOT NULL)",
		"CREATE TABLE " + schema + ".base_users (id BIGINT PRIMARY KEY, group_id BIGINT)",
		"INSERT INTO " + schema + ".tbl_groups (id, bid, name, superadmin) VALUES " +
			"(1, 'base.group_user', 'User', false), (2, 'base.group_multi_company', 'Multi', false), " +
			"(3, 'base.group_system', 'System', false), (4, 'base.group_no_one', 'Technical', true), (5, 'x.wrapper', 'Wrapper', false)",
		"INSERT INTO " + schema + ".tbl_group_implied_rel (gid, hid) VALUES (2, 1), (3, 2), (5, 4)",
		"INSERT INTO " + schema + ".base_users (id, group_id) VALUES (10, 1), (11, 3), (12, 4)",
	} {
		if _, err := admin.Exec(ctx, stmt); err != nil {
			t.Skipf("%s: %v", stmt, err)
		}
	}
	t.Cleanup(func() { _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE") })

	poolCfg, err := pgxpool.ParseConfig(fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Name, cfg.SSLMode))
	if err != nil {
		t.Fatal(err)
	}
	poolCfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	poolCfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatal(err)
	}
	bunDB := &orm.BunDB{DB: bun.NewDB(stdlib.OpenDBFromPool(pool), pgdialect.New()), Pool: pool}
	t.Cleanup(func() { _ = bunDB.Close() })
	testutil.SwapBunDB(t, bunDB)
	return ctx
}

// accessCaller binds an authenticated-looking caller holding groupID as its primary role.
func accessCaller(ctx context.Context, userID, groupID int64, sudo bool) (context.Context, *orm.Environment) {
	// The swapped default DB stands in for the request-bound tenant DB: base never falls back to it.
	env := orm.NewEnvironment(orm.GetBunDB(), userID, map[string]any{
		orm.RBACContextRoleIDKey:   groupID,
		orm.RBACContextGroupIDsKey: []int64{groupID},
	}, sudo)
	return orm.ContextWithEnvironment(ctx, env), env
}

func requireForbidden(t *testing.T, err error, what string) {
	t.Helper()
	var ormErr *orm.Error
	if !errors.As(err, &ormErr) || ormErr.Code != orm.ErrForbidden {
		t.Fatalf("%s: err = %v, want Forbidden", what, err)
	}
}

func requireAllowed(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: err = %v, want nil", what, err)
	}
}

func TestResUsersGuardInternalUser(t *testing.T) {
	ctx, env := accessCaller(setupUserAccessDB(t), accessUserPlain, accessGroupUser, false)
	self := []string{"10"}

	for _, field := range resUsersPrivilegedFields {
		err := ensureResUsersMutationAllowed(ctx, env, "write", self, orm.RecordMap{field: int64(accessGroupNoOne)})
		requireForbidden(t, err, "own "+field)
	}
	requireForbidden(t, ensureResUsersMutationAllowed(ctx, env, "write", []string{"12"}, orm.RecordMap{"password": "$2a$hash"}), "another user's password")
	requireForbidden(t, ensureResUsersMutationAllowed(ctx, env, "write", []string{"10", "11"}, orm.RecordMap{"password": "$2a$hash"}), "own plus another password")
	requireForbidden(t, ensureResUsersMutationAllowed(ctx, env, "write", []string{"11"}, orm.RecordMap{"api_key": "digest"}), "another user's api_key")
	requireForbidden(t, ensureResUsersMutationAllowed(ctx, env, "create", nil, orm.RecordMap{"login": "x"}), "create")
	requireForbidden(t, ensureResUsersMutationAllowed(ctx, env, "unlink", []string{"11"}, nil), "unlink")

	// Self-service keeps working: own password/API key and the preferences fields.
	requireAllowed(t, ensureResUsersMutationAllowed(ctx, env, "write", self, orm.RecordMap{"password": "$2a$hash"}), "own password")
	requireAllowed(t, ensureResUsersMutationAllowed(ctx, env, "write", self, orm.RecordMap{"api_key": "digest"}), "own api_key")
	profile := orm.RecordMap{"name": "Me", "email": "me@x", "lang_id": "en_us", "tz": "UTC", "signature": "s", "company_id": int64(1), "phone": "1"}
	requireAllowed(t, ensureResUsersMutationAllowed(ctx, env, "write", self, profile), "own profile")
	requireAllowed(t, ensureResUsersMutationAllowed(ctx, env, "write", self, nil), "dispatcher pre-check")
}

func TestResUsersGuardSystemCannotReachSuperadmin(t *testing.T) {
	ctx, env := accessCaller(setupUserAccessDB(t), accessUserSystem, accessGroupSys, false)
	plain := []string{"10"}

	requireAllowed(t, ensureResUsersMutationAllowed(ctx, env, "write", plain,
		orm.RecordMap{"group_id": int64(accessGroupSys), "active": false, "company_ids": []int64{1, 2}, "password": "$2a$hash", "api_key": nil}), "manage plain user")
	requireAllowed(t, ensureResUsersMutationAllowed(ctx, env, "create", nil, orm.RecordMap{"login": "new", "group_id": int64(accessGroupUser)}), "create user")
	requireAllowed(t, ensureResUsersMutationAllowed(ctx, env, "unlink", plain, nil), "unlink plain user")

	requireForbidden(t, ensureResUsersMutationAllowed(ctx, env, "write", plain, orm.RecordMap{"group_id": int64(accessGroupNoOne)}), "grant no_one")
	requireForbidden(t, ensureResUsersMutationAllowed(ctx, env, "write", []string{"11"}, orm.RecordMap{"group_id": []any{float64(accessGroupNoOne), "Technical"}}), "grant no_one to self as tuple")
	requireForbidden(t, ensureResUsersMutationAllowed(ctx, env, "write", plain, orm.RecordMap{"group_id": "5"}), "grant a group implying no_one")
	requireForbidden(t, ensureResUsersMutationAllowed(ctx, env, "create", nil, orm.RecordMap{"login": "x", "group_id": int64(accessGroupNoOne)}), "create superadmin")
	requireForbidden(t, ensureResUsersMutationAllowed(ctx, env, "write", []string{"12"}, orm.RecordMap{"password": "$2a$hash"}), "superadmin password")
	requireForbidden(t, ensureResUsersMutationAllowed(ctx, env, "write", []string{"12"}, orm.RecordMap{"active": false}), "deactivate superadmin")
	requireForbidden(t, ensureResUsersMutationAllowed(ctx, env, "unlink", []string{"12"}, nil), "unlink superadmin")
	// Any edit of a superadmin row (login rename locks it out) needs sudo; plain rows stay open.
	requireForbidden(t, ensureResUsersMutationAllowed(ctx, env, "write", []string{"12"}, orm.RecordMap{"login": "gone"}), "rename superadmin login")
	requireForbidden(t, ensureResUsersMutationAllowed(ctx, env, "write", []string{"12"}, orm.RecordMap{"name": "Admin"}), "rename superadmin")
	requireAllowed(t, ensureResUsersMutationAllowed(ctx, env, "write", plain, orm.RecordMap{"login": "renamed", "partner_id": int64(7)}), "edit plain user login")
}

func TestResUsersGuardOtherRowsNeedSystem(t *testing.T) {
	ctx, env := accessCaller(setupUserAccessDB(t), accessUserPlain, accessGroupUser, false)
	for _, values := range []orm.RecordMap{{"login": "gone"}, {"email": "x@evil"}, {"partner_id": int64(7)}, {"name": "x"}, {}} {
		requireForbidden(t, ensureResUsersMutationAllowed(ctx, env, "write", []string{"12"}, values), fmt.Sprintf("other row %v", values))
	}
	// writeResUsers checks ownership before splitting off delegated partner fields.
	_, err := writeResUsers(ctx, nil, []string{"11"}, []any{map[string]any{"email": "x@evil"}}, nil)
	requireForbidden(t, err, "writeResUsers partner-delegated field on another user")
	_, err = preferenceSaveResUsers(ctx, nil, []string{"12"}, []any{map[string]any{"name": "x"}}, nil)
	requireForbidden(t, err, "preference_save on another user")
}

func TestResUsersGuardClientCredentialsOwnNoRow(t *testing.T) {
	base := setupUserAccessDB(t)
	clientEnv := func(clientID, groupID int64) (context.Context, *orm.Environment) {
		env := orm.NewEnvironment(orm.GetBunDB(), clientID, map[string]any{
			orm.RBACContextRoleIDKey:   groupID,
			orm.RBACContextGroupIDsKey: []int64{groupID},
			orm.RBACContextAuthModeKey: resAuthModeClientCredentials,
		}, false)
		return orm.ContextWithEnvironment(base, env), env
	}
	// Client row id 10 collides with user 10: still not the client's own row.
	ctx, env := clientEnv(accessUserPlain, accessGroupUser)
	requireForbidden(t, ensureResUsersMutationAllowed(ctx, env, "write", []string{"10"}, orm.RecordMap{"password": "$2a$hash"}), "group_user client password")
	requireForbidden(t, ensureResUsersMutationAllowed(ctx, env, "write", []string{"10"}, orm.RecordMap{"api_key": "digest"}), "group_user client api_key")
	// Client row id 12 collides with the superadmin: the superadmin check still applies.
	ctx, env = clientEnv(accessUserAdmin, accessGroupSys)
	requireForbidden(t, ensureResUsersMutationAllowed(ctx, env, "write", []string{"12"}, orm.RecordMap{"password": "$2a$hash"}), "system client superadmin password")
	requireAllowed(t, ensureResUsersMutationAllowed(ctx, env, "write", []string{"10"}, orm.RecordMap{"password": "$2a$hash"}), "system client plain user password")
}

func TestResUsersGuardSudoMayGrantSuperadmin(t *testing.T) {
	ctx, env := accessCaller(setupUserAccessDB(t), 0, 0, true)
	requireAllowed(t, ensureResUsersMutationAllowed(ctx, env, "write", []string{"10"}, orm.RecordMap{"group_id": int64(accessGroupNoOne)}), "sudo grant")
	requireAllowed(t, ensureResUsersMutationAllowed(ctx, env, "write", []string{"12"}, orm.RecordMap{"password": "$2a$hash"}), "sudo superadmin password")
}

func TestResUsersMethodsRejectInternalUser(t *testing.T) {
	ctx, _ := accessCaller(setupUserAccessDB(t), accessUserPlain, accessGroupUser, false)
	_, err := createResUsers(ctx, nil, nil, []any{map[string]any{"login": "x", "name": "x"}}, nil)
	requireForbidden(t, err, "createResUsers")
	_, err = toggleActiveResUsers(ctx, nil, []string{"10"}, nil, nil)
	requireForbidden(t, err, "toggle_active")

	sysCtx, _ := accessCaller(ctx, accessUserSystem, accessGroupSys, false)
	_, err = createResUsers(sysCtx, nil, nil, []any{map[string]any{"login": "x", "group_id": int64(accessGroupNoOne)}}, nil)
	requireForbidden(t, err, "createResUsers superadmin by group_system")
	_, err = toggleActiveResUsers(sysCtx, nil, nil, []any{[]any{float64(accessUserAdmin)}}, nil)
	requireForbidden(t, err, "toggle_active superadmin by group_system (ids in args)")
	_, err = toggleActiveResUsers(sysCtx, nil, nil, nil, map[string]any{"ids": []any{"12"}})
	requireForbidden(t, err, "toggle_active superadmin by group_system (string ids in kwargs)")
	_, err = toggleActiveResUsers(sysCtx, nil, nil, nil, map[string]any{"ids": []any{float64(accessUserAdmin)}})
	requireForbidden(t, err, "toggle_active superadmin by group_system (numeric ids in kwargs)")
	_, err = toggleActiveResUsers(sysCtx, nil, nil, nil, nil)
	var ormErr *orm.Error
	if !errors.As(err, &ormErr) || ormErr.Code != orm.ErrInvalidParams {
		t.Fatalf("toggle_active without ids: err = %v, want InvalidParams", err)
	}
}

func TestResGroupsGuard(t *testing.T) {
	base := setupUserAccessDB(t)
	model := &orm.ModelMetadata{Name: ResGroupModelName}

	userCtx, userEnv := accessCaller(base, accessUserPlain, accessGroupUser, false)
	requireAllowed(t, resGroupsAccess(userCtx, userEnv, model, "read", nil, nil), "user read")
	for _, op := range []string{"create", "write", "unlink"} {
		requireForbidden(t, resGroupsAccess(userCtx, userEnv, model, op, []string{"1"}, orm.RecordMap{"name": "x"}), "user "+op)
	}

	sysCtx, sysEnv := accessCaller(base, accessUserSystem, accessGroupSys, false)
	requireAllowed(t, resGroupsAccess(sysCtx, sysEnv, model, "write", []string{"1"}, orm.RecordMap{"name": "Users"}), "system rename")
	requireAllowed(t, resGroupsAccess(sysCtx, sysEnv, model, "unlink", []string{"2"}, nil), "system unlink")
	requireAllowed(t, resGroupsAccess(sysCtx, sysEnv, model, "create", nil,
		orm.RecordMap{"name": "Sales", "implied_ids": []any{}, "superadmin": false}), "system create from an empty form")
	requireForbidden(t, resGroupsAccess(sysCtx, sysEnv, model, "write", []string{"1"}, orm.RecordMap{"implied_ids": []any{float64(accessGroupNoOne)}}), "system implied_ids")
	requireForbidden(t, resGroupsAccess(sysCtx, sysEnv, model, "write", []string{"4"}, orm.RecordMap{"implied_by_ids": []any{}}), "system implied_by_ids")
	requireForbidden(t, resGroupsAccess(sysCtx, sysEnv, model, "write", []string{"1"}, orm.RecordMap{"superadmin": true}), "system superadmin flag")
	requireForbidden(t, resGroupsAccess(sysCtx, sysEnv, model, "create", nil, orm.RecordMap{"name": "x", "implied_ids": []any{float64(accessGroupNoOne)}}), "system create implying no_one")
	// Groups that grant superadmin (no_one itself, or 5 which implies it) are sudo-only.
	requireForbidden(t, resGroupsAccess(sysCtx, sysEnv, model, "unlink", []string{"4"}, nil), "system unlink no_one")
	requireForbidden(t, resGroupsAccess(sysCtx, sysEnv, model, "write", []string{"4"}, orm.RecordMap{"bid": "x.renamed"}), "system re-bid no_one")
	requireForbidden(t, resGroupsAccess(sysCtx, sysEnv, model, "write", []string{"4"}, orm.RecordMap{"name": "x"}), "system rename no_one")
	requireForbidden(t, resGroupsAccess(sysCtx, sysEnv, model, "write", []string{"5"}, orm.RecordMap{"name": "x"}), "system rename group implying no_one")
	requireForbidden(t, resGroupsAccess(sysCtx, sysEnv, model, "write", []string{"4"}, nil), "system dispatcher pre-check on no_one")
}

func TestApplyResUsersGeneratedPasswordHasNoFixedDefault(t *testing.T) {
	first, second := map[string]any{"login": "a"}, map[string]any{"login": "b"}
	p1, err := applyResUsersGeneratedPassword(first)
	if err != nil {
		t.Fatal(err)
	}
	p2, _ := applyResUsersGeneratedPassword(second)
	if len(p1) != resUsersGeneratedPasswordLength || p1 == p2 || p1 == "12345678" || first["password"] != p1 {
		t.Fatalf("generated passwords %q/%q are not random %d-char values", p1, p2, resUsersGeneratedPasswordLength)
	}
	if p, _ := applyResUsersGeneratedPassword(map[string]any{"active": false}); p != "" {
		t.Fatalf("inactive user got password %q", p)
	}
	given := map[string]any{"password": "chosen-pass"}
	if p, _ := applyResUsersGeneratedPassword(given); p != "" || given["password"] != "chosen-pass" {
		t.Fatalf("explicit password replaced: %q, %v", p, given["password"])
	}
	if strings.Contains(fmt.Sprint(given), "12345678") {
		t.Fatal("fixed default password leaked into payload")
	}
}
