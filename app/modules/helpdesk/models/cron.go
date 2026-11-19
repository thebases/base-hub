package models

// File overview:
// - Responsibility: registers this module's periodic jobs — SLA breach
//   sweep (§4), auto-close (§4), and close-inactive (§9.6) — as
//   base-automation go_handlers at boot. Ownership: helpdesk module.
//   Audit-sensitivity: low.
// - Key symbols: `InitCron`, `sweepAutoClose`, `sweepCloseInactiveTickets`.
// - Pattern: mirrors modules/base-workflow/service/engine/runtime.go's
//   automationservice.RegisterGoHandler-at-boot / UnregisterGoHandler-on-
//   shutdown shape — this module never registers its own kernel sys.cron
//   row, it hands compiled Go logic to base-automation's single
//   `automation.dispatch_due_crons` bridge instead. The matching
//   `automation.action` (go_handler) + `automation.cron` seed rows that
//   actually schedule these three keys live in
//   data/13_automation_actions_helpdesk.json and
//   data/14_automation_crons_helpdesk.json.

import (
	"context"
	"fmt"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	automationservice "git.thebase.vn/base_hub/modules/base-automation/service"
	"github.com/uptrace/bun"
)

// inInt64 converts an []int64 into a bun.In value for a "col IN (?)" clause.
func inInt64(ids []int64) any {
	asAny := make([]any, 0, len(ids))
	for _, id := range ids {
		asAny = append(asAny, id)
	}
	return bun.In(asAny)
}

const (
	slaSweepHandlerKey      = "helpdesk.sla_sweep"
	autoCloseHandlerKey     = "helpdesk.auto_close"
	closeInactiveHandlerKey = "helpdesk.close_inactive_tickets"
	digestHandlerKey        = "helpdesk.digest"
)

// InitCron registers every helpdesk periodic job as a base-automation
// go_handler and returns the combined shutdown (unregister) func, following
// this module's own boot.RegisterRuntime convention (see helpdesk.go's
// Bootstrap). RegisterGoHandler only errors on duplicate registration, which
// would mean two owners fight over one job key — fail loudly then, mirroring
// base-workflow's engine.InitRuntime.
func InitCron() func() {
	if err := automationservice.RegisterGoHandler(slaSweepHandlerKey, sweepSlaBreaches); err != nil {
		log.Fatalf("helpdesk: register go handler %s: %v", slaSweepHandlerKey, err)
	}
	if err := automationservice.RegisterGoHandler(autoCloseHandlerKey, sweepAutoClose); err != nil {
		log.Fatalf("helpdesk: register go handler %s: %v", autoCloseHandlerKey, err)
	}
	if err := automationservice.RegisterGoHandler(closeInactiveHandlerKey, sweepCloseInactiveTickets); err != nil {
		log.Fatalf("helpdesk: register go handler %s: %v", closeInactiveHandlerKey, err)
	}
	if err := automationservice.RegisterGoHandler(digestHandlerKey, runHelpdeskDigest); err != nil {
		log.Fatalf("helpdesk: register go handler %s: %v", digestHandlerKey, err)
	}
	return func() {
		automationservice.UnregisterGoHandler(slaSweepHandlerKey)
		automationservice.UnregisterGoHandler(autoCloseHandlerKey)
		automationservice.UnregisterGoHandler(closeInactiveHandlerKey)
		automationservice.UnregisterGoHandler(digestHandlerKey)
	}
}

