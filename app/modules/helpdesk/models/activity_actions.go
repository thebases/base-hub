package models

// File overview:
// - Responsibility: implements §9.7's helpdesk_mgmt_activity parity —
//   tbl.helpdesk_ticket's perform_action (schedule a linked activity against
//   the ticket's source document, advance to the team's next stage) and
//   complete_source_activity (mark the linked activity done, advance to the
//   team's activity_stage_id). Ownership: helpdesk module. Audit-sensitivity:
//   low.
// - Key symbols: `runtimePerformAction`, `runtimeCompleteSourceActivity`.
// - Pattern: cross-module calls onto tbl.discuss_activity's
//   schedule_activity/mark_done RuntimeMethods via orm.ExecuteRuntimeMethod,
//   never a direct Go import of modules/discuss/models — the join row
//   (tbl.helpdesk_ticket_activity_link) is what keeps discuss itself
//   unaware helpdesk exists (plan §9.7's deliberate decoupling).

import (
	"context"
	"fmt"

	"git.thebase.vn/base_hub/internal/orm"
)

// runtimePerformAction is tbl.helpdesk_ticket's "perform_action" method
// (UsesPathID: the ticket named by the RPC path id). It validates the
// team's AllowSetActivity plus the ticket's ResModel/ResID/
// SourceActivityTypeID/DateDeadline/AssignedUserID are all set, schedules the activity against the
// ticket's SOURCE record (not the ticket itself), records the join row, then
// advances the ticket to its team's next stage by Sequence.
func runtimePerformAction(ctx context.Context, _ *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("tbl.helpdesk_ticket.perform_action requires an id")
	}
	ticketID, ok := int64FromAny(ids[0])
	if !ok || ticketID <= 0 {
		return nil, fmt.Errorf("tbl.helpdesk_ticket.perform_action requires a valid id")
	}

	ticketRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(HelpdeskTicketModelName, nil), ids,
		[]string{"team_id", "res_model", "res_id", "source_activity_type_id", "date_deadline", "assigned_user_id"})
	if err != nil {
		return nil, err
	}
	ticketRow, err := firstRow(ticketRows, "tbl.helpdesk_ticket")
	if err != nil {
		return nil, err
	}

	teamID := int64Value(ticketRow["team_id"])
	if teamID <= 0 {
		return nil, fmt.Errorf("tbl.helpdesk_ticket %d: perform_action requires a team", ticketID)
	}
	teamRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(HelpdeskTeamModelName, nil), []string{fmt.Sprint(teamID)}, []string{"allow_set_activity"})
	if err != nil {
		return nil, err
	}
	if len(teamRows) == 0 || !boolValue(teamRows[0]["allow_set_activity"]) {
		return nil, fmt.Errorf("tbl.helpdesk_ticket %d: team does not allow scheduling activities", ticketID)
	}

	resModel := stringValue(ticketRow["res_model"])
	resID := int64Value(ticketRow["res_id"])
	activityTypeID := int64Value(ticketRow["source_activity_type_id"])
	assignedUserID := int64Value(ticketRow["assigned_user_id"])
	if resModel == "" || resID <= 0 || activityTypeID <= 0 || assignedUserID <= 0 {
		return nil, fmt.Errorf("tbl.helpdesk_ticket %d: perform_action requires res_model, res_id, source_activity_type_id, and assigned_user_id", ticketID)
	}
	dateDeadline := ticketRow["date_deadline"]

	scheduled, err := orm.ExecuteRuntimeMethod(ctx, "tbl.discuss_activity", "schedule_activity", nil,
		[]any{resModel, resID, activityTypeID, assignedUserID, nil, nil, dateDeadline}, nil)
	if err != nil {
		return nil, err
	}
	activityID := int64Value(resultField(scheduled, "id"))
	if activityID <= 0 {
		return nil, fmt.Errorf("tbl.helpdesk_ticket %d: schedule_activity did not return an id", ticketID)
	}

	if _, err := orm.RuntimeRepositoryCreate(ctx, orm.RuntimeModelRef(HelpdeskTicketActivityLinkModelName, nil), orm.RecordMap{
		"ticket_id":   ticketID,
		"activity_id": activityID,
	}); err != nil {
		return nil, err
	}

	nextStageID, err := nextStageIDBySequence(ctx, teamID, int64Value(ticketRow["stage_id"]))
	if err != nil {
		return nil, err
	}
	if nextStageID > 0 {
		if _, err := writeHelpdeskTicket(ctx, nil, ids, []any{orm.RecordMap{"stage_id": nextStageID}}, nil); err != nil {
			return nil, err
		}
	}

	return map[string]any{"activity_id": activityID}, nil
}

