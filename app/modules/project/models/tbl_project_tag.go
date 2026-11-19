package models

// File overview:
// - Responsibility: defines the `tbl.project_tag` model — a free-form label
//   attachable to projects and tasks (`project.tags` parity with the reference module). Ownership:
//   project module. Audit-sensitivity: low.
// - Key symbols: `init`, `ProjectTagRecord`, `ProjectTagModelSpec`.

import (
	"git.thebase.vn/base_hub/internal/orm"
)

// ProjectTagRecord stores one project/task tag.
type ProjectTagRecord struct {
	orm.BaseModel
	Name  string `bun:"name,notnull" json:"name" meta:"translate,required,index,label:Name,help:Stores the tag's display name."`
	Color int    `bun:"color,notnull,default:0" json:"color" meta:"required,label:Color,help:Stores the color index shown for this tag."`
}

const ProjectTagModelName = "tbl.project_tag"

var ProjectTagModelSpec = orm.ModelSpec{
	Name:        ProjectTagModelName,
	Label:       "Project Tags",
	Description: "Project/task tags owned by the project module",
	Definition:  orm.ModelDefinition(ProjectTagModelName, (*ProjectTagRecord)(nil)),
}

var ProjectTagModel = orm.NewHandlerWithModel[ProjectTagRecord](ProjectTagModelSpec)

func init() {
	RegisterRuntimeBootstrap(ProjectTagModelName, 806, ProjectTagModel.InitDefault, ProjectTagModel.Shutdown)
}
