package models

// File overview:
// - Responsibility: defines the `tbl.discuss_guest` model — a minimal identity
//   for an external, non-base.users participant (e.g. a customer viewing/
//   commenting on their own record via a portal link), analogous to a
//   `mail.guest`. Ownership: discuss module. Audit-sensitivity: low.
// - Key symbols: `DiscussGuestRecord`, `DiscussGuestModel`.
// - Scope note: this model plus guest_service.go's CreateGuest/
//   AuthenticateGuestToken/PostMessageAsGuest give a guest an identity, a
//   bearer token, and a way to post a chatter message — they do NOT include
//   session/cookie handling, per-record authorization checks, or any HTTP
//   route wiring a portal UI would need. Building that full portal auth
//   pipeline is a separate, larger task explicitly deferred (as told to the
//   user when this scope was first discussed) — a caller of
//   AuthenticateGuestToken/PostMessageAsGuest is responsible for deciding
//   which (res_model, res_id) a given guest/token is actually allowed to
//   touch; this module has no opinion on that.

import (
	"git.thebase.vn/base_hub/internal/orm"
)

// DiscussGuestRecord stores one external participant's identity and bearer
// token. Unlike base.users, a guest has no password/login — AccessToken is
// the only credential.
type DiscussGuestRecord struct {
	orm.BaseModel
	Name        string  `bun:"name,notnull" json:"name" meta:"translate,required,index,label:Name,help:Stores this guest's display name."`
	Email       *string `bun:"email" json:"email" meta:"label:Email,help:Stores this guest's email address, if provided."`
	AccessToken string  `bun:"access_token,notnull,unique" json:"access_token" meta:"secret,required,unique,index,label:Access Token,help:Stores the bearer token this guest presents to authenticate, in place of a base.users login."`
}

const DiscussGuestModelName = "tbl.discuss_guest"

var DiscussGuestModel = orm.Define[DiscussGuestRecord](&Models, 914, orm.ModelSpec{
	Name:        DiscussGuestModelName,
	Label:       "Discuss Guests",
	Description: "External participant identities (no base.users login), owned by the discuss module",
})
