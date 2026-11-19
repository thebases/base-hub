package models

// File overview:
// - Responsibility: implements guest identity/token issuance and lets a
//   guest post a chatter message. Ownership: discuss module. Audit-
//   sensitivity: low. See discuss_guest.go's file overview for this slice's
//   deliberately narrow scope (no session/cookie handling, no per-record
//   authorization — a caller of AuthenticateGuestToken/PostMessageAsGuest
//   decides what a given guest is allowed to touch).
// - Key symbols: `CreateGuest`, `AuthenticateGuestToken`, `PostMessageAsGuest`.
// - Pattern: same-package writes go through orm.RuntimeRepository* per this
//   module's established ctx-preservation convention (see service.go).

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"git.thebase.vn/base_hub/internal/orm"
)

// discussGuestTokenBytes controls the generated access token's entropy (32
// bytes = 256 bits, hex-encoded to 64 characters) — comfortably beyond
// brute-force range for a bearer token with no rate-limited login attempt
// counter behind it.
const discussGuestTokenBytes = 32

// CreateGuest creates a tbl.discuss_guest row with a freshly generated access
// token, returning the new guest's id and that token (the token is only
// ever returned here — the caller must deliver it to the guest, e.g. via a
// portal invite link; this module does not email it or store it anywhere
// else in plaintext-retrievable form beyond the column itself).
func CreateGuest(ctx context.Context, name string, email *string) (int64, string, error) {
	token, err := generateGuestAccessToken()
	if err != nil {
		return 0, "", err
	}

	values := orm.RecordMap{
		"name":         name,
		"access_token": token,
	}
	if email != nil {
		values["email"] = *email
	}

	created, err := orm.RuntimeRepositoryCreate(ctx, orm.RuntimeModelRef(DiscussGuestModelName, nil), values)
	if err != nil {
		return 0, "", err
	}
	return int64Value(created["id"]), token, nil
}

// generateGuestAccessToken returns a cryptographically random, hex-encoded
// bearer token.
func generateGuestAccessToken() (string, error) {
	raw := make([]byte, discussGuestTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// AuthenticateGuestToken resolves a bearer token to its tbl.discuss_guest id.
// Returns found=false (never an error) for an unknown/blank token — an
// invalid token is an expected outcome for a caller to handle as
// unauthorized, not an infrastructure failure.
func AuthenticateGuestToken(ctx context.Context, token string) (int64, bool, error) {
	if token == "" {
		return 0, false, nil
	}
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(DiscussGuestModelName, nil), orm.RecordMap{
		"access_token": token,
	}, []string{"id"})
	if err != nil {
		return 0, false, err
	}
	if len(rows) == 0 {
		return 0, false, nil
	}
	return int64Value(rows[0]["id"]), true, nil
}

// PostMessageAsGuest posts a chatter message authored by a tbl.discuss_guest
// rather than a base.users, on whatever (resModel, resID) the caller has
// already authorized this guest for. It reuses the same follower-
// notification fan-out PostMessage uses, just with AuthorGuestID set instead
// of AuthorID.
func PostMessageAsGuest(ctx context.Context, resModel string, resID int64, guestID int64, subject, body *string) (int64, error) {
	values := orm.RecordMap{
		"res_model":       resModel,
		"res_id":          resID,
		"message_type":    "comment",
		"author_guest_id": guestID,
	}
	if subject != nil {
		values["subject"] = *subject
	}
	if body != nil {
		values["body"] = *body
	}

	created, err := orm.RuntimeRepositoryCreate(ctx, orm.RuntimeModelRef(DiscussMessageModelName, nil), values)
	if err != nil {
		return 0, err
	}
	messageID := int64Value(created["id"])
	if messageID <= 0 {
		return 0, nil
	}

	// Reuse PostMessage's own follower fan-out so guest-authored messages
	// notify the same way user-authored ones do — no subtype restriction
	// is applied here (nil), matching a plain comment post.
	notifyFollowers(ctx, resModel, resID, nil, messageID, nil, subject, nil)
	return messageID, nil
}
