package models

// File overview:
// - Responsibility: defines the `tbl.discuss_activity_type` model — a reusable
//   activity template (e.g. "Call", "Meeting", "To Do") that
//   `tbl.discuss_activity` rows are created from. Ownership: discuss module.
//   Audit-sensitivity: low.
// - Key symbols: `DiscussActivityTypeRecord`, `DiscussActivityTypeModel`.
// - Pattern: mirrors helpdesk's minimal-model shape.

import (
	"git.thebase.vn/base_hub/internal/orm"
)

// DiscussActivityTypeRecord stores one activity type template.
// DelayUnit/DelayFrom/DecorationType/ChainingType are plain string columns
// with a small documented allowed-value convention (not DB enum types).
type DiscussActivityTypeRecord struct {
	orm.BaseModel
	Name       string  `bun:"name,notnull" json:"name" meta:"translate,required,index,label:Name,help:Stores the human-readable name of this activity type."`
	Summary    *string `bun:"summary" json:"summary" meta:"label:Summary,help:Stores the default summary text applied to activities created from this type."`
	Sequence   int     `bun:"sequence,notnull,default:10" json:"sequence" meta:"required,index,label:Sequence,help:Stores the display/ordering priority for this activity type."`
	DelayCount int     `bun:"delay_count,notnull,default:0" json:"delay_count" meta:"required,label:Delay Count,help:Stores how many delay units are added to the deadline when this type is scheduled."`
	DelayUnit  string  `bun:"delay_unit,notnull,default:'days'" json:"delay_unit" meta:"required,label:Delay Unit,help:Selects the unit the delay count is expressed in." selection:"(days,Days),(weeks,Weeks),(months,Months)"`
	DelayFrom  string  `bun:"delay_from,notnull,default:'current_date'" json:"delay_from" meta:"required,label:Delay From,help:Selects whether the delay is computed from today or from the previous activity's deadline." selection:"(current_date,Current Date),(previous_activity,Previous Activity)"`
	Icon       *string `bun:"icon" json:"icon" meta:"label:Icon,help:Stores the icon identifier shown for activities of this type."`
	// DecorationType nil means no special decoration; warning/danger drive the
	// overdue/due-today highlight color in the UI.
	DecorationType *string `bun:"decoration_type" json:"decoration_type" meta:"label:Decoration Type,help:Selects the visual emphasis applied when an activity of this type is due or overdue." selection:"(warning,Warning),(danger,Danger)"`
	ResModel       *string `bun:"res_model" json:"res_model" meta:"index,label:Resource Model,help:Restricts this activity type to one target model. Empty means it applies to every model."`
	ChainingType   string  `bun:"chaining_type,notnull,default:'suggest'" json:"chaining_type" meta:"required,label:Chaining Type,help:Selects whether completing an activity of this type merely suggests or automatically triggers the next one." selection:"(suggest,Suggest),(trigger,Trigger)"`
	// TriggeredNextTypeID is self-referencing: when ChainingType is "trigger",
	// marking an activity of this type done automatically creates one of the
	// referenced type.
	TriggeredNextTypeID *int64 `bun:"triggered_next_type_id" json:"triggered_next_type_id" meta:"kind:many2one,model:tbl.discuss_activity_type,ondelete:SET NULL,index,label:Triggered Next Type,help:Links this activity type to the tbl.discuss_activity_type automatically scheduled next when chaining_type is trigger."`
}

const DiscussActivityTypeModelName = "tbl.discuss_activity_type"

var DiscussActivityTypeModel = orm.Define[DiscussActivityTypeRecord](&Models, 903, orm.ModelSpec{
	Name:        DiscussActivityTypeModelName,
	Label:       "Discuss Activity Types",
	Description: "Reusable activity templates, owned by the discuss module",
})
