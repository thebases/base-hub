package models

// T35: run and schedule creation check record rules on the action row.

import (
	"context"
	"errors"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func denyActionAccess(t *testing.T) *[]string {
	t.Helper()
	previous := parentRecordAccess
	t.Cleanup(func() { parentRecordAccess = previous })
	var got []string
	parentRecordAccess = func(_ context.Context, model, operation string, ids []string) error {
		got = append([]string{model, operation}, ids...)
		return orm.Forbiddenf("denied")
	}
	return &got
}

func isForbiddenErr(err error) bool {
	var typed *orm.Error
	return errors.As(err, &typed) && typed.Code == orm.ErrForbidden
}

func TestRunActionChecksRecordAccess(t *testing.T) {
	got := denyActionAccess(t)
	previousHook := actionExecutorHook
	t.Cleanup(func() { actionExecutorHook = previousHook })
	actionExecutorHook = func(context.Context, int64, map[string]any) (map[string]any, error) {
		t.Error("executor must not run after a denied check")
		return nil, nil
	}
	_, err := runAutomationAction(context.Background(), nil, []string{"4"}, nil, nil)
	if !isForbiddenErr(err) {
		t.Fatalf("expected forbidden, got %v", err)
	}
	if want := []string{AutomationActionModelName, "write", "4"}; len(*got) != 3 || (*got)[0] != want[0] || (*got)[1] != want[1] || (*got)[2] != want[2] {
		t.Fatalf("unexpected check %v", *got)
	}
}

// Prepare rewrites the payload in place and inherits the parent action's company as a plain id.
func TestCronPrepareNormalizesInPlaceAndInheritsCompany(t *testing.T) {
	previous := parentRecordAccess
	t.Cleanup(func() { parentRecordAccess = previous })
	parentRecordAccess = func(context.Context, string, string, []string) error { return nil }
	company := int64(4)
	t.Cleanup(orm.StubRuntimeRepository(AutomationActionModelName, orm.RuntimeRepositoryStub{
		Read: func(context.Context, []string, []string) ([]orm.RecordMap, error) {
			return []orm.RecordMap{{"company_id": &company}}, nil
		},
	}))
	values := orm.RecordMap{"name": " n ", "interval_seconds": 60, "action_id": 3, "fail_count": 9, "company_id": 99}
	if err := prepareAutomationCron(context.Background(), orm.WriteOp{Create: true, Values: values}); err != nil {
		t.Fatal(err)
	}
	if values["name"] != "n" || values["fail_count"] != int64(0) || values["company_id"] != int64(4) {
		t.Fatalf("prepare did not normalise in place: %v", values)
	}
	// A write that does not re-point the schedule leaves company_id alone.
	written := orm.RecordMap{"name": "x"}
	if err := prepareAutomationCron(context.Background(), orm.WriteOp{IDs: []string{"1"}, Values: written}); err != nil {
		t.Fatal(err)
	}
	if _, ok := written["company_id"]; ok {
		t.Fatalf("write without action_id must not touch company_id: %v", written)
	}
}

// The action Prepare runs the superadmin guard: an RPC caller cannot create executable content.
func TestActionPrepareRunsAuthoringGuard(t *testing.T) {
	values := orm.RecordMap{"name": "a", "action_type": "lua", "lua_code": "return 1"}
	err := prepareAutomationAction(automationTestCtx(false, true), orm.WriteOp{Create: true, Values: values})
	if !isForbiddenErr(err) {
		t.Fatalf("expected forbidden, got %v", err)
	}
	if err := prepareAutomationAction(automationTestCtx(true, true), orm.WriteOp{Create: true, Values: values}); err != nil {
		t.Fatalf("superadmin create must pass: %v", err)
	}
	requireInvalid := prepareAutomationAction(automationTestCtx(true, true), orm.WriteOp{Create: true, Values: orm.RecordMap{"name": "a"}})
	var typed *orm.Error
	if !errors.As(requireInvalid, &typed) || typed.Code != orm.ErrInvalidParams {
		t.Fatalf("missing action_type must be invalid params, got %v", requireInvalid)
	}
}

func TestCronCreateAndRepointCheckAction(t *testing.T) {
	got := denyActionAccess(t)
	t.Cleanup(orm.StubRuntimeRepository(AutomationActionModelName, orm.RuntimeRepositoryStub{
		Read: func(context.Context, []string, []string) ([]orm.RecordMap, error) {
			t.Error("action must not be read after a denied check")
			return nil, nil
		},
	}))
	t.Cleanup(orm.StubRuntimeRepository(AutomationCronModelName, orm.RuntimeRepositoryStub{
		Create: func(context.Context, orm.RecordMap) (orm.RecordMap, error) { t.Error("cron created"); return nil, nil },
		Write: func(context.Context, []string, orm.RecordMap) (bool, error) {
			t.Error("cron written")
			return false, nil
		},
	}))
	err := prepareAutomationCron(context.Background(), orm.WriteOp{Create: true, Values: orm.RecordMap{"name": "n", "interval_seconds": 60, "action_id": 3}})
	if !isForbiddenErr(err) || len(*got) != 3 || (*got)[2] != "3" {
		t.Fatalf("create: expected forbidden check on action 3, got %v (%v)", err, *got)
	}
	err = prepareAutomationCron(context.Background(), orm.WriteOp{IDs: []string{"1"}, Values: orm.RecordMap{"action_id": 8}})
	if !isForbiddenErr(err) || (*got)[2] != "8" {
		t.Fatalf("write: expected forbidden check on action 8, got %v (%v)", err, *got)
	}
}

// A cron created through the guarded path keeps its parent action's company even when the
// caller's active company differs: CreateRecord's pin fills only an absent company_id, so the
// value Prepare inherited is kept and then checked against the caller's allowed companies.
func TestCronGuardedCreateKeepsParentCompany(t *testing.T) {
	AutomationCronModel.InitDefault()
	t.Cleanup(AutomationCronModel.Shutdown)
	model, ok := orm.LookupModel(AutomationCronModelName)
	if !ok {
		t.Fatal("automation.cron not registered")
	}
	previous := parentRecordAccess
	t.Cleanup(func() { parentRecordAccess = previous })
	parentRecordAccess = func(context.Context, string, string, []string) error { return nil }
	actionCompany := int64(4)
	t.Cleanup(orm.StubRuntimeRepository(AutomationActionModelName, orm.RuntimeRepositoryStub{
		Read: func(context.Context, []string, []string) ([]orm.RecordMap, error) {
			return []orm.RecordMap{{"company_id": &actionCompany}}, nil
		},
	}))
	var stored orm.RecordMap
	t.Cleanup(orm.StubRuntimeRepository(AutomationCronModelName, orm.RuntimeRepositoryStub{
		Create: func(_ context.Context, values orm.RecordMap) (orm.RecordMap, error) {
			stored = values
			return orm.RecordMap{"id": int64(1)}, nil
		},
	}))
	env := orm.NewEnvironment(nil, 7, map[string]any{
		orm.RBACContextAuthModeKey:      "bearer",
		orm.ContextKeyAllowedCompanyIDs: []int64{2, 4},
		orm.ContextKeyCurrentCompanyID:  int64(2),
	}, false)
	ctx := orm.ContextWithEnvironment(context.Background(), env)
	if _, err := orm.CreateRecord(ctx, model, orm.RecordMap{"name": "n", "interval_seconds": 60, "action_id": 3}); err != nil {
		t.Fatalf("guarded cron create: %v", err)
	}
	if got, _ := int64FromAny(stored["company_id"]); got != actionCompany {
		t.Fatalf("cron company_id = %v, want parent action company %d (not active company 2)", stored["company_id"], actionCompany)
	}
}
