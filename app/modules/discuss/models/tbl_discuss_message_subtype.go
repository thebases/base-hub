package models

// File overview:
// - Responsibility: defines the `tbl.discuss_message_subtype` model — a
//   classification tag attached to a `tbl.discuss_message` (e.g. "Note",
//   "Status Change") that also drives which followers receive a
//   notification for a given message. Ownership: discuss module.
//   Audit-sensitivity: low (no balance/payment mutation).
// - Key symbols: `DiscussMessageSubtypeRecord`, `DiscussMessageSubtypeModel`.
// - Pattern: mirrors helpdesk's minimal-model shape — a Bun-tagged struct
//   embedding orm.BaseModel plus a registered ModelSpec, no custom Fiber
//   routes/handlers since generic model-registry CRUD/RPC covers this model.

import (
	"git.thebase.vn/base_hub/internal/orm"
)

// DiscussMessageSubtypeRecord stores one message subtype definition. A blank
// ResModel means the subtype applies generically across every model (matches
// the "generic" subtype convention).
type DiscussMessageSubtypeRecord struct {
	orm.BaseModel
	Name        string  `bun:"name,notnull" json:"name" meta:"translate,required,index,label:Name,help:Stores the human-readable name of this message subtype."`
	Description *string `bun:"description" json:"description" meta:"kind:text,label:Description,help:Stores a longer description of when this subtype is used."`
	Internal    bool    `bun:"internal,notnull,default:false" json:"internal" meta:"required,label:Internal,help:Controls whether this subtype is only visible to internal users, not portal/external ones."`
	ResModel    *string `bun:"res_model" json:"res_model" meta:"index,label:Resource Model,help:Restricts this subtype to one target model. Empty means it applies to every model."`
	Default     bool    `bun:"default,notnull,default:true" json:"default" meta:"required,label:Default,help:Controls whether this subtype is auto-subscribed by default when a follower is added."`
	Sequence    int     `bun:"sequence,notnull,default:10" json:"sequence" meta:"required,index,label:Sequence,help:Stores the display/ordering priority for this subtype."`
}

const DiscussMessageSubtypeModelName = "tbl.discuss_message_subtype"

var DiscussMessageSubtypeModel = orm.Define[DiscussMessageSubtypeRecord](&Models, 900, orm.ModelSpec{
	Name:        DiscussMessageSubtypeModelName,
	Label:       "Discuss Message Subtypes",
	Description: "Classification tags for tbl.discuss_message records, owned by the discuss module",
})
