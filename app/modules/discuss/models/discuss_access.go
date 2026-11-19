package models

// Access control for chatter messages and followers (plan T23).
//
// Messages and followers are polymorphic: a row points at (res_model, res_id) on any model, so
// their access follows the target record instead of a flat ACL row.
//   - Read rule on tbl.discuss_message: a non-sudo RPC caller sees a message when they wrote it,
//     when they can read its target record (target ACL + target read record rules), or, for
//     channel messages, when they are a member of the channel.
//   - post_message / subscribe / unsubscribe check the target record: read access to post or to
//     (un)follow yourself, write access to (un)subscribe someone else, membership for channels.
//     post_message always authors as the caller.
//   - Raw create/write/unlink on both models are base.group_system only for RPC callers: the
//     chatter goes through the methods above, which keep fan-out and the checks; raw writes would
//     allow author spoofing and editing other people's messages.
// Sudo and internal callers (nil env or no RPC auth mode: inbound mail, activity/channel/rtc
// services, seeds) are not restricted, matching the kernel ACL population.

import (
	"context"
	"slices"
	"strconv"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

const discussManagerGroupBID = "base.group_system"

// Test seams over kernel helpers that need the auth database.
var (
	discussCallerGroupBIDs  = orm.CallerGroupBIDs
	discussTargetAccess     = orm.EnsureRecordAccess
	discussMessageResModels = distinctDiscussMessageResModels
	discussMemberChannelIDs = memberChannelIDsForUser
	discussTargetReadScope  = discussTargetReadDomain
)

func init() {
	orm.RegisterRecordRule(DiscussMessageModelName, discussMessageRecordRule)
}

// discussRestrictedCaller returns the caller's user id when ctx is a non-sudo RPC caller.
func discussRestrictedCaller(ctx context.Context) (int64, bool) {
	env := orm.EnvironmentFromContext(ctx)
	if env == nil || env.IsSudo() {
		return 0, false
	}
	if _, ok := env.ContextValue(orm.RBACContextAuthModeKey); !ok {
		return 0, false
	}
	return env.UserID(), true
}

// ensureDiscussTargetAccess checks a restricted caller's access to the record a message or
// follower points at. operation is "read" or "write"; channels require membership for both.
func ensureDiscussTargetAccess(ctx context.Context, resModel string, resID int64, operation string) error {
	userID, restricted := discussRestrictedCaller(ctx)
	if !restricted {
		return nil
	}
	if resModel == DiscussChannelModelName {
		_, member, err := discussChannelMemberLookup(ctx, resID, userID)
		if err != nil {
			return err
		}
		if !member {
			return orm.Forbiddenf("permission denied: not a member of channel %d", resID)
		}
		return nil
	}
	if err := discussTargetAccess(ctx, resModel, operation, []string{strconv.FormatInt(resID, 10)}); err != nil {
		return orm.Forbiddenf("permission denied: cannot %s %s %d", operation, resModel, resID)
	}
	return nil
}

// ensureFollowerChangeAllowed: following or unfollowing yourself needs read access to the target;
// changing another user's subscription needs write access (plan T23).
func ensureFollowerChangeAllowed(ctx context.Context, resModel string, resID, followerID int64) error {
	userID, restricted := discussRestrictedCaller(ctx)
	if !restricted {
		return nil
	}
	operation := "read"
	if followerID != userID {
		operation = "write"
	}
	return ensureDiscussTargetAccess(ctx, resModel, resID, operation)
}

// guardedDiscussCRUD wraps the kernel create/write/unlink for model so RPC callers outside
// base.group_system cannot use them; everyone else gets the kernel default unchanged.
func guardedDiscussCRUD(modelName string) []orm.RuntimeMethod {
	methods := make([]orm.RuntimeMethod, 0, 3)
	for _, name := range []string{"create", "write", "unlink"} {
		base, ok := orm.LookupRuntimeMethod(name)
		if !ok || base.Call == nil {
			panic("discuss models: default runtime method " + name + " is not registered")
		}
		call, operation := base.Call, name
		base.Call = func(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
			if _, restricted := discussRestrictedCaller(ctx); restricted {
				groups, err := discussCallerGroupBIDs(ctx)
				if err != nil {
					return nil, err
				}
				if !slices.Contains(groups, discussManagerGroupBID) {
					return nil, orm.Forbiddenf("permission denied: use the chatter methods instead of raw %s on %s", operation, modelName)
				}
			}
			return call(ctx, model, ids, args, kwargs)
		}
		methods = append(methods, base)
	}
	return methods
}

// discussMessageRecordRule scopes messages to what a restricted caller may see (file header).
func discussMessageRecordRule(ctx context.Context, env *orm.Environment, _ *orm.ModelMetadata, _ string) (orm.DomainNode, error) {
	ctx = orm.ContextWithEnvironment(ctx, env)
	userID, restricted := discussRestrictedCaller(ctx)
	if !restricted {
		return orm.TrueDomain(), nil
	}
	visible := []orm.DomainNode{orm.Where("author_id", "=", userID)}
	resModels, err := discussMessageResModels(ctx)
	if err != nil {
		return orm.DomainNode{}, err
	}
	for _, resModel := range resModels {
		if resModel == DiscussMessageModelName {
			continue // a message cannot be the chatter target of another message
		}
		scope, ok, err := discussTargetReadScope(ctx, userID, resModel)
		if err != nil {
			return orm.DomainNode{}, err
		}
		if ok {
			visible = append(visible, scope)
		}
	}
	return orm.Or(visible...), nil
}

// discussTargetReadDomain returns the message domain for resModel targets the caller may read,
// and false when none are readable.
func discussTargetReadDomain(ctx context.Context, userID int64, resModel string) (orm.DomainNode, bool, error) {
	if resModel == DiscussChannelModelName {
		ids, err := discussMemberChannelIDs(ctx, userID)
		if err != nil || len(ids) == 0 {
			return orm.DomainNode{}, false, err
		}
		return orm.And(orm.Where("res_model", "=", resModel), orm.Where("res_id", "in", ids)), true, nil
	}
	meta, ok := orm.LookupModel(resModel)
	if !ok {
		return orm.DomainNode{}, false, nil
	}
	if err := orm.EnsureRuntimeModelAccess(ctx, meta, orm.RuntimeMethod{Name: "search", ReadOnly: true}, nil); err != nil {
		return orm.DomainNode{}, false, nil
	}
	rule, err := orm.RecordRuleDomainFor(ctx, resModel, "read")
	if err != nil {
		return orm.DomainNode{}, false, err
	}
	if rule.Kind == orm.DomainKindTrue {
		return orm.Where("res_model", "=", resModel), true, nil
	}
	// Restricted target: list the readable ids (the search applies the caller's rules).
	result, err := orm.ExecuteRuntimeMethod(ctx, resModel, "search", nil, []any{[]any{}}, nil)
	if err != nil {
		log.Warnf("discuss: message read rule could not scope %s for user %d: %v", resModel, userID, err)
		return orm.DomainNode{}, false, nil
	}
	ids := anyIDs(result)
	if len(ids) == 0 {
		return orm.DomainNode{}, false, nil
	}
	return orm.And(orm.Where("res_model", "=", resModel), orm.Where("res_id", "in", ids)), true, nil
}

func distinctDiscussMessageResModels(ctx context.Context) ([]string, error) {
	var models []string
	err := orm.BunDBFromContext(ctx).NewSelect().
		TableExpr("tbl_discuss_message").
		ColumnExpr("DISTINCT res_model").
		Scan(ctx, &models)
	return models, err
}

func memberChannelIDsForUser(ctx context.Context, userID int64) ([]any, error) {
	var ids []int64
	err := orm.BunDBFromContext(ctx).NewSelect().
		TableExpr("tbl_discuss_channel_member").
		ColumnExpr("channel_id").
		Where("user_id = ?", userID).
		Scan(ctx, &ids)
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out, err
}

// anyIDs flattens a search result ([]int64 / []any / []string) into domain "in" values.
func anyIDs(result any) []any {
	switch typed := result.(type) {
	case []any:
		return typed
	case []int64:
		out := make([]any, len(typed))
		for i, id := range typed {
			out[i] = id
		}
		return out
	case []string:
		out := make([]any, len(typed))
		for i, id := range typed {
			out[i] = id
		}
		return out
	default:
		return nil
	}
}
