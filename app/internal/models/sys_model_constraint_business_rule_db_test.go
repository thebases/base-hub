package models

// File overview:
// - Responsibility: verifies business_rule constraints are enforced right after apply (BUG-K-1) and that a rule
//   changed directly in the shared table (another instance, raw SQL) is picked up through the fingerprint sync
//   (BUG-K-2), against the RUNTIME_GRAPH_TEST_DB PostgreSQL, using sys.sequence as the target model.

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
)

// setupBusinessRuleFixture reuses the sys.sequence fixture as the target model
// and recreates sys_model_constraint with message as translated jsonb, the way
// auto-migrate stores a translate field.
func setupBusinessRuleFixture(t *testing.T) context.Context {
	t.Helper()
	setupSysSequenceFixture(t)
	ctx := context.Background()
	db := orm.GetBunDB()
	if _, err := db.NewDropTable().Model((*SysModelConstraintRecord)(nil)).IfExists().Cascade().Exec(ctx); err != nil {
		t.Fatalf("drop sys_model_constraint: %v", err)
	}
	if _, err := db.NewCreateTable().Model((*SysModelConstraintRecord)(nil)).Exec(ctx); err != nil {
		t.Fatalf("create sys_model_constraint: %v", err)
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE sys_model_constraint ALTER COLUMN message TYPE jsonb USING jsonb_build_object('en', message)`); err != nil {
		t.Fatalf("convert message to jsonb: %v", err)
	}
	testCtx := sysSequenceTestContext(nil)
	dbName := sysModelConstraintDBName(testCtx)
	resetBusinessRuleSync(dbName)
	t.Cleanup(func() {
		businessRuleCache.mu.Lock()
		delete(businessRuleCache.byRow, businessRuleCacheKey(dbName, SysSequenceModelName))
		businessRuleCache.mu.Unlock()
		resetBusinessRuleSync(dbName)
	})
	return testCtx
}

// resetBusinessRuleSync forces the next handler call to re-check the fingerprint.
func resetBusinessRuleSync(dbName string) {
	businessRuleSync.mu.Lock()
	delete(businessRuleSync.byDB, dbName)
	businessRuleSync.mu.Unlock()
}

func insertBusinessRuleRow(t *testing.T, ctx context.Context, name, state string) string {
	t.Helper()
	var id int64
	err := orm.GetBunDB().NewRaw(`INSERT INTO sys_model_constraint (bid, name, model_name, constraint_type, invalid_domain, trigger_fields, message, state, created_at, updated_at)
		VALUES (?, ?, ?, ?, '[["code","=","forbidden.code"]]'::jsonb, '["code"]'::jsonb, '{"en":"code is forbidden"}'::jsonb, ?, now(), now()) RETURNING id`,
		name, name, SysSequenceModelName, sysModelConstraintTypeBusinessRule, state).Scan(ctx, &id)
	if err != nil {
		t.Fatalf("insert business rule row: %v", err)
	}
	return strconv.FormatInt(id, 10)
}

func TestBusinessRuleEnforcedRightAfterApply(t *testing.T) {
	ctx := setupBusinessRuleFixture(t)
	id := insertBusinessRuleRow(t, ctx, "k1_forbidden_code", "draft")
	row := orm.RecordMap{
		"id": id, "name": "k1_forbidden_code", "model_name": SysSequenceModelName,
		"constraint_type": sysModelConstraintTypeBusinessRule, "state": "draft",
		"invalid_domain": []any{[]any{"code", "=", "forbidden.code"}}, "trigger_fields": []string{"code"},
		"message": "code is forbidden",
	}
	if err := ReconcileModelConstraint(ctx, orm.GetBunDB(), sysModelConstraintDBName(ctx), row); err != nil {
		t.Fatalf("apply business rule: %v", err)
	}
	// Suppress the fingerprint sync so only the apply path's own cache refresh
	// can make the rule visible.
	businessRuleSync.mu.Lock()
	businessRuleSync.byDB[sysModelConstraintDBName(ctx)] = businessRuleSyncState{checkedAt: time.Now(), fingerprint: "suppressed"}
	businessRuleSync.mu.Unlock()
	if row["state"] != "draft" {
		t.Fatalf("caller's row must stay unchanged for before/after logging, got state %v", row["state"])
	}
	_, err := createSysSequence(ctx, nil, nil, nil, map[string]any{"values": map[string]any{
		"name": "Forbidden", "code": "forbidden.code", "implementation": SysSequenceImplementationNoGap,
	}})
	if err == nil || !strings.Contains(err.Error(), "code is forbidden") {
		t.Fatalf("expected business_rule_violation right after apply, got %v", err)
	}
}

func TestBusinessRuleCacheSyncsRulesChangedElsewhere(t *testing.T) {
	ctx := setupBusinessRuleFixture(t)
	dbName := sysModelConstraintDBName(ctx)
	// A rule known locally (so the handler is registered) that never matches.
	other := insertBusinessRuleRow(t, ctx, "k2_registered", "draft")
	if _, err := orm.GetBunDB().ExecContext(ctx, `UPDATE sys_model_constraint SET invalid_domain = '[["code","=","never.used"]]'::jsonb WHERE id = ?`, other); err != nil {
		t.Fatalf("narrow registered rule: %v", err)
	}
	if err := ReconcileModelConstraint(ctx, orm.GetBunDB(), dbName, orm.RecordMap{
		"id": other, "name": "k2_registered", "model_name": SysSequenceModelName,
		"constraint_type": sysModelConstraintTypeBusinessRule, "state": "draft",
		"invalid_domain": []any{[]any{"code", "=", "never.used"}}, "trigger_fields": []string{"code"},
	}); err != nil {
		t.Fatalf("apply registered rule: %v", err)
	}

	// Another instance activates a rule: this process only sees the DB row.
	insertBusinessRuleRow(t, ctx, "k2_remote", "active")
	resetBusinessRuleSync(dbName)
	_, err := createSysSequence(ctx, nil, nil, nil, map[string]any{"values": map[string]any{
		"name": "Remote", "code": "forbidden.code", "implementation": SysSequenceImplementationNoGap,
	}})
	if err == nil || !strings.Contains(err.Error(), "code is forbidden") {
		t.Fatalf("expected remote rule (en message) to be enforced after sync, got %v", err)
	}

	// Another instance disables it again.
	if _, err := orm.GetBunDB().ExecContext(ctx, `UPDATE sys_model_constraint SET state = 'disabled', updated_at = now() + interval '1 second' WHERE name = 'k2_remote'`); err != nil {
		t.Fatalf("disable remote rule: %v", err)
	}
	resetBusinessRuleSync(dbName)
	if _, err := createSysSequence(ctx, nil, nil, nil, map[string]any{"values": map[string]any{
		"name": "Remote", "code": "forbidden.code", "implementation": SysSequenceImplementationNoGap,
	}}); err != nil {
		t.Fatalf("expected disabled remote rule to stop applying, got %v", err)
	}
}
