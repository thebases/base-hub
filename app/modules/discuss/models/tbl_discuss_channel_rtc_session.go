package models

// File overview:
// - Responsibility: defines the `tbl.discuss_channel_rtc_session` model — tracks
//   one member's live WebRTC participation state (camera/mic/screen-share)
//   in a channel call. Ownership: discuss module. Audit-sensitivity: low.
// - Key symbols: `DiscussChannelRTCSessionRecord`, `DiscussChannelRTCSessionModel`.
// - Pattern: mirrors helpdesk's minimal-model shape. This module does not
//   provision or know about any TURN/STUN server or media transport — it
//   only persists session state and relays SDP/ICE signaling payloads
//   through base-bus (see rtc_service.go); the actual peer connection is
//   entirely a client-side concern.

import (
	"git.thebase.vn/base_hub/internal/orm"
)

// DiscussChannelRTCSessionRecord stores one member's live call state. A row
// existing means that member currently has an active session in the call;
// EndRtcSession deletes the row rather than flagging it inactive (matching
// this module's tbl.discuss_activity "ephemeral, deleted on completion"
// convention rather than growing an ever-larger history table here —
// tbl.discuss_call_history is the durable record of that a call happened).
type DiscussChannelRTCSessionRecord struct {
	orm.BaseModel
	ChannelMemberID   int64 `bun:"channel_member_id,notnull" json:"channel_member_id" meta:"kind:many2one,model:tbl.discuss_channel_member,ondelete:CASCADE,required,index,label:Channel Member,help:Links this session to the tbl.discuss_channel_member currently in the call."`
	IsScreenSharingOn bool  `bun:"is_screen_sharing_on,notnull,default:false" json:"is_screen_sharing_on" meta:"required,label:Screen Sharing,help:Reports whether this participant is currently sharing their screen."`
	IsCameraOn        bool  `bun:"is_camera_on,notnull,default:false" json:"is_camera_on" meta:"required,label:Camera On,help:Reports whether this participant's camera is currently on."`
	IsMuted           bool  `bun:"is_muted,notnull,default:false" json:"is_muted" meta:"required,label:Muted,help:Reports whether this participant's microphone is currently muted."`
	IsDeaf            bool  `bun:"is_deaf,notnull,default:false" json:"is_deaf" meta:"required,label:Deafened,help:Reports whether this participant has muted incoming audio."`
}

const DiscussChannelRTCSessionModelName = "tbl.discuss_channel_rtc_session"

var DiscussChannelRTCSessionModel = orm.Define[DiscussChannelRTCSessionRecord](&Models, 912, orm.ModelSpec{
	Name:        DiscussChannelRTCSessionModelName,
	Label:       "Discuss Channel RTC Sessions",
	Description: "Live per-member call participation state, owned by the discuss module",
})
