package models

// File overview:
// - Responsibility: defines the `tbl.discuss_channel` model — a chat/group/
//   broadcast channel that `tbl.discuss_channel_member` rows attach users to and
//   whose messages ride the existing `tbl.discuss_message` model
//   (ResModel="tbl.discuss_channel", ResID=channel.id), per Phase 2 of the plan.
//   Ownership: discuss module. Audit-sensitivity: low (no balance/payment
//   mutation).
// - Key symbols: `DiscussChannelRecord`, `DiscussChannelModel`.
// - Pattern: mirrors helpdesk's minimal-model shape — a Bun-tagged struct
//   embedding orm.BaseModel plus a registered ModelSpec, no custom Fiber
//   routes/handlers since generic model-registry CRUD/RPC covers this model.

import (
	"git.thebase.vn/base_hub/internal/orm"
)

// DiscussChannelRecord stores one chat/group/channel container. Messages are
// not stored on this model directly — they are `tbl.discuss_message` rows keyed
// off (res_model="tbl.discuss_channel", res_id=this channel's id), reusing
// Phase 1's chatter core instead of duplicating a message table.
type DiscussChannelRecord struct {
	orm.BaseModel
	Name        string  `bun:"name,notnull" json:"name" meta:"required,index,label:Name,help:Stores the human-readable name of this channel."`
	ChannelType string  `bun:"channel_type,notnull,default:'chat'" json:"channel_type" meta:"required,index,label:Channel Type,help:Selects whether this is a 1:1 chat, a private group, or a broadcast channel." selection:"(chat,Chat),(group,Group),(channel,Channel)"`
	Description *string `bun:"description" json:"description" meta:"kind:text,label:Description,help:Stores an optional longer description of this channel."`
}

const DiscussChannelModelName = "tbl.discuss_channel"

var DiscussChannelModel = orm.Define[DiscussChannelRecord](&Models, 908, orm.ModelSpec{
	Name:        DiscussChannelModelName,
	Label:       "Discuss Channels",
	Description: "Chat/group/broadcast channels, owned by the discuss module",
})
