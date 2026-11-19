package models

// File overview:
// - Responsibility: implements the reassign-then-delete RuntimeMethod shared
//   by tbl.project_stage and tbl.project_task_type (mirrors the reference module's
//   project_project_stage_delete/project_task_type_delete wizards, folded
//   into one RuntimeMethod parameter per plan §1 Phase 3 rather than a
//   separate wizard model). Ownership: project module. Audit-sensitivity: low.
// - Key symbols: `unlinkProjectStageWithReassign`, `unlinkProjectTaskTypeWithReassign`.

import (
	"context"
	"fmt"

	"git.thebase.vn/base_hub/internal/orm"
)

// unlinkProjectStageWithReassign reassigns every tbl.project referencing
// stageID (ids[0]) onto kwargs["replacement_stage_id"] (when given) before
// deleting the stage, so no project is left dangling on a deleted stage.
func unlinkProjectStageWithReassign(ctx context.Context, model *orm.ModelMetadata, ids []string, _ []any, kwargs map[string]any) (any, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("tbl.project_stage.unlink_with_reassign requires a stage id")
	}
	if replacementID, ok := int64FromAny(kwargs["replacement_stage_id"]); ok && replacementID > 0 {
		affected, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(ProjectModelName, nil), orm.RecordMap{"stage_id": int64Value(ids[0])}, []string{"id"})
		if err != nil {
			return nil, err
		}
		for _, row := range affected {
			affectedID := fmt.Sprint(int64Value(row["id"]))
			if _, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(ProjectModelName, nil), []string{affectedID}, orm.RecordMap{"stage_id": replacementID}); err != nil {
				return nil, err
			}
		}
	}
	return orm.RuntimeRepositoryUnlink(ctx, orm.RuntimeModelRef(ProjectStageModelName, model), ids)
}

// unlinkProjectTaskTypeWithReassign is the task-stage equivalent: reassigns
// every tbl.project_task referencing this stage onto
// kwargs["replacement_stage_id"] before deleting.
func unlinkProjectTaskTypeWithReassign(ctx context.Context, model *orm.ModelMetadata, ids []string, _ []any, kwargs map[string]any) (any, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("tbl.project_task_type.unlink_with_reassign requires a stage id")
	}
	if replacementID, ok := int64FromAny(kwargs["replacement_stage_id"]); ok && replacementID > 0 {
		affected, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(ProjectTaskModelName, nil), orm.RecordMap{"stage_id": int64Value(ids[0])}, []string{"id"})
		if err != nil {
			return nil, err
		}
		for _, row := range affected {
			affectedID := fmt.Sprint(int64Value(row["id"]))
			if _, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(ProjectTaskModelName, nil), []string{affectedID}, orm.RecordMap{"stage_id": replacementID}); err != nil {
				return nil, err
			}
		}
	}
	return orm.RuntimeRepositoryUnlink(ctx, orm.RuntimeModelRef(ProjectTaskTypeModelName, model), ids)
}
