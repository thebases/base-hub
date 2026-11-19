package models

// File overview:
// - Responsibility: defines the `tbl.discuss_message` model — the core
//   polymorphic chatter message row, keyed off an arbitrary (res_model,
//   res_id) target the same way `sys.attachment` binds to a record.
//   Ownership: discuss module. Audit-sensitivity: low (no balance/payment
//   mutation) — message rows are themselves the audit trail for other
//   modules' field-tracking notifications, but this model has no balance
//   mutation of its own.
// - Key symbols: `DiscussMessageRecord`, `DiscussMessageModel`.
// - Pattern: mirrors helpdesk's minimal-model shape — a Bun-tagged struct
//   embedding orm.BaseModel plus a registered ModelSpec, no custom Fiber
//   routes/handlers since generic model-registry CRUD/RPC covers this model.

import (
	"git.thebase.vn/base_hub/internal/orm"
)

// DiscussMessageRecord stores one chatter message posted against an
// arbitrary target record. `MessageType` is kept as a plain string column
// with a small documented allowed-value convention (not a DB enum type):
//   - message_type: comment | notification | email | auto_comment (default "comment")
type DiscussMessageRecord struct {
	orm.BaseModel
	// ResModel/ResID bind this message to one record on any registered model,
	// the same polymorphic (model, record) convention sys.attachment already
	// uses — no per-model message table needed.
	ResModel    string  `bun:"res_model,notnull" json:"res_model" meta:"required,index,label:Resource Model,help:Stores the model name of the record this message belongs to."`
	ResID       int64   `bun:"res_id,notnull" json:"res_id" meta:"required,index,label:Resource ID,help:Stores the record id this message belongs to."`
	Subject     *string `bun:"subject" json:"subject" meta:"label:Subject,help:Stores the optional short subject line for this message."`
	Body        *string `bun:"body" json:"body" meta:"kind:text,label:Body,help:Stores the full message body."`
	MessageType string  `bun:"message_type,notnull,default:'comment'" json:"message_type" meta:"required,index,label:Message Type,help:Selects the origin/kind of this message." selection:"(comment,Comment),(notification,Notification),(email,Email),(auto_comment,Auto Comment)"`
	SubtypeID   *int64  `bun:"subtype_id" json:"subtype_id" meta:"kind:many2one,model:tbl.discuss_message_subtype,ondelete:SET NULL,index,label:Subtype,help:Links this message to the tbl.discuss_message_subtype used to route follower notifications."`
	AuthorID    *int64  `bun:"author_id" json:"author_id" meta:"kind:many2one,model:base.users,ondelete:SET NULL,index,label:Author,help:Links this message to the base.users record that posted it."`
	// ParentID supports simple threading/reply-to; self-referencing so a
	// message can quote/reply to an earlier one on the same target record.
	ParentID *int64 `bun:"parent_id" json:"parent_id" meta:"kind:many2one,model:tbl.discuss_message,ondelete:SET NULL,index,label:Parent Message,help:Links this message to the tbl.discuss_message it replies to, if any."`
	// MessageIDHeader stores the outbound/inbound email Message-ID header for
	// this message, when one exists, so a later inbound reply's In-Reply-To/
	// References headers can be matched back to this row (see
	// inbound_router.go's message-id lookup).
	MessageIDHeader *string `bun:"message_id_header,unique" json:"message_id_header" meta:"unique,index,label:Message-ID Header,help:Stores the email Message-ID header this message corresponds to, used to match inbound replies."`
	// AuthorGuestID is the guest counterpart to AuthorID — set instead of
	// AuthorID when an external tbl.discuss_guest (not a base.users) posted this
	// message, as an author_id/author_guest_id pair.
	AuthorGuestID *int64 `bun:"author_guest_id" json:"author_guest_id" meta:"kind:many2one,model:tbl.discuss_guest,ondelete:SET NULL,index,label:Author Guest,help:Links this message to the tbl.discuss_guest that posted it, if the author was an external guest rather than a base.users."`
}

const DiscussMessageModelName = "tbl.discuss_message"

var DiscussMessageModel = orm.Define[DiscussMessageRecord](&Models, 901, orm.ModelSpec{
	Name:        DiscussMessageModelName,
	Label:       "Discuss Messages",
	Description: "Polymorphic chatter messages, owned by the discuss module",
	// Raw create/write/unlink are base.group_system only for RPC callers (plan T23, discuss_access.go).
	RuntimeMethods: append([]orm.RuntimeMethod{
		// post_message is the only supported write path for chatter messages —
		// it runs follower fan-out + notification + realtime publish that a
		// raw generic `create` on this model would silently skip.
		{Name: "post_message", Public: true, ModelOnly: true, Call: runtimePostMessage},
	}, guardedDiscussCRUD(DiscussMessageModelName)...),
})
