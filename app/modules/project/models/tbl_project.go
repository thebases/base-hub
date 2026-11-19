package models

// File overview:
// - Responsibility: defines the `tbl.project` model — a project header
//   (`project.project` parity with the reference module; see the project
//   module's implementation plan under `.agents/planning/`). Ownership:
//   project module. Audit-sensitivity: low.
// - Key symbols: `init`, `ProjectRecord`, `ProjectModelSpec`.
// - Simplification vs. reference: the reference module's `project.project.write()` also
//   auto-creates a `project.update` row when `last_update_status` is written
//   directly on the project. This repo only supports the (more common)
//   opposite direction — creating a `tbl.project_update` row mirrors its
//   status back onto the project (see tbl_project_update.go's
//   `createProjectUpdate`) — to avoid a create/write feedback cycle between
//   the two models.

import (
	"context"

	"git.thebase.vn/base_hub/internal/orm"
)

// ProjectRecord stores one project header.
type ProjectRecord struct {
	orm.BaseModel
	Name        string `bun:"name,notnull" json:"name" meta:"translate,required,index,label:Name,help:Stores the project's display name."`
	Description string `bun:"description,nullzero" json:"description" meta:"kind:text,label:Description,help:Stores the project's free-text description."`
	CompanyID   *int64 `bun:"company_id" json:"company_id" meta:"kind:many2one,model:tbl.company,ondelete:SET NULL,index,label:Company ID,help:Links this record to the related tbl.company record for company id."`

	Sequence  int    `bun:"sequence,notnull,default:10" json:"sequence" default:"10" meta:"required,index,label:Sequence,help:Stores the project's ordering position in list/kanban views."`
	PartnerID *int64 `bun:"partner_id" json:"partner_id" meta:"kind:many2one,model:tbl.partner,ondelete:SET NULL,index,label:Customer,help:Links this project to the tbl.partner it is run for."`
	UserID    *int64 `bun:"user_id" json:"user_id" meta:"kind:many2one,model:base.users,ondelete:SET NULL,index,label:Project Manager,help:Links this project to the base.users responsible for it."`
	Color     int    `bun:"color,notnull,default:0" json:"color" default:"0" meta:"label:Color,help:Stores the kanban card color index."`
	// PrivacyVisibility keeps all four reference values even though this repo has
	// no portal concept yet (see plan §1) — it still gates
	// collaborator/follower behavior internally.
	PrivacyVisibility string  `bun:"privacy_visibility,notnull,default:'invited_users'" json:"privacy_visibility" default:"invited_users" meta:"required,label:Visibility,help:Stores who can access this project." selection:"(followers,Invited internal users),(invited_users,Invited portal users and all internal users),(employees,All internal users),(portal,Invited portal users and all internal users)"`
	DateStart         *string `bun:"date_start" json:"date_start" meta:"kind:date,label:Start Date,help:Stores the project's planned start date."`
	Date              *string `bun:"date" json:"date" meta:"kind:date,label:Expiration Date,help:Stores the project's planned end date."`

	AllowTaskDependencies bool `bun:"allow_task_dependencies,notnull,default:false" json:"allow_task_dependencies" default:"false" meta:"required,label:Task Dependencies,help:Controls whether this project's tasks may declare blocking dependencies."`
	AllowMilestones       bool `bun:"allow_milestones,notnull,default:false" json:"allow_milestones" default:"false" meta:"required,label:Milestones,help:Controls whether this project exposes milestones."`
	AllowRecurringTasks   bool `bun:"allow_recurring_tasks,notnull,default:false" json:"allow_recurring_tasks" default:"false" meta:"required,label:Recurring Tasks,help:Controls whether this project's tasks may recur."`

	StageID *int64  `bun:"stage_id" json:"stage_id" meta:"kind:many2one,model:tbl.project_stage,ondelete:SET NULL,index,label:Stage,help:Links this project to its current pipeline stage."`
	TagIDs  []int64 `bun:"tag_ids,type:jsonb,nullzero" json:"tag_ids" meta:"kind:many2many,model:tbl.project_tag,relation_table:tbl_project_tag_rel,relation_column:project_id,relation_key:tag_id,label:Tags,help:Lists the tbl.project_tag records attached to this project."`

	IsTemplate       bool    `bun:"is_template,notnull,default:false" json:"is_template" default:"false" meta:"required,label:Is a Template,help:Marks this project as a reusable template rather than a live project."`
	FavoriteUserIDs  []int64 `bun:"favorite_user_ids,type:jsonb,nullzero" json:"favorite_user_ids" meta:"kind:many2many,model:base.users,relation_table:tbl_project_favorite_user_rel,relation_column:project_id,relation_key:user_id,label:Favorite Of,help:Lists the base.users records that starred this project."`
	LastUpdateStatus string  `bun:"last_update_status,notnull,default:'to_define'" json:"last_update_status" default:"to_define" meta:"required,index,label:Last Update Status,help:Stores this project's current status-update state." selection:"(on_track,On Track),(at_risk,At Risk),(off_track,Off Track),(on_hold,On Hold),(to_define,Set Status),(done,Done)"`
	LastUpdateID     *int64  `bun:"last_update_id" json:"last_update_id" meta:"kind:many2one,model:tbl.project_update,ondelete:SET NULL,index,readonly,label:Last Update,help:Links to the most recent tbl.project_update row, auto-created whenever last_update_status changes."`
}

const ProjectModelName = "tbl.project"

var ProjectDefinition = orm.ModelDefinition(ProjectModelName, (*ProjectRecord)(nil))

var ProjectModelSpec = orm.ModelSpec{
	Name:         ProjectModelName,
	Label:        "Project",
	Description:  "Project header owned by the project module",
	Definition:   ProjectDefinition,
	DefaultOrder: "sequence",
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "default_get", Public: true, ModelOnly: true, ReadOnly: true, Call: defaultGetProject},
	},
}

var ProjectModel = orm.NewHandlerWithModel[ProjectRecord](ProjectModelSpec)

func init() {
	RegisterRuntimeBootstrap(ProjectModelName, 800, ProjectModel.InitDefault, ProjectModel.Shutdown)
}

// defaultGetProject extends the generic defaults (sequence 10, visibility,
// status "Set Status") with the "To Do" stage so a new project starts in it.
func defaultGetProject(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
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
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(ProjectStageModelName, nil), orm.RecordMap{"name": "To Do"}, []string{"id"})
	if err == nil && len(rows) > 0 {
		if id, ok := int64FromAny(rows[0]["id"]); ok {
			defaults["stage_id"] = id
		}
	}
	return defaults, nil
}
