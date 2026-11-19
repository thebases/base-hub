package models

// File overview:
// - Responsibility: defines the `tbl.discuss_mail_server` model — a configurable
//   outbound SMTP server definition. This repo has no existing SMTP infra
//   anywhere (confirmed absent repo-wide before this task), so outbound email
//   is entirely config-driven through rows of this model rather than any
//   hardcoded server; with zero active rows, SendQueuedMail (mail_sender.go)
//   simply no-ops every pending tbl.discuss_mail row into "exception", which is
//   the expected state in an environment with no real SMTP credentials.
//   Ownership: discuss module. Audit-sensitivity: low (no balance/payment
//   mutation), but Password is a credential, see the field comment below for
//   the known plaintext-storage limitation.
// - Key symbols: `DiscussMailServerRecord`, `DiscussMailServerModel`.
// - Pattern: mirrors helpdesk's minimal-model shape — a Bun-tagged struct
//   embedding orm.BaseModel plus a registered ModelSpec, no custom Fiber
//   routes/handlers since generic model-registry CRUD/RPC covers this model.
//   `orm.BaseModel` already provides `Active` (see model_base.go), so this
//   model reuses it instead of adding a duplicate column.

import (
	"git.thebase.vn/base_hub/internal/orm"
)

// DiscussMailServerRecord stores one outbound SMTP server configuration.
// Multiple rows may exist; SendQueuedMail picks the active one with the
// lowest Sequence.
type DiscussMailServerRecord struct {
	orm.BaseModel
	Name     string  `bun:"name,notnull" json:"name" meta:"translate,required,index,label:Name,help:Stores the human-readable name of this mail server."`
	Host     string  `bun:"host,notnull" json:"host" meta:"required,label:Host,help:Stores the SMTP server hostname or address."`
	Port     int     `bun:"port,notnull,default:587" json:"port" meta:"required,label:Port,help:Stores the SMTP server port."`
	Username *string `bun:"username" json:"username" meta:"label:Username,help:Stores the SMTP auth username, if this server requires authentication."`
	// Password is sealed with secretbox on create/write and never returned to clients (plan T23,
	// mail_server_secret.go).
	Password *string `bun:"password" json:"password" meta:"secret,label:Password,help:Stores the SMTP auth password, if this server requires authentication. Encrypted at rest and never returned by the API."`
	UseTLS   bool    `bun:"use_tls,notnull,default:true" json:"use_tls" meta:"required,label:Use TLS,help:Controls whether the connection to this SMTP server is upgraded with TLS."`
	Sequence int     `bun:"sequence,notnull,default:10" json:"sequence" meta:"required,index,label:Sequence,help:Stores the selection priority when more than one active mail server is configured; lower runs first."`
}

const DiscussMailServerModelName = "tbl.discuss_mail_server"

var DiscussMailServerModel = orm.Define[DiscussMailServerRecord](&Models, 910, orm.ModelSpec{
	Name:        DiscussMailServerModelName,
	Label:       "Discuss Mail Servers",
	Description: "Configurable outbound SMTP server definitions, owned by the discuss module",
	// create/write seal the password before storing it (mail_server_secret.go).
	Prepare: prepareMailServer,
})
