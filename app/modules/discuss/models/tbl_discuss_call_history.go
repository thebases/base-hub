package models

// File overview:
// - Responsibility: defines the `tbl.discuss_call_history` model — a durable
//   ledger row per call (start/end time) on a channel, independent of the
//   ephemeral per-participant tbl.discuss_channel_rtc_session rows. Ownership:
//   discuss module. Audit-sensitivity: low.
// - Key symbols: `DiscussCallHistoryRecord`, `DiscussCallHistoryModel`.

import (
	"time"

	"git.thebase.vn/base_hub/internal/orm"
)

// DiscussCallHistoryRecord stores one call's lifetime on a channel. StartedAt
// is stamped when the first participant's rtc session is created; EndedAt is
// stamped when the last remaining rtc session in that channel is removed.
type DiscussCallHistoryRecord struct {
	orm.BaseModel
	ChannelID          int64      `bun:"channel_id,notnull" json:"channel_id" meta:"kind:many2one,model:tbl.discuss_channel,ondelete:CASCADE,required,index,label:Channel,help:Links this call record to the tbl.discuss_channel it happened in."`
	StartedAt          time.Time  `bun:"started_at,notnull" json:"started_at" meta:"required,label:Started At,help:Stores when the first participant joined this call."`
	EndedAt            *time.Time `bun:"ended_at" json:"ended_at" meta:"label:Ended At,help:Stores when the last participant left this call, if it has ended."`
	StartCallMessageID *int64     `bun:"start_call_message_id" json:"start_call_message_id" meta:"kind:many2one,model:tbl.discuss_message,ondelete:SET NULL,index,label:Start Call Message,help:Links this call to the tbl.discuss_message posted announcing it started."`
}

const DiscussCallHistoryModelName = "tbl.discuss_call_history"

var DiscussCallHistoryModel = orm.Define[DiscussCallHistoryRecord](&Models, 913, orm.ModelSpec{
	Name:        DiscussCallHistoryModelName,
	Label:       "Discuss Call History",
	Description: "Durable call start/end ledger, owned by the discuss module",
})
