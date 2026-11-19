package models

// File overview:
// - Responsibility: the project-owned entry points onto the discuss chatter
//   core (never create tbl.discuss_message rows directly) — one wrapper per
//   top-level model that needs chatter (project, task). Ownership: project
//   module. Audit-sensitivity: low.
// - Key symbols: `PostProjectMessage`, `PostTaskMessage`.
// - Pattern: mirrors modules/helpdesk/models/service.go's PostTicketMessage —
//   cross-module call goes through orm.ExecuteRuntimeMethod targeting
//   tbl.discuss_message's "post_message" RuntimeMethod, never a direct Go
//   import of modules/discuss/models.

import (
	"context"
	"fmt"

	"git.thebase.vn/base_hub/internal/orm"
)

// PostProjectMessage posts a chatter message/note on projectID.
func PostProjectMessage(ctx context.Context, projectID int64, authorID *int64, body string, isNote bool) (int64, error) {
	return postChatterMessage(ctx, ProjectModelName, projectID, authorID, body, isNote)
}

// PostTaskMessage posts a chatter message/note on taskID.
func PostTaskMessage(ctx context.Context, taskID int64, authorID *int64, body string, isNote bool) (int64, error) {
	return postChatterMessage(ctx, ProjectTaskModelName, taskID, authorID, body, isNote)
}

func postChatterMessage(ctx context.Context, modelName string, recordID int64, authorID *int64, body string, isNote bool) (int64, error) {
	if recordID <= 0 {
		return 0, fmt.Errorf("%s: post_message requires a positive record id", modelName)
	}
	kwargs := map[string]any{"is_note": isNote}
	args := []any{modelName, recordID, authorIDAny(authorID), nil, "comment", nil, body}
	result, err := orm.ExecuteRuntimeMethod(ctx, "tbl.discuss_message", "post_message", nil, args, kwargs)
	if err != nil {
		return 0, err
	}
	return int64Value(resultField(result, "id")), nil
}

// authorIDAny lets a nil *int64 pass through as a real nil arg (so
// post_message's own "default to calling user" fallback applies) instead of
// a typed-nil interface value.
func authorIDAny(authorID *int64) any {
	if authorID == nil {
		return nil
	}
	return *authorID
}

// resultField pulls one field out of ExecuteRuntimeMethod's `any` result,
// which is a map[string]any for every RuntimeMethod in this repo's convention.
func resultField(result any, field string) any {
	if typed, ok := result.(map[string]any); ok {
		return typed[field]
	}
	return nil
}
