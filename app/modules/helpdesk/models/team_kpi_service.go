package models

// File overview:
// - Responsibility: team-level KPI compute fields on tbl.helpdesk_team
//   (open/unassigned/urgent/sla-failed ticket counts, 7-day closed count,
//   7-day SLA success rate, 7-day average rating) — the numbers a support
//   manager checks per team, surfaced as ordinary read-only fields so any
//   existing list/kanban/form view can show them without new frontend code.
//   Ownership: helpdesk module. Audit-sensitivity: low (reporting only).
// - Key symbols: `computeOpenTicketCount`, `computeUnassignedTicketCount`,
//   `computeUrgentTicketCount`, `computeSlaFailedCount`,
//   `computeTicketClosed7d`, `computeSuccessRate7d`, `computeAvgRating7d`.
// - Pattern: one raw-Bun `GROUP BY team_id` aggregate per field, same shape
//   as tbl_helpdesk_ticket_hooks.go's computeDuplicateCount — a RecordSet
//   only carries ids, so every handler re-joins whatever team-level flags
//   (use_sla/use_rating) it needs directly in its own query rather than
//   relying on data the caller might have loaded.

import (
	"context"
	"fmt"

	"git.thebase.vn/base_hub/internal/orm"
	"github.com/uptrace/bun"
)

// teamIDsAsAny converts a RecordSet's string ids to the []any shape bun.In
// expects, skipping any id that doesn't parse as int64.
func teamIDsAsAny(records orm.RecordSet) []any {
	ids := records.IDs()
	out := make([]any, 0, len(ids))
	for _, id := range ids {
		if value, ok := int64FromAny(id); ok {
			out = append(out, value)
		}
	}
	return out
}

// computeOpenTicketCount counts, per team, tickets whose current stage is
// not closed (or has no stage yet — an unstaged ticket is never "closed").
func computeOpenTicketCount(ctx context.Context, records orm.RecordSet, _ orm.FieldMetadata) (map[string]any, error) {
	return teamTicketCountQuery(ctx, records, "")
}

// computeUnassignedTicketCount counts, per team, open tickets with no
// assignee.
func computeUnassignedTicketCount(ctx context.Context, records orm.RecordSet, _ orm.FieldMetadata) (map[string]any, error) {
	return teamTicketCountQuery(ctx, records, "t.assignee_id IS NULL")
}

// computeUrgentTicketCount counts, per team, open tickets at urgent
// priority.
func computeUrgentTicketCount(ctx context.Context, records orm.RecordSet, _ orm.FieldMetadata) (map[string]any, error) {
	return teamTicketCountQuery(ctx, records, "t.priority = 'urgent'")
}

// teamTicketCountQuery is the shared shape for the three "open ticket"
// counters above: join the ticket's current stage to test is_closed, filter
// to the requested team ids, apply an optional extra predicate, group by
// team.
func teamTicketCountQuery(ctx context.Context, records orm.RecordSet, extraPredicate string) (map[string]any, error) {
	teamIDs := teamIDsAsAny(records)
	if len(teamIDs) == 0 {
		return map[string]any{}, nil
	}
	db := orm.BunDBFromContext(ctx)
	if db == nil {
		return nil, fmt.Errorf("teamTicketCountQuery: no database in context")
	}
	type row struct {
		TeamID int64 `bun:"team_id"`
		Count  int   `bun:"count"`
	}
	var rows []row
	query := db.NewSelect().
		TableExpr("tbl_helpdesk_ticket AS t").
		Join("LEFT JOIN tbl_helpdesk_stage AS s ON s.id = t.stage_id").
		ColumnExpr("t.team_id AS team_id").
		ColumnExpr("COUNT(*) AS count").
		Where("t.team_id IN (?)", bun.In(teamIDs)).
		Where("COALESCE(s.is_closed, false) = false").
		Group("t.team_id")
	if extraPredicate != "" {
		query = query.Where(extraPredicate)
	}
	if err := query.Scan(ctx, &rows); err != nil {
		return nil, err
	}
	return rowsToTeamMap(rows, func(r row) (int64, any) { return r.TeamID, r.Count }), nil
}

// computeSlaFailedCount counts, per team, distinct tickets carrying at
// least one failed tbl.helpdesk_sla_status row.
func computeSlaFailedCount(ctx context.Context, records orm.RecordSet, _ orm.FieldMetadata) (map[string]any, error) {
	teamIDs := teamIDsAsAny(records)
	if len(teamIDs) == 0 {
		return map[string]any{}, nil
	}
	db := orm.BunDBFromContext(ctx)
	if db == nil {
		return nil, fmt.Errorf("computeSlaFailedCount: no database in context")
	}
	type row struct {
		TeamID int64 `bun:"team_id"`
		Count  int   `bun:"count"`
	}
	var rows []row
	if err := db.NewSelect().
		TableExpr("tbl_helpdesk_ticket AS t").
		Join("JOIN tbl_helpdesk_sla_status AS ss ON ss.ticket_id = t.id AND ss.status = 'failed'").
		ColumnExpr("t.team_id AS team_id").
		ColumnExpr("COUNT(DISTINCT t.id) AS count").
		Where("t.team_id IN (?)", bun.In(teamIDs)).
		Group("t.team_id").
		Scan(ctx, &rows); err != nil {
		return nil, err
	}
	return rowsToTeamMap(rows, func(r row) (int64, any) { return r.TeamID, r.Count }), nil
}

