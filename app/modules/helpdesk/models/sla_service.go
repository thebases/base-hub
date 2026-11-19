package models

// File overview:
// - Responsibility: implements SLA matching (plan §4) — on ticket
//   create/write, recomputes which tbl.helpdesk_sla policies match the
//   ticket and idempotently upserts one tbl.helpdesk_sla_status row per
//   match, then recomputes the ticket's stored SlaDeadline as the min
//   ongoing deadline. Also the breach-sweep cron body. Ownership: helpdesk
//   module. Audit-sensitivity: low.
// - Key symbols: `recomputeSlaForTicket`, `sweepSlaBreaches`.
// - Simplification (documented per plan §4/§8): deadline = created_at +
//   DurationHours, a fixed-duration calculation — no working-calendar/
//   business-hours service exists in this repo yet.

import (
	"context"
	"fmt"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// recomputeSlaForTicket loads the ticket's matchable fields, finds every
// tbl.helpdesk_sla policy that matches, upserts a tbl.helpdesk_sla_status row
// per match (never duplicating an existing (ticket_id, sla_id) pair), then
// recomputes the ticket's stored sla_deadline. Best-effort: logs and returns
// on error rather than failing the caller's create/write.
func recomputeSlaForTicket(ctx context.Context, ticketID int64) {
	if ticketID <= 0 {
		return
	}
	ticketRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(HelpdeskTicketModelName, nil), []string{fmt.Sprint(ticketID)},
		[]string{"team_id", "priority", "tag_ids", "ticket_type_id", "partner_id", "stage_id", "created_at"})
	if err != nil {
		log.Warnf("tbl.helpdesk_ticket %d: failed to read ticket for SLA matching: %v", ticketID, err)
		return
	}
	ticketRow, err := firstRow(ticketRows, "tbl.helpdesk_ticket")
	if err != nil {
		return
	}
	teamID, ok := int64FromAny(ticketRow["team_id"])
	if !ok || teamID <= 0 {
		return
	}

	teamRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(HelpdeskTeamModelName, nil), []string{fmt.Sprint(teamID)}, []string{"use_sla"})
	if err != nil || len(teamRows) == 0 || !boolValue(teamRows[0]["use_sla"]) {
		return
	}

	slaRows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(HelpdeskSlaModelName, nil), orm.RecordMap{"team_id": teamID}, nil)
	if err != nil {
		log.Warnf("tbl.helpdesk_ticket %d: failed to load SLA policies for team %d: %v", ticketID, teamID, err)
		return
	}

	currentStageID := int64Value(ticketRow["stage_id"])
	priority := stringValue(ticketRow["priority"])
	tagIDs := int64SliceFromAny(ticketRow["tag_ids"])
	ticketTypeID := int64Value(ticketRow["ticket_type_id"])
	partnerID := int64Value(ticketRow["partner_id"])
	createdAt, _ := ticketRow["created_at"].(time.Time)

	for _, slaRow := range slaRows {
		if !slaMatches(slaRow, priority, tagIDs, ticketTypeID, partnerID, currentStageID) {
			continue
		}
		upsertSlaStatus(ctx, ticketID, int64Value(slaRow["id"]), createdAt, slaDurationHours(slaRow))
	}

	recomputeTicketSlaDeadline(ctx, ticketID)
}

// slaMatches applies a per-field domain match: a nil/empty restriction
// always matches; a non-empty restriction must contain the ticket's value.
// ExcludeStageIDs stops a match once the ticket has reached one of those
// stages.
func slaMatches(slaRow orm.RecordMap, priority string, tagIDs []int64, ticketTypeID, partnerID, currentStageID int64) bool {
	if slaPriority := stringValue(slaRow["priority"]); slaPriority != "" && slaPriority != priority {
		return false
	}
	if ids := int64SliceFromAny(slaRow["ticket_type_ids"]); len(ids) > 0 && !containsInt64(ids, ticketTypeID) {
		return false
	}
	if ids := int64SliceFromAny(slaRow["partner_ids"]); len(ids) > 0 && !containsInt64(ids, partnerID) {
		return false
	}
	if ids := int64SliceFromAny(slaRow["tag_ids"]); len(ids) > 0 && !anyInCommon(ids, tagIDs) {
		return false
	}
	if excludeIDs := int64SliceFromAny(slaRow["exclude_stage_ids"]); containsInt64(excludeIDs, currentStageID) {
		return false
	}
	return true
}

