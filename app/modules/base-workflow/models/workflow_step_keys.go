package models

// Step key integrity (plan T62).
//
// step_key identifies a step inside its workflow: run audit and resume (step_runs[].step_key), DAG
// edges (from_step_key/to_step_key) and condition routing all match on it. Nothing stopped two
// actions of one workflow from sharing a key, an edge from naming a step that does not exist, or a
// deleted/renamed step from leaving its edges behind. Rules enforced here:
//   - step_key is unique per workflow: action create/write, replace_action_sequence, copy, import;
//   - an edge names existing steps of its workflow: edge create/write, replace_workflow_edges;
//   - deleting a step deletes its edges, renaming it rewrites them, and replace_action_sequence
//     drops the edges of steps that are no longer in the sequence;
//   - a unique index (workflow_id, step_key) backs the rule, created at boot only when the data
//     has no duplicates (otherwise a warning lists them and boot continues without the index).

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/uptrace/bun"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

const workflowActionStepKeyIndex = "base_workflow_action_workflow_step_key_uniq"

func init() {
	orm.RegisterLegacyDataMigration("base-workflow: unique step_key per workflow", ensureWorkflowActionStepKeyIndex)
	// Canvas save (replace_action_sequence) recreates every step under new ids, leaving seed
	// external ids stale; reimport then adopts the step with the same (workflow_id, step_key).
	orm.RegisterImportMatchFields(WorkflowActionModelName, "workflow_id", "step_key")
}

// workflowStepKeyIDs maps every step_key of a workflow's actions to its action id.
func workflowStepKeyIDs(ctx context.Context, workflowID int64) (map[string]int64, error) {
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WorkflowActionModelName, nil), orm.RecordMap{
		"workflow_id": workflowID,
		"__order":     "id asc",
	}, []string{"id", "step_key"})
	if err != nil {
		return nil, err
	}
	keys := make(map[string]int64, len(rows))
	for _, row := range rows {
		if key := strings.TrimSpace(stringValue(row["step_key"])); key != "" {
			keys[key] = int64Value(row["id"])
		}
	}
	return keys, nil
}

// guardWorkflowActionStepKeyUnique refuses a step_key already used by another action of the same
// workflow. normalized carries the resolved workflow_id and step_key; excludeID is the row written.
func guardWorkflowActionStepKeyUnique(ctx context.Context, normalized orm.RecordMap, excludeID int64) error {
	workflowID := workflowIDFromAny(normalized["workflow_id"])
	stepKey := strings.TrimSpace(stringValue(normalized["step_key"]))
	if workflowID <= 0 || stepKey == "" {
		return nil
	}
	keys, err := workflowStepKeyIDs(ctx, workflowID)
	if err != nil {
		return err
	}
	if id, exists := keys[stepKey]; exists && id != excludeID {
		log.Errorf("base.workflow.action step_key %q is already used in workflow %d", stepKey, workflowID)
		return orm.Conflictf("base.workflow.action step_key %q is already used by another step of this workflow", stepKey)
	}
	return nil
}

// assignUniqueStepKeys checks the step keys of a whole submitted sequence. An explicit duplicate is
// refused; a key generated from the step name (explicit[i] false) gets a _2, _3 ... suffix instead,
// so two unnamed steps of the same type still save.
func assignUniqueStepKeys(method string, items []orm.RecordMap, explicit []bool) error {
	used := make(map[string]struct{}, len(items))
	for index, item := range items {
		if !explicit[index] {
			continue
		}
		key := strings.TrimSpace(stringValue(item["step_key"]))
		if _, exists := used[key]; exists {
			log.Errorf("%s step_key %q is used by more than one step", method, key)
			return orm.InvalidParamsf("%s step_key %q is used by more than one step", method, key)
		}
		used[key] = struct{}{}
	}
	for index, item := range items {
		if explicit[index] {
			continue
		}
		base := strings.TrimSpace(stringValue(item["step_key"]))
		key := base
		for suffix := 2; ; suffix++ {
			if _, exists := used[key]; !exists {
				break
			}
			key = base + "_" + strconv.Itoa(suffix)
		}
		item["step_key"] = key
		used[key] = struct{}{}
	}
	return nil
}

// explicitStepKey reports whether the caller sent a non-empty step_key.
func explicitStepKey(values orm.RecordMap) bool {
	key, ok := stringFromAny(values["step_key"])
	return ok && strings.TrimSpace(key) != ""
}

// validateWorkflowEdgeStepKeys refuses an edge whose ends are not steps of the workflow.
func validateWorkflowEdgeStepKeys(method string, stepKeys map[string]int64, from, to string) error {
	var missing []string
	for _, key := range []string{from, to} {
		if _, ok := stepKeys[key]; !ok {
			missing = append(missing, key)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	log.Errorf("%s edge %s->%s references unknown step(s) %s", method, from, to, strings.Join(missing, ", "))
	return orm.InvalidParamsf("%s edge %s->%s references step(s) that do not exist in this workflow: %s; save the steps first", method, from, to, strings.Join(missing, ", "))
}

// guardWorkflowEdgeStepsExist loads the workflow's step keys and validates one normalized edge.
func guardWorkflowEdgeStepsExist(ctx context.Context, method string, normalized orm.RecordMap) error {
	workflowID := workflowIDFromAny(normalized["workflow_id"])
	if workflowID <= 0 {
		return nil
	}
	keys, err := workflowStepKeyIDs(ctx, workflowID)
	if err != nil {
		return err
	}
	return validateWorkflowEdgeStepKeys(method, keys, stringValue(normalized["from_step_key"]), stringValue(normalized["to_step_key"]))
}

// workflowEdgeRows lists the edges of a workflow.
func workflowEdgeRows(ctx context.Context, workflowID int64) ([]orm.RecordMap, error) {
	return orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WorkflowEdgeModelName, nil), orm.RecordMap{
		"workflow_id": workflowID,
		"__order":     "id asc",
	}, []string{"id", "from_step_key", "to_step_key"})
}

