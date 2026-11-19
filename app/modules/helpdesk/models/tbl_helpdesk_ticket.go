package models

// File overview:
// - Responsibility: defines the `tbl.helpdesk_ticket` model — a minimal support
//   ticket record. Ownership: helpdesk module. Audit-sensitivity: low (no
//   balance/payment mutation); stage/priority/assignee changes are plain
//   business-state transitions, not financially audit-sensitive.
// - Key symbols: `init`, `HelpdeskTicketRecord`, `HelpdeskTicketModelSpec`.
// - Pattern: mirrors base-calendar's minimal-model shape — a Bun-tagged
//   struct embedding orm.BaseModel (which already supplies id/active/
//   created_at/updated_at) plus a registered ModelSpec, no custom Fiber
//   routes/handlers since generic model-registry CRUD/RPC covers this model.

import (
	"time"

	"git.thebase.vn/base_hub/internal/orm"
)

// HelpdeskTicketRecord stores one support ticket. Lifecycle is carried solely
// by stage_id (tbl.helpdesk_stage; is_closed marks closed stages — an
// unstaged ticket counts as open). `priority` is a plain string column with a
// documented allowed-value convention (not a DB enum type):
//   - priority: low | normal | high | urgent (default "normal")
type HelpdeskTicketRecord struct {
	orm.BaseModel
	Subject     string `bun:"subject,notnull" json:"subject" meta:"required,index,label:Subject,help:Stores the short human-readable summary of this ticket."`
	Description string `bun:"description" json:"description" meta:"kind:text,label:Description,help:Stores the full description/body of this ticket."`
	Priority    string `bun:"priority,notnull,default:'normal'" json:"priority" meta:"required,index,label:Priority,help:Stores the ticket urgency level." selection:"(low,Low),(normal,Normal),(high,High),(urgent,Urgent)"`
	AssigneeID  *int64 `bun:"assignee_id" json:"assignee_id" meta:"kind:many2one,model:base.users,ondelete:SET NULL,index,label:Assignee,help:Links this ticket to the base.users record responsible for handling it."`
	CompanyID   *int64 `bun:"company_id" json:"company_id" meta:"kind:many2one,model:tbl.company,ondelete:SET NULL,index,label:Company ID,help:Links this record to the related tbl.company record for company id."`

	// --- §3/§5 Phase 1 additions (team/stage/tags/type/partner/kanban/close/assign/sla) ---
	TeamID       *int64     `bun:"team_id" json:"team_id" meta:"kind:many2one,model:tbl.helpdesk_team,ondelete:SET NULL,index,label:Team,help:Links this ticket to the tbl.helpdesk_team responsible for it."`
	StageID      *int64     `bun:"stage_id" json:"stage_id" meta:"kind:many2one,model:tbl.helpdesk_stage,ondelete:SET NULL,index,label:Stage,help:Links this ticket to its current pipeline stage."`
	TagIDs       []int64    `bun:"tag_ids,type:jsonb,nullzero" json:"tag_ids" meta:"kind:many2many,model:tbl.helpdesk_tag,relation_table:tbl_helpdesk_ticket_tag_rel,relation_column:ticket_id,relation_key:tag_id,label:Tags,help:Lists the tbl.helpdesk_tag records attached to this ticket."`
	TicketTypeID *int64     `bun:"ticket_type_id" json:"ticket_type_id" meta:"kind:many2one,model:tbl.helpdesk_ticket_type,ondelete:SET NULL,index,label:Ticket Type,help:Links this ticket to its classification."`
	PartnerID    *int64     `bun:"partner_id" json:"partner_id" meta:"kind:many2one,model:tbl.partner,ondelete:SET NULL,index,label:Customer,help:Links this ticket to the tbl.partner it was raised for."`
	Color        int        `bun:"color,notnull,default:0" json:"color" meta:"required,label:Color,help:Stores the kanban card color index."`
	CloseDate    *time.Time `bun:"close_date" json:"close_date" meta:"index,label:Close Date,help:Stores when this ticket last transitioned into a closed stage. Cleared when it moves back out."`
	AssignDate   *time.Time `bun:"assign_date" json:"assign_date" meta:"index,label:Assign Date,help:Stores when this ticket was first assigned to a user."`
	// SlaDeadline is a stored compute — see service.go's _compute_sla_deadline
	// equivalent (recomputeSlaForTicket): min(deadline) across ongoing
	// tbl.helpdesk_sla_status rows for this ticket.
	SlaDeadline *time.Time `bun:"sla_deadline" json:"sla_deadline" meta:"index,label:SLA Deadline,help:Stores the earliest ongoing SLA deadline for this ticket, recomputed on SLA match."`

	// --- §9.1 core helpdesk_mgmt fields missed by the original plan §3 ---
	// Number is filled by the kernel's use_sequence rule from the sys.sequence row whose code is
	// "tbl.helpdesk_ticket.number" (data/05_sys_sequence_ticket_number.json), e.g. "HD/0001".
	Number          string     `bun:"number,notnull,default:'/'" json:"number" meta:"required,index,readonly,use_sequence,label:Number,help:Stores the ticket's sequence number, assigned on create."`
	LastStageUpdate *time.Time `bun:"last_stage_update" json:"last_stage_update" meta:"index,label:Last Stage Update,help:Stores when this ticket's stage last changed. Bumped by the stage-transition write hook."`
	// Unattended is a stored compute mirroring the current stage's own
	// Unattended flag — recomputed by the stage-transition write hook rather
	// than a generic compute-field pass, since it only ever changes when
	// stage_id changes.
	Unattended     bool   `bun:"unattended,notnull,default:false" json:"unattended" meta:"required,label:Unattended,help:Mirrors the current stage's unattended flag; recomputed whenever stage_id changes."`
	CategoryID     *int64 `bun:"category_id" json:"category_id" meta:"kind:many2one,model:tbl.helpdesk_ticket_category,ondelete:SET NULL,index,label:Category,help:Links this ticket to its category, distinct from tags and ticket type."`
	ChannelID      *int64 `bun:"channel_id" json:"channel_id" meta:"kind:many2one,model:tbl.helpdesk_ticket_channel,ondelete:SET NULL,index,label:Channel,help:Links this ticket to how it came in (phone/email/portal/etc.)."`
	DuplicateID    *int64 `bun:"duplicate_id" json:"duplicate_id" meta:"kind:many2one,model:tbl.helpdesk_ticket,ondelete:SET NULL,index,label:Duplicate Of,help:Links this ticket to the ticket it duplicates, when duplicate tracking is enabled on the team."`
	DuplicateCount int    `bun:"duplicate_count" json:"duplicate_count" meta:"compute:_compute_duplicate_count,stored:false,readonly,label:Duplicate Count,help:Counts how many other tickets point duplicate_id at this one."`
	// RelatedTicketIDs is a symmetric self many2many — see the write hook in
	// service.go (syncRelatedTicketSymmetry) that mirrors additions/removals
	// onto every affected ticket B so the link always reads both ways.
	RelatedTicketIDs []int64 `bun:"related_ticket_ids,type:jsonb,nullzero" json:"related_ticket_ids" meta:"kind:many2many,model:tbl.helpdesk_ticket,relation_table:tbl_helpdesk_ticket_relationship_rel,relation_column:ticket_id1,relation_key:ticket_id2,label:Related Tickets,help:Lists other tickets related to this one. Always kept bidirectional by a write hook."`

	// --- §9.7 helpdesk_mgmt_activity: polymorphic source document + linked-activity fields ---
	// ResModel/ResID mirror sys.attachment's (sys_model, model_id) polymorphic
	// pair — the source document perform_action schedules an activity
	// against, which is deliberately NOT always the ticket itself.
	ResModel             *string    `bun:"res_model" json:"res_model" meta:"index,label:Source Model,help:Stores the model name of the source document perform_action schedules activities against."`
	ResID                *int64     `bun:"res_id" json:"res_id" meta:"index,label:Source Record ID,help:Stores the source document's record id, paired with res_model."`
	SourceActivityTypeID *int64     `bun:"source_activity_type_id" json:"source_activity_type_id" meta:"kind:many2one,model:tbl.discuss_activity_type,ondelete:SET NULL,index,label:Source Activity Type,help:Links to the discuss activity type perform_action schedules."`
	DateDeadline         *time.Time `bun:"date_deadline" json:"date_deadline" meta:"index,label:Activity Deadline,help:Stores the deadline perform_action passes to the scheduled activity."`
	AssignedUserID       *int64     `bun:"assigned_user_id" json:"assigned_user_id" meta:"kind:many2one,model:base.users,ondelete:SET NULL,index,label:Activity Assignee,help:Links to the user perform_action assigns the scheduled activity to."`
}

