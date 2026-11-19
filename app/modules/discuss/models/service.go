package models

// File overview:
// - Responsibility: implements the chatter business logic that isn't plain
//   CRUD — PostMessage (message_post equivalent), Subscribe, and Unsubscribe.
//   Ownership: discuss module. Audit-sensitivity: low (no balance/payment
//   mutation), but this is the audit trail other modules rely on, so writes
//   here must be durable before any best-effort realtime fan-out happens.
// - Key symbols: `PostMessage`, `Subscribe`, `Unsubscribe`.
// - Pattern: same-package model creates (tbl.discuss_message, tbl.discuss_followers,
//   tbl.discuss_notification) go through the shared `orm.RuntimeRepository*`
//   helpers (the same ctx-aware path createResPartner/createResCurrency use
//   from outside a RuntimeMethod callback) instead of `Handler[T].Create`,
//   because `Handler[T].Create` runs on `context.Background()` and drops the
//   caller's environment/session — RuntimeRepositoryCreate keeps this
//   request's ctx (transaction, RBAC, env cache) intact. The one genuinely
//   cross-module call (`base.service.bus.publish`) goes through
//   `orm.ExecuteRuntimeMethod`, matching the base-database notify pattern.

import (
	"context"
	"strconv"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"git.thebase.vn/base_hub/internal/utils/anyconv"
	"github.com/uptrace/bun"
)

// discussFollowerSubtypeRow is a thin read-model over the followers<->subtype
// many2many join table, used only to test subtype-restriction membership
// (the generic ORM read path returns many2many fields as [id, display] pairs,
// which is awkward to test membership against; a direct join-table read is
// the "raw Bun when the ORM path can't express it cleanly" case the module
// layering guide calls out).
type discussFollowerSubtypeRow struct {
	bun.BaseModel `bun:"table:tbl_discuss_followers_subtype_rel"`
	FollowerID    int64 `bun:"follower_id"`
	SubtypeID     int64 `bun:"subtype_id"`
}

// PostMessage creates one tbl.discuss_message row targeting (resModel, resID),
// then fans out one tbl.discuss_notification per matching follower plus every
// explicit recipientUserIDs entry (deduped against the follower set), and
// publishes a realtime event for each over base-bus. It returns the created
// message id. messageIDHeader is optional and only meaningful for messages
// that correspond to an actual email (outbound or inbound) — see
// inbound_router.go, which reads it back to match reply threads.
// recipientUserIDs is nil for every system-generated caller (activity
// notes, channel/rtc/inbound-email messages, guest posts) — only the
// human-facing "post_message" RuntimeMethod (runtimePostMessage) requires
// and passes it, since it's the caller enforcing the "send message needs an
// explicit receiver" chatter invariant.
func PostMessage(ctx context.Context, resModel string, resID int64, authorID *int64, subtypeID *int64, messageType string, subject, body *string, parentID *int64, messageIDHeader *string, recipientUserIDs []int64) (int64, error) {
	// Build only the fields the caller actually provided so unset optional
	// values fall back to each column's own DB default (e.g. message_type).
	values := orm.RecordMap{
		"res_model": resModel,
		"res_id":    resID,
	}
	if messageType != "" {
		values["message_type"] = messageType
	}
	if subject != nil {
		values["subject"] = *subject
	}
	if body != nil {
		values["body"] = *body
	}
	if subtypeID != nil {
		values["subtype_id"] = *subtypeID
	}
	if authorID != nil {
		values["author_id"] = *authorID
	}
	if parentID != nil {
		values["parent_id"] = *parentID
	}
	if messageIDHeader != nil {
		values["message_id_header"] = *messageIDHeader
	}

	created, err := orm.RuntimeRepositoryCreate(ctx, orm.RuntimeModelRef(DiscussMessageModelName, nil), values)
	if err != nil {
		return 0, err
	}
	messageID := int64Value(created["id"])
	if messageID <= 0 {
		log.Errorf("tbl.discuss_message: create did not return an inserted id")
		return 0, nil
	}

	// The message row is already durable at this point; auto-subscribe,
	// follower fan-out, and the realtime publish below are best-effort and
	// never unwind the create.
	subscribeRecipients(ctx, resModel, resID, recipientUserIDs)
	notifyFollowers(ctx, resModel, resID, subtypeID, messageID, authorID, subject, recipientUserIDs)
	return messageID, nil
}

