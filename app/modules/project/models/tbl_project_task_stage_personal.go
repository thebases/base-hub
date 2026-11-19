package models

// File overview:
// - Responsibility: defines the `tbl.project_task_stage_personal` model — a
//   per-(task,user) join row letting each assignee keep their own personal
//   Kanban column for a shared task, independent of the task's shared
//   `stage_id` (`project.task.stage.personal` parity with the reference module). Ownership:
//   project module. Audit-sensitivity: low.
// - Key symbols: `init`, `ProjectTaskStagePersonalRecord`.
// - Simplification vs. reference: the personal-kanban VIEW itself (grouping
//   "My Tasks" by this row's stage_id) is a Phase 2 frontend concern — this
//   model only stores the join row and is auto-populated on task
//   create/assignee-change (see tbl_project_task_hooks.go).

import (
	"git.thebase.vn/base_hub/internal/orm"
)

// ProjectTaskStagePersonalRecord stores one user's personal stage for one task.
type ProjectTaskStagePersonalRecord struct {
	orm.BaseModel
	TaskID  int64  `bun:"task_id,notnull" json:"task_id" meta:"required,kind:many2one,model:tbl.project_task,ondelete:CASCADE,index,label:Task,help:Links to the tbl.project_task this personal stage applies to."`
	UserID  int64  `bun:"user_id,notnull" json:"user_id" meta:"required,kind:many2one,model:base.users,ondelete:CASCADE,index,label:User,help:Links to the base.users this personal stage belongs to."`
	StageID *int64 `bun:"stage_id" json:"stage_id" meta:"kind:many2one,model:tbl.project_task_type,ondelete:SET NULL,index,label:Personal Stage,help:Links to the tbl.project_task_type row (with user_id set) this user placed the task in."`
}

const ProjectTaskStagePersonalModelName = "tbl.project_task_stage_personal"

var ProjectTaskStagePersonalModelSpec = orm.ModelSpec{
	Name:        ProjectTaskStagePersonalModelName,
	Label:       "Personal Task Stages",
	Description: "Per-user personal Kanban column for a task, owned by the project module",
	Definition:  orm.ModelDefinition(ProjectTaskStagePersonalModelName, (*ProjectTaskStagePersonalRecord)(nil)),
}

var ProjectTaskStagePersonalModel = orm.NewHandlerWithModel[ProjectTaskStagePersonalRecord](ProjectTaskStagePersonalModelSpec)

func init() {
	RegisterRuntimeBootstrap(ProjectTaskStagePersonalModelName, 805, ProjectTaskStagePersonalModel.InitDefault, ProjectTaskStagePersonalModel.Shutdown)
}
