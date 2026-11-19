package models

// File overview:
// - Responsibility: defines the `tbl.helpdesk_digest_log` model — a durable
//   per-team, per-run snapshot of the team_kpi_service.go KPIs (tickets
//   closed / SLA success rate / average rating over the run's window). Kept
//   distinct from the live compute fields on tbl.helpdesk_team so those stay
//   the "current" numbers while this table is a historical/audit trail of
//   what the digest reported at each run — no outbound email/chatter
//   mechanism is added; this is purely a stored record a manager can list.
//   Ownership: helpdesk module. Audit-sensitivity: low.
// - Key symbols: `init`, `HelpdeskDigestLogRecord`, `HelpdeskDigestLogModelSpec`.

import (
	"git.thebase.vn/base_hub/internal/orm"
)

// HelpdeskDigestLogRecord stores one digest run's snapshot for one team.
type HelpdeskDigestLogRecord struct {
	orm.BaseModel
	TeamID        int64   `bun:"team_id,notnull" json:"team_id" meta:"kind:many2one,model:tbl.helpdesk_team,ondelete:CASCADE,required,index,label:Team,help:Links this digest snapshot to the team it summarizes."`
	PeriodStart   string  `bun:"period_start,notnull" json:"period_start" meta:"required,index,label:Period Start,help:Stores the ISO-8601 start timestamp of the summarized window."`
	PeriodEnd     string  `bun:"period_end,notnull" json:"period_end" meta:"required,index,label:Period End,help:Stores the ISO-8601 end timestamp of the summarized window."`
	TicketsClosed int     `bun:"tickets_closed,notnull,default:0" json:"tickets_closed" meta:"required,label:Tickets Closed,help:Stores how many tickets the team closed within the window."`
	SuccessRate   int     `bun:"success_rate,notnull,default:-1" json:"success_rate" meta:"required,label:SLA Success Rate,help:Stores the team's SLA success percentage for the window. -1 means no SLA data."`
	AvgRating     float64 `bun:"avg_rating,notnull,default:-1" json:"avg_rating" meta:"required,label:Average Rating,help:Stores the team's average customer rating for the window. -1 means no rating data."`
}

const HelpdeskDigestLogModelName = "tbl.helpdesk_digest_log"

var HelpdeskDigestLogModelSpec = orm.ModelSpec{
	Name:        HelpdeskDigestLogModelName,
	Label:       "Helpdesk Digest Log",
	Description: "Per-team periodic KPI snapshots owned by the helpdesk module",
	Definition:  orm.ModelDefinition(HelpdeskDigestLogModelName, (*HelpdeskDigestLogRecord)(nil)),
}

var HelpdeskDigestLogModel = orm.NewHandlerWithModel[HelpdeskDigestLogRecord](HelpdeskDigestLogModelSpec)

// init registers this package runtime during bootstrap so its models, hooks, or commands are available before requests arrive.
func init() {
	// Register runtime metadata during bootstrap so the model is available before requests arrive.
	RegisterRuntimeBootstrap(HelpdeskDigestLogModelName, 912, HelpdeskDigestLogModel.InitDefault, HelpdeskDigestLogModel.Shutdown)
}
