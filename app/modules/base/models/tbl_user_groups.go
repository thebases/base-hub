package models

// File overview:
// - Responsibility: res.users group membership parsing and lookup.
// - Key symbols: `hasGroupsResUsers`, `hasGroupResUsers`, `parseResUsersGroupSpec`, `parseResUsersMembership`.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"github.com/uptrace/bun"
)

func hasGroupsResUsers(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	spec := firstNonEmpty(strings.TrimSpace(stringArg(args, 0)), strings.TrimSpace(stringValue(kwargs["groups"])))
	if spec == "" {
		return false, nil
	}
	groupSpec, ok, err := parseResUsersMembership(ctx, ids, spec)
	if err != nil {
		return nil, err
	}
	if !ok {
		return false, nil
	}
	return len(groupSpec) > 0, nil
}

func hasGroupResUsers(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	return hasGroupsResUsers(ctx, model, ids, args, kwargs)
}

// parseResUsersGroupSpec parses the res users group spec handled by this package.
func parseResUsersGroupSpec(spec string) ([]string, []string) {
	pieces := strings.Split(spec, ",")
	positive := make([]string, 0, len(pieces))
	negative := make([]string, 0, len(pieces))
	for _, piece := range pieces {
		piece = strings.TrimSpace(piece)
		if piece == "" {
			continue
		}
		if strings.HasPrefix(piece, "!") {
			negative = append(negative, strings.TrimSpace(strings.TrimPrefix(piece, "!")))
			continue
		}
		positive = append(positive, piece)
	}
	return positive, negative
}

// parseResUsersMembership parses the res users membership handled by this package.
func parseResUsersMembership(ctx context.Context, ids []string, spec string) (map[string]struct{}, bool, error) {
	positive, negative := parseResUsersGroupSpec(spec)
	if len(ids) == 0 {
		env := orm.EnvironmentFromContext(ctx)
		if env != nil && env.UserID() > 0 {
			ids = []string{fmt.Sprintf("%d", env.UserID())}
		}
	}
	if len(ids) == 0 {
		return nil, false, nil
	}
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(ResUserModelName, nil), ids, []string{"group_id"})
	if err != nil {
		return nil, false, err
	}
	bidGroups, err := lookupGroupBIDs(ctx, resUsersGroupMembership(rows))
	if err != nil {
		return nil, false, err
	}
	for _, bidValue := range negative {
		if _, ok := bidGroups[bidValue]; ok {
			return nil, false, nil
		}
	}
	for _, bidValue := range positive {
		if _, ok := bidGroups[bidValue]; !ok {
			return nil, false, nil
		}
	}
	return bidGroups, true, nil
}

// resUsersGroupMembership collects the primary group ids of the read user rows. group_id is a
// many2one, so a runtime read returns it as an [id, name] tuple.
func resUsersGroupMembership(rows []orm.RecordMap) map[string]struct{} {
	membership := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if groupID, ok := many2oneID(row["group_id"]); ok {
			membership[fmt.Sprintf("%d", groupID)] = struct{}{}
		}
	}
	return membership
}

// lookupGroupBIDs looks up the group bids handled by this package.
func lookupGroupBIDs(ctx context.Context, membership map[string]struct{}) (map[string]struct{}, error) {
	if len(membership) == 0 {
		return map[string]struct{}{}, nil
	}
	ids := make([]string, 0, len(membership))
	for id := range membership {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	db, err := runtimeDBForModel(ctx)
	if err != nil {
		return nil, err
	}
	rows := []ResGroupRecord{}
	if err := db.NewSelect().
		Model(&rows).
		Column("id", "bid").
		Where("id IN (?)", bun.In(idsToAny(ids))).
		Scan(ctx); err != nil {
		return nil, err
	}
	out := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		bidValue := strings.TrimSpace(row.BID)
		if bidValue != "" {
			out[bidValue] = struct{}{}
		}
	}
	return out, nil
}
