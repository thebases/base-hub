package models

// File overview:
// - Responsibility: defines the `tbl.helpdesk_ticket_activity_link` model —
//   a helpdesk-owned join row between a ticket and a discuss.activity it
//   scheduled via perform_action (§9.7). Ownership: helpdesk module.
//   Audit-sensitivity: low.
// - Key symbols: `init`, `HelpdeskTicketActivityLinkRecord`, `HelpdeskTicketActivityLinkModelSpec`.
// - Pattern: kept helpdesk-side deliberately — discuss.activity must never
//   learn about helpdesk (would couple the shared discuss module to a single
//   consumer), so this join lives here instead of adding a ticket_id column
//   to tbl.discuss_activity.

import (
	"git.thebase.vn/base_hub/internal/orm"
)

// HelpdeskTicketActivityLinkRecord stores one (ticket, discuss activity) link
// created by tbl.helpdesk_ticket's perform_action RuntimeMethod.
type HelpdeskTicketActivityLinkRecord struct {
	orm.BaseModel
	TicketID   int64 `bun:"ticket_id,notnull" json:"ticket_id" meta:"kind:many2one,model:tbl.helpdesk_ticket,ondelete:CASCADE,required,index,label:Ticket,help:Links this row to the ticket that scheduled the activity."`
	ActivityID int64 `bun:"activity_id,notnull" json:"activity_id" meta:"kind:many2one,model:tbl.discuss_activity,ondelete:CASCADE,required,index,label:Activity,help:Links this row to the discuss activity that was scheduled."`
}

const HelpdeskTicketActivityLinkModelName = "tbl.helpdesk_ticket_activity_link"

var HelpdeskTicketActivityLinkModelSpec = orm.ModelSpec{
	Name:        HelpdeskTicketActivityLinkModelName,
	Label:       "Helpdesk Ticket Activity Links",
	Description: "Join rows between a ticket and its perform_action-scheduled discuss activity, owned by the helpdesk module",
	Definition:  orm.ModelDefinition(HelpdeskTicketActivityLinkModelName, (*HelpdeskTicketActivityLinkRecord)(nil)),
}

var HelpdeskTicketActivityLinkModel = orm.NewHandlerWithModel[HelpdeskTicketActivityLinkRecord](HelpdeskTicketActivityLinkModelSpec)

// init registers this package runtime during bootstrap so its models, hooks, or commands are available before requests arrive.
func init() {
	// Register runtime metadata during bootstrap so the model is available before requests arrive.
	RegisterRuntimeBootstrap(HelpdeskTicketActivityLinkModelName, 911, HelpdeskTicketActivityLinkModel.InitDefault, HelpdeskTicketActivityLinkModel.Shutdown)
}
