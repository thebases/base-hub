package models

// File overview:
// - Responsibility: implements Phase 3's WebRTC call lifecycle and signaling
//   relay — starting/ending a member's session, updating its mute/camera/
//   screen state, and relaying SDP/ICE payloads between peers. Ownership:
//   discuss module. Audit-sensitivity: low.
// - Key symbols: `StartRtcSession`, `EndRtcSession`, `UpdateRtcSessionState`,
//   `RelaySignal`.
// - Pattern: this module has no TURN/STUN server and no media transport of
//   its own — it only persists session/call rows and relays opaque signaling
//   payloads through base-bus (`orm.ExecuteRuntimeMethod(ctx,
//   "base.service.bus", "publish", ...)`, the exact call shape every other
//   file in this module already uses), per the plan's hard base-bus-only
//   delivery requirement. Lifecycle events (start/end/state-changed)
//   broadcast on the channel's own topic (discuss:channel:<id>), matching
//   Phase 2's membership events; point-to-point signaling (offer/answer/ICE
//   candidate) targets the recipient's own topic (discuss:user:<id>),
//   matching Phase 1's per-user notification channel — reusing both
//   already-established topic conventions rather than inventing a third.

import (
	"context"
	"strconv"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// StartRtcSession creates a tbl.discuss_channel_rtc_session row for (channelID,
// userID) — the caller must already be a channel member, matching
// JoinChannel's own precondition. If this is the first active session in the
// channel, it also opens a tbl.discuss_call_history row and posts a "call
// started" system message. Publishes "rtc_session_started" on the channel's
// topic either way.
func StartRtcSession(ctx context.Context, channelID, userID int64) (int64, error) {
	memberID, found, err := findChannelMemberID(ctx, channelID, userID)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, orm.Forbiddenf("tbl.discuss_channel %d: user %d is not a member, cannot start an rtc session", channelID, userID)
	}

	wasEmpty, err := channelHasNoActiveSessions(ctx, channelID)
	if err != nil {
		return 0, err
	}

	created, err := orm.RuntimeRepositoryCreate(ctx, orm.RuntimeModelRef(DiscussChannelRTCSessionModelName, nil), orm.RecordMap{
		"channel_member_id": memberID,
	})
	if err != nil {
		return 0, err
	}
	sessionID := int64Value(created["id"])

	if wasEmpty {
		startCallHistory(ctx, channelID, userID)
	}

	publishChannelEvent(ctx, channelID, "rtc_session_started", map[string]any{
		"channel_id": channelID,
		"user_id":    userID,
		"session_id": sessionID,
	})
	return sessionID, nil
}

// channelHasNoActiveSessions reports whether no tbl.discuss_channel_rtc_session
// row currently exists for any member of channelID — used to decide whether
// a newly-starting session is the one that opens the call history record.
func channelHasNoActiveSessions(ctx context.Context, channelID int64) (bool, error) {
	rows := []discussChannelMemberPairRow{}
	if err := orm.BunDBFromContext(ctx).NewRaw(`
		SELECT dcm.channel_id AS channel_id
		FROM discuss_channel_rtc_session AS s
		JOIN discuss_channel_member AS dcm ON dcm.id = s.channel_member_id
		WHERE dcm.channel_id = ?
		LIMIT 1
	`, channelID).Scan(ctx, &rows); err != nil {
		return false, err
	}
	return len(rows) == 0, nil
}

// startCallHistory opens a tbl.discuss_call_history row for channelID and posts
// a system message announcing the call, linking the two.
func startCallHistory(ctx context.Context, channelID, userID int64) {
	body := "Call started"
	messageID, err := PostMessage(ctx, DiscussChannelModelName, channelID, &userID, nil, "notification", nil, &body, nil, nil, nil)
	if err != nil {
		log.Warnf("tbl.discuss_channel %d: failed to post call-started message: %v", channelID, err)
	}

	values := orm.RecordMap{
		"channel_id": channelID,
		"started_at": time.Now().UTC(),
	}
	if messageID > 0 {
		values["start_call_message_id"] = messageID
	}
	if _, err := orm.RuntimeRepositoryCreate(ctx, orm.RuntimeModelRef(DiscussCallHistoryModelName, nil), values); err != nil {
		log.Warnf("tbl.discuss_channel %d: failed to open call history: %v", channelID, err)
	}
}

// EndRtcSession deletes the caller's tbl.discuss_channel_rtc_session row, if
// any. If it was the last active session in the channel, it also closes the
// open tbl.discuss_call_history row. Publishes "rtc_session_ended" either way
// the row existed.
func EndRtcSession(ctx context.Context, channelID, userID int64) error {
	sessionID, found, err := findRtcSessionID(ctx, channelID, userID)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if _, err := orm.RuntimeRepositoryUnlink(ctx, orm.RuntimeModelRef(DiscussChannelRTCSessionModelName, nil), []string{strconv.FormatInt(sessionID, 10)}); err != nil {
		return err
	}

	isEmptyNow, err := channelHasNoActiveSessions(ctx, channelID)
	if err != nil {
		log.Warnf("tbl.discuss_channel %d: failed to check remaining rtc sessions: %v", channelID, err)
	} else if isEmptyNow {
		closeCallHistory(ctx, channelID)
	}

	publishChannelEvent(ctx, channelID, "rtc_session_ended", map[string]any{
		"channel_id": channelID,
		"user_id":    userID,
	})
	return nil
}

