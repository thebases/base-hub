package models

// File overview:
// - Responsibility: implements the tbl.discuss_activity lifecycle actions
//   (mark done + chaining, scheduling) and the tbl.discuss_canned_response
//   "last used" bump — the business logic plan §5 calls out as missing next
//   to PostMessage/Subscribe/Unsubscribe. Ownership: discuss module.
//   Audit-sensitivity: low (no balance/payment mutation), but MarkActivityDone
//   posts the completion as a chatter message so the (res_model, res_id)
//   target keeps a durable trail of what activity ran and when, matching
//   the "activity done" log-note behavior.
// - Key symbols: `MarkActivityDone`, `ScheduleActivity`, `UseCannedResponse`.
// - Pattern: same `orm.RuntimeRepository*` ctx-aware helpers service.go uses
//   (not `Handler[T]`, which drops the caller's ctx) — kept in its own file
//   per this module's one-file-per-concern convention
//   (channel_service.go/rtc_service.go/guest_service.go/mail_sender.go).

import (
	"context"
	"fmt"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"git.thebase.vn/base_hub/internal/utils/anyconv"
)

// MarkActivityDone completes activityID: it deletes the activity row and —
// when the activity's type chains ("trigger" + TriggeredNextTypeID set) —
// schedules the next activity in the chain, then posts a chatter message on
// the activity's target record recording completion (with feedbackNote if any).
//
// The delete and the chained create run in one transaction that first locks
// the activity row (plan T73): a failed chain no longer loses the activity, and
// a double submit chains once — the second call waits on the lock, then finds
// the row gone and returns NotFound. The completion note is posted after the
// commit, so it is only written for a completion that actually happened and a
// failed best-effort fan-out insert cannot abort the transaction.
func MarkActivityDone(ctx context.Context, activityID int64, feedbackNote *string) error {
	if activityID <= 0 {
		log.Errorf("tbl.discuss_activity.mark_done requires a positive activity id")
		return orm.InvalidParamsf("tbl.discuss_activity.mark_done requires a positive activity id")
	}

	var activity discussActivityRow
	var activityType discussActivityTypeRow
	err := activityDoneTx(ctx, activityID, func(txCtx context.Context) error {
		var err error
		if activity, err = readActivityRow(txCtx, activityID); err != nil {
			return err
		}
		if activityType, err = readActivityTypeRow(txCtx, activity.activityTypeID); err != nil {
			return err
		}
		// The row is a completed to-do, not a soft-deletable audit record;
		// the completion note posted below is the durable trace.
		if _, err := orm.RuntimeRepositoryUnlink(txCtx, orm.RuntimeModelRef(DiscussActivityModelName, nil), []string{fmt.Sprint(activityID)}); err != nil {
			return err
		}
		if activityType.chainingType == "trigger" && activityType.triggeredNextTypeID != nil {
			return chainNextActivity(txCtx, activity, *activityType.triggeredNextTypeID)
		}
		return nil
	})
	if err != nil {
		return err
	}

	postActivityDoneMessage(ctx, activity, activityType, feedbackNote)
	return nil
}

// discussActivityRow is the subset of tbl.discuss_activity fields the lifecycle
// actions in this file need.
type discussActivityRow struct {
	id             int64
	resModel       string
	resID          int64
	activityTypeID int64
	summary        *string
	userID         int64
	dateDeadline   time.Time
}

// discussActivityTypeRow is the subset of tbl.discuss_activity_type fields the
// lifecycle actions in this file need.
type discussActivityTypeRow struct {
	name                string
	delayCount          int64
	delayUnit           string
	delayFrom           string
	chainingType        string
	triggeredNextTypeID *int64
}

// readActivityRow reads one tbl.discuss_activity row by id.
func readActivityRow(ctx context.Context, activityID int64) (discussActivityRow, error) {
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(DiscussActivityModelName, nil), []string{fmt.Sprint(activityID)},
		[]string{"id", "res_model", "res_id", "activity_type_id", "summary", "user_id", "date_deadline"})
	if err != nil {
		return discussActivityRow{}, err
	}
	if len(rows) == 0 {
		log.Errorf("tbl.discuss_activity %d not found", activityID)
		return discussActivityRow{}, orm.NotFoundf("tbl.discuss_activity %d not found", activityID)
	}
	row := rows[0]
	// Runtime reads return many2one fields as [id, display_name] and dates as
	// time.Time; the old int64Value/string parsing read 0 and a zero deadline.
	activityTypeID, _ := anyconv.Many2OneID(row["activity_type_id"])
	userID, _ := anyconv.Many2OneID(row["user_id"])
	deadline, _ := anyconv.Time(row["date_deadline"])
	return discussActivityRow{
		id:             activityID,
		resModel:       stringValue(row["res_model"]),
		resID:          int64Value(row["res_id"]),
		activityTypeID: activityTypeID,
		summary:        stringPointerFromAny(row["summary"]),
		userID:         userID,
		dateDeadline:   deadline,
	}, nil
}