// deleteWorkflowEdgesNotIn removes every edge of the workflow with an end outside keep (the step
// keys that still exist). Used after a step delete and after replace_action_sequence.
func deleteWorkflowEdgesNotIn(ctx context.Context, workflowID int64, keep map[string]struct{}) error {
	rows, err := workflowEdgeRows(ctx, workflowID)
	if err != nil {
		return err
	}
	var orphanIDs []string
	for _, row := range rows {
		_, fromOK := keep[strings.TrimSpace(stringValue(row["from_step_key"]))]
		_, toOK := keep[strings.TrimSpace(stringValue(row["to_step_key"]))]
		if !fromOK || !toOK {
			orphanIDs = append(orphanIDs, strconv.FormatInt(int64Value(row["id"]), 10))
		}
	}
	if len(orphanIDs) == 0 {
		return nil
	}
	if _, err := orm.RuntimeRepositoryUnlink(ctx, orm.RuntimeModelRef(WorkflowEdgeModelName, nil), orphanIDs); err != nil {
		return err
	}
	log.Infof("base.workflow %d: removed %d edge(s) of steps that no longer exist", workflowID, len(orphanIDs))
	return nil
}

// renameWorkflowEdgeStepKey rewrites the edges of a workflow from oldKey to newKey.
func renameWorkflowEdgeStepKey(ctx context.Context, workflowID int64, oldKey, newKey string) error {
	if workflowID <= 0 || oldKey == "" || oldKey == newKey {
		return nil
	}
	rows, err := workflowEdgeRows(ctx, workflowID)
	if err != nil {
		return err
	}
	edgeModel := orm.RuntimeModelRef(WorkflowEdgeModelName, nil)
	for _, row := range rows {
		values := orm.RecordMap{}
		if strings.TrimSpace(stringValue(row["from_step_key"])) == oldKey {
			values["from_step_key"] = newKey
		}
		if strings.TrimSpace(stringValue(row["to_step_key"])) == oldKey {
			values["to_step_key"] = newKey
		}
		if len(values) == 0 {
			continue
		}
		if _, err := orm.RuntimeRepositoryWrite(ctx, edgeModel, []string{strconv.FormatInt(int64Value(row["id"]), 10)}, values); err != nil {
			return err
		}
	}
	return nil
}

// ensureWorkflowActionStepKeyIndex creates the unique index (workflow_id, step_key) on
// base_workflow_action when no workflow has duplicate keys. With duplicates it logs them and
// returns nil: boot continues, the application checks above still apply to new writes, and the
// index is created on a later boot once an operator has renamed the duplicates.
func ensureWorkflowActionStepKeyIndex(ctx context.Context, tx bun.IDB) error {
	table := WorkflowActionModelSpec.ResolveStorageName()
	var installed bool
	if err := tx.NewRaw("SELECT to_regclass(?) IS NOT NULL", table).Scan(ctx, &installed); err != nil {
		return fmt.Errorf("check %s table: %w", table, err)
	}
	if !installed {
		return nil
	}
	var indexed bool
	if err := tx.NewRaw("SELECT to_regclass(?) IS NOT NULL", workflowActionStepKeyIndex).Scan(ctx, &indexed); err != nil {
		return fmt.Errorf("check %s index: %w", workflowActionStepKeyIndex, err)
	}
	if indexed {
		return nil
	}
	type duplicateRow struct {
		WorkflowID int64  `bun:"workflow_id"`
		StepKey    string `bun:"step_key"`
		Count      int64  `bun:"count"`
	}
	var duplicates []duplicateRow
	if err := tx.NewRaw(`SELECT workflow_id, step_key, COUNT(*) AS count FROM ?
		WHERE workflow_id IS NOT NULL AND step_key IS NOT NULL
		GROUP BY workflow_id, step_key HAVING COUNT(*) > 1
		ORDER BY workflow_id, step_key LIMIT 50`, bun.Ident(table)).Scan(ctx, &duplicates); err != nil {
		return fmt.Errorf("find duplicate step keys in %s: %w", table, err)
	}
	if len(duplicates) > 0 {
		listed := make([]string, 0, len(duplicates))
		for _, row := range duplicates {
			listed = append(listed, fmt.Sprintf("workflow %d step %q x%d", row.WorkflowID, row.StepKey, row.Count))
		}
		sort.Strings(listed)
		log.Warnf("base-workflow: unique index %s not created: duplicate step keys (rename them, the index is retried on next boot): %s",
			workflowActionStepKeyIndex, strings.Join(listed, "; "))
		return nil
	}
	if _, err := tx.NewRaw("CREATE UNIQUE INDEX IF NOT EXISTS ? ON ? (workflow_id, step_key)",
		bun.Ident(workflowActionStepKeyIndex), bun.Ident(table)).Exec(ctx); err != nil {
		return fmt.Errorf("create %s: %w", workflowActionStepKeyIndex, err)
	}
	log.Infof("base-workflow: created unique index %s on %s", workflowActionStepKeyIndex, table)
	return nil
}