// subscribeRecipients upserts a tbl.discuss_followers row for every explicitly
// addressed recipient of a "Send message" post, so naming someone in the
// composer's "To" field keeps them subscribed to the record going forward,
// not just notified of this one message. It is a no-op for callers that pass
// no recipients (notes, system-generated messages).
func subscribeRecipients(ctx context.Context, resModel string, resID int64, recipientUserIDs []int64) {
	for _, recipientUserID := range recipientUserIDs {
		if recipientUserID <= 0 {
			continue
		}
		if err := Subscribe(ctx, resModel, resID, recipientUserID, nil); err != nil {
			log.Warnf("tbl.discuss_message: failed to auto-subscribe recipient %d to %s/%d: %v", recipientUserID, resModel, resID, err)
		}
	}
}

// notifyFollowers loads every tbl.discuss_followers row on (resModel, resID),
// keeps the ones whose subtype restriction (if any) matches the message's
// subtype, unions that set with explicitRecipientUserIDs (deduped), then
// creates one tbl.discuss_notification per resulting user and publishes a
// realtime event for it. explicitRecipientUserIDs lets a message reach a
// specifically-addressed user who isn't (yet) a follower of the target
// record — the chatter "Send message" composer's required-receiver input.
func notifyFollowers(ctx context.Context, resModel string, resID int64, subtypeID *int64, messageID int64, authorID *int64, subject *string, explicitRecipientUserIDs []int64) {
	followerUserIDs, err := matchingFollowerUserIDs(ctx, resModel, resID, subtypeID)
	if err != nil {
		log.Warnf("tbl.discuss_message %d: failed to resolve followers for %s/%d: %v", messageID, resModel, resID, err)
		return
	}

	seen := make(map[int64]bool, len(followerUserIDs)+len(explicitRecipientUserIDs))
	recipients := make([]int64, 0, len(followerUserIDs)+len(explicitRecipientUserIDs))
	// Walk each source list in order (followers, then explicit recipients) so a
	// user named in both never gets notified twice for the same message.
	for _, userID := range followerUserIDs {
		if seen[userID] {
			continue
		}
		seen[userID] = true
		recipients = append(recipients, userID)
	}
	for _, userID := range explicitRecipientUserIDs {
		if userID <= 0 || seen[userID] {
			continue
		}
		seen[userID] = true
		recipients = append(recipients, userID)
	}

	// Walk each matching recipient in order so notification creation and the
	// realtime publish stay scoped to one recipient at a time.
	for _, recipientUserID := range recipients {
		createNotificationAndPublish(ctx, messageID, recipientUserID, resModel, resID, authorID, subject)
	}
}

// matchingFollowerUserIDs returns the user ids of every follower on
// (resModel, resID) whose subtype restriction, if any, includes subtypeID
// (or who has no subtype restriction at all).
func matchingFollowerUserIDs(ctx context.Context, resModel string, resID int64, subtypeID *int64) ([]int64, error) {
	followerRows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(DiscussFollowersModelName, nil), orm.RecordMap{
		"res_model": resModel,
		"res_id":    resID,
	}, []string{"id", "user_id"})
	if err != nil {
		return nil, err
	}
	if len(followerRows) == 0 {
		return nil, nil
	}

	followerIDToUserID := make(map[int64]int64, len(followerRows))
	followerIDs := make([]int64, 0, len(followerRows))
	for _, row := range followerRows {
		followerID := int64Value(row["id"])
		// user_id is a many2one: runtime reads return [id, display_name].
		followerIDToUserID[followerID], _ = anyconv.Many2OneID(row["user_id"])
		followerIDs = append(followerIDs, followerID)
	}

	restricted, matched, err := followerSubtypeRestrictions(ctx, followerIDs, subtypeID)
	if err != nil {
		return nil, err
	}

	result := make([]int64, 0, len(followerIDs))
	for _, followerID := range followerIDs {
		// A follower with no restriction rows at all always matches; a
		// restricted follower only matches when its restriction explicitly
		// includes this message's subtype.
		if !restricted[followerID] || matched[followerID] {
			result = append(result, followerIDToUserID[followerID])
		}
	}
	return result, nil
}