// readActivityTypeRow reads one tbl.discuss_activity_type row by id.
func readActivityTypeRow(ctx context.Context, activityTypeID int64) (discussActivityTypeRow, error) {
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(DiscussActivityTypeModelName, nil), []string{fmt.Sprint(activityTypeID)},
		[]string{"id", "name", "delay_count", "delay_unit", "delay_from", "chaining_type", "triggered_next_type_id"})
	if err != nil {
		return discussActivityTypeRow{}, err
	}
	if len(rows) == 0 {
		log.Errorf("tbl.discuss_activity_type %d not found", activityTypeID)
		return discussActivityTypeRow{}, orm.NotFoundf("tbl.discuss_activity_type %d not found", activityTypeID)
	}
	row := rows[0]
	return discussActivityTypeRow{
		name:                stringValue(row["name"]),
		delayCount:          int64Value(row["delay_count"]),
		delayUnit:           stringValue(row["delay_unit"]),
		delayFrom:           stringValue(row["delay_from"]),
		chainingType:        stringValue(row["chaining_type"]),
		triggeredNextTypeID: int64PointerFromAny(row["triggered_next_type_id"]),
	}, nil
}

// postActivityDoneMessage posts an internal chatter note on the activity's
// target record recording that it was completed, matching the
// "activity done" log-note behavior. Best-effort: it runs after the
// delete/chaining committed, since the message is an audit convenience, not
// the source of truth for "is this activity done".
func postActivityDoneMessage(ctx context.Context, activity discussActivityRow, activityType discussActivityTypeRow, feedbackNote *string) {
	summary := activityType.name
	if activity.summary != nil && *activity.summary != "" {
		summary = *activity.summary
	}
	body := fmt.Sprintf("Activity done: %s", summary)
	if feedbackNote != nil && *feedbackNote != "" {
		body = fmt.Sprintf("%s\n%s", body, *feedbackNote)
	}

	var authorID *int64
	if env := orm.EnvironmentFromContext(ctx); env != nil {
		if userID := env.UserID(); userID > 0 {
			authorID = &userID
		}
	}

	if _, err := PostMessage(ctx, activity.resModel, activity.resID, authorID, nil, "notification", nil, &body, nil, nil, nil); err != nil {
		log.Warnf("tbl.discuss_activity %d: failed to post completion message: %v", activity.id, err)
	}
}

// chainNextActivity schedules the next activity in a "trigger" chain, using
// nextTypeID's own delay settings to compute its deadline — matching the
// chaining semantics where the *next* type's delay (not the completed
// activity's) determines when the follow-up is due.
func chainNextActivity(ctx context.Context, completed discussActivityRow, nextTypeID int64) error {
	nextType, err := readActivityTypeRow(ctx, nextTypeID)
	if err != nil {
		return err
	}
	deadline := computeActivityDeadline(nextType, completed.dateDeadline)
	_, err = ScheduleActivity(ctx, completed.resModel, completed.resID, nextTypeID, completed.userID, nil, nil, deadline)
	return err
}

// computeActivityDeadline derives a new activity's deadline from its type's
// DelayCount/DelayUnit/DelayFrom, basing the computation on either today or
// the previous activity's own deadline.
func computeActivityDeadline(activityType discussActivityTypeRow, previousDeadline time.Time) time.Time {
	base := time.Now().UTC()
	if activityType.delayFrom == "previous_activity" && !previousDeadline.IsZero() {
		base = previousDeadline
	}
	switch activityType.delayUnit {
	case "weeks":
		return base.AddDate(0, 0, 7*int(activityType.delayCount))
	case "months":
		return base.AddDate(0, int(activityType.delayCount), 0)
	default:
		return base.AddDate(0, 0, int(activityType.delayCount))
	}
}

