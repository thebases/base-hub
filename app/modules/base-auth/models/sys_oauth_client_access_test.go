package models

import (
	"context"
	"errors"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

const testSuperRoleID = 99

func stubOauthClientAccess(t *testing.T, targetsSuper bool) {
	t.Helper()
	prevDefault, prevGroups, prevTargets := oauthClientDefaultAccess, oauthClientGroupsGrantSuper, oauthClientTargetsSuperCapable
	oauthClientDefaultAccess = func(context.Context, *orm.Environment, *orm.ModelMetadata, string, []string, orm.RecordMap) error {
		return nil
	}
	oauthClientGroupsGrantSuper = func(_ context.Context, ids []int64) (bool, error) {
		for _, id := range ids {
			if id == testSuperRoleID {
				return true, nil
			}
		}
		return false, nil
	}
	oauthClientTargetsSuperCapable = func(context.Context, *orm.Environment, []string) (bool, error) { return targetsSuper, nil }
	t.Cleanup(func() {
		oauthClientDefaultAccess, oauthClientGroupsGrantSuper, oauthClientTargetsSuperCapable = prevDefault, prevGroups, prevTargets
	})
}

func oauthSystemEnv() *orm.Environment {
	return orm.NewEnvironment(nil, 7, map[string]any{orm.RBACContextAuthModeKey: "bearer"}, false)
}

func wantForbidden(t *testing.T, label string, err error) {
	t.Helper()
	var ormErr *orm.Error
	if !errors.As(err, &ormErr) || ormErr.Code != orm.ErrForbidden {
		t.Fatalf("%s: err = %v, want forbidden", label, err)
	}
}

func TestSysOauthClientAccessBlocksSuperadminGrants(t *testing.T) {
	stubOauthClientAccess(t, false)
	ctx := context.Background()
	meta := &orm.ModelMetadata{Name: SysOauthClientModelName}

	wantForbidden(t, "create is_superadmin", sysOauthClientAccess(ctx, oauthSystemEnv(), meta, "create", nil, orm.RecordMap{"is_superadmin": true}))
	wantForbidden(t, "create superadmin role", sysOauthClientAccess(ctx, oauthSystemEnv(), meta, "create", nil, orm.RecordMap{"role_id": []any{float64(testSuperRoleID), "Superadmin"}}))
	wantForbidden(t, "write superadmin role", sysOauthClientAccess(ctx, oauthSystemEnv(), meta, "write", []string{"3"}, orm.RecordMap{"role_id": testSuperRoleID}))

	// Ordinary clients stay manageable by group_system.
	if err := sysOauthClientAccess(ctx, oauthSystemEnv(), meta, "create", nil, orm.RecordMap{"is_superadmin": false, "role_id": int64(5)}); err != nil {
		t.Fatalf("create ordinary client: %v", err)
	}
	if err := sysOauthClientAccess(ctx, oauthSystemEnv(), meta, "write", []string{"3"}, orm.RecordMap{"client_secret_hash": "x"}); err != nil {
		t.Fatalf("re-key ordinary client: %v", err)
	}
	// Sudo (superadmin) may do anything.
	if err := sysOauthClientAccess(ctx, orm.NewEnvironment(nil, 1, nil, true), meta, "create", nil, orm.RecordMap{"is_superadmin": true}); err != nil {
		t.Fatalf("sudo create: %v", err)
	}
}

func TestSysOauthClientAccessProtectsSuperadminClients(t *testing.T) {
	stubOauthClientAccess(t, true)
	ctx := context.Background()
	meta := &orm.ModelMetadata{Name: SysOauthClientModelName}

	// generate_client_secret writes client_secret_hash on the target: taking over its secret.
	wantForbidden(t, "re-key superadmin client", sysOauthClientAccess(ctx, oauthSystemEnv(), meta, "write", []string{"3"}, orm.RecordMap{"client_secret_hash": "x"}))
	wantForbidden(t, "unlink superadmin client", sysOauthClientAccess(ctx, oauthSystemEnv(), meta, "unlink", []string{"3"}, nil))
	if err := sysOauthClientAccess(ctx, oauthSystemEnv(), meta, "read", []string{"3"}, nil); err != nil {
		t.Fatalf("read stays on ACL: %v", err)
	}
}
