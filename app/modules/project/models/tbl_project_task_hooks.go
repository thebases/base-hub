package models

// File overview:
// - Responsibility: implements tbl.project_task's create/write business
//   logic: stage-transition-driven is_closed/kanban_state/date_done/
//   date_last_stage_update, the private-task-has-no-parent constraint,
//   stage_find default-stage resolution, personal-stage auto-assignment on
//   create, and triggering the next recurring occurrence on close. Ownership:
//   project module. Audit-sensitivity: low.
// - Key symbols: `createProjectTask`, `writeProjectTask`,
//   `applyProjectTaskStageTransition`, `populateMissingPersonalStages`.
// - Pattern: mirrors modules/helpdesk/models/tbl_helpdesk_ticket_hooks.go's
//   read-current-row-then-mutate-clone shape.

import (
	"context"
	"fmt"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// createProjectTask validates the private-task constraint, resolves a
// default stage when none was given, applies stage-transition-derived
// fields, creates the row, then best-effort populates personal stages for
// every assignee and (if recurring) the backing recurrence rule.
func createProjectTask(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		return nil, fmt.Errorf("tbl.project_task.create requires values")
	}
	working := cloneRecordMap(values)

	// _private_task_has_no_parent: a task with no project must not have a parent.
	if !hasProjectID(working) && hasParentID(working) {
		return nil, fmt.Errorf("tbl.project_task: a private task (no project) cannot have a parent task")
	}

	if !hasRecordMapKey(working, "stage_id") || working["stage_id"] == nil {
		if projectID, ok := int64FromAny(working["project_id"]); ok && projectID > 0 {
			if stageID, err := findDefaultProjectTaskStage(ctx, projectID); err == nil && stageID > 0 {
				working["stage_id"] = stageID
			}
		}
	}
	if err := applyProjectTaskStageTransition(ctx, working); err != nil {
		return nil, err
	}
	if err := recomputeProjectTaskKanbanState(ctx, working); err != nil {
		return nil, err
	}

	created, err := orm.CreateRecord(ctx, orm.RuntimeModelRef(ProjectTaskModelName, model), working)
	if err != nil {
		return nil, err
	}
	taskID := int64Value(created["id"])
	if taskID > 0 {
		populateMissingPersonalStages(ctx, taskID, int64SliceFromAny(created["user_ids"]))
		if boolValue(created["recurring_task"]) {
			if err := ensureProjectTaskRecurrenceRule(ctx, taskID, created); err != nil {
				log.Warnf("tbl.project_task %d: failed to create recurrence rule: %v", taskID, err)
			}
		}
	}
	return created, nil
}

// writeProjectTask reacts to stage_id/depend_on_ids/user_ids changes: stage
// transitions derive is_closed/date_done/date_last_stage_update/kanban_state
// (and, when the task just closed and is recurring, spawn the next
// occurrence); depend_on_ids changes refresh this task's own blocked state;
// user_ids changes populate personal stages for newly-added assignees.
func writeProjectTask(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		return nil, fmt.Errorf("tbl.project_task.write requires values")
	}
	working := cloneRecordMap(values)

	if hasRecordMapKey(working, "stage_id") {
		if err := applyProjectTaskStageTransition(ctx, working); err != nil {
			return nil, err
		}
	}
	if hasRecordMapKey(working, "depend_on_ids") || hasRecordMapKey(working, "stage_id") {
		if err := recomputeProjectTaskKanbanState(ctx, working); err != nil {
			return nil, err
		}
	}

	result, err := orm.WriteRecords(ctx, orm.RuntimeModelRef(ProjectTaskModelName, model), ids, working)
	if err != nil {
		return nil, err
	}

	if hasRecordMapKey(working, "user_ids") {
		for _, id := range ids {
			taskID := int64Value(id)
			populateMissingPersonalStages(ctx, taskID, int64SliceFromAny(working["user_ids"]))
		}
	}

	if isClosed, ok := working["is_closed"].(bool); ok && isClosed {
		for _, id := range ids {
			spawnNextProjectTaskOccurrenceIfRecurring(ctx, int64Value(id))
		}
	}

	return result, nil
}

