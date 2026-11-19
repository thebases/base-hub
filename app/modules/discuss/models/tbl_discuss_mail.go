package models

// File overview:
// - Responsibility: defines the `tbl.discuss_mail` model — the outbound email
//   queue, one row per send attempt. EnqueueMail (mail_sender.go) creates rows
//   here with State="outgoing"; the cron-driven SendQueuedMail sweep advances
//   each row to "sent" or "exception". Ownership: discuss module.
//   Audit-sensitivity: low (no balance/payment mutation), but this is the
//   durable record of what was attempted to be sent and why it failed, so
//   rows are never deleted by the sending logic itself.
// - Key symbols: `DiscussMailRecord`, `DiscussMailModel`.
// - Pattern: mirrors helpdesk's minimal-model shape — a Bun-tagged struct
//   embedding orm.BaseModel plus a registered ModelSpec, no custom Fiber
//   routes/handlers since generic model-registry CRUD/RPC covers this model.

import (
	"time"

	"git.thebase.vn/base_hub/internal/orm"
)

// DiscussMailRecord stores one outbound email attempt. State moves
// outgoing -> sending -> sent | exception | cancel; FailureReason is only populated on
// the exception path.
type DiscussMailRecord struct {
	orm.BaseModel
	// MessageID optionally links this email back to the tbl.discuss_message it
	// mirrors (e.g. a chatter post that also gets emailed out). Not every
	// tbl.discuss_mail row originates from a message, so this stays nullable.
	MessageID *int64 `bun:"message_id" json:"message_id" meta:"kind:many2one,model:tbl.discuss_message,ondelete:SET NULL,index,label:Message,help:Links this email to the tbl.discuss_message it mirrors, if any."`
	// MailServerID records which server actually sent (or attempted to send)
	// this row, kept for audit even if the server config later changes.
	MailServerID  *int64     `bun:"mail_server_id" json:"mail_server_id" meta:"kind:many2one,model:tbl.discuss_mail_server,ondelete:SET NULL,index,label:Mail Server,help:Links this email to the tbl.discuss_mail_server used to send it."`
	EmailTo       string     `bun:"email_to,notnull" json:"email_to" meta:"required,index,label:To,help:Stores the recipient email address."`
	EmailCc       *string    `bun:"email_cc" json:"email_cc" meta:"label:Cc,help:Stores an optional comma-separated list of Cc recipient email addresses."`
	Subject       *string    `bun:"subject" json:"subject" meta:"label:Subject,help:Stores the email subject line."`
	BodyHTML      *string    `bun:"body_html" json:"body_html" meta:"kind:text,label:Body,help:Stores the full HTML email body."`
	State         string     `bun:"state,notnull,default:'outgoing'" json:"state" meta:"required,index,label:State,help:Tracks this email's delivery lifecycle." selection:"(outgoing,Outgoing),(sending,Sending),(sent,Sent),(exception,Exception),(cancel,Cancelled)"`
	FailureReason *string    `bun:"failure_reason" json:"failure_reason" meta:"kind:text,label:Failure Reason,help:Stores the error that moved this email to the exception state."`
	ScheduledAt   *time.Time `bun:"scheduled_at" json:"scheduled_at" meta:"label:Scheduled At,help:Stores when this email should be attempted; empty means as soon as possible."`
	SentAt        *time.Time `bun:"sent_at" json:"sent_at" meta:"readonly,label:Sent At,help:Stores the timestamp this email was successfully sent."`
}

const DiscussMailModelName = "tbl.discuss_mail"

var DiscussMailModel = orm.Define[DiscussMailRecord](&Models, 911, orm.ModelSpec{
	Name:        DiscussMailModelName,
	Label:       "Discuss Outbound Mail",
	Description: "Outbound email queue, one row per send attempt, owned by the discuss module",
})
