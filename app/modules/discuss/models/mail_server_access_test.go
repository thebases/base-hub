package models

import (
	"context"
	"errors"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func stubMailServerAccess(t *testing.T, groups ...string) {
	t.Helper()
	prevGroups, prevDefault := mailServerCallerGroupBIDs, mailServerDefaultAccess
	mailServerCallerGroupBIDs = func(context.Context) ([]string, error) { return groups, nil }
	// The base.group_user prefix baseline already passes the default ACL for tbl.*.
	mailServerDefaultAccess = func(context.Context, *orm.Environment, *orm.ModelMetadata, string, []string, orm.RecordMap) error {
		return nil
	}
	t.Cleanup(func() { mailServerCallerGroupBIDs, mailServerDefaultAccess = prevGroups, prevDefault })
}

func mailServerRPCEnv(userID int64) *orm.Environment {
	return orm.NewEnvironment(nil, userID, map[string]any{orm.RBACContextAuthModeKey: "bearer"}, false)
}

func TestDiscussMailServerAccessInternalUserDenied(t *testing.T) {
	stubMailServerAccess(t, "base.group_user", "base.group_multi_company")
	meta := &orm.ModelMetadata{Name: DiscussMailServerModelName}
	for _, op := range []string{"read", "create", "write", "unlink"} {
		err := discussMailServerAccess(context.Background(), mailServerRPCEnv(7), meta, op, nil, nil)
		var ormErr *orm.Error
		if !errors.As(err, &ormErr) || ormErr.Code != orm.ErrForbidden {
			t.Fatalf("%s: err = %v, want forbidden", op, err)
		}
	}
}

func TestDiscussMailServerAccessSystemSudoAndInternalAllowed(t *testing.T) {
	ctx := context.Background()
	meta := &orm.ModelMetadata{Name: DiscussMailServerModelName}

	stubMailServerAccess(t, "base.group_user", "base.group_system")
	for _, op := range []string{"read", "write"} {
		if err := discussMailServerAccess(ctx, mailServerRPCEnv(7), meta, op, nil, nil); err != nil {
			t.Fatalf("group_system %s: %v", op, err)
		}
	}

	stubMailServerAccess(t, "base.group_user")
	if err := discussMailServerAccess(ctx, orm.NewEnvironment(nil, 7, nil, true), meta, "read", nil, nil); err != nil {
		t.Fatalf("sudo read: %v", err)
	}
	if err := discussMailServerAccess(ctx, orm.NewEnvironment(nil, 0, nil, false), meta, "read", nil, nil); err != nil {
		t.Fatalf("internal read: %v", err)
	}
	if err := discussMailServerAccess(ctx, nil, meta, "read", nil, nil); err != nil {
		t.Fatalf("nil env read: %v", err)
	}
}
