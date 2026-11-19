package models

// File overview:
// - Responsibility: defines the `tbl.project_milestone` model — a project
//   deliverable/checkpoint with a deadline (`project.milestone` parity with
//   the reference module). Ownership: project module. Audit-sensitivity: low.
// - Key symbols: `init`, `ProjectMilestoneRecord`, `toggleProjectMilestoneReached`,
//   `computeProjectMilestoneTaskCount`, `computeProjectMilestoneDoneTaskCount`.

import (
	"context"
	"fmt"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"github.com/uptrace/bun"
)

// ProjectMilestoneRecord stores one project milestone.
type ProjectMilestoneRecord struct {
	orm.BaseModel
	Name        string     `bun:"name,notnull" json:"name" meta:"translate,required,index,label:Name,help:Stores the milestone's display name."`
	Sequence    int        `bun:"sequence,notnull,default:10" json:"sequence" meta:"required,index,label:Sequence,help:Stores the milestone's ordering position."`
	ProjectID   int64      `bun:"project_id,notnull" json:"project_id" meta:"required,kind:many2one,model:tbl.project,ondelete:CASCADE,index,label:Project,help:Links this milestone to its owning tbl.project."`
	Deadline    *string    `bun:"deadline" json:"deadline" meta:"kind:date,index,label:Deadline,help:Stores the milestone's target date."`
	IsReached   bool       `bun:"is_reached,notnull,default:false" json:"is_reached" meta:"required,index,label:Reached,help:Marks whether this milestone has been reached."`
	ReachedDate *time.Time `bun:"reached_date" json:"reached_date" meta:"readonly,label:Reached Date,help:Stores when is_reached was last toggled true; cleared when toggled back to false."`

	TaskCount     int `bun:"task_count" json:"task_count" meta:"compute:_compute_task_count,stored:false,readonly,label:Task Count,help:Counts the tasks linked to this milestone."`
	DoneTaskCount int `bun:"done_task_count" json:"done_task_count" meta:"compute:_compute_done_task_count,stored:false,readonly,label:Done Task Count,help:Counts the closed tasks linked to this milestone."`
}

const ProjectMilestoneModelName = "tbl.project_milestone"

var ProjectMilestoneModelSpec = orm.ModelSpec{
	Name:         ProjectMilestoneModelName,
	Label:        "Project Milestones",
	Description:  "Project milestones owned by the project module",
	Definition:   orm.ModelDefinition(ProjectMilestoneModelName, (*ProjectMilestoneRecord)(nil)),
	DefaultOrder: "sequence",
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "toggle_is_reached", Public: true, UsesPathID: true, Call: toggleProjectMilestoneReached},
	},
}

var ProjectMilestoneModel = orm.NewHandlerWithModel[ProjectMilestoneRecord](ProjectMilestoneModelSpec)

func init() {
	RegisterRuntimeBootstrap(ProjectMilestoneModelName, 808, ProjectMilestoneModel.InitDefault, ProjectMilestoneModel.Shutdown)
	orm.RegisterCompute(ProjectMilestoneModelName, "task_count", computeProjectMilestoneTaskCount)
	orm.RegisterCompute(ProjectMilestoneModelName, "done_task_count", computeProjectMilestoneDoneTaskCount)
}

// toggleProjectMilestoneReached flips is_reached and stamps/clears reached_date.
func toggleProjectMilestoneReached(ctx context.Context, model *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("tbl.project_milestone.toggle_is_reached requires a milestone id")
	}
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(ProjectMilestoneModelName, model), ids, []string{"is_reached"})
	if err != nil {
		return nil, err
	}
	row, err := firstRow(rows, "tbl.project_milestone")
	if err != nil {
		return nil, err
	}
	values := orm.RecordMap{"is_reached": !boolValue(row["is_reached"])}
	if values["is_reached"].(bool) {
		values["reached_date"] = time.Now().UTC()
	} else {
		values["reached_date"] = nil
	}
	return orm.WriteRecords(ctx, orm.RuntimeModelRef(ProjectMilestoneModelName, model), ids, values)
}

// computeProjectMilestoneTaskCount counts every task linked to each milestone id.
func computeProjectMilestoneTaskCount(ctx context.Context, records orm.RecordSet, _ orm.FieldMetadata) (map[string]any, error) {
	return countTasksByMilestone(ctx, records, false)
}

// computeProjectMilestoneDoneTaskCount counts only closed tasks per milestone id.
func computeProjectMilestoneDoneTaskCount(ctx context.Context, records orm.RecordSet, _ orm.FieldMetadata) (map[string]any, error) {
	return countTasksByMilestone(ctx, records, true)
}

// countTasksByMilestone groups tbl_project_task rows by milestone_id.
// tbl_project_task.is_closed is a stored compute mirrored from its stage
// (see tbl_project_task_hooks.go), so no join is needed for the doneOnly filter.
func countTasksByMilestone(ctx context.Context, records orm.RecordSet, doneOnly bool) (map[string]any, error) {
	type row struct {
		MilestoneID int64 `bun:"milestone_id"`
		Count       int   `bun:"count"`
	}
	var rows []row
	db := orm.BunDBFromContext(ctx)
	idsAsAny := make([]any, 0, len(records.IDs()))
	for _, id := range records.IDs() {
		if value, ok := int64FromAny(id); ok {
			idsAsAny = append(idsAsAny, value)
		}
	}
	if len(idsAsAny) == 0 {
		return map[string]any{}, nil
	}
	query := db.NewSelect().
		Table("tbl_project_task").
		Column("milestone_id").
		ColumnExpr("COUNT(*) AS count").
		Where("milestone_id IN (?)", bun.In(idsAsAny))
	if doneOnly {
		query = query.Where("is_closed = true")
	}
	if err := query.Group("milestone_id").Scan(ctx, &rows); err != nil {
		return nil, err
	}
	out := make(map[string]any, len(rows))
	for _, r := range rows {
		out[fmt.Sprint(r.MilestoneID)] = r.Count
	}
	return out, nil
}
