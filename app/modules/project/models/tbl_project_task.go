package models

// File overview:
// - Responsibility: defines the `tbl.project_task` model — a project task,
//   the largest model in this module (`project.task` parity with the
//   reference module; see the project module's implementation plan under
//   `.agents/planning/`). Ownership: project module. Audit-sensitivity: low.
// - Key symbols: `init`, `ProjectTaskRecord`, `ProjectTaskModelSpec`.
// - Simplifications vs. reference (documented once here, applies to the
//   whole model): no portal (`access_token`/`access_url`/readable/writable
//   field whitelists dropped); no dynamic "Properties" custom-fields
//   framework; no accounting/profitability fields; the full reference `state`
//   selection (01_in_progress/.../04_waiting_normal) is simplified to
//   `kanban_state` (grey/blocked/ready, reusing the exact convention already
//   established by modules/helpdesk/models/tbl_helpdesk_ticket.go) computed
//   from task-dependency blocking — see dependency_service.go.
//   `personal_stage_id`/`recurrence_id` business logic lives in
//   tbl_project_task_hooks.go/recurrence_service.go respectively.

import (
	"context"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
)

// ProjectTaskRecord stores one project task.
type ProjectTaskRecord struct {
	orm.BaseModel
	Name        string `bun:"name,notnull" json:"name" meta:"translate,required,index,label:Title,help:Stores the task's display title."`
	Description string `bun:"description,nullzero" json:"description" meta:"kind:text,label:Description,help:Stores the task's free-text description."`
	// Priority is the reference project module's simplified two-value "star" priority
	// (distinct from helpdesk's four-value scale).
	Priority string  `bun:"priority,notnull,default:'0'" json:"priority" meta:"required,label:Priority,help:Stores whether this task is starred/important." selection:"(0,Normal),(1,Important)"`
	Sequence int     `bun:"sequence,notnull,default:10" json:"sequence" meta:"required,index,label:Sequence,help:Stores the task's ordering position within its stage."`
	StageID  *int64  `bun:"stage_id" json:"stage_id" meta:"kind:many2one,model:tbl.project_task_type,ondelete:SET NULL,index,label:Stage,help:Links this task to its current Kanban stage."`
	TagIDs   []int64 `bun:"tag_ids,type:jsonb,nullzero" json:"tag_ids" meta:"kind:many2many,model:tbl.project_tag,relation_table:tbl_project_task_tag_rel,relation_column:task_id,relation_key:tag_id,label:Tags,help:Lists the tbl.project_tag records attached to this task."`
	// StageColor reads the stage's color index so kanban cards and gantt pills
	// are tinted by stage; non-stored, so a stage color edit shows immediately.
	StageColor int `bun:"stage_color" json:"stage_color" meta:"related:stage_id.color,stored:false,readonly,label:Stage Color,help:Reads the color index of this task's stage; drives the kanban card and gantt pill color."`
	// KanbanState/IsClosed mirror the current stage's is_closed flag plus
	// task-dependency blocking — recomputed by the stage/dependency write
	// hooks in tbl_project_task_hooks.go/dependency_service.go, not a
	// generic compute-field pass (only ever changes on those two triggers).
	KanbanState string `bun:"kanban_state,notnull,default:'grey'" json:"kanban_state" meta:"required,label:Kanban State,help:Stores the kanban activity-state indicator for this task." selection:"(grey,In Progress),(blocked,Blocked),(ready,Ready)"`
	IsClosed    bool   `bun:"is_closed,notnull,default:false" json:"is_closed" meta:"required,index,label:Is Closed,help:Mirrors the current stage's is_closed flag; recomputed whenever stage_id changes."`

	// DateStart/DateDeadline bound the planned window the gantt view draws;
	// DateDone is the actual completion timestamp (set on closing-stage
	// transition) used to judge whether the task finished in time.
	DateStart           *time.Time `bun:"date_start" json:"date_start" meta:"index,label:Start Date,help:Stores the task's planned start date."`
	DateDeadline        *time.Time `bun:"date_deadline" json:"date_deadline" meta:"index,label:Deadline,help:Stores the task's target completion date."`
	DateAssign          *time.Time `bun:"date_assign" json:"date_assign" meta:"index,label:Assign Date,help:Stores when this task was first assigned to a user."`
	DateDone            *time.Time `bun:"date_done" json:"date_done" meta:"index,label:Done Date,help:Stores when this task was actually marked done/closed."`
	DateLastStageUpdate *time.Time `bun:"date_last_stage_update" json:"date_last_stage_update" meta:"index,label:Last Stage Update,help:Stores when this task's stage last changed."`

	// ProjectID is nullable: a task with no project is a private "to-do"
	// (reference project_todo-equivalent parity, see plan §4 — falls out for free here).
	ProjectID        *int64 `bun:"project_id" json:"project_id" meta:"kind:many2one,model:tbl.project,ondelete:CASCADE,index,label:Project,help:Links this task to its owning tbl.project. Empty means a private to-do task."`
	DisplayInProject bool   `bun:"display_in_project,notnull,default:true" json:"display_in_project" meta:"required,label:Display in Project,help:Controls whether this task shows in its project's own task list (false for tasks only shown as a parent's subtask row)."`

	AllocatedHours float64 `bun:"allocated_hours,notnull,default:0" json:"allocated_hours" meta:"label:Allocated Hours,help:Stores the planned effort for this task, in hours."`
	RoleIDs        []int64 `bun:"role_ids,type:jsonb,nullzero" json:"role_ids" meta:"kind:many2many,model:tbl.project_role,relation_table:tbl_project_task_role_rel,relation_column:task_id,relation_key:role_id,label:Roles,help:Lists the tbl.project_role placeholders assigned to this task."`
	UserIDs        []int64 `bun:"user_ids,type:jsonb,nullzero" json:"user_ids" meta:"kind:many2many,model:base.users,relation_table:tbl_project_task_assignee_rel,relation_column:task_id,relation_key:user_id,label:Assignees,help:Lists the base.users records assigned to this task."`

	PartnerID    *int64 `bun:"partner_id" json:"partner_id" meta:"kind:many2one,model:tbl.partner,ondelete:SET NULL,index,label:Customer,help:Links this task to the tbl.partner it was raised for."`
	PartnerPhone string `bun:"partner_phone,nullzero" json:"partner_phone" meta:"label:Phone,help:Stores a contact phone number for this task, independent of the linked partner's own record."`
	EmailFrom    string `bun:"email_from,nullzero" json:"email_from" meta:"label:Email From,help:Stores the originating email address, when this task was created from an inbound message."`
	EmailCc      string `bun:"email_cc,nullzero" json:"email_cc" meta:"label:Email Cc,help:Stores extra email addresses to keep in the loop on chatter notifications."`

	CompanyID *int64 `bun:"company_id" json:"company_id" meta:"kind:many2one,model:tbl.company,ondelete:SET NULL,index,label:Company ID,help:Links this record to the related tbl.company record for company id."`
	Color     int    `bun:"color,notnull,default:0" json:"color" meta:"required,label:Color,help:Stores the kanban card color index."`

	// ParentID/subtasks: self-referential. A private task (ProjectID nil)
	// must not have a parent (mirrors the reference module's "_private_task_has_no_parent"
	// constraint) — enforced in tbl_project_task_hooks.go.
	ParentID           *int64 `bun:"parent_id" json:"parent_id" meta:"kind:many2one,model:tbl.project_task,ondelete:SET NULL,index,label:Parent Task,help:Links this task to its parent task, when it is a subtask."`
	SubtaskCount       int    `bun:"subtask_count" json:"subtask_count" meta:"compute:_compute_subtask_count,stored:false,readonly,label:Subtask Count,help:Counts the direct subtasks of this task."`
	IsOnTime           bool   `bun:"is_on_time" json:"is_on_time" meta:"compute:_compute_is_on_time,stored:false,readonly,label:On Time,help:True when the task is done and date_done is not later than date_deadline (a done task with no deadline counts as on time)."`
	ClosedSubtaskCount int    `bun:"closed_subtask_count" json:"closed_subtask_count" meta:"compute:_compute_closed_subtask_count,stored:false,readonly,label:Closed Subtask Count,help:Counts the closed direct subtasks of this task."`

	MilestoneID *int64 `bun:"milestone_id" json:"milestone_id" meta:"kind:many2one,model:tbl.project_milestone,ondelete:SET NULL,index,label:Milestone,help:Links this task to the tbl.project_milestone it contributes to."`

	// DependOnIDs/DependentIDs: directional task-dependency graph (not the
	// symmetric relation helpdesk's related_ticket_ids uses) — DependOnIDs
	// is stored; DependentIDs is its computed inverse. See
	// dependency_service.go.
	DependOnIDs  []int64 `bun:"depend_on_ids,type:jsonb,nullzero" json:"depend_on_ids" meta:"kind:many2many,model:tbl.project_task,relation_table:tbl_project_task_dependency_rel,relation_column:task_id,relation_key:depends_on_id,label:Blocked By,help:Lists the tbl.project_task records this task is blocked by."`
	DependentIDs []int64 `bun:"dependent_ids" json:"dependent_ids" meta:"compute:_compute_dependent_ids,stored:false,readonly,kind:many2many,model:tbl.project_task,label:Blocking,help:Lists the tbl.project_task records blocked by this task (computed inverse of depend_on_ids)."`

	// Recurrence: RecurrenceID points at a base.calendar.recurrence row
	// (base-calendar module, reused rather than reinventing date math — see
	// recurrence_service.go and plan §2). repeat_* columns are the
	// project-specific "what pattern to create the rule with" config,
	// mirrored onto/read back from that row.
	RecurringTask  bool       `bun:"recurring_task,notnull,default:false" json:"recurring_task" meta:"required,label:Recurring,help:Marks this task as the template for a recurring series."`
	RecurrenceID   *int64     `bun:"recurrence_id" json:"recurrence_id" meta:"kind:many2one,model:base.calendar.recurrence,ondelete:SET NULL,index,label:Recurrence Rule,help:Links to the base.calendar.recurrence row driving this task's recurrence."`
	RepeatInterval int        `bun:"repeat_interval,notnull,default:1" json:"repeat_interval" meta:"label:Repeat Every,help:Stores the recurrence interval, in units of repeat_unit."`
	RepeatUnit     string     `bun:"repeat_unit,notnull,default:'week'" json:"repeat_unit" meta:"label:Repeat Unit,help:Stores the recurrence unit." selection:"(day,Days),(week,Weeks),(month,Months),(year,Years)"`
	RepeatType     string     `bun:"repeat_type,notnull,default:'forever'" json:"repeat_type" meta:"label:Repeat Until,help:Stores whether the recurrence runs forever or stops at repeat_until." selection:"(forever,Forever),(until,Until a date)"`
	RepeatUntil    *time.Time `bun:"repeat_until" json:"repeat_until" meta:"kind:date,label:Repeat Until Date,help:Stores the last date this recurrence generates an occurrence for, when repeat_type=until."`

	IsTemplate bool `bun:"is_template,notnull,default:false" json:"is_template" meta:"required,label:Is a Template,help:Marks this task as a reusable template rather than a live task."`
}

