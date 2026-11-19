package models

import (
	"context"
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

// TestSysModelActionRefID covers every many2one shape the runtime repository may return.
func TestSysModelActionRefID(t *testing.T) {
	seven := int64(7)
	var nilPtr *int64
	cases := []struct {
		raw  any
		want int64
		ok   bool
	}{
		{int64(5), 5, true},
		{&seven, 7, true},
		{nilPtr, 0, false},
		{nil, 0, false},
		{[]any{int64(9), "x"}, 9, true},
		// Shape the runtime repository read actually returns for a many2one.
		{[2]any{int64(18765), "base.users.change_password"}, 18765, true},
		{[2]any{nil, ""}, 0, false},
		{map[string]any{"id": int64(3)}, 3, true},
		{int64(0), 0, false},
	}
	for _, tc := range cases {
		got, ok := sysModelActionRefID(tc.raw)
		if got != tc.want || ok != tc.ok {
			t.Errorf("sysModelActionRefID(%#v) = %d,%v; want %d,%v", tc.raw, got, ok, tc.want, tc.ok)
		}
	}
}

// sysModelActionBoundTestRecord is a throwaway binding model; a Go type can register only once.
type sysModelActionBoundTestRecord struct {
	orm.BaseModel
	Name string `bun:"name" json:"name"`
}

// TestValidateSysModelActionRowRejectsIncompleteDefinitions checks the DB-free invariants:
// name, registered binding model, known type, and a target matching the type.
func TestValidateSysModelActionRowRejectsIncompleteDefinitions(t *testing.T) {
	const boundModel = "test.sys.model.action.bound"
	if _, ok := orm.LookupModel(boundModel); !ok {
		if _, err := orm.RegisterModelType[sysModelActionBoundTestRecord](orm.ModelSpec{
			Name:       boundModel,
			Definition: orm.ModelDefinition(boundModel, (*sysModelActionBoundTestRecord)(nil)),
		}.WithStorageName("test_sys_model_action_bound")); err != nil {
			t.Fatalf("register model %q: %v", boundModel, err)
		}
	}
	base := func() orm.RecordMap {
		return orm.RecordMap{"name": "Open views", "model_name": boundModel, "action_type": "window", "window_action_id": int64(1)}
	}
	cases := map[string]struct {
		mutate func(orm.RecordMap)
		want   string
	}{
		"missing name":    {func(r orm.RecordMap) { r["name"] = " " }, "name is required"},
		"unknown model":   {func(r orm.RecordMap) { r["model_name"] = "no.such.model" }, "is not registered"},
		"unknown type":    {func(r orm.RecordMap) { r["action_type"] = "server" }, "unsupported action_type"},
		"window no ref":   {func(r orm.RecordMap) { r["window_action_id"] = nil }, "window_action_id is required"},
		"client no ref":   {func(r orm.RecordMap) { r["action_type"] = "client" }, "client_action_id is required"},
		"model no method": {func(r orm.RecordMap) { r["action_type"] = "model" }, "operation_id is required"},
	}
	for name, tc := range cases {
		row := base()
		tc.mutate(row)
		err := validateSysModelActionRow(context.Background(), row)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want error containing %q", name, err, tc.want)
		}
	}
	if err := validateSysModelActionRow(context.Background(), base()); err != nil {
		t.Errorf("valid window action rejected: %v", err)
	}
}

