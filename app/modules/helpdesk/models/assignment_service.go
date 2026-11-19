package models

// File overview:
// - Responsibility: implements the team AssignMethod picker (plan §4) —
//   manual (no-op), random (round-robin among team members), balanced
//   (member with fewest currently-open tickets). Ownership: helpdesk module.
//   Audit-sensitivity: low.
// - Key symbols: `applyAssignmentPick`, `pickRandomAssignee`, `pickBalancedAssignee`.

import (
	"context"
	"fmt"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"github.com/uptrace/bun"
)

// applyAssignmentPick mutates working["assignee_id"]/working["assign_date"]
// in place based on working["team_id"]'s AssignMethod, when a team is set
// and no assignee was already supplied. It is a no-op for "manual" or when
// no team is set.
func applyAssignmentPick(ctx context.Context, working orm.RecordMap) error {
	teamID, ok := int64FromAny(working["team_id"])
	if !ok || teamID <= 0 {
		return nil
	}
	teamRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(HelpdeskTeamModelName, nil), []string{fmt.Sprint(teamID)}, []string{"assign_method", "member_ids"})
	if err != nil {
		return err
	}
	teamRow, err := firstRow(teamRows, "tbl.helpdesk_team")
	if err != nil {
		return err
	}
	method := stringValue(teamRow["assign_method"])
	memberIDs := int64SliceFromAny(teamRow["member_ids"])
	if method == "manual" || method == "" || len(memberIDs) == 0 {
		return nil
	}

	var assigneeID int64
	switch method {
	case "random":
		assigneeID, err = pickRandomAssignee(ctx, memberIDs)
	case "balanced":
		assigneeID, err = pickBalancedAssignee(ctx, memberIDs)
	}
	if err != nil {
		return err
	}
	if assigneeID > 0 {
		working["assignee_id"] = assigneeID
		working["assign_date"] = time.Now().UTC()
	}
	return nil
}

// pickRandomAssignee picks uniformly among memberIDs via a real SQL
// ORDER BY random() LIMIT 1 (plan §4's "ORDER BY random() among members"
// option) rather than a time-seeded Go index, so the pick is genuinely
// uniform and not biased by call timing.
func pickRandomAssignee(ctx context.Context, memberIDs []int64) (int64, error) {
	if len(memberIDs) == 0 {
		return 0, nil
	}
	// bun's pgdialect formats a Go []int64 arg as a native Postgres array
	// literal (e.g. '{1,2,3}'), so it can be unnested directly — no manual
	// IN-list/placeholder construction needed.
	db := orm.BunDBFromContext(ctx)
	var picked int64
	if err := db.NewSelect().
		ColumnExpr("member_id").
		TableExpr("unnest(?::bigint[]) AS member_id", memberIDs).
		OrderExpr("random()").
		Limit(1).
		Scan(ctx, &picked); err != nil {
		return 0, err
	}
	return picked, nil
}

// pickBalancedAssignee picks the member with the fewest currently-open
// tickets (stage is not closed).
func pickBalancedAssignee(ctx context.Context, memberIDs []int64) (int64, error) {
	type row struct {
		AssigneeID int64 `bun:"assignee_id"`
		OpenCount  int   `bun:"open_count"`
	}
	var rows []row
	idsAsAny := make([]any, 0, len(memberIDs))
	for _, id := range memberIDs {
		idsAsAny = append(idsAsAny, id)
	}
	db := orm.BunDBFromContext(ctx)
	if err := db.NewSelect().
		Table("tbl_helpdesk_ticket").
		Column("assignee_id").
		ColumnExpr("COUNT(*) AS open_count").
		Where("assignee_id IN (?)", bun.In(idsAsAny)).
		Where(openTicketSQL("tbl_helpdesk_ticket")).
		Group("assignee_id").
		Scan(ctx, &rows); err != nil {
		return 0, err
	}
	openCountByMember := make(map[int64]int, len(memberIDs))
	for _, id := range memberIDs {
		openCountByMember[id] = 0
	}
	for _, r := range rows {
		openCountByMember[r.AssigneeID] = r.OpenCount
	}
	var best int64
	bestCount := -1
	for _, id := range memberIDs {
		count := openCountByMember[id]
		if bestCount == -1 || count < bestCount {
			best = id
			bestCount = count
		}
	}
	return best, nil
}
