package models

// File overview:
// - Responsibility: defines the `tbl.project_role` model — a placeholder
//   role (e.g. "Developer"/"Designer") assignable to a task, filled in when
//   instantiating from a template (`project.role` parity with the reference module). Ownership:
//   project module. Audit-sensitivity: low.
// - Key symbols: `init`, `ProjectRoleRecord`, `ProjectRoleModelSpec`.

import (
	"git.thebase.vn/base_hub/internal/orm"
)

// ProjectRoleRecord stores one placeholder role.
type ProjectRoleRecord struct {
	orm.BaseModel
	Name     string `bun:"name,notnull" json:"name" meta:"translate,required,index,label:Name,help:Stores the role's display name."`
	Color    int    `bun:"color,notnull,default:0" json:"color" meta:"required,label:Color,help:Stores the color index shown for this role."`
	Sequence int    `bun:"sequence,notnull,default:10" json:"sequence" meta:"required,index,label:Sequence,help:Stores the role's ordering position."`
}

const ProjectRoleModelName = "tbl.project_role"

var ProjectRoleModelSpec = orm.ModelSpec{
	Name:         ProjectRoleModelName,
	Label:        "Project Roles",
	Description:  "Placeholder task roles owned by the project module",
	Definition:   orm.ModelDefinition(ProjectRoleModelName, (*ProjectRoleRecord)(nil)),
	DefaultOrder: "sequence",
}

var ProjectRoleModel = orm.NewHandlerWithModel[ProjectRoleRecord](ProjectRoleModelSpec)

func init() {
	RegisterRuntimeBootstrap(ProjectRoleModelName, 807, ProjectRoleModel.InitDefault, ProjectRoleModel.Shutdown)
}
