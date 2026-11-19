package models

// File overview:
// - Responsibility: defines the `tbl.project_task_type` model — a TASK kanban
//   stage (`project.task.type` parity with the reference module), distinct from `tbl.project_stage`
//   above: a task-type row is shared across one or more projects (ProjectIDs
//   m2m), not owned by exactly one project. A row with UserID set is a
//   personal-stage template (see `tbl.project_task_stage_personal`), not a
//   shared project stage. Ownership: project module. Audit-sensitivity: low.
// - Key symbols: `init`, `ProjectTaskTypeRecord`, `ProjectTaskTypeModelSpec`.
// - Simplification vs. reference: rating-request scheduling
//   (rating_template_id/rating_status/rating_status_period/
//   rating_request_deadline) is deferred to Phase 2 per the plan (no
//   mail-template infra exists yet) — only `rating_active` is kept as a
//   parity marker for the UI toggle.

import (
	"git.thebase.vn/base_hub/internal/orm"
)

// ProjectTaskTypeRecord stores one task kanban column, optionally scoped to
// a single user as a personal-stage template.
type ProjectTaskTypeRecord struct {
	orm.BaseModel
	Name       string  `bun:"name,notnull" json:"name" meta:"translate,required,index,label:Name,help:Stores the stage's display name."`
	Sequence   int     `bun:"sequence,notnull,default:10" json:"sequence" meta:"required,index,label:Sequence,help:Stores the stage's ordering position."`
	Fold       bool    `bun:"fold,notnull,default:false" json:"fold" meta:"required,label:Fold,help:Controls whether this stage's kanban column starts folded."`
	IsClosed   bool    `bun:"is_closed,notnull,default:false" json:"is_closed" meta:"required,label:Is Closed,help:Controls whether a task in this stage is considered done/closed."`
	Color      int     `bun:"color,notnull,default:0" json:"color" meta:"label:Color,help:Stores the stage color index; tasks in this stage are drawn with it in kanban and gantt."`
	ProjectIDs []int64 `bun:"project_ids,type:jsonb,nullzero" json:"project_ids" meta:"kind:many2many,model:tbl.project,relation_table:tbl_project_task_type_project_rel,relation_column:type_id,relation_key:project_id,label:Projects,help:Lists the tbl.project records that expose this stage in their task pipeline."`
	// UserID, when set, marks this row as a personal-stage template owned by
	// that user rather than a shared project stage.
	UserID       *int64 `bun:"user_id" json:"user_id" meta:"kind:many2one,model:base.users,ondelete:SET NULL,index,label:Personal Stage Owner,help:When set, marks this row as a personal Kanban column template owned by this user instead of a shared project stage."`
	RatingActive bool   `bun:"rating_active,notnull,default:false" json:"rating_active" meta:"required,label:Rating Requested,help:Controls whether moving a task into this stage requests a customer satisfaction rating (scheduling deferred to Phase 2)."`
}

const ProjectTaskTypeModelName = "tbl.project_task_type"

var ProjectTaskTypeModelSpec = orm.ModelSpec{
	Name:         ProjectTaskTypeModelName,
	Label:        "Task Stages",
	Description:  "Task kanban stages (shared across projects) owned by the project module",
	Definition:   orm.ModelDefinition(ProjectTaskTypeModelName, (*ProjectTaskTypeRecord)(nil)),
	DefaultOrder: "sequence",
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "unlink_with_reassign", Public: true, UsesPathID: true, Call: unlinkProjectTaskTypeWithReassign},
	},
}

var ProjectTaskTypeModel = orm.NewHandlerWithModel[ProjectTaskTypeRecord](ProjectTaskTypeModelSpec)

func init() {
	RegisterRuntimeBootstrap(ProjectTaskTypeModelName, 804, ProjectTaskTypeModel.InitDefault, ProjectTaskTypeModel.Shutdown)
}
