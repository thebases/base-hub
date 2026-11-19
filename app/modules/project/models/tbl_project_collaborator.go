package models

// File overview:
// - Responsibility: defines the `tbl.project_collaborator` model — an
//   internal sharing record naming who a project was shared with (
//   `project.collaborator` parity). Ownership: project module.
//   Audit-sensitivity: low.
// - Key symbols: `init`, `ProjectCollaboratorRecord`.
// - Simplification vs. reference: this repo has no portal concept, so the
//   portal-token/URL/record-rule mechanics behind the reference module's "project sharing" are
//   dropped — this model only stores the sharing record itself (who has
//   access, at what access level) for a future ACL layer to consume.

import (
	"git.thebase.vn/base_hub/internal/orm"
)

// ProjectCollaboratorRecord stores one project-sharing grant.
type ProjectCollaboratorRecord struct {
	orm.BaseModel
	ProjectID     int64  `bun:"project_id,notnull" json:"project_id" meta:"required,kind:many2one,model:tbl.project,ondelete:CASCADE,index,label:Project,help:Links this grant to the shared tbl.project."`
	PartnerID     int64  `bun:"partner_id,notnull" json:"partner_id" meta:"required,kind:many2one,model:tbl.partner,ondelete:CASCADE,index,label:Partner,help:Links this grant to the tbl.partner it was shared with."`
	PartnerEmail  string `bun:"partner_email,nullzero" json:"partner_email" meta:"label:Email,help:Stores the shared partner's email, mirrored for display without an extra lookup."`
	LimitedAccess bool   `bun:"limited_access,notnull,default:false" json:"limited_access" meta:"required,label:Limited Access,help:Restricts this collaborator to tasks they are directly assigned to or following."`
}

const ProjectCollaboratorModelName = "tbl.project_collaborator"

var ProjectCollaboratorModelSpec = orm.ModelSpec{
	Name:        ProjectCollaboratorModelName,
	Label:       "Project Collaborators",
	Description: "Project sharing grants owned by the project module",
	Definition:  orm.ModelDefinition(ProjectCollaboratorModelName, (*ProjectCollaboratorRecord)(nil)),
}

var ProjectCollaboratorModel = orm.NewHandlerWithModel[ProjectCollaboratorRecord](ProjectCollaboratorModelSpec)

func init() {
	RegisterRuntimeBootstrap(ProjectCollaboratorModelName, 810, ProjectCollaboratorModel.InitDefault, ProjectCollaboratorModel.Shutdown)
}