// runHelpdeskDigest is the digest cron body: once per run (scheduled daily,
// see data/14_automation_crons_helpdesk.json), snapshot every team's
// tickets-closed/SLA-success-rate/avg-rating KPIs over the last 24 hours
// into a new tbl.helpdesk_digest_log row. Reuses the same aggregate shape as
// team_kpi_service.go's 7-day compute fields, just over a 24h window and
// written to a durable table instead of returned live — deliberately no
// outbound email/chatter here, since no mail-template renderer exists in
// this repo (same simplification already applied to close-inactive's
// warning/closing notes).
func runHelpdeskDigest(ctx context.Context, _ map[string]any) error {
	db := orm.BunDBFromContext(ctx)
	if db == nil {
		return fmt.Errorf("%s: no database in context", digestHandlerKey)
	}
	periodEnd := time.Now().UTC()
	periodStart := periodEnd.Add(-24 * time.Hour)

	type row struct {
		TeamID      int64   `bun:"team_id"`
		UseSla      bool    `bun:"use_sla"`
		UseRating   bool    `bun:"use_rating"`
		ClosedCount int     `bun:"closed_count"`
		FailedCount int     `bun:"failed_count"`
		RatingSum   float64 `bun:"rating_sum"`
		RatingN     int     `bun:"rating_n"`
	}
	var rows []row
	if err := db.NewSelect().
		TableExpr("tbl_helpdesk_team AS team").
		Join("LEFT JOIN tbl_helpdesk_ticket AS t ON t.team_id = team.id AND t.close_date >= ? AND t.close_date < ?", periodStart, periodEnd).
		Join("LEFT JOIN tbl_helpdesk_sla_status AS ss ON ss.ticket_id = t.id AND ss.status = 'failed'").
		Join("LEFT JOIN tbl_helpdesk_rating AS r ON r.ticket_id = t.id").
		ColumnExpr("team.id AS team_id").
		ColumnExpr("team.use_sla AS use_sla").
		ColumnExpr("team.use_rating AS use_rating").
		ColumnExpr("COUNT(DISTINCT t.id) AS closed_count").
		ColumnExpr("COUNT(DISTINCT ss.ticket_id) AS failed_count").
		ColumnExpr("COALESCE(SUM(r.value), 0) AS rating_sum").
		ColumnExpr("COUNT(r.id) AS rating_n").
		Group("team.id", "team.use_sla", "team.use_rating").
		Scan(ctx, &rows); err != nil {
		return fmt.Errorf("%s: failed to aggregate team KPIs: %w", digestHandlerKey, err)
	}

	for _, r := range rows {
		successRate := -1
		if r.UseSla && r.ClosedCount > 0 {
			successRate = (r.ClosedCount - r.FailedCount) * 100 / r.ClosedCount
		}
		avgRating := -1.0
		if r.UseRating && r.RatingN > 0 {
			avgRating = r.RatingSum / float64(r.RatingN)
		}
		if _, err := orm.RuntimeRepositoryCreate(ctx, orm.RuntimeModelRef(HelpdeskDigestLogModelName, nil), orm.RecordMap{
			"team_id":        r.TeamID,
			"period_start":   periodStart.Format(time.RFC3339),
			"period_end":     periodEnd.Format(time.RFC3339),
			"tickets_closed": r.ClosedCount,
			"success_rate":   successRate,
			"avg_rating":     avgRating,
		}); err != nil {
			log.Warnf("%s: failed to write digest log for team %d: %v", digestHandlerKey, r.TeamID, err)
		}
	}
	return nil
}

// sweepAutoClose moves stale tickets (team.auto_close_ticket=true,
// updated_at older than auto_close_days) into auto_close_stage_id, or the
// team's last stage by sequence when unset.
func sweepAutoClose(ctx context.Context, _ map[string]any) error {
	db := orm.BunDBFromContext(ctx)
	if db == nil {
		return fmt.Errorf("%s: no database in context", autoCloseHandlerKey)
	}
	teamRows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(HelpdeskTeamModelName, nil), orm.RecordMap{"auto_close_ticket": true},
		[]string{"id", "auto_close_days", "auto_close_stage_id"})
	if err != nil {
		return err
	}
	for _, team := range teamRows {
		teamID := int64Value(team["id"])
		days := int64Value(team["auto_close_days"])
		if days <= 0 {
			days = 14
		}
		cutoff := time.Now().UTC().AddDate(0, 0, -int(days))
		stageID := int64Value(team["auto_close_stage_id"])
		if stageID <= 0 {
			stageID, err = lastStageIDBySequence(ctx, teamID)
			if err != nil || stageID <= 0 {
				continue
			}
		}
		var staleIDs []int64
		if err := db.NewSelect().
			Table("tbl_helpdesk_ticket").
			Column("id").
			Where("team_id = ?", teamID).
			Where(openTicketSQL("tbl_helpdesk_ticket")).
			Where("updated_at < ?", cutoff).
			Scan(ctx, &staleIDs); err != nil {
			log.Warnf("%s: failed to load stale tickets for team %d: %v", autoCloseHandlerKey, teamID, err)
			continue
		}
		for _, ticketID := range staleIDs {
			if _, err := writeHelpdeskTicket(ctx, nil, []string{fmt.Sprint(ticketID)}, []any{orm.RecordMap{"stage_id": stageID}}, nil); err != nil {
				log.Warnf("%s: failed to auto-close ticket %d: %v", autoCloseHandlerKey, ticketID, err)
			}
		}
	}
	return nil
}

// lastStageIDBySequence returns the id of the team's own stage with the
// highest Sequence.
func lastStageIDBySequence(ctx context.Context, teamID int64) (int64, error) {
	db := orm.BunDBFromContext(ctx)
	var stageID int64
	err := db.NewSelect().
		Table("tbl_helpdesk_stage AS s").
		Join("JOIN tbl_helpdesk_stage_team_rel AS r ON r.stage_id = s.id").
		Column("s.id").
		Where("r.team_id = ?", teamID).
		OrderExpr("s.sequence DESC").
		Limit(1).
		Scan(ctx, &stageID)
	return stageID, err
}

