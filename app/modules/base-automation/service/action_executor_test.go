package service

import (
	"context"
	"errors"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
	automationmodels "git.thebase.vn/base_hub/modules/base-automation/models"
)

func TestExecuteActionDispatchesLuaScript(t *testing.T) {
	restore := orm.StubRuntimeRepository(automationmodels.AutomationActionModelName, orm.RuntimeRepositoryStub{
		Read: func(_ context.Context, ids []string, _ []string) ([]orm.RecordMap, error) {
			if len(ids) != 1 || ids[0] != "1" {
				t.Fatalf("read ids = %#v, want [1]", ids)
			}
			return []orm.RecordMap{{
				"id":          int64(1),
				"action_type": "lua",
				"lua_code":    `return {greeting = "hi " .. payload.name}`,
			}}, nil
		},
	})
	defer restore()

	result, err := ExecuteAction(context.Background(), 1, map[string]any{"name": "world"})
	if err != nil {
		t.Fatalf("ExecuteAction() error = %v", err)
	}
	if got := result["greeting"]; got != "hi world" {
		t.Fatalf("greeting = %#v, want %q", got, "hi world")
	}
}

func TestExecuteActionDispatchesSysModelCreate(t *testing.T) {
	const targetModel = "automation.test.target"

	restoreAction := orm.StubRuntimeRepository(automationmodels.AutomationActionModelName, orm.RuntimeRepositoryStub{
		Read: func(context.Context, []string, []string) ([]orm.RecordMap, error) {
			return []orm.RecordMap{{
				"id":          int64(2),
				"action_type": "sys_model",
				"model":       targetModel,
				"operation":   "create",
				"values":      map[string]any{"name": "created via automation"},
			}}, nil
		},
	})
	defer restoreAction()

	created := false
	restoreTarget := orm.StubRuntimeRepository(targetModel, orm.RuntimeRepositoryStub{
		Create: func(_ context.Context, values orm.RecordMap) (orm.RecordMap, error) {
			created = true
			if values["name"] != "created via automation" {
				t.Fatalf("create values = %#v, want name set", values)
			}
			return orm.RecordMap{"id": int64(99), "name": values["name"]}, nil
		},
	})
	defer restoreTarget()

	result, err := ExecuteAction(context.Background(), 2, nil)
	if err != nil {
		t.Fatalf("ExecuteAction() error = %v", err)
	}
	if !created {
		t.Fatalf("expected target model Create to be called")
	}
	if result["id"] != int64(99) {
		t.Fatalf("result = %#v, want id=99", result)
	}
}

func TestExecuteActionRejectsUnknownActionType(t *testing.T) {
	restore := orm.StubRuntimeRepository(automationmodels.AutomationActionModelName, orm.RuntimeRepositoryStub{
		Read: func(context.Context, []string, []string) ([]orm.RecordMap, error) {
			return []orm.RecordMap{{"id": int64(3), "action_type": "unknown"}}, nil
		},
	})
	defer restore()

	if _, err := ExecuteAction(context.Background(), 3, nil); err == nil {
		t.Fatalf("expected an error for an unsupported action_type")
	}
}

func TestRunLuaActionByIDKeepsPayloadAndRejectsNonLua(t *testing.T) {
	rows := map[string]orm.RecordMap{
		"1": {"id": int64(1), "action_type": "lua", "lua_code": `payload.seen = raw .. payload.name`, "values": map[string]any{"name": "default"}},
		"2": {"id": int64(2), "action_type": "sys_model"},
	}
	restore := orm.StubRuntimeRepository(automationmodels.AutomationActionModelName, orm.RuntimeRepositoryStub{
		Read: func(_ context.Context, ids []string, _ []string) ([]orm.RecordMap, error) {
			if row, ok := rows[ids[0]]; ok {
				return []orm.RecordMap{row}, nil
			}
			return nil, nil
		},
	})
	defer restore()

	result, err := RunLuaActionByID(context.Background(), 1, map[string]any{}, map[string]any{"raw": "frame:"})
	if err != nil {
		t.Fatalf("RunLuaActionByID() error = %v", err)
	}
	if result["seen"] != "frame:default" || result["name"] != "default" {
		t.Fatalf("result = %#v, want mutated payload with default value", result)
	}
	if _, err := RunLuaActionByID(context.Background(), 2, nil, nil); err == nil {
		t.Fatal("non-lua action must be rejected")
	}
	if _, err := RunLuaActionByID(context.Background(), 3, nil, nil); err == nil {
		t.Fatal("missing action must error")
	}
}

func TestExecuteActionRefusesInactiveAction(t *testing.T) {
	restore := orm.StubRuntimeRepository(automationmodels.AutomationActionModelName, orm.RuntimeRepositoryStub{
		Read: func(context.Context, []string, []string) ([]orm.RecordMap, error) {
			return []orm.RecordMap{{"id": int64(5), "active": false, "action_type": "lua", "lua_code": `error("must not run")`}}, nil
		},
	})
	defer restore()

	_, err := ExecuteAction(context.Background(), 5, nil)
	var typed *orm.Error
	if !errors.As(err, &typed) || typed.Code != orm.ErrForbidden {
		t.Fatalf("ExecuteAction(inactive) error = %v, want forbidden", err)
	}
	if _, err := RunLuaActionByID(context.Background(), 5, nil, nil); !errors.As(err, &typed) || typed.Code != orm.ErrForbidden {
		t.Fatalf("RunLuaActionByID(inactive) error = %v, want forbidden", err)
	}
}
