package models

// File overview:
// - Responsibility: defines the `tbl.discuss_notification` model — the
//   per-recipient fan-out/read-tracking row for a `tbl.discuss_message`,
//   delivered live via base-bus (see service.go's PostMessage). This is
//   what backs unread-count/message_needaction-style computations.
//   Ownership: discuss module. Audit-sensitivity: low.
// - Key symbols: `DiscussNotificationRecord`, `DiscussNotificationModel`.
// - Pattern: mirrors helpdesk's minimal-model shape. (message_id, user_id) is
//   a composite-unique group so a user never gets two notification rows for
//   the same message.

import (
	"time"

	"git.thebase.vn/base_hub/internal/orm"
)

// DiscussNotificationRecord stores one recipient's delivery/read state for a
// tbl.discuss_message. NotificationType only supports "inbox" in Phase 1 — no
// outbound-mail infra exists in this repo, so the "email" type is dropped.
type DiscussNotificationRecord struct {
	orm.BaseModel
	MessageID        int64      `bun:"message_id,notnull,unique:discuss_notification_uniq" json:"message_id" meta:"kind:many2one,model:tbl.discuss_message,ondelete:CASCADE,required,index,label:Message,help:Links this notification to the tbl.discuss_message it fans out."`
	UserID           int64      `bun:"user_id,notnull,unique:discuss_notification_uniq" json:"user_id" meta:"kind:many2one,model:base.users,ondelete:CASCADE,required,index,label:Recipient,help:Links this notification to the base.users recipient."`
	NotificationType string     `bun:"notification_type,notnull,default:'inbox'" json:"notification_type" meta:"required,label:Notification Type,help:Selects the delivery channel for this notification." selection:"(inbox,Inbox)"`
	IsRead           bool       `bun:"is_read,notnull,default:false" json:"is_read" meta:"required,index,label:Is Read,help:Controls whether the recipient has marked this notification read."`
	ReadAt           *time.Time `bun:"read_at" json:"read_at" meta:"label:Read At,help:Stores the timestamp the recipient marked this notification read."`
}

const DiscussNotificationModelName = "tbl.discuss_notification"

var DiscussNotificationModel = orm.Define[DiscussNotificationRecord](&Models, 907, orm.ModelSpec{
	Name:        DiscussNotificationModelName,
	Label:       "Discuss Notifications",
	Description: "Per-recipient message fan-out/read tracking, owned by the discuss module",
})
