package models

// File overview:
// - Responsibility: defines the `tbl.helpdesk_ticket_category` model — a
//   ticket category, distinct from tags and ticket type, used by the
//   close-inactive sweep's category filter (§9.1/§9.6). Ownership: helpdesk
//   module. Audit-sensitivity: low.
// - Key symbols: `init`, `HelpdeskTicketCategoryRecord`, `HelpdeskTicketCategoryModelSpec`.

import (
	"git.thebase.vn/base_hub/internal/orm"
)

// HelpdeskTicketCategoryRecord stores one ticket category.
type HelpdeskTicketCategoryRecord struct {
	orm.BaseModel
	Name string `bun:"name,notnull" json:"name" meta:"translate,required,index,label:Name,help:Stores the category's display name."`
}

const HelpdeskTicketCategoryModelName = "tbl.helpdesk_ticket_category"

var HelpdeskTicketCategoryModelSpec = orm.ModelSpec{
	Name:        HelpdeskTicketCategoryModelName,
	Label:       "Helpdesk Ticket Categories",
	Description: "Ticket categories owned by the helpdesk module",
	Definition:  orm.ModelDefinition(HelpdeskTicketCategoryModelName, (*HelpdeskTicketCategoryRecord)(nil)),
}

var HelpdeskTicketCategoryModel = orm.NewHandlerWithModel[HelpdeskTicketCategoryRecord](HelpdeskTicketCategoryModelSpec)

// init registers this package runtime during bootstrap so its models, hooks, or commands are available before requests arrive.
func init() {
	// Register runtime metadata during bootstrap so the model is available before requests arrive.
	RegisterRuntimeBootstrap(HelpdeskTicketCategoryModelName, 906, HelpdeskTicketCategoryModel.InitDefault, HelpdeskTicketCategoryModel.Shutdown)
}