// TestValidateSysModelActionRowsPrefersWrittenValues reproduces the "operation_id is required"
// false positive: the read-back row carries a stale operation_id (false, from the request's
// record cache) while the write itself set it — validation must trust the written value.
func TestValidateSysModelActionRowsPrefersWrittenValues(t *testing.T) {
	const boundModel = "test.sys.model.action.bound"
	if _, ok := orm.LookupModel(boundModel); !ok {
		if _, err := orm.RegisterModelType[sysModelActionBoundTestRecord](orm.ModelSpec{
			Name:       boundModel,
			Definition: orm.ModelDefinition(boundModel, (*sysModelActionBoundTestRecord)(nil)),
		}.WithStorageName("test_sys_model_action_bound")); err != nil {
			t.Fatalf("register model %q: %v", boundModel, err)
		}
	}
	// Kernel models register at bootstrap, not in unit tests; register them here (once).
	if _, ok := orm.LookupModel(SysModelActionModelName); !ok {
		if _, err := orm.RegisterModelType[SysModelActionRecord](SysModelActionModelSpec); err != nil {
			t.Fatalf("register %s: %v", SysModelActionModelName, err)
		}
	}
	if _, ok := orm.LookupModel(SysModelOperationModelName); !ok {
		if _, err := orm.RegisterModelType[SysModelOperationRecord](SysModelOperationModelSpec); err != nil {
			t.Fatalf("register %s: %v", SysModelOperationModelName, err)
		}
	}
	restoreAction := orm.StubRuntimeRepository(SysModelActionModelName, orm.RuntimeRepositoryStub{
		Read: func(context.Context, []string, []string) ([]orm.RecordMap, error) {
			return []orm.RecordMap{{"id": int64(1), "name": "Run", "model_name": boundModel, "action_type": "model", "operation_id": false}}, nil
		},
	})
	defer restoreAction()
	restoreOperation := orm.StubRuntimeRepository(SysModelOperationModelName, orm.RuntimeRepositoryStub{
		Read: func(context.Context, []string, []string) ([]orm.RecordMap, error) {
			return []orm.RecordMap{{"id": int64(5), "name": boundModel + ".do_it", "model_name": boundModel, "method_name": "do_it", "is_public": true, "uses_path_id": false}}, nil
		},
	})
	defer restoreOperation()

	meta, _ := orm.LookupModel(SysModelActionModelName)
	records := orm.NewRecordSet(meta, nil, []string{"1"})
	if err := validateSysModelActionRows(context.Background(), records, orm.RecordMap{"operation_id": []any{int64(5), boundModel + ".do_it"}}); err != nil {
		t.Fatalf("written operation_id was ignored: %v", err)
	}
	// Without the write, the stale read still fails — the overlay must not invent values.
	if err := validateSysModelActionRows(context.Background(), records, orm.RecordMap{"name": "Run"}); err == nil {
		t.Fatalf("expected missing operation_id to be rejected")
	}
}

// TestSysModelActionShowsInView covers explicit view_type lists and the per-type empty default.
func TestSysModelActionShowsInView(t *testing.T) {
	cases := []struct {
		row  orm.RecordMap
		mode string
		want bool
	}{
		{orm.RecordMap{"action_type": "model", "view_type": ""}, "form", true},
		{orm.RecordMap{"action_type": "model", "view_type": false}, "list", false},
		{orm.RecordMap{"action_type": "window", "view_type": ""}, "list", true},
		{orm.RecordMap{"action_type": "client"}, "kanban", true},
		{orm.RecordMap{"action_type": "client"}, "calendar", false},
		{orm.RecordMap{"action_type": "model", "view_type": " Form , LIST "}, "list", true},
		{orm.RecordMap{"action_type": "window", "view_type": "form"}, "list", false},
	}
	for _, tc := range cases {
		if got := sysModelActionShowsInView(tc.row, tc.mode); got != tc.want {
			t.Errorf("sysModelActionShowsInView(%v, %q) = %v; want %v", tc.row, tc.mode, got, tc.want)
		}
	}
}

// TestSysModelActionTypeOnchange checks the view_type suggestion per action type.
func TestSysModelActionTypeOnchange(t *testing.T) {
	cases := map[string]any{"model": "form", "window": "form,list,kanban", "client": "form,list,kanban", "": nil}
	for actionType, want := range cases {
		got, err := sysModelActionTypeOnchange(context.Background(), orm.RecordSet{}, orm.RecordMap{"action_type": actionType}, nil, nil)
		if err != nil {
			t.Fatalf("%q: %v", actionType, err)
		}
		if want == nil {
			if got != nil {
				t.Errorf("%q: got %v, want no change", actionType, got)
			}
			continue
		}
		if got["view_type"] != want {
			t.Errorf("%q: view_type = %v; want %v", actionType, got["view_type"], want)
		}
	}
}
