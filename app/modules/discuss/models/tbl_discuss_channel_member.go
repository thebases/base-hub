package models

// File overview:
// - Responsibility: defines the `tbl.discuss_channel_member` model — records
//   that one base.users is a member of a `tbl.discuss_channel`, tracking its
//   last-seen message (read receipts) and an optional mute-until timestamp.
//   Ownership: discuss module. Audit-sensitivity: low.
// - Key symbols: `DiscussChannelMemberRecord`, `DiscussChannelMemberModel`.
// - Pattern: mirrors helpdesk's minimal-model shape. The (channel_id, user_id)
//   pair is a composite-unique group (same Bun `unique:<group>` tag
//   convention `tbl.discuss_followers`/`tbl.discuss_notification` already use) so a
//   user can only have one membership row per channel.

import (
	"time"

	"git.thebase.vn/base_hub/internal/orm"
)

// DiscussChannelMemberRecord stores one user's membership in a channel.
// LastSeenMessageID is the read-receipt marker (advances via MarkSeen);
// MuteUntil is a personal notification-silencing setting.
type DiscussChannelMemberRecord struct {
	orm.BaseModel
	ChannelID         int64      `bun:"channel_id,notnull,unique:discuss_channel_member_uniq" json:"channel_id" meta:"kind:many2one,model:tbl.discuss_channel,ondelete:CASCADE,required,index,label:Channel,help:Links this membership row to the tbl.discuss_channel it belongs to."`
	UserID            int64      `bun:"user_id,notnull,unique:discuss_channel_member_uniq" json:"user_id" meta:"kind:many2one,model:base.users,ondelete:CASCADE,required,index,label:User,help:Links this membership row to the base.users record that is a member."`
	LastSeenMessageID *int64     `bun:"last_seen_message_id" json:"last_seen_message_id" meta:"kind:many2one,model:tbl.discuss_message,ondelete:SET NULL,index,label:Last Seen Message,help:Tracks the last tbl.discuss_message this member has read, for unread-count/read-receipt computations."`
	MuteUntil         *time.Time `bun:"mute_until" json:"mute_until" meta:"label:Mute Until,help:Stores the timestamp until which this member has muted notifications for this channel."`
}

const DiscussChannelMemberModelName = "tbl.discuss_channel_member"

var DiscussChannelMemberModel = orm.Define[DiscussChannelMemberRecord](&Models, 909, orm.ModelSpec{
	Name:        DiscussChannelMemberModelName,
	Label:       "Discuss Channel Members",
	Description: "Per-user channel membership, read-receipt, and mute state, owned by the discuss module",
})
