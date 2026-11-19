package models

// File overview:
// - Responsibility: defines the `tbl.project_stage` model — a project kanban
//   stage (`project.project.stage` parity with the reference module). Ownership: project module.
//   Audit-sensitivity: low.
// - Key symbols: `init`, `ProjectStageRecord`, `ProjectStageModelSpec`.

import (
	"git.thebase.vn/base_hub/internal/orm"
)

// ProjectStageRecord stores one project pipeline stage.
type ProjectStageRecord struct {
	orm.BaseModel
	Name      string `bun:"name,notnull" json:"name" meta:"translate,required,index,label:Name,help:Stores the stage's display name."`
	Sequence  int    `bun:"sequence,notnull,default:10" json:"sequence" meta:"required,index,label:Sequence,help:Stores the stage's ordering position."`
	Fold      bool   `bun:"fold,notnull,default:false" json:"fold" meta:"required,label:Fold,help:Controls whether this stage's kanban column starts folded."`
	CompanyID *int64 `bun:"company_id" json:"company_id" meta:"kind:many2one,model:tbl.company,ondelete:SET NULL,index,label:Company ID,help:Links this record to the related tbl.company record for company id."`
}

const ProjectStageModelName = "tbl.project_stage"

var ProjectStageModelSpec = orm.ModelSpec{
	Name:         ProjectStageModelName,
	Label:        "Project Stages",
	Description:  "Project pipeline stages owned by the project module",
	Definition:   orm.ModelDefinition(ProjectStageModelName, (*ProjectStageRecord)(nil)),
	DefaultOrder: "sequence",
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "unlink_with_reassign", Public: true, UsesPathID: true, Call: unlinkProjectStageWithReassign},
	},
}

var ProjectStageModel = orm.NewHandlerWithModel[ProjectStageRecord](ProjectStageModelSpec)

func init() {
	RegisterRuntimeBootstrap(ProjectStageModelName, 803, ProjectStageModel.InitDefault, ProjectStageModel.Shutdown)
}