const ProjectTaskModelName = "tbl.project_task"

var ProjectTaskModelSpec = orm.ModelSpec{
	Name:        ProjectTaskModelName,
	Label:       "Project Tasks",
	Description: "Project tasks owned by the project module",
	Definition:  orm.ModelDefinition(ProjectTaskModelName, (*ProjectTaskRecord)(nil)),
	StandardHooks: orm.StandardRuntimeHooks{
		Create: createProjectTask,
		Write:  writeProjectTask,
	},
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "default_get", Public: true, ModelOnly: true, ReadOnly: true, Call: defaultGetProjectTask},
	},
}

var ProjectTaskModel = orm.NewHandlerWithModel[ProjectTaskRecord](ProjectTaskModelSpec)

func init() {
	RegisterRuntimeBootstrap(ProjectTaskModelName, 811, ProjectTaskModel.InitDefault, ProjectTaskModel.Shutdown)
	orm.RegisterCompute(ProjectTaskModelName, "subtask_count", computeProjectTaskSubtaskCount)
	orm.RegisterCompute(ProjectTaskModelName, "closed_subtask_count", computeProjectTaskClosedSubtaskCount)
	orm.RegisterCompute(ProjectTaskModelName, "is_on_time", computeProjectTaskIsOnTime)
	orm.RegisterCompute(ProjectTaskModelName, "dependent_ids", computeProjectTaskDependentIDs)
}

// defaultGetProjectTask extends the generic defaults with the "New" stage so a
// new task starts in it.
func defaultGetProjectTask(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	raw, err := orm.RuntimeDefaultGet(ctx, model, ids, args, kwargs)
	if err != nil {
		return nil, err
	}
	defaults, ok := raw.(map[string]any)
	if !ok {
		return raw, nil
	}
	if _, set := defaults["stage_id"]; set {
		return defaults, nil
	}
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(ProjectTaskTypeModelName, nil), orm.RecordMap{"name": "New"}, []string{"id"})
	if err == nil && len(rows) > 0 {
		if id, ok := int64FromAny(rows[0]["id"]); ok {
			defaults["stage_id"] = id
		}
	}
	return defaults, nil
}
