package models

// File overview:
// - Responsibility: defines the `tbl.helpdesk_ticket_channel` model — a
//   free-form "how the ticket came in" source (phone/email/portal/etc, §9.1).
//   Ownership: helpdesk module. Audit-sensitivity: low.
// - Key symbols: `init`, `HelpdeskTicketChannelRecord`, `HelpdeskTicketChannelModelSpec`.

import (
	"git.thebase.vn/base_hub/internal/orm"
)

// HelpdeskTicketChannelRecord stores one ticket intake channel.
type HelpdeskTicketChannelRecord struct {
	orm.BaseModel
	Name string `bun:"name,notnull" json:"name" meta:"translate,required,index,label:Name,help:Stores the channel's display name."`
}

const HelpdeskTicketChannelModelName = "tbl.helpdesk_ticket_channel"

var HelpdeskTicketChannelModelSpec = orm.ModelSpec{
	Name:        HelpdeskTicketChannelModelName,
	Label:       "Helpdesk Ticket Channels",
	Description: "Ticket intake channels owned by the helpdesk module",
	Definition:  orm.ModelDefinition(HelpdeskTicketChannelModelName, (*HelpdeskTicketChannelRecord)(nil)),
}

var HelpdeskTicketChannelModel = orm.NewHandlerWithModel[HelpdeskTicketChannelRecord](HelpdeskTicketChannelModelSpec)

// init registers this package runtime during bootstrap so its models, hooks, or commands are available before requests arrive.
func init() {
	// Register runtime metadata during bootstrap so the model is available before requests arrive.
	RegisterRuntimeBootstrap(HelpdeskTicketChannelModelName, 907, HelpdeskTicketChannelModel.InitDefault, HelpdeskTicketChannelModel.Shutdown)
}