func containsInt64(list []int64, value int64) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func anyInCommon(a, b []int64) bool {
	for _, item := range a {
		if containsInt64(b, item) {
			return true
		}
	}
	return false
}

func slaDurationHours(slaRow orm.RecordMap) float64 {
	switch typed := slaRow["duration_hours"].(type) {
	case float64:
		return typed
	case int64:
		return float64(typed)
	case int:
		return float64(typed)
	default:
		return 24
	}
}

// upsertSlaStatus creates the (ticket_id, sla_id) row if missing; never
// duplicates an existing match per plan §4.
func upsertSlaStatus(ctx context.Context, ticketID, slaID int64, createdAt time.Time, durationHours float64) {
	existing, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(HelpdeskSlaStatusModelName, nil), orm.RecordMap{
		"ticket_id": ticketID,
		"sla_id":    slaID,
	}, []string{"id"})
	if err != nil {
		log.Warnf("tbl.helpdesk_sla_status: failed to check existing match ticket=%d sla=%d: %v", ticketID, slaID, err)
		return
	}
	if len(existing) > 0 {
		return
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	deadline := createdAt.Add(time.Duration(durationHours * float64(time.Hour)))
	if _, err := orm.RuntimeRepositoryCreate(ctx, orm.RuntimeModelRef(HelpdeskSlaStatusModelName, nil), orm.RecordMap{
		"ticket_id": ticketID,
		"sla_id":    slaID,
		"deadline":  deadline,
		"status":    "ongoing",
	}); err != nil {
		log.Warnf("tbl.helpdesk_sla_status: failed to create match ticket=%d sla=%d: %v", ticketID, slaID, err)
	}
}

// recomputeTicketSlaDeadline sets the ticket's stored sla_deadline to the
// min deadline among its ongoing SLA status rows (nil when none ongoing).
func recomputeTicketSlaDeadline(ctx context.Context, ticketID int64) {
	statusRows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(HelpdeskSlaStatusModelName, nil), orm.RecordMap{
		"ticket_id": ticketID,
		"status":    "ongoing",
	}, []string{"deadline"})
	if err != nil {
		log.Warnf("tbl.helpdesk_ticket %d: failed to load SLA status rows for deadline recompute: %v", ticketID, err)
		return
	}
	var earliest *time.Time
	for _, row := range statusRows {
		deadline, ok := row["deadline"].(time.Time)
		if !ok {
			continue
		}
		if earliest == nil || deadline.Before(*earliest) {
			earliest = &deadline
		}
	}
	value := any(nil)
	if earliest != nil {
		value = *earliest
	}
	if _, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(HelpdeskTicketModelName, nil), []string{fmt.Sprint(ticketID)}, orm.RecordMap{"sla_deadline": value}); err != nil {
		log.Warnf("tbl.helpdesk_ticket %d: failed to write recomputed sla_deadline: %v", ticketID, err)
	}
}

// sweepSlaBreaches is the cron body for the SLA breach sweep: every ongoing
// SLA status row past its deadline with no reached_at is marked "failed".
func sweepSlaBreaches(ctx context.Context, _ map[string]any) error {
	db := orm.BunDBFromContext(ctx)
	if db == nil {
		return fmt.Errorf("helpdesk.sla_sweep: no database in context")
	}
	_, err := db.NewUpdate().
		Table("tbl_helpdesk_sla_status").
		Set("status = 'failed'").
		Set("updated_at = now()").
		Where("status = 'ongoing'").
		Where("deadline IS NOT NULL AND deadline < now()").
		Where("reached_at IS NULL").
		Exec(ctx)
	return err
}