// followerSubtypeRestrictions reads the follower<->subtype join table once
// for every follower id, returning which followers have any restriction row
// at all and which have a restriction row matching subtypeID.
func followerSubtypeRestrictions(ctx context.Context, followerIDs []int64, subtypeID *int64) (map[int64]bool, map[int64]bool, error) {
	restricted := make(map[int64]bool, len(followerIDs))
	matched := make(map[int64]bool, len(followerIDs))
	if len(followerIDs) == 0 {
		return restricted, matched, nil
	}

	rows, err := followerSubtypeRows(ctx, followerIDs)
	if err != nil {
		return nil, nil, err
	}
	for _, row := range rows {
		restricted[row.FollowerID] = true
		if subtypeID != nil && row.SubtypeID == *subtypeID {
			matched[row.FollowerID] = true
		}
	}
	return restricted, matched, nil
}

// followerSubtypeRows reads the follower<->subtype join rows of followerIDs; a var so unit tests
// can replace the raw-Bun read.
var followerSubtypeRows = loadFollowerSubtypeRows

func loadFollowerSubtypeRows(ctx context.Context, followerIDs []int64) ([]discussFollowerSubtypeRow, error) {
	rows := []discussFollowerSubtypeRow{}
	err := orm.BunDBFromContext(ctx).NewSelect().
		Model(&rows).
		Where("follower_id IN (?)", bun.In(followerIDs)).
		Scan(ctx)
	return rows, err
}

// createNotificationAndPublish creates one tbl.discuss_notification row for a
// matched follower, then publishes a "notification_created" event on that
// user's base-bus channel so an open /bus/ws session receives it live.
func createNotificationAndPublish(ctx context.Context, messageID, followerUserID int64, resModel string, resID int64, authorID *int64, subject *string) {
	notifValues := orm.RecordMap{
		"message_id":        messageID,
		"user_id":           followerUserID,
		"notification_type": "inbox",
		"is_read":           false,
	}
	created, err := orm.RuntimeRepositoryCreate(ctx, orm.RuntimeModelRef(DiscussNotificationModelName, nil), notifValues)
	if err != nil {
		log.Warnf("tbl.discuss_notification: failed to create for message %d user %d: %v", messageID, followerUserID, err)
		return
	}
	notificationID := int64Value(created["id"])

	payload := map[string]any{
		"message_id":      messageID,
		"notification_id": notificationID,
		"res_model":       resModel,
		"res_id":          resID,
		"author_id":       authorID,
	}
	if subject != nil {
		payload["subject"] = *subject
	}
	channels := []string{"discuss:user:" + strconv.FormatInt(followerUserID, 10)}
	// base.service.bus.publish delivery is best-effort realtime: a failed
	// publish never rolls back the already-durable notification row, the
	// recipient just falls back to polling/reading the notification later.
	if _, err := orm.ExecuteRuntimeMethod(ctx, "base.service.bus", "publish", nil, []any{channels, "notification_created", payload}, nil); err != nil {
		log.Warnf("tbl.discuss_notification %d: base.service.bus.publish failed: %v", notificationID, err)
	}
}

// Subscribe upserts a tbl.discuss_followers row for (resModel, resID, userID).
// A non-nil subtypeIDs (even empty: "all subtypes") sets/replaces the subtype
// restriction; nil means "not specified" and keeps an existing follower's
// restriction untouched, so auto-subscribing a message recipient never wipes
// the subtypes that follower chose (plan T73).
func Subscribe(ctx context.Context, resModel string, resID int64, userID int64, subtypeIDs []int64) error {
	existingID, found, err := findFollowerID(ctx, resModel, resID, userID)
	if err != nil {
		return err
	}
	if found && subtypeIDs == nil {
		return nil
	}
	if found {
		_, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(DiscussFollowersModelName, nil), []string{strconv.FormatInt(existingID, 10)}, orm.RecordMap{
			"subtype_ids": subtypeIDs,
		})
		return err
	}
	_, err = orm.RuntimeRepositoryCreate(ctx, orm.RuntimeModelRef(DiscussFollowersModelName, nil), orm.RecordMap{
		"res_model":   resModel,
		"res_id":      resID,
		"user_id":     userID,
		"subtype_ids": subtypeIDs,
	})
	return err
}

