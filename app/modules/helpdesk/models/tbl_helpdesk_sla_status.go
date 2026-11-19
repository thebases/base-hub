package models

// File overview:
// - Responsibility: defines the `tbl.helpdesk_sla_status` model — one row per
//   (ticket, matched SLA policy), tracking its deadline/reached/failed
//   state. Ownership: helpdesk module. Audit-sensitivity: low.
// - Key symbols: `init`, `HelpdeskSlaStatusRecord`, `HelpdeskSlaStatusModelSpec`.

import (
	"time"

	"git.thebase.vn/base_hub/internal/orm"
)

// HelpdeskSlaStatusRecord stores one ticket<->SLA-policy match outcome.
type HelpdeskSlaStatusRecord struct {
	orm.BaseModel
	TicketID  int64      `bun:"ticket_id,notnull" json:"ticket_id" meta:"kind:many2one,model:tbl.helpdesk_ticket,ondelete:CASCADE,required,index,label:Ticket,help:Links this SLA status row to the ticket it was matched against."`
	SlaID     int64      `bun:"sla_id,notnull" json:"sla_id" meta:"kind:many2one,model:tbl.helpdesk_sla,ondelete:CASCADE,required,index,label:SLA Policy,help:Links this SLA status row to the policy that matched."`
	Deadline  *time.Time `bun:"deadline" json:"deadline" meta:"index,label:Deadline,help:Stores the computed deadline for this SLA match (created_at + policy duration_hours)."`
	ReachedAt *time.Time `bun:"reached_at" json:"reached_at" meta:"index,label:Reached At,help:Stores when the ticket reached the policy's target stage, if it did."`
	Status    string     `bun:"status,notnull,default:'ongoing'" json:"status" meta:"required,index,label:Status,help:Stores this SLA match's current outcome." selection:"(ongoing,Ongoing),(reached,Reached),(failed,Failed)"`
}

const HelpdeskSlaStatusModelName = "tbl.helpdesk_sla_status"

var HelpdeskSlaStatusModelSpec = orm.ModelSpec{
	Name:        HelpdeskSlaStatusModelName,
	Label:       "Helpdesk SLA Status",
	Description: "Per-ticket SLA policy match outcomes, owned by the helpdesk module",
	Definition:  orm.ModelDefinition(HelpdeskSlaStatusModelName, (*HelpdeskSlaStatusRecord)(nil)),
}

var HelpdeskSlaStatusModel = orm.NewHandlerWithModel[HelpdeskSlaStatusRecord](HelpdeskSlaStatusModelSpec)

// init registers this package runtime during bootstrap so its models, hooks, or commands are available before requests arrive.
func init() {
	// Register runtime metadata during bootstrap so the model is available before requests arrive.
	RegisterRuntimeBootstrap(HelpdeskSlaStatusModelName, 909, HelpdeskSlaStatusModel.InitDefault, HelpdeskSlaStatusModel.Shutdown)
}