// computeTicketClosed7d counts, per team, tickets closed within the last 7
// days (close_date already set by the stage-transition hook).
func computeTicketClosed7d(ctx context.Context, records orm.RecordSet, _ orm.FieldMetadata) (map[string]any, error) {
	teamIDs := teamIDsAsAny(records)
	if len(teamIDs) == 0 {
		return map[string]any{}, nil
	}
	db := orm.BunDBFromContext(ctx)
	if db == nil {
		return nil, fmt.Errorf("computeTicketClosed7d: no database in context")
	}
	type row struct {
		TeamID int64 `bun:"team_id"`
		Count  int   `bun:"count"`
	}
	var rows []row
	if err := db.NewSelect().
		TableExpr("tbl_helpdesk_ticket AS t").
		ColumnExpr("t.team_id AS team_id").
		ColumnExpr("COUNT(*) AS count").
		Where("t.team_id IN (?)", bun.In(teamIDs)).
		Where("t.close_date >= now() - interval '7 days'").
		Group("t.team_id").
		Scan(ctx, &rows); err != nil {
		return nil, err
	}
	return rowsToTeamMap(rows, func(r row) (int64, any) { return r.TeamID, r.Count }), nil
}

// computeSuccessRate7d computes, per team, the SLA success rate over tickets
// closed in the last 7 days: (closed - failed) * 100 / closed. Sentinel -1
// means "no SLA data for this window" (either the team doesn't use SLA, or
// it closed zero tickets in the window) — mirrors the ticket-count fields'
// own "no data" convention of a plain zero being ambiguous with "0 closed".
func computeSuccessRate7d(ctx context.Context, records orm.RecordSet, _ orm.FieldMetadata) (map[string]any, error) {
	teamIDs := teamIDsAsAny(records)
	if len(teamIDs) == 0 {
		return map[string]any{}, nil
	}
	db := orm.BunDBFromContext(ctx)
	if db == nil {
		return nil, fmt.Errorf("computeSuccessRate7d: no database in context")
	}
	type row struct {
		TeamID      int64 `bun:"team_id"`
		UseSla      bool  `bun:"use_sla"`
		ClosedCount int   `bun:"closed_count"`
		FailedCount int   `bun:"failed_count"`
	}
	var rows []row
	if err := db.NewSelect().
		TableExpr("tbl_helpdesk_team AS team").
		Join("LEFT JOIN tbl_helpdesk_ticket AS t ON t.team_id = team.id AND t.close_date >= now() - interval '7 days'").
		Join("LEFT JOIN tbl_helpdesk_sla_status AS ss ON ss.ticket_id = t.id AND ss.status = 'failed'").
		ColumnExpr("team.id AS team_id").
		ColumnExpr("team.use_sla AS use_sla").
		ColumnExpr("COUNT(DISTINCT t.id) AS closed_count").
		ColumnExpr("COUNT(DISTINCT ss.ticket_id) AS failed_count").
		Where("team.id IN (?)", bun.In(teamIDs)).
		Group("team.id", "team.use_sla").
		Scan(ctx, &rows); err != nil {
		return nil, err
	}
	out := make(map[string]any, len(rows))
	for _, r := range rows {
		rate := -1
		if r.UseSla && r.ClosedCount > 0 {
			rate = (r.ClosedCount - r.FailedCount) * 100 / r.ClosedCount
		}
		out[fmt.Sprint(r.TeamID)] = rate
	}
	return out, nil
}

// computeAvgRating7d computes, per team, the average tbl.helpdesk_rating
// value across tickets closed in the last 7 days. Sentinel -1 means "no
// rating data" (team doesn't use ratings, or no ratings were submitted).
func computeAvgRating7d(ctx context.Context, records orm.RecordSet, _ orm.FieldMetadata) (map[string]any, error) {
	teamIDs := teamIDsAsAny(records)
	if len(teamIDs) == 0 {
		return map[string]any{}, nil
	}
	db := orm.BunDBFromContext(ctx)
	if db == nil {
		return nil, fmt.Errorf("computeAvgRating7d: no database in context")
	}
	type row struct {
		TeamID    int64   `bun:"team_id"`
		UseRating bool    `bun:"use_rating"`
		RatingSum float64 `bun:"rating_sum"`
		RatingN   int     `bun:"rating_n"`
	}
	var rows []row
	if err := db.NewSelect().
		TableExpr("tbl_helpdesk_team AS team").
		Join("LEFT JOIN tbl_helpdesk_ticket AS t ON t.team_id = team.id AND t.close_date >= now() - interval '7 days'").
		Join("LEFT JOIN tbl_helpdesk_rating AS r ON r.ticket_id = t.id").
		ColumnExpr("team.id AS team_id").
		ColumnExpr("team.use_rating AS use_rating").
		ColumnExpr("COALESCE(SUM(r.value), 0) AS rating_sum").
		ColumnExpr("COUNT(r.id) AS rating_n").
		Where("team.id IN (?)", bun.In(teamIDs)).
		Group("team.id", "team.use_rating").
		Scan(ctx, &rows); err != nil {
		return nil, err
	}
	out := make(map[string]any, len(rows))
	for _, r := range rows {
		rating := -1.0
		if r.UseRating && r.RatingN > 0 {
			rating = r.RatingSum / float64(r.RatingN)
		}
		out[fmt.Sprint(r.TeamID)] = rating
	}
	return out, nil
}

// rowsToTeamMap is the shared "rows -> per-record-id map[string]any" fold
// every compute handler above needs, generic over the row type so each
// query's own struct shape stays local to its own function.
func rowsToTeamMap[T any](rows []T, extract func(T) (int64, any)) map[string]any {
	out := make(map[string]any, len(rows))
	for _, r := range rows {
		teamID, value := extract(r)
		out[fmt.Sprint(teamID)] = value
	}
	return out
}
