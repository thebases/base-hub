package models

// File overview:
// - Responsibility: defines the `tbl.discuss_canned_response` model — a
//   reusable text shortcut (e.g. typing ":thanks" expands to a full reply)
//   that can be scoped to specific groups. Ownership: discuss module.
//   Audit-sensitivity: low.
// - Key symbols: `DiscussCannedResponseRecord`, `DiscussCannedResponseModel`.
// - Pattern: mirrors helpdesk's minimal-model shape.

import (
	"time"

	"git.thebase.vn/base_hub/internal/orm"
)

// DiscussCannedResponseRecord stores one canned-response shortcut. A blank
// GroupIDs means the response is private to its creator (no group grants
// visibility to anyone else).
type DiscussCannedResponseRecord struct {
	orm.BaseModel
	Source       string     `bun:"source,notnull" json:"source" meta:"required,index,label:Shortcut,help:Stores the shortcut text a user types to trigger the substitution."`
	Substitution string     `bun:"substitution,notnull" json:"substitution" meta:"kind:text,required,label:Substitution,help:Stores the full text the shortcut expands to."`
	GroupIDs     []int64    `bun:"group_ids,type:jsonb,nullzero" json:"group_ids" meta:"kind:many2many,model:tbl.groups,relation_table:tbl_discuss_canned_response_group_rel,relation_column:canned_response_id,relation_key:group_id,label:Groups,help:Restricts visibility of this canned response to members of these groups. Empty means private to the creator."`
	LastUsedAt   *time.Time `bun:"last_used_at" json:"last_used_at" meta:"label:Last Used At,help:Stores the timestamp this canned response was last inserted into a message."`
}

const DiscussCannedResponseModelName = "tbl.discuss_canned_response"

var DiscussCannedResponseModel = orm.Define[DiscussCannedResponseRecord](&Models, 905, orm.ModelSpec{
	Name:        DiscussCannedResponseModelName,
	Label:       "Discuss Canned Responses",
	Description: "Reusable text shortcuts for chatter replies, owned by the discuss module",
	RuntimeMethods: []orm.RuntimeMethod{
		// use bumps LastUsedAt each time the composer inserts this shortcut.
		orm.Typed(orm.RuntimeMethod{Name: "use", Public: true, UsesPathID: true}, useCannedResponse),
	},
})
