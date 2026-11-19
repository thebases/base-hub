package models

// File overview:
// - Responsibility: defines the `tbl.helpdesk_ticket_type` model — a ticket
//   classification (Question/Issue/Bug), optionally restricted to specific
//   teams (§9.2 helpdesk_type parity). Ownership: helpdesk module.
//   Audit-sensitivity: low.
// - Key symbols: `init`, `HelpdeskTicketTypeRecord`, `HelpdeskTicketTypeModelSpec`.

import (
	"git.thebase.vn/base_hub/internal/orm"
)

// HelpdeskTicketTypeRecord stores one ticket type.
type HelpdeskTicketTypeRecord struct {
	orm.BaseModel
	Name         string `bun:"name,notnull" json:"name" meta:"translate,required,index,label:Name,help:Stores the ticket type's display name."`
	ShowInPortal bool   `bun:"show_in_portal,notnull,default:true" json:"show_in_portal" meta:"required,label:Show In Portal,help:Controls whether this type is offered on a customer-facing ticket submission form."`
	// TeamIDs is the inverse side of tbl.helpdesk_team.TypeIDs, sharing the
	// same relation table with column/key swapped.
	TeamIDs []int64 `bun:"team_ids,type:jsonb,nullzero" json:"team_ids" meta:"kind:many2many,model:tbl.helpdesk_team,relation_table:tbl_helpdesk_ticket_type_team_rel,relation_column:type_id,relation_key:team_id,label:Teams,help:Lists the tbl.helpdesk_team records whose tickets may use this type. Empty means every team."`
}

const HelpdeskTicketTypeModelName = "tbl.helpdesk_ticket_type"

var HelpdeskTicketTypeModelSpec = orm.ModelSpec{
	Name:        HelpdeskTicketTypeModelName,
	Label:       "Helpdesk Ticket Types",
	Description: "Ticket classifications owned by the helpdesk module",
	Definition:  orm.ModelDefinition(HelpdeskTicketTypeModelName, (*HelpdeskTicketTypeRecord)(nil)),
}

var HelpdeskTicketTypeModel = orm.NewHandlerWithModel[HelpdeskTicketTypeRecord](HelpdeskTicketTypeModelSpec)

// init registers this package runtime during bootstrap so its models, hooks, or commands are available before requests arrive.
func init() {
	// Register runtime metadata during bootstrap so the model is available before requests arrive.
	RegisterRuntimeBootstrap(HelpdeskTicketTypeModelName, 905, HelpdeskTicketTypeModel.InitDefault, HelpdeskTicketTypeModel.Shutdown)
}