// Unsubscribe deletes the tbl.discuss_followers row for (resModel, resID, userID),
// if one exists.
func Unsubscribe(ctx context.Context, resModel string, resID int64, userID int64) error {
	existingID, found, err := findFollowerID(ctx, resModel, resID, userID)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	_, err = orm.RuntimeRepositoryUnlink(ctx, orm.RuntimeModelRef(DiscussFollowersModelName, nil), []string{strconv.FormatInt(existingID, 10)})
	return err
}

// findFollowerID looks up the tbl.discuss_followers row id for one
// (resModel, resID, userID) triple, if any.
func findFollowerID(ctx context.Context, resModel string, resID int64, userID int64) (int64, bool, error) {
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(DiscussFollowersModelName, nil), orm.RecordMap{
		"res_model": resModel,
		"res_id":    resID,
		"user_id":   userID,
	}, []string{"id"})
	if err != nil {
		return 0, false, err
	}
	if len(rows) == 0 {
		return 0, false, nil
	}
	return int64Value(rows[0]["id"]), true, nil
}

// int64Value coerces a RecordMap value of unknown numeric shape into int64,
// returning 0 for anything it can't convert. Kept package-local (like the
// base module's own int64Value) rather than exported from internal/orm.
func int64Value(value any) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case int32:
		return int64(typed)
	case float64:
		return int64(typed)
	case string:
		parsed, err := strconv.ParseInt(typed, 10, 64)
		if err != nil {
			return 0
		}
		return parsed
	default:
		return 0
	}
}

// runtimePostMessage is the orm.RuntimeMethod wrapper around PostMessage,
// registered as tbl.discuss_message's "post_message" method so the frontend
// (and any other module) posts chatter messages through follower fan-out +
// notification + realtime-publish, never via a raw generic `create` on
// tbl.discuss_message (which would silently skip all of that).
func runtimePostMessage(ctx context.Context, _ *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	resModel := stringValue(firstNonNil(argValue(args, 0), mapValue(kwargs, "res_model")))
	if resModel == "" {
		log.Errorf("tbl.discuss_message.post_message requires res_model")
		return nil, orm.InvalidParamsf("tbl.discuss_message.post_message requires res_model")
	}
	resID := int64Value(firstNonNil(argValue(args, 1), mapValue(kwargs, "res_id")))
	if resID <= 0 {
		log.Errorf("tbl.discuss_message.post_message requires a positive res_id")
		return nil, orm.InvalidParamsf("tbl.discuss_message.post_message requires a positive res_id")
	}

	authorID := int64PointerFromAny(firstNonNil(argValue(args, 2), mapValue(kwargs, "author_id")))
	messageIDHeader := stringPointerFromAny(firstNonNil(argValue(args, 8), mapValue(kwargs, "message_id_header")))
	// An RPC caller always posts as themselves, on a record they can read (plan T23), and cannot
	// pick the Message-ID header (it would steer inbound email replies into this thread).
	if userID, restricted := discussRestrictedCaller(ctx); restricted {
		if authorID != nil && *authorID != userID {
			return nil, orm.Forbiddenf("permission denied: post_message cannot set another author")
		}
		authorID = &userID
		messageIDHeader = nil
		if err := ensureDiscussTargetAccess(ctx, resModel, resID, "read"); err != nil {
			return nil, err
		}
	}
	// Default the author to the calling user when the caller didn't pass one
	// explicitly — the common case for a chatter composer submit.
	if authorID == nil {
		if env := orm.EnvironmentFromContext(ctx); env != nil {
			userID := env.UserID()
			if userID > 0 {
				authorID = &userID
			}
		}
	}
	subtypeID := int64PointerFromAny(firstNonNil(argValue(args, 3), mapValue(kwargs, "subtype_id")))
	messageType := stringValue(firstNonNil(argValue(args, 4), mapValue(kwargs, "message_type")))
	subject := stringPointerFromAny(firstNonNil(argValue(args, 5), mapValue(kwargs, "subject")))
	body := stringPointerFromAny(firstNonNil(argValue(args, 6), mapValue(kwargs, "body")))
	parentID := int64PointerFromAny(firstNonNil(argValue(args, 7), mapValue(kwargs, "parent_id")))
	recipientUserIDs := int64SliceFromAny(mapValue(kwargs, "recipient_ids"))
	// "Log note" submits pass is_note:true to skip the receiver requirement —
	// an internal note only needs to notify existing followers, matching
	// the note/comment split (see chatter-format.js's isNote convention).
	// Everything else is a "Send message" comment, which per explicit product
	// direction must name at least one receiver so it never silently posts
	// into the void with nobody addressed.
	isNote, _ := mapValue(kwargs, "is_note").(bool)
	if !isNote && len(recipientUserIDs) == 0 {
		log.Errorf("tbl.discuss_message.post_message requires recipient_ids for a non-note message")
		return nil, orm.InvalidParamsf("tbl.discuss_message.post_message requires at least one recipient_id for a non-note message")
	}

	messageID, err := PostMessage(ctx, resModel, resID, authorID, subtypeID, messageType, subject, body, parentID, messageIDHeader, recipientUserIDs)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": messageID}, nil
}

