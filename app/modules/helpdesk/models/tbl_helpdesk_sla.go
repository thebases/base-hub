package models

// File overview:
// - Responsibility: defines the `tbl.helpdesk_sla` model — an SLA policy
//   matched against tickets by team/priority/tags/type/partner. Ownership:
//   helpdesk module. Audit-sensitivity: low.
// - Key symbols: `init`, `HelpdeskSlaRecord`, `HelpdeskSlaModelSpec`.

import (
	"git.thebase.vn/base_hub/internal/orm"
)

// HelpdeskSlaRecord stores one SLA policy. A nil Priority means "any
// priority matches". DurationHours is a fixed-duration simplification of the
// deadline computation — no working-calendar/business-hours service exists
// in this repo yet, so the deadline is always created_at + DurationHours
// rather than a calendar-aware calculation.
type HelpdeskSlaRecord struct {
	orm.BaseModel
	Name            string  `bun:"name,notnull" json:"name" meta:"translate,required,index,label:Name,help:Stores the SLA policy's display name."`
	TeamID          int64   `bun:"team_id,notnull" json:"team_id" meta:"kind:many2one,model:tbl.helpdesk_team,ondelete:CASCADE,required,index,label:Team,help:Links this SLA policy to the team it applies to."`
	Priority        *string `bun:"priority" json:"priority" meta:"label:Priority,help:Restricts this policy to one ticket priority. Empty matches any priority." selection:"(low,Low),(normal,Normal),(high,High),(urgent,Urgent)"`
	StageID         *int64  `bun:"stage_id" json:"stage_id" meta:"kind:many2one,model:tbl.helpdesk_stage,ondelete:SET NULL,index,label:Target Stage,help:Links to the stage a matching ticket must reach before this deadline."`
	DurationHours   float64 `bun:"duration_hours,notnull,default:24" json:"duration_hours" meta:"required,label:Duration Hours,help:Stores the fixed number of hours from ticket creation until this SLA's deadline."`
	TagIDs          []int64 `bun:"tag_ids,type:jsonb,nullzero" json:"tag_ids" meta:"kind:many2many,model:tbl.helpdesk_tag,relation_table:tbl_helpdesk_sla_tag_rel,relation_column:sla_id,relation_key:tag_id,label:Tags,help:Restricts this policy to tickets carrying at least one of these tags. Empty matches any tags."`
	TicketTypeIDs   []int64 `bun:"ticket_type_ids,type:jsonb,nullzero" json:"ticket_type_ids" meta:"kind:many2many,model:tbl.helpdesk_ticket_type,relation_table:tbl_helpdesk_sla_ticket_type_rel,relation_column:sla_id,relation_key:type_id,label:Ticket Types,help:Restricts this policy to these ticket types. Empty matches any type."`
	PartnerIDs      []int64 `bun:"partner_ids,type:jsonb,nullzero" json:"partner_ids" meta:"kind:many2many,model:tbl.partner,relation_table:tbl_helpdesk_sla_partner_rel,relation_column:sla_id,relation_key:partner_id,label:Partners,help:Restricts this policy to these customers. Empty matches any partner."`
	ExcludeStageIDs []int64 `bun:"exclude_stage_ids,type:jsonb,nullzero" json:"exclude_stage_ids" meta:"kind:many2many,model:tbl.helpdesk_stage,relation_table:tbl_helpdesk_sla_exclude_stage_rel,relation_column:sla_id,relation_key:stage_id,label:Exclude Stages,help:Lists stages that stop this SLA from matching once the ticket reaches them."`
}

const HelpdeskSlaModelName = "tbl.helpdesk_sla"

var HelpdeskSlaModelSpec = orm.ModelSpec{
	Name:        HelpdeskSlaModelName,
	Label:       "Helpdesk SLA Policies",
	Description: "SLA policies owned by the helpdesk module",
	Definition:  orm.ModelDefinition(HelpdeskSlaModelName, (*HelpdeskSlaRecord)(nil)),
}

var HelpdeskSlaModel = orm.NewHandlerWithModel[HelpdeskSlaRecord](HelpdeskSlaModelSpec)

// init registers this package runtime during bootstrap so its models, hooks, or commands are available before requests arrive.
func init() {
	// Register runtime metadata during bootstrap so the model is available before requests arrive.
	RegisterRuntimeBootstrap(HelpdeskSlaModelName, 908, HelpdeskSlaModel.InitDefault, HelpdeskSlaModel.Shutdown)
}
