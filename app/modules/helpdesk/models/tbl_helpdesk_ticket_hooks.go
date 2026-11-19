package models

// File overview:
// - Responsibility: implements tbl.helpdesk_ticket's create/write business
//   logic: ticket number assignment, stage-transition-driven
//   close_date/last_stage_update/unattended, the assignment-method
//   auto-picker, the §9.3 symmetric related-ticket write hook, and the
//   DuplicateCount compute. Ownership: helpdesk module. Audit-sensitivity:
//   low (no balance/payment mutation) — stage transitions are plain
//   business-state changes, but LastStageUpdate/CloseDate must stay accurate
//   since close-inactive (§9.6) and SLA (§4) both key off them.
// - Key symbols: `createHelpdeskTicket`, `writeHelpdeskTicket`,
//   `pickAssignee`, `syncRelatedTicketSymmetry`, `computeDuplicateCount`.
// - Pattern: mirrors modules/website/models/tbl_website_page.go's write-hook
//   shape (read current row for fields not in the write payload, mutate a
//   cloned RecordMap, delegate to the guarded orm.WriteRecords/CreateRecord).

import (
	"context"
	"fmt"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

// createHelpdeskTicket assigns the ticket Number sequence, applies the
// stage-transition-derived fields for the ticket's initial stage (if any),
// runs the team assignment-method picker when no assignee was given, and
// finally creates the row.
func createHelpdeskTicket(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		return nil, fmt.Errorf("tbl.helpdesk_ticket.create requires values")
	}
	working := cloneRecordMap(values)

	if err := applyStageTransitionFields(ctx, working, nil); err != nil {
		return nil, err
	}

	// Assignment-method auto-pick only runs when the caller didn't already
	// supply an assignee and a team is set.
	if !hasRecordMapKey(working, "assignee_id") || working["assignee_id"] == nil {
		if err := applyAssignmentPick(ctx, working); err != nil {
			log.Warnf("tbl.helpdesk_ticket: assignment pick failed on create: %v", err)
		}
	}

	created, err := orm.CreateRecord(ctx, orm.RuntimeModelRef(HelpdeskTicketModelName, model), working)
	if err != nil {
		return nil, err
	}
	ticketID := int64Value(created["id"])
	if ticketID > 0 {
		recomputeSlaForTicket(ctx, ticketID)
	}
	return created, nil
}

// writeHelpdeskTicket reacts to stage_id/related_ticket_ids/team_id changes:
// stage transitions derive close_date/last_stage_update/unattended;
// related_ticket_ids changes are mirrored symmetrically onto affected
// tickets; team/priority/tags/type/partner changes trigger an SLA rematch.
func writeHelpdeskTicket(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		return nil, fmt.Errorf("tbl.helpdesk_ticket.write requires values")
	}
	working := cloneRecordMap(values)

	if hasRecordMapKey(working, "stage_id") {
		if err := applyStageTransitionFields(ctx, working, ids); err != nil {
			return nil, err
		}
	}

	// related_ticket_ids is intercepted before the generic write: the OLD
	// pivot state must be read before WriteRecords overwrites it,
	// so the symmetric mirror below can diff old vs. new. The mirror itself
	// runs after the write and only touches OTHER tickets' pivot rows, never
	// a second recursive Write call on this same ticket.
	var relatedChangeTicketID int64
	var relatedChangeOldIDs []int64
	var relatedChangeNewIDs []int64
	if hasRecordMapKey(working, "related_ticket_ids") && len(ids) > 0 {
		relatedChangeTicketID = int64Value(ids[0])
		relatedChangeNewIDs = int64SliceFromAny(working["related_ticket_ids"])
		relatedChangeOldIDs, err = readRelatedTicketIDs(ctx, relatedChangeTicketID)
		if err != nil {
			return nil, err
		}
	}

	result, err := orm.WriteRecords(ctx, orm.RuntimeModelRef(HelpdeskTicketModelName, model), ids, working)
	if err != nil {
		return nil, err
	}

	if relatedChangeTicketID > 0 {
		syncRelatedTicketSymmetry(ctx, relatedChangeTicketID, relatedChangeOldIDs, relatedChangeNewIDs)
	}

	// An SLA-relevant field change (team/priority/tags/type/partner)
	// re-triggers matching for every affected ticket.
	if slaMatchFieldsChanged(working) {
		for _, id := range ids {
			recomputeSlaForTicket(ctx, int64Value(id))
		}
	}

	return result, nil
}

