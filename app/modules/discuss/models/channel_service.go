package models

// File overview:
// - Responsibility: implements Phase 2's channel/chat business logic —
//   creating channels and memberships, the get-or-create 1:1
//   dedup lookup, join/leave/seen/mute membership mutations, and posting a
//   channel-scoped message. Ownership: discuss module. Audit-sensitivity:
//   low (no balance/payment mutation), same tier as the rest of this module.
// - Key symbols: `CreateChannel`, `GetOrCreateChat`, `JoinChannel`,
//   `LeaveChannel`, `MarkSeen`, `SetMute`, `PostChannelMessage`.
// - Pattern: same-package model writes (tbl.discuss_channel,
//   tbl.discuss_channel_member) go through `orm.RuntimeRepository*` (not `Handler[T].Create`, for
//   the same ctx-preservation reason documented in service.go). The 1:1 chat
//   dedup lookup is a direct raw-Bun GROUP BY/HAVING query — the "raw Bun
//   when the ORM read path can't express it cleanly" escape hatch service.go
//   already uses for follower-subtype membership. Every realtime event goes
//   through `orm.ExecuteRuntimeMethod(ctx, "base.service.bus", "publish", ...)`,
//   matching the exact call shape service.go's createNotificationAndPublish
//   uses — no new transport, per plan §8's hard requirement.

import (
	"context"
	"strconv"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// discussChannelMemberPairRow is a thin read-model over the channel-member
// table, used only by GetOrCreateChat's raw dedup query below.
type discussChannelMemberPairRow struct {
	ChannelID int64 `bun:"channel_id"`
}

// CreateChannel creates one tbl.discuss_channel row plus one
// tbl.discuss_channel_member row per requested member id, returning the new channel's id.
func CreateChannel(ctx context.Context, name string, channelType string, description *string, memberUserIDs []int64) (int64, error) {
	values := orm.RecordMap{
		"name": name,
	}
	if channelType != "" {
		values["channel_type"] = channelType
	}
	if description != nil {
		values["description"] = *description
	}

	created, err := orm.RuntimeRepositoryCreate(ctx, orm.RuntimeModelRef(DiscussChannelModelName, nil), values)
	if err != nil {
		return 0, err
	}
	channelID := int64Value(created["id"])
	if channelID <= 0 {
		log.Errorf("tbl.discuss_channel: create did not return an inserted id")
		return 0, nil
	}

	// Walk each requested member in order, creating one membership row per id.
	// A failed member insert only logs and continues, matching this module's
	// established best-effort fan-out convention for secondary writes.
	for _, memberUserID := range memberUserIDs {
		if _, err := orm.RuntimeRepositoryCreate(ctx, orm.RuntimeModelRef(DiscussChannelMemberModelName, nil), orm.RecordMap{
			"channel_id": channelID,
			"user_id":    memberUserID,
		}); err != nil {
			log.Warnf("tbl.discuss_channel %d: failed to add member %d: %v", channelID, memberUserID, err)
		}
	}
	return channelID, nil
}

// GetOrCreateChat returns the id of the existing 1:1 chat-type channel whose
// exact member set is {userAID, userBID}, creating one if none exists yet
// (1:1 chat dedup).
func GetOrCreateChat(ctx context.Context, userAID, userBID int64) (int64, error) {
	existingChannelID, found, err := findExistingChatChannel(ctx, userAID, userBID)
	if err != nil {
		return 0, err
	}
	if found {
		return existingChannelID, nil
	}
	return CreateChannel(ctx, "", "chat", nil, []int64{userAID, userBID})
}

// findExistingChatChannel looks up a chat-type channel whose member set is
// exactly {userAID, userBID} via a direct join-table GROUP BY/HAVING query —
// the generic ORM read path has no clean way to express "exactly these two
// members and no others", so this reaches for raw Bun instead.
func findExistingChatChannel(ctx context.Context, userAID, userBID int64) (int64, bool, error) {
	rows := []discussChannelMemberPairRow{}
	// HAVING COUNT(*) = 2 confirms the channel has exactly two members total;
	// the FILTER count confirms both requested users are among them, so
	// together they pin down "exactly this pair, no one else".
	if err := orm.BunDBFromContext(ctx).NewRaw(`
		SELECT dcm.channel_id AS channel_id
		FROM discuss_channel_member AS dcm
		JOIN discuss_channel AS dc ON dc.id = dcm.channel_id
		WHERE dc.channel_type = ?
		GROUP BY dcm.channel_id
		HAVING COUNT(*) = 2
		   AND COUNT(*) FILTER (WHERE dcm.user_id IN (?, ?)) = 2
		LIMIT 1
	`, "chat", userAID, userBID).Scan(ctx, &rows); err != nil {
		return 0, false, err
	}
	if len(rows) == 0 {
		return 0, false, nil
	}
	return rows[0].ChannelID, true, nil
}

// JoinChannel creates a tbl.discuss_channel_member row for (channelID, userID)
// if one doesn't already exist, then publishes a "member_joined" event.
func JoinChannel(ctx context.Context, channelID, userID int64) error {
	_, found, err := findChannelMemberID(ctx, channelID, userID)
	if err != nil {
		return err
	}
	if found {
		return nil
	}
	if _, err := orm.RuntimeRepositoryCreate(ctx, orm.RuntimeModelRef(DiscussChannelMemberModelName, nil), orm.RecordMap{
		"channel_id": channelID,
		"user_id":    userID,
	}); err != nil {
		return err
	}

	publishChannelEvent(ctx, channelID, "member_joined", map[string]any{
		"channel_id": channelID,
		"user_id":    userID,
	})
	return nil
}

// LeaveChannel deletes the matching tbl.discuss_channel_member row, if any, then
// publishes a "member_left" event.
func LeaveChannel(ctx context.Context, channelID, userID int64) error {
	existingID, found, err := findChannelMemberID(ctx, channelID, userID)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if _, err := orm.RuntimeRepositoryUnlink(ctx, orm.RuntimeModelRef(DiscussChannelMemberModelName, nil), []string{strconv.FormatInt(existingID, 10)}); err != nil {
		return err
	}

	publishChannelEvent(ctx, channelID, "member_left", map[string]any{
		"channel_id": channelID,
		"user_id":    userID,
	})
	return nil
}

// MarkSeen advances a member's LastSeenMessageID (read receipt), then
// publishes a "seen_updated" event.
func MarkSeen(ctx context.Context, channelID, userID, messageID int64) error {
	existingID, found, err := findChannelMemberID(ctx, channelID, userID)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if _, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(DiscussChannelMemberModelName, nil), []string{strconv.FormatInt(existingID, 10)}, orm.RecordMap{
		"last_seen_message_id": messageID,
	}); err != nil {
		return err
	}

	publishChannelEvent(ctx, channelID, "seen_updated", map[string]any{
		"channel_id": channelID,
		"user_id":    userID,
		"message_id": messageID,
	})
	return nil
}

