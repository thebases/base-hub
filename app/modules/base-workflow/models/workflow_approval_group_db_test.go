package models

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	testutil "git.thebase.vn/base_hub/internal/test"
)

// TestWorkflowRunApprovalGroupColumnAgainstPostgres migrates base.workflow.run through the ORM on a
// throwaway schema (approval_group_id must be a nullable bigint column) and runs the unchanged
// claim/release SQL against it: a refused admission puts the wait back with its approval group,
// and a won decision keeps the group as the record of who could decide.
func TestWorkflowRunApprovalGroupColumnAgainstPostgres(t *testing.T) {
	bunDB, err := orm.NewBunDB(testutil.EnvPostgresConfig())
	if err != nil {
		t.Skipf("open postgres fixture: %v", err)
	}
	t.Cleanup(func() { _ = bunDB.Close() })
	// One connection, so the schema's search_path holds for every statement of the migration.
	bunDB.DB.SetMaxOpenConns(1)
	ctx := context.Background()
	schema := fmt.Sprintf("wf_approval_group_test_%d", time.Now().UnixNano())
	if _, err := bunDB.DB.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Skipf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = bunDB.DB.ExecContext(ctx, "SET search_path TO public")
		_, _ = bunDB.DB.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE")
	})
	if _, err := bunDB.DB.ExecContext(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	registry := orm.NewModelRegistry()
	meta, err := registry.Register(reflect.TypeOf(WorkflowRunRecord{}), WorkflowRunModelSpec)
	if err != nil {
		t.Fatal(err)
	}
	if err := orm.AutoMigrateRegisteredModelsToDB(ctx, bunDB.DB, registry); err != nil {
		t.Fatalf("migrate base.workflow.run: %v", err)
	}
	var column struct {
		DataType   string `bun:"data_type"`
		IsNullable string `bun:"is_nullable"`
	}
	if err := bunDB.DB.NewRaw(`SELECT data_type, is_nullable FROM information_schema.columns WHERE table_schema = ? AND table_name = ? AND column_name = 'approval_group_id'`,
		schema, meta.StorageName).Scan(ctx, &column); err != nil {
		t.Fatalf("approval_group_id column: %v", err)
	}
	if column.DataType != "bigint" || column.IsNullable != "YES" {
		t.Fatalf("approval_group_id = %+v, want a nullable bigint", column)
	}

	envCtx := orm.ContextWithEnvironment(ctx, orm.NewEnvironmentWithDBName(bunDB.DB, "approval_group_test", 0, nil, true))
	var id int64
	if err := bunDB.DB.NewRaw(fmt.Sprintf(`INSERT INTO %s (workflow_id, status, trigger_type, input_payload, output_payload, resume_token_hash, approval_expires_at,
		current_step, step_runs, attempt_count, approval_group_id, created_at, updated_at)
		VALUES (7, ?, 'webhook', '{"in":1}', '{"checkpoint":2}', ?, 0, 'wait', '[]', 2, 12, now(), now()) RETURNING id`, meta.StorageName),
		WorkflowRunStatusWaitingApproval, HashWorkflowResumeToken("tok")).Scan(ctx, &id); err != nil {
		t.Fatal(err)
	}
	group := func() any {
		t.Helper()
		var value *int64
		if err := bunDB.DB.NewRaw(fmt.Sprintf("SELECT approval_group_id FROM %s WHERE id = ?", meta.StorageName), id).Scan(ctx, &value); err != nil {
			t.Fatal(err)
		}
		if value == nil {
			return nil
		}
		return *value
	}
	// A group member decides without the token (the engine waived it), in-request: pending.
	decision := approvalDecisionKwargs(WorkflowRunStatusPending)
	decision["id"], decision["lease_owner"], decision["without_token"] = id, "member", true
	if won, err := claimWorkflowApproval(envCtx, meta, nil, nil, decision); err != nil || won != true {
		t.Fatalf("claim = %v, %v", won, err)
	}
	restore := map[string]any{"id": id, "lease_owner": "member", "token_hash": HashWorkflowResumeToken("tok"), "current_step": "wait",
		"output_payload": map[string]any{"checkpoint": 2}, "step_runs": []any{}, "attempt_count": 2}
	if got, err := releaseWorkflowApprovalClaim(envCtx, meta, nil, nil, restore); err != nil || got != true {
		t.Fatalf("release = %v, %v", got, err)
	}
	if g := group(); g != int64(12) {
		t.Fatalf("approval_group_id after a refused admission = %v, want 12", g)
	}
	decision = approvalDecisionKwargs(WorkflowRunStatusRetryWaiting)
	decision["id"], decision["lease_owner"], decision["without_token"] = id, "member", true
	if won, err := claimWorkflowApproval(envCtx, meta, nil, nil, decision); err != nil || won != true {
		t.Fatalf("second claim = %v, %v", won, err)
	}
	if g := group(); g != int64(12) {
		t.Fatalf("approval_group_id after the decision = %v, want 12", g)
	}
}
