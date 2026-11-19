package models

// File overview:
// - Responsibility: implements the §9.3 symmetric related-ticket write hook
//   — when related_ticket_ids changes on ticket A, mirrors the change onto
//   every affected ticket B via raw pivot-table writes, so the link always
//   reads bidirectionally. Ownership: helpdesk module. Audit-sensitivity: low.
// - Key symbols: `syncRelatedTicketSymmetry`.
// - Pattern: raw Bun against the pivot table (tbl_helpdesk_ticket_relationship_rel),
//   the same "raw Bun when the ORM path can't express it" escape hatch
//   tbl_group.go's m2m helpers use — deliberately not a second Write() call
//   on tbl.helpdesk_ticket, to avoid recursive hook re-entry (plan §9.3).

import (
	"context"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

const helpdeskTicketRelationshipRelTable = "tbl_helpdesk_ticket_relationship_rel"

// syncRelatedTicketSymmetry compares ticketID's oldRelatedIDs (read by the
// caller before its own Write overwrote the pivot) against newRelatedIDs and
// mirrors every add/remove onto the other side, so B always lists A back
// whenever A lists B.
func syncRelatedTicketSymmetry(ctx context.Context, ticketID int64, currentIDs, newRelatedIDs []int64) {
	db := orm.BunDBFromContext(ctx)
	if db == nil {
		return
	}

	currentSet := make(map[int64]bool, len(currentIDs))
	for _, id := range currentIDs {
		currentSet[id] = true
	}
	newSet := make(map[int64]bool, len(newRelatedIDs))
	for _, id := range newRelatedIDs {
		newSet[id] = true
	}

	// Added links: ensure ticketID is present on the other side's own list.
	for _, otherID := range newRelatedIDs {
		if !currentSet[otherID] {
			if err := ensureRelatedTicketPair(ctx, ticketID, otherID); err != nil {
				log.Warnf("tbl.helpdesk_ticket %d: failed to mirror related link to %d: %v", ticketID, otherID, err)
			}
		}
	}
	// Removed links: ensure ticketID is dropped from the other side's list.
	for _, otherID := range currentIDs {
		if !newSet[otherID] {
			if err := removeRelatedTicketPair(ctx, ticketID, otherID); err != nil {
				log.Warnf("tbl.helpdesk_ticket %d: failed to unmirror related link from %d: %v", ticketID, otherID, err)
			}
		}
	}
}

// readRelatedTicketIDs reads every pivot row touching ticketID on either
// column, returning the other side's id for each.
func readRelatedTicketIDs(ctx context.Context, ticketID int64) ([]int64, error) {
	var rows []struct {
		TicketID1 int64 `bun:"ticket_id1"`
		TicketID2 int64 `bun:"ticket_id2"`
	}
	db := orm.BunDBFromContext(ctx)
	if err := db.NewSelect().
		Table(helpdeskTicketRelationshipRelTable).
		Column("ticket_id1", "ticket_id2").
		Where("ticket_id1 = ? OR ticket_id2 = ?", ticketID, ticketID).
		Scan(ctx, &rows); err != nil {
		return nil, err
	}
	out := make([]int64, 0, len(rows))
	for _, row := range rows {
		if row.TicketID1 == ticketID {
			out = append(out, row.TicketID2)
		} else {
			out = append(out, row.TicketID1)
		}
	}
	return out, nil
}

// ensureRelatedTicketPair inserts (ticketID, otherID) into the pivot table
// on the "otherID's own related_ticket_ids" side (ticket_id1=otherID,
// ticket_id2=ticketID) if not already present — mirrors A's link onto B
// without touching A's own row (already written by the caller's Write).
func ensureRelatedTicketPair(ctx context.Context, ticketID, otherID int64) error {
	db := orm.BunDBFromContext(ctx)
	exists, err := db.NewSelect().
		Table(helpdeskTicketRelationshipRelTable).
		Where("(ticket_id1 = ? AND ticket_id2 = ?) OR (ticket_id1 = ? AND ticket_id2 = ?)", otherID, ticketID, ticketID, otherID).
		Exists(ctx)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = db.NewInsert().
		Table(helpdeskTicketRelationshipRelTable).
		Value("ticket_id1", "?", otherID).
		Value("ticket_id2", "?", ticketID).
		Exec(ctx)
	return err
}

// removeRelatedTicketPair deletes any pivot row pairing ticketID and
// otherID, in either column order.
func removeRelatedTicketPair(ctx context.Context, ticketID, otherID int64) error {
	db := orm.BunDBFromContext(ctx)
	_, err := db.NewDelete().
		Table(helpdeskTicketRelationshipRelTable).
		Where("(ticket_id1 = ? AND ticket_id2 = ?) OR (ticket_id1 = ? AND ticket_id2 = ?)", otherID, ticketID, ticketID, otherID).
		Exec(ctx)
	return err
}
