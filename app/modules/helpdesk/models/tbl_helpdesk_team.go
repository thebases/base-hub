package models

// File overview:
// - Responsibility: defines the `tbl.helpdesk_team` model — a support team
//   that owns tickets, drives assignment method, SLA usage, auto-close, and
//   (per plan §9) partner-response auto-stage-transition, close-inactive
//   sweeps, and activity-linked stage advancement. Ownership: helpdesk
//   module. Audit-sensitivity: low.
// - Key symbols: `init`, `HelpdeskTeamRecord`, `HelpdeskTeamModelSpec`.

import (
	"git.thebase.vn/base_hub/internal/orm"
)

// HelpdeskTeamRecord stores one support team configuration.
type HelpdeskTeamRecord struct {
	orm.BaseModel
	Name        string  `bun:"name,notnull" json:"name" meta:"translate,required,index,label:Name,help:Stores the team's display name."`
	Description *string `bun:"description" json:"description" meta:"kind:text,label:Description,help:Stores a free-form description of this team's scope."`
	// Visibility gates row-level access via sys.rule: private = only members,
	// internal = every internal user, portal = members plus the ticket's own
	// linked partner.
	Visibility       string `bun:"visibility,notnull,default:'internal'" json:"visibility" meta:"required,label:Visibility,help:Controls which users can see this team's tickets." selection:"(private,Private),(internal,Internal),(portal,Portal)"`
	AssignMethod     string `bun:"assign_method,notnull,default:'manual'" json:"assign_method" meta:"required,label:Assign Method,help:Selects how new tickets are auto-assigned to a team member." selection:"(manual,Manual),(random,Random),(balanced,Balanced)"`
	UseSla           bool   `bun:"use_sla,notnull,default:false" json:"use_sla" meta:"required,label:Use SLA,help:Controls whether SLA policies are matched against this team's tickets."`
	UseRating        bool   `bun:"use_rating,notnull,default:false" json:"use_rating" meta:"required,label:Use Rating,help:Controls whether customers can submit a satisfaction rating for this team's closed tickets."`
	AutoCloseTicket  bool   `bun:"auto_close_ticket,notnull,default:false" json:"auto_close_ticket" meta:"required,label:Auto Close Ticket,help:Controls whether the auto-close cron moves stale tickets to a closing stage."`
	AutoCloseDays    int    `bun:"auto_close_days,notnull,default:14" json:"auto_close_days" meta:"required,label:Auto Close Days,help:Stores how many days of inactivity trigger auto-close."`
	AutoCloseStageID *int64 `bun:"auto_close_stage_id" json:"auto_close_stage_id" meta:"kind:many2one,model:tbl.helpdesk_stage,ondelete:SET NULL,index,label:Auto Close Stage,help:Links to the stage a ticket is moved to when auto-closed. Falls back to the team's last stage by sequence when unset."`
	TeamLeadID       *int64 `bun:"team_lead_id" json:"team_lead_id" meta:"kind:many2one,model:base.users,ondelete:SET NULL,index,label:Team Lead,help:Links this team to its responsible lead user."`
	// MemberIDs is the team roster used by assignment ("random"/"balanced")
	// and by the "private" visibility rule.
	MemberIDs []int64 `bun:"member_ids,type:jsonb,nullzero" json:"member_ids" meta:"kind:many2many,model:base.users,relation_table:tbl_helpdesk_team_member_rel,relation_column:team_id,relation_key:user_id,label:Members,help:Lists the base.users records that belong to this team."`

	// §9.2 helpdesk_type: restrict which ticket types this team's tickets may use.
	TypeIDs []int64 `bun:"type_ids,type:jsonb,nullzero" json:"type_ids" meta:"kind:many2many,model:tbl.helpdesk_ticket_type,relation_table:tbl_helpdesk_ticket_type_team_rel,relation_column:team_id,relation_key:type_id,label:Ticket Types,help:Lists the tbl.helpdesk_ticket_type records this team's tickets may use."`
	// §9.1 duplicate tracking: UI-only gate, no hook logic beyond the FK itself.
	DuplicateTrackingEnabled bool `bun:"duplicate_tracking_enabled,notnull,default:false" json:"duplicate_tracking_enabled" meta:"required,label:Duplicate Tracking Enabled,help:Controls whether the ticket form offers duplicate-ticket tracking."`

	// §9.4 helpdesk_ticket_partner_response: auto-advance stage when the
	// ticket's own partner replies via chatter.
	AutoupdateTicketStage bool    `bun:"autoupdate_ticket_stage,notnull,default:false" json:"autoupdate_ticket_stage" meta:"required,label:Autoupdate Ticket Stage,help:Controls whether a partner chatter reply automatically advances the ticket's stage."`
	AutoupdateSrcStageIDs []int64 `bun:"autoupdate_src_stage_ids,type:jsonb,nullzero" json:"autoupdate_src_stage_ids" meta:"kind:many2many,model:tbl.helpdesk_stage,relation_table:tbl_helpdesk_team_autoupdate_src_stage_rel,relation_column:team_id,relation_key:stage_id,label:Autoupdate Source Stages,help:Lists the stages a partner reply will transition out of."`
	AutoupdateDestStageID *int64  `bun:"autoupdate_dest_stage_id" json:"autoupdate_dest_stage_id" meta:"kind:many2one,model:tbl.helpdesk_stage,ondelete:SET NULL,index,label:Autoupdate Destination Stage,help:Links to the stage a ticket moves to after a matching partner reply."`

	// §9.6 helpdesk_ticket_close_inactive: warning/closing sweep configuration.
	CloseInactiveTickets     bool    `bun:"close_inactive_tickets,notnull,default:false" json:"close_inactive_tickets" meta:"required,label:Close Inactive Tickets,help:Controls whether the close-inactive cron sweeps this team's tickets."`
	TicketStageIDs           []int64 `bun:"ticket_stage_ids,type:jsonb,nullzero" json:"ticket_stage_ids" meta:"kind:many2many,model:tbl.helpdesk_stage,relation_table:tbl_helpdesk_team_ticket_stage_rel,relation_column:team_id,relation_key:stage_id,label:Inactive Sweep Stages,help:Lists the stages the close-inactive sweep considers eligible."`
	TicketCategoryIDs        []int64 `bun:"ticket_category_ids,type:jsonb,nullzero" json:"ticket_category_ids" meta:"kind:many2many,model:tbl.helpdesk_ticket_category,relation_table:tbl_helpdesk_team_ticket_category_rel,relation_column:team_id,relation_key:category_id,label:Inactive Sweep Categories,help:Lists the categories the close-inactive sweep considers eligible. Empty means all categories."`
	InactiveDaysLimitWarning int     `bun:"inactive_days_limit_warning,notnull,default:7" json:"inactive_days_limit_warning" meta:"required,label:Inactive Warning Days,help:Stores how many inactive days trigger a warning chatter note. Zero disables the warning phase."`
	InactiveDaysLimitClosing int     `bun:"inactive_days_limit_closing,notnull,default:14" json:"inactive_days_limit_closing" meta:"required,label:Inactive Closing Days,help:Stores how many inactive days trigger auto-closing."`
	ClosingStageID           *int64  `bun:"closing_stage_id" json:"closing_stage_id" meta:"kind:many2one,model:tbl.helpdesk_stage,ondelete:SET NULL,index,label:Closing Stage,help:Links to the stage a ticket moves to when the close-inactive sweep closes it."`

	// §9.7 helpdesk_mgmt_activity: activity-linked stage advancement.
	AllowSetActivity bool   `bun:"allow_set_activity,notnull,default:false" json:"allow_set_activity" meta:"required,label:Allow Set Activity,help:Controls whether this team's tickets can schedule a linked activity via perform_action."`
	ActivityStageID  *int64 `bun:"activity_stage_id" json:"activity_stage_id" meta:"kind:many2one,model:tbl.helpdesk_stage,ondelete:SET NULL,index,label:Activity Stage,help:Links to the stage a ticket moves to when its linked activity is completed."`

	// Team-level KPI compute fields (stored:false) — the numbers a support
	// manager checks per team, backed by team_kpi_service.go's raw-Bun
	// aggregates, same RegisterCompute pattern as tbl.helpdesk_ticket's
	// DuplicateCount.
	OpenTicketCount       int `bun:"open_ticket_count" json:"open_ticket_count" meta:"compute:_compute_open_ticket_count,stored:false,readonly,label:Open Tickets,help:Counts this team's tickets whose current stage is not closed."`
	UnassignedTicketCount int `bun:"unassigned_ticket_count" json:"unassigned_ticket_count" meta:"compute:_compute_unassigned_ticket_count,stored:false,readonly,label:Unassigned Tickets,help:Counts this team's open tickets with no assignee."`
	UrgentTicketCount     int `bun:"urgent_ticket_count" json:"urgent_ticket_count" meta:"compute:_compute_urgent_ticket_count,stored:false,readonly,label:Urgent Tickets,help:Counts this team's open tickets at urgent priority."`
	SlaFailedCount        int `bun:"sla_failed_count" json:"sla_failed_count" meta:"compute:_compute_sla_failed_count,stored:false,readonly,label:SLA Failed,help:Counts this team's tickets carrying at least one failed SLA status."`
	TicketClosed7d        int `bun:"ticket_closed_7d" json:"ticket_closed_7d" meta:"compute:_compute_ticket_closed_7d,stored:false,readonly,label:Closed (7 Days),help:Counts this team's tickets closed within the last 7 days."`
	// SuccessRate7d/AvgRating7d use a -1 sentinel meaning "no data for this
	// window" (team doesn't use SLA/rating, or nothing closed in the window)
	// — distinct from an honest 0% / 0-star result.
	SuccessRate7d int     `bun:"success_rate_7d" json:"success_rate_7d" meta:"compute:_compute_success_rate_7d,stored:false,readonly,label:SLA Success Rate (7 Days),help:Stores the percentage of tickets closed in the last 7 days that met their SLA. -1 means no SLA data for this window."`
	AvgRating7d   float64 `bun:"avg_rating_7d" json:"avg_rating_7d" meta:"compute:_compute_avg_rating_7d,stored:false,readonly,label:Average Rating (7 Days),help:Stores the average customer rating (1-5) across tickets closed in the last 7 days. -1 means no rating data for this window."`

	// Per-team targets (goals a manager sets for the team, compared against
	// the KPI fields above). Deviation from a per-user target: this repo's
	// base.users model is a shared core model outside this module's
	// ownership, so targets live on the team instead of on each user.
	TargetClosedWeekly int     `bun:"target_closed_weekly,notnull,default:0" json:"target_closed_weekly" meta:"required,label:Weekly Closed Target,help:Stores this team's goal for tickets closed per 7-day window. Zero disables the target."`
	TargetSuccessRate  int     `bun:"target_success_rate,notnull,default:0" json:"target_success_rate" meta:"required,label:SLA Success Rate Target,help:Stores this team's SLA success rate goal (0-100). Zero disables the target."`
	TargetRating       float64 `bun:"target_rating,notnull,default:0" json:"target_rating" meta:"required,label:Rating Target,help:Stores this team's average rating goal (0-5). Zero disables the target."`
}