// sweepCloseInactiveTickets implements plan §9.6: per team with
// close_inactive_tickets=true, a warning phase (post a chatter note once
// last_stage_update crosses inactive_days_limit_warning) and a closing phase
// (write stage_id = closing_stage_id + closing chatter note once past
// inactive_days_limit_closing) — email templates simplified to chatter notes
// since no mail-template infra exists in this repo (documented per plan §9.6).
func sweepCloseInactiveTickets(ctx context.Context, _ map[string]any) error {
	teamRows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(HelpdeskTeamModelName, nil), orm.RecordMap{"close_inactive_tickets": true},
		[]string{"id", "ticket_stage_ids", "ticket_category_ids", "inactive_days_limit_warning", "inactive_days_limit_closing", "closing_stage_id"})
	if err != nil {
		return err
	}
	for _, team := range teamRows {
		closeInactiveTeamSweep(ctx, team)
	}
	return nil
}

// closeInactiveTeamSweep runs both phases for one team row.
func closeInactiveTeamSweep(ctx context.Context, team orm.RecordMap) {
	teamID := int64Value(team["id"])
	stageIDs := int64SliceFromAny(team["ticket_stage_ids"])
	categoryIDs := int64SliceFromAny(team["ticket_category_ids"])
	warningDays := int64Value(team["inactive_days_limit_warning"])
	closingDays := int64Value(team["inactive_days_limit_closing"])
	closingStageID := int64Value(team["closing_stage_id"])
	if len(stageIDs) == 0 {
		return
	}

	tickets, err := eligibleInactiveTickets(ctx, teamID, stageIDs, categoryIDs)
	if err != nil {
		log.Warnf("%s: failed to load eligible tickets for team %d: %v", closeInactiveHandlerKey, teamID, err)
		return
	}

	now := time.Now().UTC()
	for _, ticket := range tickets {
		lastStageUpdate, ok := ticket["last_stage_update"].(time.Time)
		if !ok {
			continue
		}
		inactiveDays := int64(now.Sub(lastStageUpdate).Hours() / 24)
		ticketID := int64Value(ticket["id"])

		if closingDays > 0 && inactiveDays >= closingDays && closingStageID > 0 {
			if _, err := writeHelpdeskTicket(ctx, nil, []string{fmt.Sprint(ticketID)}, []any{orm.RecordMap{"stage_id": closingStageID}}, nil); err != nil {
				log.Warnf("%s: failed to close inactive ticket %d: %v", closeInactiveHandlerKey, ticketID, err)
				continue
			}
			if _, err := PostTicketMessage(ctx, ticketID, nil, "Ticket automatically closed due to inactivity.", true, nil); err != nil {
				log.Warnf("%s: failed to post closing note on ticket %d: %v", closeInactiveHandlerKey, ticketID, err)
			}
			continue
		}
		if warningDays > 0 && inactiveDays >= warningDays && closingDays > 0 {
			remaining := closingDays - inactiveDays
			if remaining < 0 {
				remaining = 0
			}
			note := fmt.Sprintf("Ticket will be closed in %d day(s) if it stays inactive.", remaining)
			if _, err := PostTicketMessage(ctx, ticketID, nil, note, true, nil); err != nil {
				log.Warnf("%s: failed to post warning note on ticket %d: %v", closeInactiveHandlerKey, ticketID, err)
			}
		}
	}
}

// eligibleInactiveTickets loads open tickets in the team's configured
// sweep stages, optionally filtered to its configured categories.
func eligibleInactiveTickets(ctx context.Context, teamID int64, stageIDs, categoryIDs []int64) ([]orm.RecordMap, error) {
	db := orm.BunDBFromContext(ctx)
	if db == nil {
		return nil, fmt.Errorf("%s: no database in context", closeInactiveHandlerKey)
	}
	var rows []struct {
		ID              int64     `bun:"id"`
		LastStageUpdate time.Time `bun:"last_stage_update"`
	}
	query := db.NewSelect().
		Table("tbl_helpdesk_ticket").
		Column("id", "last_stage_update").
		Where("team_id = ?", teamID).
		Where("stage_id IN (?)", inInt64(stageIDs)).
		Where(openTicketSQL("tbl_helpdesk_ticket")).
		Where("last_stage_update IS NOT NULL")
	if len(categoryIDs) > 0 {
		query = query.Where("category_id IN (?)", inInt64(categoryIDs))
	}
	if err := query.Scan(ctx, &rows); err != nil {
		return nil, err
	}
	out := make([]orm.RecordMap, 0, len(rows))
	for _, row := range rows {
		out = append(out, orm.RecordMap{"id": row.ID, "last_stage_update": row.LastStageUpdate})
	}
	return out, nil
}