const HelpdeskTicketModelName = "tbl.helpdesk_ticket"

var HelpdeskTicketModelSpec = orm.ModelSpec{
	Name:        HelpdeskTicketModelName,
	Label:       "Helpdesk Tickets",
	Description: "Support ticket owned by the helpdesk module",
	Definition:  orm.ModelDefinition(HelpdeskTicketModelName, (*HelpdeskTicketRecord)(nil)),
	StandardHooks: orm.StandardRuntimeHooks{
		Create: createHelpdeskTicket,
		Write:  writeHelpdeskTicket,
	},
	RuntimeMethods: []orm.RuntimeMethod{
		// perform_action/complete_source_activity implement §9.7's
		// activity-linked stage advancement — see activity_actions.go.
		{Name: "perform_action", Public: true, UsesPathID: true, Call: runtimePerformAction},
		{Name: "complete_source_activity", Public: true, UsesPathID: true, Call: runtimeCompleteSourceActivity},
	},
}

var HelpdeskTicketModel = orm.NewHandlerWithModel[HelpdeskTicketRecord](HelpdeskTicketModelSpec)

// init registers this package runtime during bootstrap so its models, hooks, or commands are available before requests arrive.
func init() {
	// Register runtime metadata during bootstrap so the model is available before requests arrive.
	RegisterRuntimeBootstrap(HelpdeskTicketModelName, 900, HelpdeskTicketModel.InitDefault, HelpdeskTicketModel.Shutdown)
	// DuplicateCount (§9.1) is a stored:false compute — one handler per
	// model+field, registered once at bootstrap.
	orm.RegisterCompute(HelpdeskTicketModelName, "duplicate_count", computeDuplicateCount)
}
