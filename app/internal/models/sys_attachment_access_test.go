package models

import (
	"context"
	"errors"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func attachmentCallerCtx(userID int64, companies, groups []int64) context.Context {
	return orm.ContextWithEnvironment(context.Background(), orm.EnvironmentForIdentity(&orm.RBACIdentity{
		UserID: userID, Login: "u", RoleID: 2, AuthMode: "bearer",
		CompanyID: 1, CompanyIDs: companies, GroupIDs: groups,
	}))
}

func attachmentForbidden(err error) bool {
	var typed *orm.Error
	return errors.As(err, &typed) && typed.Code == orm.ErrForbidden
}

func TestSysAttachmentWriteAllowed(t *testing.T) {
	previous := sysAttachmentTargetAccess
	t.Cleanup(func() { sysAttachmentTargetAccess = previous })
	var gotOperation string
	writable := false
	sysAttachmentTargetAccess = func(_ context.Context, _ string, operation string, _ []string) error {
		gotOperation = operation
		if writable {
			return nil
		}
		return errors.New("denied")
	}
	ctx := attachmentCallerCtx(7, []int64{1}, nil)
	env := orm.EnvironmentFromContext(ctx)

	if !sysAttachmentWriteAllowed(ctx, env, sysAttachmentAccessRow{ID: 1, CreateUID: 7}) {
		t.Fatal("creator must be able to write")
	}
	bound := sysAttachmentAccessRow{ID: 2, CreateUID: 9, ResModel: "tbl.partner", ModelId: 5}
	if sysAttachmentWriteAllowed(ctx, env, bound) {
		t.Fatal("a reader without write access on the bound record must not write")
	}
	if gotOperation != "write" {
		t.Fatalf("target check used operation %q, want write", gotOperation)
	}
	writable = true
	if !sysAttachmentWriteAllowed(ctx, env, bound) {
		t.Fatal("write access on the bound record must allow writing")
	}
	// Scope grants (public, company, own user scope) never allow writes on another user's row.
	one := int64(1)
	seven := int64(7)
	for _, row := range []sysAttachmentAccessRow{
		{ID: 3, CreateUID: 9, IsPublic: true},
		{ID: 4, CreateUID: 9, CompanyID: &one},
		{ID: 5, CreateUID: 9, UserID: &seven},
	} {
		if sysAttachmentWriteAllowed(ctx, env, row) {
			t.Fatalf("scope-only row %d must not be writable by a non-creator", row.ID)
		}
	}
}

func TestSysAttachmentPublishNeedsCreator(t *testing.T) {
	previous := sysAttachmentRowsByID
	t.Cleanup(func() { sysAttachmentRowsByID = previous })
	sysAttachmentRowsByID = func(context.Context, []string) ([]sysAttachmentAccessRow, error) {
		return []sysAttachmentAccessRow{{ID: 1, CreateUID: 9, IsPublic: false}}, nil
	}
	ctx := attachmentCallerCtx(7, []int64{1}, nil)
	ids := []string{"1"}

	if err := ensureSysAttachmentPublishAllowed(ctx, ids, orm.RecordMap{"is_public": true}); !attachmentForbidden(err) {
		t.Fatalf("non-creator publishing must be forbidden, got %v", err)
	}
	if err := ensureSysAttachmentPublishAllowed(ctx, ids, orm.RecordMap{"is_public": "true"}); !attachmentForbidden(err) {
		t.Fatalf("non-bool is_public must count as a change, got %v", err)
	}
	if err := ensureSysAttachmentPublishAllowed(ctx, ids, orm.RecordMap{"is_public": false}); err != nil {
		t.Fatalf("unchanged is_public must pass, got %v", err)
	}
	if err := ensureSysAttachmentPublishAllowed(ctx, ids, orm.RecordMap{"name": "x"}); err != nil {
		t.Fatalf("no is_public key must pass, got %v", err)
	}
	if err := ensureSysAttachmentPublishAllowed(attachmentCallerCtx(9, []int64{1}, nil), ids, orm.RecordMap{"is_public": true}); err != nil {
		t.Fatalf("creator may publish, got %v", err)
	}
}

func TestSysAttachmentRecordRuleScopesReads(t *testing.T) {
	prevBase, prevModels, prevScope := sysAttachmentBaseRule, sysAttachmentBoundModels, sysAttachmentTargetReadScope
	t.Cleanup(func() {
		sysAttachmentBaseRule, sysAttachmentBoundModels, sysAttachmentTargetReadScope = prevBase, prevModels, prevScope
	})
	sysAttachmentBaseRule = func(context.Context, *orm.Environment, *orm.ModelMetadata, string) (orm.DomainNode, error) {
		return orm.TrueDomain(), nil
	}
	sysAttachmentBoundModels = func(context.Context) ([]string, error) {
		return []string{"tbl.partner", "tbl.hidden"}, nil
	}
	// tbl.partner: only record 5 readable; tbl.hidden: nothing readable.
	sysAttachmentTargetReadScope = func(_ context.Context, targetModel string) (orm.DomainNode, bool, error) {
		if targetModel == "tbl.partner" {
			return orm.And(orm.Where("sys_model", "=", targetModel), orm.Where("model_id", "in", []any{int64(5)})), true, nil
		}
		return orm.DomainNode{}, false, nil
	}

	ctx := attachmentCallerCtx(7, []int64{1}, []int64{40})
	env := orm.EnvironmentFromContext(ctx)
	rule, err := sysAttachmentRecordRule(ctx, env, nil, "read")
	if err != nil {
		t.Fatal(err)
	}
	row := func(values orm.RecordMap) orm.RecordMap {
		out := orm.RecordMap{"create_uid": int64(9), "is_public": false, "sys_model": nil, "model_id": nil,
			"company_id": nil, "user_id": nil, "group_id": nil}
		for k, v := range values {
			out[k] = v
		}
		return out
	}
	cases := []struct {
		name string
		row  orm.RecordMap
		want bool
	}{
		{"own upload", row(orm.RecordMap{"create_uid": int64(7), "sys_model": "tbl.hidden", "model_id": int64(1)}), true},
		{"public", row(orm.RecordMap{"is_public": true, "sys_model": "tbl.hidden", "model_id": int64(1)}), true},
		{"unbound unscoped", row(nil), true},
		{"unbound scoped to other user", row(orm.RecordMap{"user_id": int64(8)}), false},
		{"unbound scoped to me", row(orm.RecordMap{"user_id": int64(7)}), true},
		{"company scope", row(orm.RecordMap{"company_id": int64(1), "sys_model": "tbl.hidden", "model_id": int64(1)}), true},
		{"other company scope", row(orm.RecordMap{"company_id": int64(2)}), false},
		{"group scope", row(orm.RecordMap{"group_id": int64(40)}), true},
		{"bound to readable record", row(orm.RecordMap{"sys_model": "tbl.partner", "model_id": int64(5)}), true},
		{"bound to unreadable record", row(orm.RecordMap{"sys_model": "tbl.partner", "model_id": int64(6)}), false},
		{"bound to hidden model", row(orm.RecordMap{"sys_model": "tbl.hidden", "model_id": int64(1)}), false},
	}
	for _, tc := range cases {
		got, err := orm.EvaluateDomain(tc.row, rule)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: visible=%v, want %v", tc.name, got, tc.want)
		}
	}

	// Writes are decided per id by the access handler; internal callers keep the base rule.
	for _, op := range []string{"write", "unlink"} {
		domain, err := sysAttachmentRecordRule(ctx, env, nil, op)
		if err != nil || domain.Kind != orm.DomainKindTrue {
			t.Fatalf("%s rule = %+v, %v; want base (true)", op, domain, err)
		}
	}
	internal := orm.NewEnvironmentWithDBName(nil, "db", 7, nil, false)
	if domain, err := sysAttachmentRecordRule(context.Background(), internal, nil, "read"); err != nil || domain.Kind != orm.DomainKindTrue {
		t.Fatalf("internal caller rule = %+v, %v; want base (true)", domain, err)
	}
}
