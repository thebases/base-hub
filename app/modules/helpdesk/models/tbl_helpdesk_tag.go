package models

// File overview:
// - Responsibility: defines the `tbl.helpdesk_tag` model — a free-form label
//   attachable to tickets. Ownership: helpdesk module. Audit-sensitivity: low.
// - Key symbols: `init`, `HelpdeskTagRecord`, `HelpdeskTagModelSpec`.

import (
	"git.thebase.vn/base_hub/internal/orm"
)

// HelpdeskTagRecord stores one ticket tag.
type HelpdeskTagRecord struct {
	orm.BaseModel
	Name  string `bun:"name,notnull" json:"name" meta:"translate,required,index,label:Name,help:Stores the tag's display name."`
	Color int    `bun:"color,notnull,default:0" json:"color" meta:"required,label:Color,help:Stores the kanban color index shown for this tag."`
}

const HelpdeskTagModelName = "tbl.helpdesk_tag"

var HelpdeskTagModelSpec = orm.ModelSpec{
	Name:        HelpdeskTagModelName,
	Label:       "Helpdesk Tags",
	Description: "Ticket tags owned by the helpdesk module",
	Definition:  orm.ModelDefinition(HelpdeskTagModelName, (*HelpdeskTagRecord)(nil)),
}

var HelpdeskTagModel = orm.NewHandlerWithModel[HelpdeskTagRecord](HelpdeskTagModelSpec)

// init registers this package runtime during bootstrap so its models, hooks, or commands are available before requests arrive.
func init() {
	// Register runtime metadata during bootstrap so the model is available before requests arrive.
	RegisterRuntimeBootstrap(HelpdeskTagModelName, 904, HelpdeskTagModel.InitDefault, HelpdeskTagModel.Shutdown)
}