// slaMatchFieldsChanged reports whether working touches any field the SLA
// matcher keys on.
func slaMatchFieldsChanged(working orm.RecordMap) bool {
	for _, key := range []string{"team_id", "priority", "tag_ids", "ticket_type_id", "partner_id", "stage_id"} {
		if hasRecordMapKey(working, key) {
			return true
		}
	}
	return false
}

// applyStageTransitionFields mutates working in place: when stage_id is
// present, it loads the target stage's IsClosed/Unattended and sets
// close_date/last_stage_update/unattended accordingly. currentIDs is
// nil on create (no prior stage to compare against).
func applyStageTransitionFields(ctx context.Context, working orm.RecordMap, currentIDs []string) error {
	stageID, ok := int64FromAny(working["stage_id"])
	if !ok || stageID <= 0 {
		return nil
	}
	stageRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(HelpdeskStageModelName, nil), []string{fmt.Sprint(stageID)}, []string{"is_closed", "unattended"})
	if err != nil {
		return err
	}
	stageRow, err := firstRow(stageRows, "tbl.helpdesk_stage")
	if err != nil {
		return err
	}
	isClosed := boolValue(stageRow["is_closed"])
	now := time.Now().UTC()
	working["last_stage_update"] = now
	working["unattended"] = boolValue(stageRow["unattended"])
	if isClosed {
		working["close_date"] = now
	} else {
		// Clear close_date when moving back out of a closed stage, unless the
		// caller is explicitly setting a different close_date value itself.
		if !hasRecordMapKey(working, "close_date") {
			working["close_date"] = nil
		}
	}
	_ = currentIDs
	return nil
}

// openTicketSQL is the SQL predicate for "ticket is not closed": its stage is
// not flagged is_closed (an unstaged ticket counts as open). ticketAlias is
// the ticket table's name/alias in the enclosing query.
func openTicketSQL(ticketAlias string) string {
	return "COALESCE((SELECT s.is_closed FROM tbl_helpdesk_stage AS s WHERE s.id = " + ticketAlias + ".stage_id), false) = false"
}

// computeDuplicateCount is the stored:false compute handler for
// DuplicateCount — counts, per requested record id, how many other tickets
// point duplicate_id at it.
func computeDuplicateCount(ctx context.Context, records orm.RecordSet, _ orm.FieldMetadata) (map[string]any, error) {
	type row struct {
		DuplicateID int64 `bun:"duplicate_id"`
		Count       int   `bun:"count"`
	}
	var rows []row
	db := orm.BunDBFromContext(ctx)
	idsAsAny := make([]any, 0, len(records.IDs()))
	for _, id := range records.IDs() {
		if value, ok := int64FromAny(id); ok {
			idsAsAny = append(idsAsAny, value)
		}
	}
	if len(idsAsAny) == 0 {
		return map[string]any{}, nil
	}
	if err := db.NewSelect().
		Table("tbl_helpdesk_ticket").
		Column("duplicate_id").
		ColumnExpr("COUNT(*) AS count").
		Where("duplicate_id IN (?)", bun.In(idsAsAny)).
		Group("duplicate_id").
		Scan(ctx, &rows); err != nil {
		return nil, err
	}
	out := make(map[string]any, len(rows))
	for _, r := range rows {
		out[fmt.Sprint(r.DuplicateID)] = r.Count
	}
	return out, nil
}
