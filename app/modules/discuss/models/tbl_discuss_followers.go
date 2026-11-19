package models

// File overview:
// - Responsibility: defines the `tbl.discuss_followers` model — records that one
//   base.users follows a given (res_model, res_id) target, optionally scoped
//   to a subset of `tbl.discuss_message_subtype`s. Ownership: discuss module.
//   Audit-sensitivity: low.
// - Key symbols: `DiscussFollowersRecord`, `DiscussFollowersModel`.
// - Pattern: mirrors helpdesk's minimal-model shape. The (res_model, res_id,
//   user_id) triple is a composite-unique group (Bun's `unique:<group>` tag
//   convention) so the same user can only follow a given target once.

import (
	"git.thebase.vn/base_hub/internal/orm"
)

// DiscussFollowersRecord stores one user's subscription to a target record.
// SubtypeIDs is stored through an explicit many2many join table
// (tbl_discuss_followers_subtype_rel) rather than a jsonb column so PostMessage
// can query subtype membership directly with SQL.
type DiscussFollowersRecord struct {
	orm.BaseModel
	ResModel   string  `bun:"res_model,notnull,unique:discuss_followers_uniq" json:"res_model" meta:"required,index,label:Resource Model,help:Stores the model name of the record being followed."`
	ResID      int64   `bun:"res_id,notnull,unique:discuss_followers_uniq" json:"res_id" meta:"required,index,label:Resource ID,help:Stores the record id being followed."`
	UserID     int64   `bun:"user_id,notnull,unique:discuss_followers_uniq" json:"user_id" meta:"kind:many2one,model:base.users,ondelete:CASCADE,required,index,label:User,help:Links this follower row to the base.users record that follows the target."`
	SubtypeIDs []int64 `bun:"subtype_ids,type:jsonb,nullzero" json:"subtype_ids" meta:"kind:many2many,model:tbl.discuss_message_subtype,relation_table:tbl_discuss_followers_subtype_rel,relation_column:follower_id,relation_key:subtype_id,label:Subtypes,help:Restricts which tbl.discuss_message_subtype notifications this follower receives. Empty means all subtypes."`
}

const DiscussFollowersModelName = "tbl.discuss_followers"

var DiscussFollowersModel = orm.Define[DiscussFollowersRecord](&Models, 902, orm.ModelSpec{
	Name:        DiscussFollowersModelName,
	Label:       "Discuss Followers",
	Description: "Per-user subscriptions to a target record, owned by the discuss module",
	RuntimeMethods: append([]orm.RuntimeMethod{
		// subscribe/unsubscribe keep the upsert-by-(res_model,res_id,user_id)
		// and subtype-restriction defaulting logic server-side, instead of the
		// frontend doing a raw find-then-create/write/unlink on this model.
		{Name: "subscribe", Public: true, ModelOnly: true, Call: runtimeSubscribe},
		{Name: "unsubscribe", Public: true, ModelOnly: true, Call: runtimeUnsubscribe},
	}, guardedDiscussCRUD(DiscussFollowersModelName)...), // raw CRUD: plan T23, discuss_access.go
})
