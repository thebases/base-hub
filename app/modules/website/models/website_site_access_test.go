package models

import (
	"context"
	"errors"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func stubWebsiteSiteAccess(t *testing.T, groups ...string) {
	t.Helper()
	prevGroups, prevDefault := websiteSiteCallerGroupBIDs, websiteSiteDefaultAccess
	websiteSiteCallerGroupBIDs = func(context.Context) ([]string, error) { return groups, nil }
	// The base.group_user prefix baseline already passes the default ACL for tbl.*.
	websiteSiteDefaultAccess = func(context.Context, *orm.Environment, *orm.ModelMetadata, string, []string, orm.RecordMap) error {
		return nil
	}
	t.Cleanup(func() { websiteSiteCallerGroupBIDs, websiteSiteDefaultAccess = prevGroups, prevDefault })
}

func websiteRPCEnv(userID int64) *orm.Environment {
	return orm.NewEnvironment(nil, userID, map[string]any{orm.RBACContextAuthModeKey: "bearer"}, false)
}

func TestWebsiteSiteAccessInternalUserCannotMutate(t *testing.T) {
	stubWebsiteSiteAccess(t, "base.group_user", "base.group_multi_company")
	ctx := context.Background()
	for _, name := range websiteGuardedModelNames {
		meta := &orm.ModelMetadata{Name: name}
		for _, op := range []string{"create", "write", "unlink"} {
			err := websiteSiteAccess(ctx, websiteRPCEnv(7), meta, op, nil, orm.RecordMap{"custom_head_html": "<script>"})
			var ormErr *orm.Error
			if !errors.As(err, &ormErr) || ormErr.Code != orm.ErrForbidden {
				t.Fatalf("%s %s: err = %v, want forbidden", name, op, err)
			}
		}
		if err := websiteSiteAccess(ctx, websiteRPCEnv(7), meta, "read", nil, nil); err != nil {
			t.Fatalf("%s read must stay on standard ACL: %v", name, err)
		}
	}
}

func TestWebsiteSiteAccessSystemSudoAndInternalAllowed(t *testing.T) {
	ctx := context.Background()
	meta := &orm.ModelMetadata{Name: WebsiteSiteModelName}

	stubWebsiteSiteAccess(t, "base.group_user", "base.group_system")
	if err := websiteSiteAccess(ctx, websiteRPCEnv(7), meta, "write", nil, nil); err != nil {
		t.Fatalf("group_system write: %v", err)
	}

	stubWebsiteSiteAccess(t, "base.group_user")
	if err := websiteSiteAccess(ctx, orm.NewEnvironment(nil, 7, nil, true), meta, "create", nil, nil); err != nil {
		t.Fatalf("sudo create: %v", err)
	}
	if err := websiteSiteAccess(ctx, orm.NewEnvironment(nil, 0, nil, false), meta, "create", nil, nil); err != nil {
		t.Fatalf("internal create: %v", err)
	}
	if err := websiteSiteAccess(ctx, nil, meta, "create", nil, nil); err != nil {
		t.Fatalf("nil env create: %v", err)
	}
}

func TestWebsiteSiteAccessRawHTMLNeedsSuperadmin(t *testing.T) {
	ctx := context.Background()
	meta := &orm.ModelMetadata{Name: WebsiteSiteModelName}
	stubWebsiteSiteAccess(t, "base.group_user", "base.group_system")

	for _, field := range websiteSiteRawHTMLFields {
		for _, op := range []string{"create", "write"} {
			err := websiteSiteAccess(ctx, websiteRPCEnv(7), meta, op, nil, orm.RecordMap{field: "<script>x()</script>"})
			var ormErr *orm.Error
			if !errors.As(err, &ormErr) || ormErr.Code != orm.ErrForbidden {
				t.Fatalf("group_system %s %s: err = %v, want forbidden", op, field, err)
			}
		}
		// Clearing and the create-form's empty default stay allowed.
		if err := websiteSiteAccess(ctx, websiteRPCEnv(7), meta, "write", nil, orm.RecordMap{field: "  "}); err != nil {
			t.Fatalf("group_system clear %s: %v", field, err)
		}
		if err := websiteSiteAccess(ctx, orm.NewEnvironment(nil, 7, nil, true), meta, "write", nil, orm.RecordMap{field: "<script>"}); err != nil {
			t.Fatalf("superadmin set %s: %v", field, err)
		}
	}
}
