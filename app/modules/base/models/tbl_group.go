package models

// File overview:
// - Responsibility: defines the `base` module model contract for res group, including field metadata, runtime methods, and module-specific invariants.
// - Key symbols: `ResGroupPrivilegeModel`, `ResGroupModel`, `ResGroupRecord`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.

import "git.thebase.vn/base_hub/internal/orm"

// ResGroupPrivilegeRecord ports the lightweight privilege grouping metadata
// used to organize role families on the users form and in admin tooling.
type ResGroupPrivilegeRecord struct {
	orm.BaseModel
	Name        string  `bun:"name" json:"name" meta:"index,label:Name,help:Stores the human readable name for this record."`
	Description string  `bun:"description" json:"description" meta:"label:Description,help:Stores the descriptive notes for this record."`
	Placeholder string  `bun:"placeholder" json:"placeholder" meta:"label:Placeholder,help:Stores the placeholder value for this record."`
	Sequence    int64   `bun:"sequence" json:"sequence" meta:"index,label:Sequence,help:Stores the numeric value for sequence."`
	CategoryID  *int64  `bun:"category_id" json:"category_id" meta:"kind:many2one,model:sys.module.category,ondelete:SET NULL,index,label:Category ID,help:Links this record to the related sys.module.category record for category id."`
	GroupIDs    []int64 `bun:"group_ids,type:jsonb,nullzero" json:"group_ids" meta:"kind:one2many,model:tbl.groups,inverse:privilege_id,stored:false,label:Group IDs,help:Lists the related tbl.groups records linked back through privilege_id."`
}

// ResGroupRecord expands the legacy role table with the same structural
// contract the base.groups reference model uses for BIDs, implied roles, and
// privilege grouping.
type ResGroupRecord struct {
	orm.BaseModel
	BID            string  `bun:"bid" json:"bid" meta:"index,unique,label:BID,help:Stores the universal global unique record identifier for this record."`
	Name           string  `bun:"name" json:"name" meta:"index,label:Name,help:Stores the human readable name for this record."`
	FullName       string  `bun:"full_name" json:"full_name" meta:"label:Full Name,help:Stores the full name value for this record."`
	Sequence       int64   `bun:"sequence" json:"sequence" meta:"index,label:Sequence,help:Stores the numeric value for sequence."`
	PrivilegeID    *int64  `bun:"privilege_id" json:"privilege_id" meta:"kind:many2one,model:base.groups.privilege,ondelete:SET NULL,index,label:Privilege ID,help:Links this record to the related base.groups.privilege record for privilege id."`
	CategoryBID    string  `bun:"category_bid" json:"category_bid" meta:"label:Category BID,help:Stores the category bid value for this record."`
	Comment        string  `bun:"comment" json:"comment" meta:"label:Comment,help:Stores the comment value for this record."`
	APIKeyDuration float64 `bun:"api_key_duration" json:"api_key_duration" meta:"label:API Key Duration,help:Stores the numeric value for api key duration."`
	ImpliedIDs     []int64 `bun:"implied_ids,type:jsonb,nullzero" json:"implied_ids" meta:"kind:many2many,model:tbl.groups,relation_table:tbl_group_implied_rel,relation_column:gid,relation_key:hid,label:Implied IDs,help:Lists the related tbl.groups records associated with this record."`
	ImpliedByIDs   []int64 `bun:"implied_by_ids,type:jsonb,nullzero" json:"implied_by_ids" meta:"kind:many2many,model:tbl.groups,relation_table:tbl_group_implied_rel,relation_column:hid,relation_key:gid,label:Implied By IDs,help:Lists the related tbl.groups records associated with this record."`
	UserIDs        []int64 `bun:"user_ids,type:jsonb,nullzero" json:"user_ids" meta:"kind:many2many,model:base.users,relation_table:tbl_group_user_rel,relation_column:gid,relation_key:uid,label:User IDs,help:Lists the related base.users records associated with this record."`
	Description    string  `bun:"description" json:"description" meta:"label:Description,help:Stores the descriptive notes for this record."`
	Superadmin     bool    `bun:"superadmin" json:"superadmin" meta:"label:Superadmin,help:Controls whether superadmin is enabled for this record."`
	ModelAccessIDs []int64 `bun:"model_access_ids,type:jsonb,nullzero" json:"model_access_ids" meta:"kind:one2many,model:sys.model.access,inverse:group_id,stored:false,label:Model Access IDs,help:Lists the sys.model.access permission rows granted to this group."`
}

var ResGroupPrivilegeModelName = "base.groups.privilege"
var ResGroupModelName = "tbl.groups"

var ResGroupPrivilegeModelSpec = orm.ModelSpec{
	Name:        ResGroupPrivilegeModelName,
	Label:       "Group Privilege",
	Description: "Privilege grouping metadata for role families",
	Definition:  orm.ModelDefinition(ResGroupPrivilegeModelName, (*ResGroupPrivilegeRecord)(nil)),
}

var ResGroupModelSpec = orm.ModelSpec{
	Name:        ResGroupModelName,
	Label:       "Groups",
	Description: "RBAC role definitions used by RPC authentication and model access checks",
	Definition:  orm.ModelDefinition(ResGroupModelName, (*ResGroupRecord)(nil)),
	// Physical storage uses the tbl_* prefix while the logical model name stays
	// dotted (tbl.groups).
}

// ResGroupPrivilegeModel keeps privilege metadata available to the same
// metadata-driven runtime used by groups, users, and sys.model.access.
var ResGroupPrivilegeModel = orm.Define[ResGroupPrivilegeRecord](&Models, 200, ResGroupPrivilegeModelSpec)

// ResGroupModel should be extended with composition-style helpers so RBAC
// rules, normalization, and runtime hooks stay small and easy to audit.
var ResGroupModel = orm.Define[ResGroupRecord](&Models, 210, ResGroupModelSpec)
