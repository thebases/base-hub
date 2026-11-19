package models

// File overview:
// - Responsibility: defines the `tbl.project_update` model — a status-update
//   feed entry for a project (`project.update` parity with the reference module). Ownership:
//   project module. Audit-sensitivity: low.
// - Key symbols: `init`, `ProjectUpdateRecord`, `createProjectUpdate`.
// - Simplification vs. reference: the auto-generated rich-text description
//   snapshot (milestone/task-stat summary) is deferred to Phase 2 — `create`
//   only defaults `progress`/`status` and stamps `date`.

import (
	"context"
	"fmt"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
)

// ProjectUpdateRecord stores one project status-update feed entry.
type ProjectUpdateRecord struct {
	orm.BaseModel
	Name        string    `bun:"name,notnull" json:"name" meta:"translate,required,index,label:Title,help:Stores the update's short title."`
	ProjectID   int64     `bun:"project_id,notnull" json:"project_id" meta:"required,kind:many2one,model:tbl.project,ondelete:CASCADE,index,label:Project,help:Links this update to its owning tbl.project."`
	Status      string    `bun:"status,notnull,default:'to_define'" json:"status" meta:"required,index,label:Status,help:Stores the project status this update reports." selection:"(on_track,On Track),(at_risk,At Risk),(off_track,Off Track),(on_hold,On Hold),(to_define,Set Status),(done,Done)"`
	Progress    int       `bun:"progress,notnull,default:0" json:"progress" meta:"required,label:Progress,help:Stores the project completion percentage this update reports."`
	UserID      *int64    `bun:"user_id" json:"user_id" meta:"kind:many2one,model:base.users,ondelete:SET NULL,index,label:Author,help:Links to the base.users who authored this update."`
	Description string    `bun:"description,nullzero" json:"description" meta:"kind:text,label:Description,help:Stores the update's free-text body."`
	Date        time.Time `bun:"date,notnull,default:now()" json:"date" meta:"index,readonly,label:Date,help:Stores when this update was posted."`
}

const ProjectUpdateModelName = "tbl.project_update"

var ProjectUpdateModelSpec = orm.ModelSpec{
	Name:        ProjectUpdateModelName,
	Label:       "Project Updates",
	Description: "Project status-update feed owned by the project module",
	Definition:  orm.ModelDefinition(ProjectUpdateModelName, (*ProjectUpdateRecord)(nil)),
	StandardHooks: orm.StandardRuntimeHooks{
		Create: createProjectUpdate,
	},
	DefaultOrder: "date desc",
}

var ProjectUpdateModel = orm.NewHandlerWithModel[ProjectUpdateRecord](ProjectUpdateModelSpec)

func init() {
	RegisterRuntimeBootstrap(ProjectUpdateModelName, 809, ProjectUpdateModel.InitDefault, ProjectUpdateModel.Shutdown)
}

// createProjectUpdate stamps date/defaults, creates the row, then mirrors
// status onto the parent project (last_update_status/last_update_id) — the
// same "update the header, not just the feed" behavior the reference module's
// project.project.write() triggers in the other direction.
func createProjectUpdate(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		return nil, fmt.Errorf("tbl.project_update.create requires values")
	}
	working := cloneRecordMap(values)
	if !hasRecordMapKey(working, "date") {
		working["date"] = time.Now().UTC()
	}
	created, err := orm.CreateRecord(ctx, orm.RuntimeModelRef(ProjectUpdateModelName, model), working)
	if err != nil {
		return nil, err
	}
	projectID := int64Value(created["project_id"])
	updateID := int64Value(created["id"])
	if projectID > 0 && updateID > 0 {
		if _, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(ProjectModelName, nil), []string{fmt.Sprint(projectID)}, orm.RecordMap{
			"last_update_status": created["status"],
			"last_update_id":     updateID,
		}); err != nil {
			return nil, err
		}
	}
	return created, nil
}