// runtimeSubscribe is the orm.RuntimeMethod wrapper around Subscribe,
// registered as tbl.discuss_followers' "subscribe" method so subtype-restriction
// defaulting logic stays server-side instead of the frontend doing a raw
// create/write on tbl.discuss_followers.
func runtimeSubscribe(ctx context.Context, _ *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	resModel := stringValue(firstNonNil(argValue(args, 0), mapValue(kwargs, "res_model")))
	resID := int64Value(firstNonNil(argValue(args, 1), mapValue(kwargs, "res_id")))
	if resModel == "" || resID <= 0 {
		log.Errorf("tbl.discuss_followers.subscribe requires res_model and res_id")
		return nil, orm.InvalidParamsf("tbl.discuss_followers.subscribe requires res_model and res_id")
	}
	userID := int64Value(firstNonNil(argValue(args, 2), mapValue(kwargs, "user_id")))
	if userID <= 0 {
		// Default to the calling user when no explicit user_id was given —
		// covers the common "follow this record" self-subscribe case.
		if env := orm.EnvironmentFromContext(ctx); env != nil {
			userID = env.UserID()
		}
	}
	if userID <= 0 {
		log.Errorf("tbl.discuss_followers.subscribe requires user_id")
		return nil, orm.InvalidParamsf("tbl.discuss_followers.subscribe requires user_id")
	}
	subtypeIDs := int64SliceFromAny(firstNonNil(argValue(args, 3), mapValue(kwargs, "subtype_ids")))
	if err := ensureFollowerChangeAllowed(ctx, resModel, resID, userID); err != nil {
		return nil, err
	}

	err := Subscribe(ctx, resModel, resID, userID, subtypeIDs)
	if err != nil {
		return nil, err
	}
	return true, nil
}

// runtimeUnsubscribe is the orm.RuntimeMethod wrapper around Unsubscribe,
// registered as tbl.discuss_followers' "unsubscribe" method.
func runtimeUnsubscribe(ctx context.Context, _ *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	resModel := stringValue(firstNonNil(argValue(args, 0), mapValue(kwargs, "res_model")))
	resID := int64Value(firstNonNil(argValue(args, 1), mapValue(kwargs, "res_id")))
	if resModel == "" || resID <= 0 {
		log.Errorf("tbl.discuss_followers.unsubscribe requires res_model and res_id")
		return nil, orm.InvalidParamsf("tbl.discuss_followers.unsubscribe requires res_model and res_id")
	}
	userID := int64Value(firstNonNil(argValue(args, 2), mapValue(kwargs, "user_id")))
	if userID <= 0 {
		if env := orm.EnvironmentFromContext(ctx); env != nil {
			userID = env.UserID()
		}
	}
	if userID <= 0 {
		log.Errorf("tbl.discuss_followers.unsubscribe requires user_id")
		return nil, orm.InvalidParamsf("tbl.discuss_followers.unsubscribe requires user_id")
	}
	if err := ensureFollowerChangeAllowed(ctx, resModel, resID, userID); err != nil {
		return nil, err
	}

	err := Unsubscribe(ctx, resModel, resID, userID)
	if err != nil {
		return nil, err
	}
	return true, nil
}