// ScheduleActivity creates one tbl.discuss_activity row on (resModel, resID),
// keeping the "ResModel/ResID always both set" validation in one place so
// the frontend never does a raw `create` on tbl.discuss_activity directly.
func ScheduleActivity(ctx context.Context, resModel string, resID int64, activityTypeID int64, userID int64, summary, note *string, dateDeadline time.Time) (int64, error) {
	if resModel == "" || resID <= 0 {
		log.Errorf("tbl.discuss_activity.schedule_activity requires res_model and res_id")
		return 0, orm.InvalidParamsf("tbl.discuss_activity.schedule_activity requires res_model and res_id")
	}
	if activityTypeID <= 0 {
		log.Errorf("tbl.discuss_activity.schedule_activity requires activity_type_id")
		return 0, orm.InvalidParamsf("tbl.discuss_activity.schedule_activity requires activity_type_id")
	}
	if userID <= 0 {
		log.Errorf("tbl.discuss_activity.schedule_activity requires user_id")
		return 0, orm.InvalidParamsf("tbl.discuss_activity.schedule_activity requires user_id")
	}
	if dateDeadline.IsZero() {
		dateDeadline = time.Now().UTC()
	}

	values := orm.RecordMap{
		"res_model":        resModel,
		"res_id":           resID,
		"activity_type_id": activityTypeID,
		"user_id":          userID,
		"date_deadline":    dateDeadline,
	}
	if summary != nil {
		values["summary"] = *summary
	}
	if note != nil {
		values["note"] = *note
	}

	created, err := orm.RuntimeRepositoryCreate(ctx, orm.RuntimeModelRef(DiscussActivityModelName, nil), values)
	if err != nil {
		return 0, err
	}
	return int64Value(created["id"]), nil
}

// UseCannedResponse bumps a tbl.discuss_canned_response's LastUsedAt to now,
// called each time the composer inserts it into a message.
func UseCannedResponse(ctx context.Context, cannedResponseID int64) error {
	if cannedResponseID <= 0 {
		log.Errorf("tbl.discuss_canned_response.use requires a positive id")
		return orm.InvalidParamsf("tbl.discuss_canned_response.use requires a positive id")
	}
	_, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(DiscussCannedResponseModelName, nil), []string{fmt.Sprint(cannedResponseID)}, orm.RecordMap{
		"last_used_at": time.Now().UTC(),
	})
	return err
}

// runtimeMarkActivityDone is the orm.RuntimeMethod wrapper around
// MarkActivityDone, registered as tbl.discuss_activity's "mark_done" method
// (UsesPathID: true — it acts on the activity named by the RPC path id).
func runtimeMarkActivityDone(ctx context.Context, _ *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	if len(ids) == 0 {
		log.Errorf("tbl.discuss_activity.mark_done requires an id")
		return nil, orm.InvalidParamsf("tbl.discuss_activity.mark_done requires an id")
	}
	activityID, ok := anyconv.Int64(ids[0])
	if !ok || activityID <= 0 {
		log.Errorf("tbl.discuss_activity.mark_done requires a valid id")
		return nil, orm.InvalidParamsf("tbl.discuss_activity.mark_done requires a valid id")
	}
	feedbackNote := stringPointerFromAny(firstNonNil(argValue(args, 0), mapValue(kwargs, "feedback_note")))

	err := MarkActivityDone(ctx, activityID, feedbackNote)
	if err != nil {
		return nil, err
	}
	return true, nil
}

// runtimeScheduleActivity is the orm.RuntimeMethod wrapper around
// ScheduleActivity, registered as tbl.discuss_activity's "schedule_activity"
// method (ModelOnly: true — it creates a new activity, no existing id).
func runtimeScheduleActivity(ctx context.Context, _ *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	resModel := stringValue(firstNonNil(argValue(args, 0), mapValue(kwargs, "res_model")))
	resID := int64Value(firstNonNil(argValue(args, 1), mapValue(kwargs, "res_id")))
	activityTypeID := int64Value(firstNonNil(argValue(args, 2), mapValue(kwargs, "activity_type_id")))
	userID := int64Value(firstNonNil(argValue(args, 3), mapValue(kwargs, "user_id")))
	// Default the assignee to the calling user when none was given explicitly.
	if userID <= 0 {
		if env := orm.EnvironmentFromContext(ctx); env != nil {
			userID = env.UserID()
		}
	}
	summary := stringPointerFromAny(firstNonNil(argValue(args, 4), mapValue(kwargs, "summary")))
	note := stringPointerFromAny(firstNonNil(argValue(args, 5), mapValue(kwargs, "note")))
	deadline, _ := anyconv.Time(firstNonNil(argValue(args, 6), mapValue(kwargs, "date_deadline")))

	activityID, err := ScheduleActivity(ctx, resModel, resID, activityTypeID, userID, summary, note, deadline)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": activityID}, nil
}

// useCannedResponse is tbl.discuss_canned_response's "use" method body (UsesPathID: it acts on the
// response named by the RPC path id); orm.Typed supplies the call plumbing and Call.ID the id check.
func useCannedResponse(ctx context.Context, c orm.Call[struct{}]) (bool, error) {
	cannedResponseID, err := c.ID()
	if err != nil {
		return false, err
	}
	if err := UseCannedResponse(ctx, cannedResponseID); err != nil {
		return false, err
	}
	return true, nil
}
