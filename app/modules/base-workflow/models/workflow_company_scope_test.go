package models

// Plan T41: credential lookup scoped by workflow and company; child rows follow their workflow's
// company; run state methods are server-side only.

import (
	"context"
	"errors"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func int64Ptr(v int64) *int64 { return &v }

func stubWorkflowCompany(t *testing.T, companies map[string]any) {
	t.Helper()
	t.Cleanup(orm.StubRuntimeRepository(FlowModelName, orm.RuntimeRepositoryStub{
		Read: func(_ context.Context, ids []string, _ []string) ([]orm.RecordMap, error) {
			company, ok := companies[ids[0]]
			if !ok {
				return nil, nil
			}
			return []orm.RecordMap{{"id": ids[0], "company_id": company}}, nil
		},
	}))
}

func TestLookupWorkflowCredentialIsScoped(t *testing.T) {
	prevDecrypt := WorkflowCredentialDecryptHook
	WorkflowCredentialDecryptHook = func(ciphertext string) (string, error) { return ciphertext, nil }
	t.Cleanup(func() { WorkflowCredentialDecryptHook = prevDecrypt })

	rows := []orm.RecordMap{
		{"id": int64(1), "name": "k", "workflow_id": nil, "company_id": nil, "encrypted_value": "tenant"},
		{"id": int64(2), "name": "k", "workflow_id": nil, "company_id": int64(1), "encrypted_value": "company-1"},
		{"id": int64(3), "name": "k", "workflow_id": int64(10), "company_id": int64(1), "encrypted_value": "workflow-10"},
		{"id": int64(4), "name": "k", "workflow_id": int64(20), "company_id": int64(2), "encrypted_value": "workflow-20"},
		{"id": int64(5), "name": "only2", "workflow_id": nil, "company_id": int64(2), "encrypted_value": "company-2"},
	}
	t.Cleanup(orm.StubRuntimeRepository(WorkflowCredentialModelName, orm.RuntimeRepositoryStub{
		SearchRead: func(_ context.Context, filter orm.RecordMap, _ []string) ([]orm.RecordMap, error) {
			out := []orm.RecordMap{}
			for _, row := range rows {
				if row["name"] == filter["name"] {
					out = append(out, row)
				}
			}
			return out, nil
		},
	}))

	cases := []struct {
		name string
		ctx  context.Context
		ref  string
		want string
	}{
		{"workflow row wins", ContextWithWorkflowScope(context.Background(), 10, int64Ptr(1)), "k", "workflow-10"},
		{"company shared row", ContextWithWorkflowScope(context.Background(), 11, int64Ptr(1)), "k", "company-1"},
		{"other company's workflow row is hidden", ContextWithWorkflowScope(context.Background(), 30, int64Ptr(2)), "k", "tenant"},
		{"company-less workflow sees tenant row only", ContextWithWorkflowScope(context.Background(), 40, nil), "k", "tenant"},
		{"retry scope (no workflow) sees company workflow rows", ContextWithWorkflowScope(context.Background(), 0, int64Ptr(2)), "k", "workflow-20"},
		{"no scope: tenant rows only", context.Background(), "k", "tenant"},
	}
	for _, tc := range cases {
		got, err := LookupWorkflowCredential(tc.ctx, tc.ref)
		if err != nil || got != tc.want {
			t.Fatalf("%s: got %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}

	_, err := LookupWorkflowCredential(ContextWithWorkflowScope(context.Background(), 10, int64Ptr(1)), "only2")
	var ormErr *orm.Error
	if !errors.As(err, &ormErr) || ormErr.Code != orm.ErrNotFound {
		t.Fatalf("company 2 credential from company 1 workflow: err = %v, want not found", err)
	}
}

func TestApplyWorkflowChildCompanyOnWrite(t *testing.T) {
	stubWorkflowCompany(t, map[string]any{"7": int64(3)})

	values := orm.RecordMap{"company_id": int64(99), "name": "x"}
	if err := applyWorkflowChildCompanyOnWrite(context.Background(), values); err != nil {
		t.Fatal(err)
	}
	if _, ok := values["company_id"]; ok {
		t.Fatalf("client company_id must be dropped: %#v", values)
	}

	values = orm.RecordMap{"company_id": int64(99), "workflow_id": int64(7)}
	if err := applyWorkflowChildCompanyOnWrite(context.Background(), values); err != nil {
		t.Fatal(err)
	}
	// A plain id, not *int64: the guarded write's company check reads it as an integer (T87).
	if values["company_id"] != int64(3) {
		t.Fatalf("company_id = %#v, want the workflow's (int64 3)", values["company_id"])
	}

	created := orm.RecordMap{"workflow_id": int64(7)}
	if err := inheritWorkflowChildCompanyID(context.Background(), nil, created); err != nil {
		t.Fatal(err)
	}
	if created["company_id"] != int64(3) {
		t.Fatalf("create company_id = %#v, want the workflow's (int64 3)", created["company_id"])
	}
}

func TestWriteFlowCompanyChangeCascadesToChildren(t *testing.T) {
	t.Cleanup(orm.StubRuntimeRepository(FlowModelName, orm.RuntimeRepositoryStub{
		Read: func(_ context.Context, _ []string, _ []string) ([]orm.RecordMap, error) {
			return []orm.RecordMap{{"id": int64(11), "topic": "t", "version": int64(1), "is_active": true, "company_id": int64(1)}}, nil
		},
		Write: func(context.Context, []string, orm.RecordMap) (bool, error) { return true, nil },
	}))
	moved := map[string][]string{}
	for _, name := range workflowCompanyChildModels {
		name := name
		t.Cleanup(orm.StubRuntimeRepository(name, orm.RuntimeRepositoryStub{
			SearchRead: func(_ context.Context, filter orm.RecordMap, _ []string) ([]orm.RecordMap, error) {
				if int64Value(filter["workflow_id"]) != 11 {
					return nil, nil
				}
				return []orm.RecordMap{{"id": int64(5), "company_id": int64(1)}, {"id": int64(6), "company_id": int64(2)}}, nil
			},
			Write: func(_ context.Context, ids []string, values orm.RecordMap) (bool, error) {
				if values["company_id"] != int64(2) {
					t.Fatalf("%s cascade values = %#v", name, values)
				}
				moved[name] = append(moved[name], ids...)
				return true, nil
			},
		}))
	}

	if _, err := writeFlow(context.Background(), nil, []string{"11"}, []any{orm.RecordMap{"company_id": int64(2)}}, nil); err != nil {
		t.Fatalf("writeFlow: %v", err)
	}
	for _, name := range workflowCompanyChildModels {
		if len(moved[name]) != 1 || moved[name][0] != "5" {
			t.Fatalf("%s moved %v, want [5] (row 6 is already in company 2)", name, moved[name])
		}
	}
}

func TestCreateWorkflowCredentialRefusesDuplicateNameInScope(t *testing.T) {
	stubWorkflowCompany(t, map[string]any{"7": int64(3)})
	t.Cleanup(orm.StubRuntimeRepository(WorkflowCredentialModelName, orm.RuntimeRepositoryStub{
		SearchRead: func(context.Context, orm.RecordMap, []string) ([]orm.RecordMap, error) {
			return []orm.RecordMap{{"id": int64(1), "workflow_id": int64(7), "company_id": int64(3)}}, nil
		},
		Create: func(_ context.Context, values orm.RecordMap) (orm.RecordMap, error) {
			t.Fatalf("duplicate must not be created: %#v", values)
			return nil, nil
		},
	}))
	_, err := createWorkflowCredential(context.Background(), nil, nil, []any{orm.RecordMap{"name": "k", "workflow_id": int64(7)}}, nil)
	var ormErr *orm.Error
	if !errors.As(err, &ormErr) || ormErr.Code != orm.ErrConflict {
		t.Fatalf("err = %v, want conflict", err)
	}
}

func TestWorkflowRunStateMethodsAreInternal(t *testing.T) {
	for _, method := range WorkflowRunModelSpec.RuntimeMethods {
		switch method.Name {
		// create too: a client-built run (status waiting_retry, forged checkpoint) would be resumed
		// by the sudo cron.
		case "create", "write", "claim_due_retries", "reap_stale_running":
			if !method.Internal {
				t.Fatalf("base.workflow.run.%s must be Internal", method.Name)
			}
		}
	}
}