const HelpdeskTeamModelName = "tbl.helpdesk_team"

var HelpdeskTeamModelSpec = orm.ModelSpec{
	Name:        HelpdeskTeamModelName,
	Label:       "Helpdesk Teams",
	Description: "Support team configuration owned by the helpdesk module",
	Definition:  orm.ModelDefinition(HelpdeskTeamModelName, (*HelpdeskTeamRecord)(nil)),
}

var HelpdeskTeamModel = orm.NewHandlerWithModel[HelpdeskTeamRecord](HelpdeskTeamModelSpec)

// init registers this package runtime during bootstrap so its models, hooks, or commands are available before requests arrive.
func init() {
	// Register runtime metadata during bootstrap so the model is available before requests arrive.
	RegisterRuntimeBootstrap(HelpdeskTeamModelName, 902, HelpdeskTeamModel.InitDefault, HelpdeskTeamModel.Shutdown)
	// Team-level KPI compute fields (team_kpi_service.go) — one handler per
	// field, registered once at bootstrap; ValidateComputeHandlers() fails
	// startup if any compute-tagged field above is missing its handler.
	orm.RegisterCompute(HelpdeskTeamModelName, "open_ticket_count", computeOpenTicketCount)
	orm.RegisterCompute(HelpdeskTeamModelName, "unassigned_ticket_count", computeUnassignedTicketCount)
	orm.RegisterCompute(HelpdeskTeamModelName, "urgent_ticket_count", computeUrgentTicketCount)
	orm.RegisterCompute(HelpdeskTeamModelName, "sla_failed_count", computeSlaFailedCount)
	orm.RegisterCompute(HelpdeskTeamModelName, "ticket_closed_7d", computeTicketClosed7d)
	orm.RegisterCompute(HelpdeskTeamModelName, "success_rate_7d", computeSuccessRate7d)
	orm.RegisterCompute(HelpdeskTeamModelName, "avg_rating_7d", computeAvgRating7d)
}
