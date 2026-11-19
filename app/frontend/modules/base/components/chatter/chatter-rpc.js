// Thin RPC layer for the chatter widget — every call goes through
// createModelRpc (services/api/jsonv2.js), the same helper AttachmentWidget.js
// already uses. No bespoke fetch/transport here; mutations call the backend's
// RuntimeMethods (post_message/subscribe/unsubscribe/mark_done/
// schedule_activity/use — see modules/discuss/models/service.go +
// activity_service.go) instead of raw create/write/unlink, so follower
// fan-out, notification rows, and activity-chaining stay server-side.
import { createModelRpc, searchRelationChoices } from "@/services/api/jsonv2.js";

// Model names carry the repo-wide `tbl.` prefix convention (see
// .agents/memory/context/discuss-module-context.md's 2026-09-20 rename) —
// these must match modules/discuss/models/tbl_discuss_*.go's *ModelName consts
// exactly, since the ORM registry does no dotted-name aliasing/normalization.
const DISCUSS_MESSAGE_MODEL = "tbl.discuss_message";
const DISCUSS_ACTIVITY_MODEL = "tbl.discuss_activity";
const DISCUSS_ACTIVITY_TYPE_MODEL = "tbl.discuss_activity_type";
const DISCUSS_FOLLOWERS_MODEL = "tbl.discuss_followers";
const DISCUSS_CANNED_RESPONSE_MODEL = "tbl.discuss_canned_response";
const DISCUSS_MESSAGE_SUBTYPE_MODEL = "tbl.discuss_message_subtype";
const BASE_USERS_MODEL = "base.users";
const USER_SEARCH_LIMIT = 10;

function targetDomain(modelName, recordId) {
  return [
    ["res_model", "=", modelName],
    ["res_id", "=", recordId],
  ];
}

// Newest message first per chatter requirement — the feed no longer relies
// on scroll-to-bottom to reveal the latest message; it's already row 0.
export async function fetchMessages(modelName, recordId) {
  const response = await createModelRpc(DISCUSS_MESSAGE_MODEL).search(
    targetDomain(modelName, recordId),
    ["id", "res_model", "res_id", "subject", "body", "message_type", "subtype_id", "author_id", "author_guest_id", "parent_id", "created_at"],
    "created_at DESC",
    0,
    200
  );
  return Array.isArray(response.result) ? response.result : [];
}

// recipientUserIds is required by the backend for every non-note ("Send
// message") post — see modules/discuss/models/service.go's runtimePostMessage,
// which rejects post_message calls with is_note:false and no recipient_ids.
export async function postMessage(modelName, recordId, { subtypeId, messageType, subject, body, parentId, recipientUserIds, isNote } = {}) {
  const response = await createModelRpc(DISCUSS_MESSAGE_MODEL).call("post_message", [], {
    res_model: modelName,
    res_id: recordId,
    subtype_id: subtypeId,
    message_type: messageType,
    subject,
    body,
    parent_id: parentId,
    recipient_ids: recipientUserIds,
    is_note: isNote,
  });
  return response.result;
}

// searchUsers resolves base.users options for the chatter's recipient/
// follower pickers, excluding ids already selected — reuses the same
// name_search-backed helper the many2one/x2many form widgets use
// (searchRelationChoices), no bespoke user-search transport.
export async function searchUsers(query, excludeIds = []) {
  const domain = excludeIds.length > 0 ? [["id", "not in", excludeIds]] : [];
  return searchRelationChoices(BASE_USERS_MODEL, query ?? "", USER_SEARCH_LIMIT, domain);
}

export async function fetchActivities(modelName, recordId) {
  const response = await createModelRpc(DISCUSS_ACTIVITY_MODEL).search(
    targetDomain(modelName, recordId),
    ["id", "res_model", "res_id", "activity_type_id", "summary", "note", "date_deadline", "user_id", "automated"],
    "date_deadline ASC",
    0,
    100
  );
  return Array.isArray(response.result) ? response.result : [];
}

export async function fetchActivityTypes() {
  const response = await createModelRpc(DISCUSS_ACTIVITY_TYPE_MODEL).search([], ["id", "name", "res_model"], "sequence ASC", 0, 200);
  return Array.isArray(response.result) ? response.result : [];
}

export async function markActivityDone(activityId, feedbackNote) {
  const response = await createModelRpc(DISCUSS_ACTIVITY_MODEL).callById("mark_done", activityId, [], { feedback_note: feedbackNote });
  return response.result;
}

export async function scheduleActivity({ modelName, recordId, activityTypeId, userId, summary, note, dateDeadline }) {
  const response = await createModelRpc(DISCUSS_ACTIVITY_MODEL).call("schedule_activity", [], {
    res_model: modelName,
    res_id: recordId,
    activity_type_id: activityTypeId,
    user_id: userId,
    summary,
    note,
    date_deadline: dateDeadline,
  });
  return response.result;
}

export async function fetchFollowers(modelName, recordId) {
  const response = await createModelRpc(DISCUSS_FOLLOWERS_MODEL).search(targetDomain(modelName, recordId), ["id", "user_id", "subtype_ids"], "id ASC", 0, 200);
  return Array.isArray(response.result) ? response.result : [];
}

export async function subscribeFollower(modelName, recordId, userId, subtypeIds) {
  const response = await createModelRpc(DISCUSS_FOLLOWERS_MODEL).call("subscribe", [], {
    res_model: modelName,
    res_id: recordId,
    user_id: userId,
    subtype_ids: subtypeIds,
  });
  return response.result;
}

export async function unsubscribeFollower(modelName, recordId, userId) {
  const response = await createModelRpc(DISCUSS_FOLLOWERS_MODEL).call("unsubscribe", [], {
    res_model: modelName,
    res_id: recordId,
    user_id: userId,
  });
  return response.result;
}

export async function searchCannedResponses(query) {
  const domain = query ? [["source", "ilike", query]] : [];
  const response = await createModelRpc(DISCUSS_CANNED_RESPONSE_MODEL).search(domain, ["id", "source", "substitution"], "last_used_at DESC", 0, 10);
  return Array.isArray(response.result) ? response.result : [];
}

// findInternalNoteSubtypeId resolves the discuss.message.subtype id used for
// "Log note" composer submits — Internal=true means only internal users get
// notified, matching the note/comment split (see plan §2, mail_composer
// notes). Scoped to modelName first, falling back to a generic (no
// res_model) internal subtype; returns null if no such subtype is seeded,
// in which case the note simply posts without a subtype restriction.
export async function findInternalNoteSubtypeId(modelName) {
  const scoped = await createModelRpc(DISCUSS_MESSAGE_SUBTYPE_MODEL).search(
    [
      ["internal", "=", true],
      ["res_model", "=", modelName],
    ],
    ["id"],
    "sequence ASC",
    0,
    1
  );
  if (Array.isArray(scoped.result) && scoped.result.length > 0) return scoped.result[0].id;

  const generic = await createModelRpc(DISCUSS_MESSAGE_SUBTYPE_MODEL).search([["internal", "=", true]], ["id"], "sequence ASC", 0, 1);
  return Array.isArray(generic.result) && generic.result.length > 0 ? generic.result[0].id : null;
}

export async function useCannedResponse(cannedResponseId) {
  const response = await createModelRpc(DISCUSS_CANNED_RESPONSE_MODEL).callById("use", cannedResponseId, [], {});
  return response.result;
}