// runtimeCompleteSourceActivity is tbl.helpdesk_ticket's
// "complete_source_activity" method (UsesPathID: the ticket). It looks up
// the join row for (ticketID, activityID), marks the discuss activity done
// via mark_done, then — when the team has activity_stage_id set — advances
// the ticket to it.
func runtimeCompleteSourceActivity(ctx context.Context, _ *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("tbl.helpdesk_ticket.complete_source_activity requires an id")
	}
	ticketID, ok := int64FromAny(ids[0])
	if !ok || ticketID <= 0 {
		return nil, fmt.Errorf("tbl.helpdesk_ticket.complete_source_activity requires a valid id")
	}
	activityID := int64Value(firstNonNil(argValue(args, 0), mapValue(kwargs, "activity_id")))
	if activityID <= 0 {
		return nil, fmt.Errorf("tbl.helpdesk_ticket %d: complete_source_activity requires activity_id", ticketID)
	}

	linkRows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(HelpdeskTicketActivityLinkModelName, nil), orm.RecordMap{
		"ticket_id":   ticketID,
		"activity_id": activityID,
	}, []string{"id"})
	if err != nil {
		return nil, err
	}
	if len(linkRows) == 0 {
		return nil, fmt.Errorf("tbl.helpdesk_ticket %d: no activity link found for activity %d", ticketID, activityID)
	}

	if _, err := orm.ExecuteRuntimeMethod(ctx, "tbl.discuss_activity", "mark_done", []string{fmt.Sprint(activityID)}, nil, nil); err != nil {
		return nil, err
	}

	ticketRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(HelpdeskTicketModelName, nil), ids, []string{"team_id"})
	if err != nil {
		return nil, err
	}
	ticketRow, err := firstRow(ticketRows, "tbl.helpdesk_ticket")
	if err != nil {
		return nil, err
	}
	teamID := int64Value(ticketRow["team_id"])
	if teamID <= 0 {
		return true, nil
	}
	teamRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(HelpdeskTeamModelName, nil), []string{fmt.Sprint(teamID)}, []string{"activity_stage_id"})
	if err != nil {
		return nil, err
	}
	activityStageID := int64Value(firstRowOrEmpty(teamRows)["activity_stage_id"])
	if activityStageID > 0 {
		if _, err := writeHelpdeskTicket(ctx, nil, ids, []any{orm.RecordMap{"stage_id": activityStageID}}, nil); err != nil {
			return nil, err
		}
	}
	return true, nil
}

// nextStageIDBySequence returns the id of the team's stage with the smallest
// Sequence greater than currentStageID's own sequence (or the team's very
// first stage when currentStageID is unset), computed inline per plan §9.7
// as a first pass (no stored next_stage_id compute field).
func nextStageIDBySequence(ctx context.Context, teamID, currentStageID int64) (int64, error) {
	db := orm.BunDBFromContext(ctx)
	if db == nil {
		return 0, fmt.Errorf("nextStageIDBySequence: no database in context")
	}
	var currentSequence int
	if currentStageID > 0 {
		if err := db.NewSelect().Table("tbl_helpdesk_stage").Column("sequence").Where("id = ?", currentStageID).Scan(ctx, &currentSequence); err != nil {
			return 0, nil
		}
	}
	var nextStageID int64
	err := db.NewSelect().
		Table("tbl_helpdesk_stage AS s").
		Join("JOIN tbl_helpdesk_stage_team_rel AS r ON r.stage_id = s.id").
		Column("s.id").
		Where("r.team_id = ?", teamID).
		Where("s.sequence > ?", currentSequence).
		OrderExpr("s.sequence ASC").
		Limit(1).
		Scan(ctx, &nextStageID)
	if err != nil {
		return 0, nil
	}
	return nextStageID, nil
}

// firstRowOrEmpty avoids a second error-return path at call sites that treat
// "no row" the same as "field absent" rather than a hard failure.
func firstRowOrEmpty(rows []orm.RecordMap) orm.RecordMap {
	if len(rows) == 0 {
		return orm.RecordMap{}
	}
	return rows[0]
}