// SetMute updates a member's MuteUntil, then publishes a "seen_updated"
// event — plan §8 groups mute-state changes with the other per-member state
// changes ("joining/leaving a channel, LastSeenMessageID advancing (read
// receipts), and mute-state changes are each their own base.service.bus.
// publish call ... event_type: member_joined / member_left / seen_updated"),
// naming only these three event types for the four listed state changes, so
// mute-state changes reuse "seen_updated" rather than introducing a fourth,
// unnamed event type.
func SetMute(ctx context.Context, channelID, userID int64, muteUntil *time.Time) error {
	existingID, found, err := findChannelMemberID(ctx, channelID, userID)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	writeValues := orm.RecordMap{
		"mute_until": nil,
	}
	if muteUntil != nil {
		writeValues["mute_until"] = *muteUntil
	}
	if _, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(DiscussChannelMemberModelName, nil), []string{strconv.FormatInt(existingID, 10)}, writeValues); err != nil {
		return err
	}

	publishChannelEvent(ctx, channelID, "seen_updated", map[string]any{
		"channel_id": channelID,
		"user_id":    userID,
		"mute_until": muteUntil,
	})
	return nil
}

// PostChannelMessage posts a tbl.discuss_message row against this channel via
// Phase 1's PostMessage (reusing the chatter core, no separate message
// table), then additionally publishes a "message_created" event on the
// channel's own bus topic — in addition to whatever PostMessage's own
// follower-based fan-out already does internally (the followers table is
// typically empty for a channel-scoped message, so this is additive, not a
// replacement).
func PostChannelMessage(ctx context.Context, channelID int64, authorID *int64, body *string) (int64, error) {
	messageID, err := PostMessage(ctx, DiscussChannelModelName, channelID, authorID, nil, "comment", nil, body, nil, nil, nil)
	if err != nil {
		return 0, err
	}
	if messageID <= 0 {
		return messageID, nil
	}

	payload := map[string]any{
		"channel_id": channelID,
		"message_id": messageID,
		"author_id":  authorID,
		"body":       body,
	}
	publishChannelEvent(ctx, channelID, "message_created", payload)
	return messageID, nil
}

// publishChannelEvent publishes one event on a channel's base-bus topic.
// Publish failure only logs a warning (best-effort realtime), matching
// service.go's createNotificationAndPublish convention.
func publishChannelEvent(ctx context.Context, channelID int64, eventType string, payload map[string]any) {
	channels := []string{"discuss:channel:" + strconv.FormatInt(channelID, 10)}
	if _, err := orm.ExecuteRuntimeMethod(ctx, "base.service.bus", "publish", nil, []any{channels, eventType, payload}, nil); err != nil {
		log.Warnf("tbl.discuss_channel %d: base.service.bus.publish(%s) failed: %v", channelID, eventType, err)
	}
}

// findChannelMemberID looks up the tbl.discuss_channel_member row id for one
// (channelID, userID) pair, if any.
func findChannelMemberID(ctx context.Context, channelID, userID int64) (int64, bool, error) {
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(DiscussChannelMemberModelName, nil), orm.RecordMap{
		"channel_id": channelID,
		"user_id":    userID,
	}, []string{"id"})
	if err != nil {
		return 0, false, err
	}
	if len(rows) == 0 {
		return 0, false, nil
	}
	return int64Value(rows[0]["id"]), true, nil
}