// closeCallHistory stamps EndedAt on the still-open (ended_at IS NULL)
// tbl.discuss_call_history row(s) for channelID. A generic RecordMap equality
// filter can't express "IS NULL" (RuntimeSearchRead compiles a nil filter
// value to `column = NULL`, which SQL NULL semantics make match nothing),
// so this reaches for raw Bun instead — the same escape hatch
// findExistingChatChannel/channelHasNoActiveSessions already use.
func closeCallHistory(ctx context.Context, channelID int64) {
	rows := []discussOpenCallHistoryRow{}
	if err := orm.BunDBFromContext(ctx).NewRaw(`
		SELECT id
		FROM discuss_call_history
		WHERE channel_id = ? AND ended_at IS NULL
	`, channelID).Scan(ctx, &rows); err != nil {
		log.Warnf("tbl.discuss_call_history: failed to find open call for channel %d: %v", channelID, err)
		return
	}
	for _, row := range rows {
		if _, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(DiscussCallHistoryModelName, nil), []string{strconv.FormatInt(row.ID, 10)}, orm.RecordMap{
			"ended_at": time.Now().UTC(),
		}); err != nil {
			log.Warnf("tbl.discuss_call_history %d: failed to close: %v", row.ID, err)
		}
	}
}

// discussOpenCallHistoryRow is a thin read-model over discuss_call_history,
// used only by closeCallHistory's raw "ended_at IS NULL" lookup.
type discussOpenCallHistoryRow struct {
	ID int64 `bun:"id"`
}

// UpdateRtcSessionState writes whichever of the four booleans the caller
// provides (nil = leave unchanged) and publishes "rtc_state_updated" with
// the resulting values.
func UpdateRtcSessionState(ctx context.Context, channelID, userID int64, isScreenSharingOn, isCameraOn, isMuted, isDeaf *bool) error {
	sessionID, found, err := findRtcSessionID(ctx, channelID, userID)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	writeValues := orm.RecordMap{}
	if isScreenSharingOn != nil {
		writeValues["is_screen_sharing_on"] = *isScreenSharingOn
	}
	if isCameraOn != nil {
		writeValues["is_camera_on"] = *isCameraOn
	}
	if isMuted != nil {
		writeValues["is_muted"] = *isMuted
	}
	if isDeaf != nil {
		writeValues["is_deaf"] = *isDeaf
	}
	if len(writeValues) == 0 {
		return nil
	}
	if _, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(DiscussChannelRTCSessionModelName, nil), []string{strconv.FormatInt(sessionID, 10)}, writeValues); err != nil {
		return err
	}

	payload := map[string]any{"channel_id": channelID, "user_id": userID}
	for key, value := range writeValues {
		payload[key] = value
	}
	publishChannelEvent(ctx, channelID, "rtc_state_updated", payload)
	return nil
}

// RelaySignal relays an opaque SDP/ICE signaling payload from one channel
// member to another over base-bus. This module never inspects or interprets
// payload — it is purely a relay, exactly like a WebRTC signaling server's
// job. toUserID targets the recipient's own per-user topic so only the
// intended peer receives it (broadcasting SDP/ICE to the whole channel would
// leak it to uninvolved members).
func RelaySignal(ctx context.Context, channelID, fromUserID, toUserID int64, signalType string, payload map[string]any) {
	envelope := map[string]any{
		"channel_id":   channelID,
		"from_user_id": fromUserID,
		"signal_type":  signalType,
		"payload":      payload,
	}
	channels := []string{"discuss:user:" + strconv.FormatInt(toUserID, 10)}
	if _, err := orm.ExecuteRuntimeMethod(ctx, "base.service.bus", "publish", nil, []any{channels, "rtc_signal", envelope}, nil); err != nil {
		log.Warnf("tbl.discuss_channel %d: failed to relay rtc signal from %d to %d: %v", channelID, fromUserID, toUserID, err)
	}
}

// findRtcSessionID looks up the tbl.discuss_channel_rtc_session row id for one
// (channelID, userID) pair, if any.
func findRtcSessionID(ctx context.Context, channelID, userID int64) (int64, bool, error) {
	memberID, found, err := findChannelMemberID(ctx, channelID, userID)
	if err != nil {
		return 0, false, err
	}
	if !found {
		return 0, false, nil
	}
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(DiscussChannelRTCSessionModelName, nil), orm.RecordMap{
		"channel_member_id": memberID,
	}, []string{"id"})
	if err != nil {
		return 0, false, err
	}
	if len(rows) == 0 {
		return 0, false, nil
	}
	return int64Value(rows[0]["id"]), true, nil
}
