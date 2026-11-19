package models

import (
	"context"
	"errors"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func automationTestCtx(sudo, rpc bool) context.Context {
	values := map[string]any{}
	if rpc {
		values[orm.RBACContextAuthModeKey] = "bearer"
	}
	return orm.ContextWithEnvironment(context.Background(), orm.NewEnvironment(nil, 7, values, sudo))
}

func isForbidden(err error) bool {
	var typed *orm.Error
	return errors.As(err, &typed) && typed.Code == orm.ErrForbidden
}

func TestActionCreateNeedsSuperadminForExecutableFields(t *testing.T) {
	values := orm.RecordMap{"name": "x", "action_type": "lua", "lua_code": "return {}"}
	if err := ensureActionCreateAllowed(automationTestCtx(false, true), values); !isForbidden(err) {
		t.Fatalf("group_system create error = %v, want forbidden", err)
	}
	if err := ensureActionCreateAllowed(automationTestCtx(true, true), values); err != nil {
		t.Fatalf("superadmin create error = %v, want nil", err)
	}
	if err := ensureActionCreateAllowed(automationTestCtx(false, false), values); err != nil {
		t.Fatalf("internal (seed) create error = %v, want nil", err)
	}
	if err := ensureActionCreateAllowed(automationTestCtx(false, true), orm.RecordMap{"name": "x", "values": map[string]any{}}); err != nil {
		t.Fatalf("create without executable content error = %v, want nil", err)
	}
}

func TestActionWriteRefusesChangedExecutableFields(t *testing.T) {
	restore := orm.StubRuntimeRepository(AutomationActionModelName, orm.RuntimeRepositoryStub{
		Read: func(_ context.Context, ids []string, _ []string) ([]orm.RecordMap, error) {
			rows := make([]orm.RecordMap, 0, len(ids))
			for range ids {
				rows = append(rows, orm.RecordMap{
					"action_type": "sys_model",
					"model":       "tbl.partner",
					"values":      map[string]any{"name": "a", "n": float64(1)},
					"lua_code":    nil,
				})
			}
			return rows, nil
		},
	})
	defer restore()

	ctx := automationTestCtx(false, true)
	ids := []string{"1", "2"}
	unchanged := orm.RecordMap{"name": "renamed", "action_type": "sys_model", "model": "tbl.partner", "values": map[string]any{"n": float64(1), "name": "a"}, "lua_code": ""}
	if err := ensureActionWriteAllowed(ctx, nil, ids, unchanged); err != nil {
		t.Fatalf("unchanged executable fields error = %v, want nil", err)
	}
	if err := ensureActionWriteAllowed(ctx, nil, ids, orm.RecordMap{"name": "only name"}); err != nil {
		t.Fatalf("name-only write error = %v, want nil", err)
	}
	for _, changed := range []orm.RecordMap{
		{"model": "base.users"},
		{"values": map[string]any{"name": "a", "n": float64(2)}},
		{"action_type": "lua", "lua_code": "return {}"},
	} {
		if err := ensureActionWriteAllowed(ctx, nil, ids, changed); !isForbidden(err) {
			t.Fatalf("write %v error = %v, want forbidden", changed, err)
		}
		if err := ensureActionWriteAllowed(automationTestCtx(true, true), nil, ids, changed); err != nil {
			t.Fatalf("superadmin write %v error = %v, want nil", changed, err)
		}
	}
}

func TestActionFindOrCreateRefusedForRestrictedCaller(t *testing.T) {
	method := guardedActionFindOrCreate()
	_, err := method.Call(automationTestCtx(false, true), nil, nil, nil, map[string]any{"values": map[string]any{"lua_code": "x"}})
	if !isForbidden(err) {
		t.Fatalf("find_or_create error = %v, want forbidden", err)
	}
}
