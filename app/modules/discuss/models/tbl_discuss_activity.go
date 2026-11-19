package models

// File overview:
// - Responsibility: defines the `tbl.discuss_activity` model — a scheduled
//   to-do/reminder on an arbitrary target record. Ownership: discuss module.
//   Audit-sensitivity: low. No `State` column: overdue/today/planned is
//   always derived at read-time from DateDeadline vs today (matches the
//   compute-only activity_state field), so there is no persisted state that
//   can go stale.
// - Key symbols: `DiscussActivityRecord`, `DiscussActivityModel`.
// - Pattern: mirrors helpdesk's minimal-model shape.

import (
	"git.thebase.vn/base_hub/internal/orm"
	"time"
)

// DiscussActivityRecord stores one scheduled activity against a target
// record. ResModel/ResID are always required together (Phase 1 drops
// the floating admin-reminder case, see plan file, so there is no
// standalone "no target" activity).
type DiscussActivityRecord struct {
	orm.BaseModel
	ResModel       string  `bun:"res_model,notnull" json:"res_model" meta:"required,index,label:Resource Model,help:Stores the model name of the record this activity belongs to."`
	ResID          int64   `bun:"res_id,notnull" json:"res_id" meta:"required,index,label:Resource ID,help:Stores the record id this activity belongs to."`
	ActivityTypeID int64   `bun:"activity_type_id,notnull" json:"activity_type_id" meta:"kind:many2one,model:tbl.discuss_activity_type,ondelete:CASCADE,required,index,label:Activity Type,help:Links this activity to the tbl.discuss_activity_type template it was created from."`
	Summary        *string `bun:"summary" json:"summary" meta:"label:Summary,help:Stores the short summary text for this activity."`
	Note           *string `bun:"note" json:"note" meta:"kind:text,label:Note,help:Stores the full note/instructions for this activity."`
	// DateDeadline is a date-only value (no time-of-day component); state
	// (overdue/today/planned) is always derived from it at read-time.
	DateDeadline time.Time `bun:"date_deadline,notnull" json:"date_deadline" meta:"kind:date,required,index,label:Deadline,help:Stores the date this activity is due."`
	UserID       int64     `bun:"user_id,notnull" json:"user_id" meta:"kind:many2one,model:base.users,ondelete:CASCADE,required,index,label:Assigned To,help:Links this activity to the base.users record responsible for completing it."`
	Automated    bool      `bun:"automated,notnull,default:false" json:"automated" meta:"required,label:Automated,help:Controls whether this activity was scheduled automatically (e.g. via chaining) rather than by a person."`
}

const DiscussActivityModelName = "tbl.discuss_activity"

var DiscussActivityModel = orm.Define[DiscussActivityRecord](&Models, 904, orm.ModelSpec{
	Name:        DiscussActivityModelName,
	Label:       "Discuss Activities",
	Description: "Scheduled to-dos/reminders on a target record, owned by the discuss module",
	RuntimeMethods: []orm.RuntimeMethod{
		// mark_done/schedule_activity keep the completion audit message +
		// chaining logic (and the res_model/res_id required-together
		// invariant) server-side instead of raw create/unlink from the
		// frontend.
		{Name: "mark_done", Public: true, UsesPathID: true, Call: runtimeMarkActivityDone},
		{Name: "schedule_activity", Public: true, ModelOnly: true, Call: runtimeScheduleActivity},
	},
})
