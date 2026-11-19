package models

// File overview:
// - Responsibility: implements tbl.project_task's directional dependency
//   graph (`depend_on_ids`/`dependent_ids` parity with the reference module) — the blocked-state
//   recompute (kanban_state) and the computed inverse relation
//   (dependent_ids), plus the subtask-count computes. Ownership: project
//   module. Audit-sensitivity: low.
// - Key symbols: `recomputeProjectTaskKanbanState`, `computeProjectTaskDependentIDs`,
//   `computeProjectTaskSubtaskCount`, `computeProjectTaskClosedSubtaskCount`.
// - Simplification vs. reference: blocked-state only refreshes for a task
//   when THAT task is written (depend_on_ids or stage_id present in the
//   write payload) — it does not cascade to every task depending on it at
//   write time. A dependent task's kanban_state is refreshed the next time
//   it is itself read/written. Documented tradeoff, avoids an unbounded
//   write-time graph walk.

import (
	"context"
	"fmt"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"github.com/uptrace/bun"
)

// recomputeProjectTaskKanbanState mutates working["kanban_state"] in place:
// "blocked" when any of its (possibly just-written) depend_on_ids is not yet
// closed, else "ready" when the task's own is_closed is true, else "grey"
// (normal in-progress marker) — reusing the exact
// grey/blocked/ready convention modules/helpdesk/models/tbl_helpdesk_ticket.go
// already established for the same UI purpose.
func recomputeProjectTaskKanbanState(ctx context.Context, working orm.RecordMap) error {
	dependOnIDs := int64SliceFromAny(working["depend_on_ids"])
	if !hasRecordMapKey(working, "depend_on_ids") {
		// Not part of this write — nothing to recompute against; leave
		// kanban_state untouched unless a stage transition just closed it.
		if isClosed, ok := working["is_closed"].(bool); ok {
			if isClosed && !hasRecordMapKey(working, "kanban_state") {
				working["kanban_state"] = "ready"
			}
		}
		return nil
	}
	if len(dependOnIDs) == 0 {
		if isClosed, ok := working["is_closed"].(bool); ok && isClosed {
			working["kanban_state"] = "ready"
		} else {
			working["kanban_state"] = "grey"
		}
		return nil
	}
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(ProjectTaskModelName, nil), int64IDsToStrings(dependOnIDs), []string{"is_closed"})
	if err != nil {
		return err
	}
	blocked := false
	for _, row := range rows {
		if !boolValue(row["is_closed"]) {
			blocked = true
			break
		}
	}
	if blocked {
		working["kanban_state"] = "blocked"
	} else if isClosed, ok := working["is_closed"].(bool); ok && isClosed {
		working["kanban_state"] = "ready"
	} else {
		working["kanban_state"] = "grey"
	}
	return nil
}

// int64IDsToStrings formats a []int64 as the []string id list RuntimeRead expects.
func int64IDsToStrings(ids []int64) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, fmt.Sprint(id))
	}
	return out
}

// computeProjectTaskDependentIDs is the stored:false compute handler for
// dependent_ids — the inverse of depend_on_ids: for each requested task id,
// lists every OTHER task whose depend_on_ids contains it.
func computeProjectTaskDependentIDs(ctx context.Context, records orm.RecordSet, _ orm.FieldMetadata) (map[string]any, error) {
	type row struct {
		TaskID      int64 `bun:"task_id"`
		DependsOnID int64 `bun:"depends_on_id"`
	}
	var rows []row
	db := orm.BunDBFromContext(ctx)
	idsAsAny := recordSetIDsAsAny(records)
	if len(idsAsAny) == 0 {
		return map[string]any{}, nil
	}
	if err := db.NewSelect().
		Table("tbl_project_task_dependency_rel").
		Column("task_id", "depends_on_id").
		Where("depends_on_id IN (?)", bun.In(idsAsAny)).
		Scan(ctx, &rows); err != nil {
		return nil, err
	}
	out := make(map[string][]int64, len(idsAsAny))
	for _, r := range rows {
		key := fmt.Sprint(r.DependsOnID)
		out[key] = append(out[key], r.TaskID)
	}
	result := make(map[string]any, len(out))
	for key, value := range out {
		result[key] = value
	}
	return result, nil
}

// computeProjectTaskSubtaskCount counts every task whose parent_id is one of
// the requested ids.
func computeProjectTaskSubtaskCount(ctx context.Context, records orm.RecordSet, _ orm.FieldMetadata) (map[string]any, error) {
	return countSubtasksByParent(ctx, records, false)
}

// computeProjectTaskClosedSubtaskCount counts only closed direct subtasks.
func computeProjectTaskClosedSubtaskCount(ctx context.Context, records orm.RecordSet, _ orm.FieldMetadata) (map[string]any, error) {
	return countSubtasksByParent(ctx, records, true)
}

func countSubtasksByParent(ctx context.Context, records orm.RecordSet, doneOnly bool) (map[string]any, error) {
	type row struct {
		ParentID int64 `bun:"parent_id"`
		Count    int   `bun:"count"`
	}
	var rows []row
	db := orm.BunDBFromContext(ctx)
	idsAsAny := recordSetIDsAsAny(records)
	if len(idsAsAny) == 0 {
		return map[string]any{}, nil
	}
	query := db.NewSelect().
		Table("tbl_project_task").
		Column("parent_id").
		ColumnExpr("COUNT(*) AS count").
		Where("parent_id IN (?)", bun.In(idsAsAny))
	if doneOnly {
		query = query.Where("is_closed = true")
	}
	if err := query.Group("parent_id").Scan(ctx, &rows); err != nil {
		return nil, err
	}
	out := make(map[string]any, len(rows))
	for _, r := range rows {
		out[fmt.Sprint(r.ParentID)] = r.Count
	}
	return out, nil
}

// computeProjectTaskIsOnTime marks tasks whose date_done is not later than
// date_deadline. Tasks that are not done yet are false; done tasks without a
// deadline cannot be late, so they count as on time.
func computeProjectTaskIsOnTime(ctx context.Context, records orm.RecordSet, _ orm.FieldMetadata) (map[string]any, error) {
	idsAsAny := recordSetIDsAsAny(records)
	if len(idsAsAny) == 0 {
		return map[string]any{}, nil
	}
	type row struct {
		ID           int64      `bun:"id"`
		DateDone     *time.Time `bun:"date_done"`
		DateDeadline *time.Time `bun:"date_deadline"`
	}
	var rows []row
	if err := orm.BunDBFromContext(ctx).NewSelect().
		Table("tbl_project_task").
		Column("id", "date_done", "date_deadline").
		Where("id IN (?)", bun.In(idsAsAny)).
		Scan(ctx, &rows); err != nil {
		return nil, err
	}
	out := make(map[string]any, len(idsAsAny))
	for _, r := range rows {
		out[fmt.Sprint(r.ID)] = r.DateDone != nil && (r.DateDeadline == nil || !r.DateDone.After(*r.DateDeadline))
	}
	return out, nil
}

// recordSetIDsAsAny converts an orm.RecordSet's ids into a []any usable with bun.In.
func recordSetIDsAsAny(records orm.RecordSet) []any {
	out := make([]any, 0, len(records.IDs()))
	for _, id := range records.IDs() {
		if value, ok := int64FromAny(id); ok {
			out = append(out, value)
		}
	}
	return out
}
