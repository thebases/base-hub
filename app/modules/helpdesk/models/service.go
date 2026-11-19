package models

// File overview:
// - Responsibility: implements §9.4's PostTicketMessage wrapper — the
//   helpdesk-owned entry point onto the discuss chatter core (never create
//   tbl.discuss_message rows directly) — plus the partner-response
//   auto-stage-transition check that runs after a successful post.
//   Ownership: helpdesk module. Audit-sensitivity: low.
// - Key symbols: `PostTicketMessage`, `checkPartnerResponseAutoTransition`.
// - Pattern: cross-module call goes through orm.ExecuteRuntimeMethod
//   targeting tbl.discuss_message's "post_message" RuntimeMethod, per plan
//   §9.4 — never a direct Go import of modules/discuss/models, keeping the
//   call path identical to how any other (non-Go) caller reaches chatter.

import (
	"context"
	"fmt"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// PostTicketMessage posts a chatter message/note on ticketID via discuss's
// post_message RuntimeMethod, then — for a non-note post — checks whether
// the author's linked partner matches the ticket's own partner and the
// team's autoupdate_ticket_stage rule applies, auto-advancing the stage.
func PostTicketMessage(ctx context.Context, ticketID int64, authorID *int64, body string, isNote bool, recipientUserIDs []int64) (int64, error) {
	if ticketID <= 0 {
		return 0, fmt.Errorf("tbl.helpdesk_ticket.post_ticket_message requires a positive ticket id")
	}
	kwargs := map[string]any{
		"is_note": isNote,
	}
	if len(recipientUserIDs) > 0 {
		ids := make([]any, 0, len(recipientUserIDs))
		for _, id := range recipientUserIDs {
			ids = append(ids, id)
		}
		kwargs["recipient_ids"] = ids
	}
	args := []any{HelpdeskTicketModelName, ticketID, authorIDAny(authorID), nil, "comment", nil, body}
	result, err := orm.ExecuteRuntimeMethod(ctx, "tbl.discuss_message", "post_message", nil, args, kwargs)
	if err != nil {
		return 0, err
	}
	messageID := int64Value(resultField(result, "id"))

	if !isNote {
		checkPartnerResponseAutoTransition(ctx, ticketID, authorID)
	}
	return messageID, nil
}

// authorIDAny lets a nil *int64 pass through as a real nil arg (so
// runtimePostMessage's own "default to calling user" fallback applies)
// instead of a typed-nil interface value.
func authorIDAny(authorID *int64) any {
	if authorID == nil {
		return nil
	}
	return *authorID
}

// resultField pulls one field out of ExecuteRuntimeMethod's `any` result,
// which is a map[string]any for every RuntimeMethod in this repo's convention.
func resultField(result any, field string) any {
	if typed, ok := result.(map[string]any); ok {
		return typed[field]
	}
	return nil
}

// checkPartnerResponseAutoTransition implements plan §9.4: resolves the
// posting author's linked partner (base.users.partner_id), and if it equals
// the ticket's own partner_id, the ticket's team has
// autoupdate_ticket_stage=true, and the ticket's current stage is in the
// team's autoupdate_src_stage_ids, writes stage_id = autoupdate_dest_stage_id.
// Best-effort: logs and returns on any lookup failure rather than failing
// the message post that already succeeded.
func checkPartnerResponseAutoTransition(ctx context.Context, ticketID int64, authorID *int64) {
	if authorID == nil {
		return
	}
	userRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef("base.users", nil), []string{fmt.Sprint(*authorID)}, []string{"partner_id"})
	if err != nil || len(userRows) == 0 {
		return
	}
	authorPartnerID := int64Value(userRows[0]["partner_id"])
	if authorPartnerID <= 0 {
		return
	}

	ticketRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(HelpdeskTicketModelName, nil), []string{fmt.Sprint(ticketID)}, []string{"partner_id", "team_id", "stage_id"})
	if err != nil || len(ticketRows) == 0 {
		return
	}
	ticketRow := ticketRows[0]
	if int64Value(ticketRow["partner_id"]) != authorPartnerID {
		return
	}
	teamID := int64Value(ticketRow["team_id"])
	if teamID <= 0 {
		return
	}

	teamRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(HelpdeskTeamModelName, nil), []string{fmt.Sprint(teamID)}, []string{"autoupdate_ticket_stage", "autoupdate_src_stage_ids", "autoupdate_dest_stage_id"})
	if err != nil || len(teamRows) == 0 || !boolValue(teamRows[0]["autoupdate_ticket_stage"]) {
		return
	}
	srcStageIDs := int64SliceFromAny(teamRows[0]["autoupdate_src_stage_ids"])
	currentStageID := int64Value(ticketRow["stage_id"])
	if !containsInt64(srcStageIDs, currentStageID) {
		return
	}
	destStageID := int64Value(teamRows[0]["autoupdate_dest_stage_id"])
	if destStageID <= 0 {
		return
	}

	// Call the same-package write hook directly (not a recursive RPC) so
	// status/close_date/last_stage_update/unattended stay consistent, the
	// same stage-transition path any other stage change goes through.
	if _, err := writeHelpdeskTicket(ctx, nil, []string{fmt.Sprint(ticketID)}, []any{orm.RecordMap{"stage_id": destStageID}}, nil); err != nil {
		log.Warnf("tbl.helpdesk_ticket %d: partner-response auto-transition write failed: %v", ticketID, err)
	}
}
