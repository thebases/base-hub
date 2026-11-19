package models

// File overview:
// - Responsibility: defines the `tbl.discuss_alias` model — schema-only in
//   Phase 1 for an inbound-email-gateway alias (e.g. "support@" routes to
//   tbl.helpdesk_ticket). Ownership: discuss module. Audit-sensitivity: low.
//   No routing/execution logic ships in this file — see the plan file's
//   explicit Phase 1 scope note.
// - Key symbols: `DiscussAliasRecord`, `DiscussAliasModel`.
// - Pattern: mirrors helpdesk's minimal-model shape. AliasModelID is a plain
//   dotted model-name string (e.g. "tbl.helpdesk_ticket"), NOT a many2one/FK —
//   this repo's sys.model registry has no established FK-by-id precedent
//   anywhere; every other model reference in this codebase is by dotted
//   string name, matching that convention here too.

import (
	"git.thebase.vn/base_hub/internal/orm"
)

// DiscussAliasRecord stores one inbound-mail alias definition. AliasDefaults
// is stored as a JSON text blob (not a Python-dict-literal, since
// this is Go) describing default field values applied to records created
// through this alias.
type DiscussAliasRecord struct {
	orm.BaseModel
	AliasName string `bun:"alias_name,notnull,unique" json:"alias_name" meta:"required,unique,index,label:Alias Name,help:Stores the local-part of the inbound email address this alias matches."`
	// AliasModelID holds the target model's dotted name (e.g.
	// "tbl.helpdesk_ticket"), not a foreign key — see file overview.
	AliasModelID  string  `bun:"alias_model_id,notnull" json:"alias_model_id" meta:"required,index,label:Target Model,help:Stores the dotted model name new records are created on when mail arrives at this alias."`
	AliasDefaults *string `bun:"alias_defaults" json:"alias_defaults" meta:"kind:text,label:Defaults,help:Stores a JSON object of default field values applied to records created through this alias."`
	// AliasForceThreadID, when set, routes every inbound message to this one
	// existing record instead of creating a new one per sender/subject.
	AliasForceThreadID *int64 `bun:"alias_force_thread_id" json:"alias_force_thread_id" meta:"label:Force Thread ID,help:Stores the record id every inbound message on this alias attaches to, if forced."`
	AliasContact       string `bun:"alias_contact,notnull,default:'everyone'" json:"alias_contact" meta:"required,label:Contact Policy,help:Selects who is allowed to create records through this alias." selection:"(everyone,Everyone),(followers,Followers Only)"`
}

const DiscussAliasModelName = "tbl.discuss_alias"

var DiscussAliasModel = orm.Define[DiscussAliasRecord](&Models, 906, orm.ModelSpec{
	Name:        DiscussAliasModelName,
	Label:       "Discuss Aliases",
	Description: "Inbound-mail alias definitions (schema only, no routing logic), owned by the discuss module",
})