func hasProjectID(working orm.RecordMap) bool {
	id, ok := int64FromAny(working["project_id"])
	return ok && id > 0
}

func hasParentID(working orm.RecordMap) bool {
	id, ok := int64FromAny(working["parent_id"])
	return ok && id > 0
}

// applyProjectTaskStageTransition mutates working in place: when stage_id is
// present, loads the target stage's IsClosed and sets
// is_closed/date_done/date_last_stage_update accordingly.
func applyProjectTaskStageTransition(ctx context.Context, working orm.RecordMap) error {
	stageID, ok := int64FromAny(working["stage_id"])
	if !ok || stageID <= 0 {
		return nil
	}
	stageRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(ProjectTaskTypeModelName, nil), []string{fmt.Sprint(stageID)}, []string{"is_closed"})
	if err != nil {
		return err
	}
	stageRow, err := firstRow(stageRows, "tbl.project_task_type")
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	working["date_last_stage_update"] = now
	isClosed := boolValue(stageRow["is_closed"])
	working["is_closed"] = isClosed
	if isClosed {
		working["date_done"] = now
	} else if !hasRecordMapKey(working, "date_done") {
		working["date_done"] = nil
	}
	return nil
}

// findDefaultProjectTaskStage resolves the lowest-sequence task-type row
// scoped to projectID (the reference module's `stage_find` fallback), skipping personal-stage
// template rows (user_id set).
func findDefaultProjectTaskStage(ctx context.Context, projectID int64) (int64, error) {
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(ProjectTaskTypeModelName, nil), orm.RecordMap{
		"project_ids": projectID,
		"__order":     "sequence",
		"__limit":     1,
	}, []string{"id"})
	if err != nil || len(rows) == 0 {
		return 0, err
	}
	return int64Value(rows[0]["id"]), nil
}

// populateMissingPersonalStages best-effort creates a
// tbl.project_task_stage_personal row for each assignee that doesn't already
// have one for taskID, defaulting to that user's lowest-sequence personal
// stage template (task_type row with user_id=that user), when one exists.
func populateMissingPersonalStages(ctx context.Context, taskID int64, userIDs []int64) {
	if taskID <= 0 || len(userIDs) == 0 {
		return
	}
	existingRows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(ProjectTaskStagePersonalModelName, nil), orm.RecordMap{"task_id": taskID}, []string{"user_id"})
	if err != nil {
		log.Warnf("tbl.project_task %d: failed to load existing personal stages: %v", taskID, err)
		return
	}
	existing := make(map[int64]bool, len(existingRows))
	for _, row := range existingRows {
		existing[int64Value(row["user_id"])] = true
	}
	for _, userID := range userIDs {
		if existing[userID] {
			continue
		}
		stageID := findDefaultPersonalStage(ctx, userID)
		values := orm.RecordMap{"task_id": taskID, "user_id": userID}
		if stageID > 0 {
			values["stage_id"] = stageID
		}
		if _, err := orm.RuntimeRepositoryCreate(ctx, orm.RuntimeModelRef(ProjectTaskStagePersonalModelName, nil), values); err != nil {
			log.Warnf("tbl.project_task %d: failed to create personal stage for user %d: %v", taskID, userID, err)
		}
	}
}

// findDefaultPersonalStage resolves userID's lowest-sequence personal-stage
// template row (task_type with user_id=userID), or 0 when none exists yet.
func findDefaultPersonalStage(ctx context.Context, userID int64) int64 {
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(ProjectTaskTypeModelName, nil), orm.RecordMap{
		"user_id": userID,
		"__order": "sequence",
		"__limit": 1,
	}, []string{"id"})
	if err != nil || len(rows) == 0 {
		return 0
	}
	return int64Value(rows[0]["id"])
}
