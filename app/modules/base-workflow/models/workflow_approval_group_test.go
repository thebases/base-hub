package models

import (
	"context"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

// stubApproverGroupBID makes group 12 the group with BID "sales.group_approver".
func stubApproverGroupBID(t *testing.T) {
	t.Helper()
	prev := workflowGroupBID
	workflowGroupBID = func(_ context.Context, id int64) (string, error) {
		if id == 12 {
			return "sales.group_approver", nil
		}
		return "", nil
	}
	t.Cleanup(func() { workflowGroupBID = prev })
}

func workflowGroupEnv(userID, roleID int64, groups ...int64) context.Context {
	return orm.ContextWithEnvironment(context.Background(), orm.NewEnvironment(nil, userID, map[string]any{
		orm.RBACContextAuthModeKey: "bearer", orm.RBACContextRoleIDKey: roleID, orm.RBACContextGroupIDsKey: groups,
	}, false))
}

// Membership of a run's approval group: direct ids (session groups or primary group) or an
// implied group matched by BID; sudo is never a member (it decides as an author).
func TestWorkflowCallerInGroup(t *testing.T) {
	stubApproverGroupBID(t)
	stubWorkflowAccess(t, "base.group_user")
	cases := []struct {
		name string
		ctx  context.Context
		want bool
	}{
		{"session group", workflowGroupEnv(5, 1, 3, 12), true},
		{"primary group", workflowGroupEnv(5, 12), true},
		{"not a member", workflowGroupEnv(5, 1, 3), false},
		{"sudo", orm.ContextWithEnvironment(context.Background(), orm.NewEnvironment(nil, 1, nil, true)), false},
		{"no environment", context.Background(), false},
	}
	for _, tc := range cases {
		if got, err := WorkflowCallerInGroup(tc.ctx, 12); err != nil || got != tc.want {
			t.Fatalf("%s: got %v, %v want %v", tc.name, got, err, tc.want)
		}
	}
	// Implied membership: the expanded BIDs carry the group's BID.
	stubWorkflowAccess(t, "base.group_user", "sales.group_approver")
	if got, err := WorkflowCallerInGroup(workflowGroupEnv(5, 1, 3), 12); err != nil || !got {
		t.Fatalf("implied member: %v, %v", got, err)
	}
	if got, _ := WorkflowCallerInGroup(workflowGroupEnv(5, 1, 3), 13); got {
		t.Fatal("a group without a BID matched through the implied path")
	}
}

// A member of the approver group still needs the operator role; without a token the RPC no longer
// refuses up front and leaves the waiver to the engine, which reads the parked run's group.
func TestDecideWorkflowApprovalLeavesTheTokenWaiverToTheEngine(t *testing.T) {
	var got []WorkflowApprovalDecision
	prev := workflowRunControlHooks
	RegisterWorkflowRunControlHooks(WorkflowRunControlHooks{DecideApproval: func(_ context.Context, _ int64, d WorkflowApprovalDecision) (orm.RecordMap, error) {
		got = append(got, d)
		return orm.RecordMap{"status": WorkflowRunStatusRetryWaiting}, nil
	}})
	t.Cleanup(func() { RegisterWorkflowRunControlHooks(prev) })

	stubWorkflowAccess(t, append(workflowTestViewer, "sales.group_approver")...)
	member := workflowGroupEnv(9, 1, 12)
	if _, err := approveWorkflowRun(member, nil, []string{"50"}, nil, nil); ormCode(err) != orm.ErrForbidden || len(got) != 0 {
		t.Fatalf("member without operator role: err = %v decisions %d", err, len(got))
	}

	prevRead := workflowApprovalReadAccess
	workflowApprovalReadAccess = func(context.Context, string, []string) error { return nil }
	t.Cleanup(func() { workflowApprovalReadAccess = prevRead })
	stubWorkflowAccess(t, append(workflowTestOperator, "sales.group_approver")...)
	if _, err := approveWorkflowRun(member, nil, []string{"50"}, nil, map[string]any{"comment": "ok"}); err != nil {
		t.Fatalf("operator member: err = %v", err)
	}
	if len(got) != 1 || got[0].TokenHash != "" || got[0].Author || got[0].By != 9 || !got[0].Approved {
		t.Fatalf("decision handed to the engine = %+v", got)
	}
}

// An imported wait_approval drops its approver group: the source database's id could name another
// group here.
func TestImportDropsApproverGroup(t *testing.T) {
	restore := orm.StubRuntimeRepository(WorkflowActionCatalogModelName, orm.RuntimeRepositoryStub{
		SearchRead: func(context.Context, orm.RecordMap, []string) ([]orm.RecordMap, error) { return nil, nil },
	})
	defer restore()
	source := map[string]any{WaitApprovalGroupParam: float64(12), "expires_in_seconds": float64(60)}
	values, err := importedWorkflowActionValues(context.Background(), orm.RecordMap{"action": "wait_approval@1", "step_key": "w", "extra_params": source}, 44)
	if err != nil {
		t.Fatal(err)
	}
	extra, _ := values["extra_params"].(map[string]any)
	if _, kept := extra[WaitApprovalGroupParam]; kept || extra["expires_in_seconds"] != float64(60) {
		t.Fatalf("imported extra_params = %v", extra)
	}
	if source[WaitApprovalGroupParam] == nil {
		t.Fatal("the export document was mutated")
	}
}
