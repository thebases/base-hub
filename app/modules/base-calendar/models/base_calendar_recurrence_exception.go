package models

// File overview:
// - Responsibility: per-occurrence override/skip rows for the generic calendar recurrence engine
//   (SRS-F-007, table `base.calendar.recurrence.exception`). Unlike the recurrence rule's
//   (sys_model, record_id) reference, `recurrence_id` is a real FK to the static
//   `base.calendar.recurrence` table, so `ON DELETE CASCADE` works at the DB level here.
// - Key symbols: `BaseCalendarRecurrenceExceptionModel`, `BaseCalendarRecurrenceExceptionRecord`,
//   `prepareBaseCalendarRecurrenceException`, `validateBaseCalendarRecurrenceExceptionRows`.
// - Pattern: read via `loadBaseCalendarRecurrenceExceptions`/`loadBaseCalendarRecurrenceExceptionByDate`
//   in base_calendar_recurrence.go, written by that file's reschedule scope handlers — this file only
//   owns the model definition, its Prepare hook (company inherited from the parent rule) and
//   the skip/override row constraint.

import (
	"context"
	"fmt"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"git.thebase.vn/base_hub/internal/utils/anyconv"
)

// BaseCalendarRecurrenceExceptionRecord stores one per-occurrence override
// (moved date/time) or skip for a recurrence rule.
type BaseCalendarRecurrenceExceptionRecord struct {
	orm.BaseModel
	RecurrenceID           int64      `bun:"recurrence_id" json:"recurrence_id" meta:"kind:many2one,model:base.calendar.recurrence,ondelete:CASCADE,index,required,label:Recurrence,help:Links this exception to the recurrence rule it overrides."`
	OriginalOccurrenceDate time.Time  `bun:"original_occurrence_date" json:"original_occurrence_date" meta:"kind:date,index,required,label:Original Date,help:Stores which rule-generated occurrence date this exception overrides."`
	OverrideDate           *time.Time `bun:"override_date" json:"override_date" meta:"label:Override Date,help:Stores the new date/time for this occurrence. Empty when is_skipped is true."`
	IsSkipped              bool       `bun:"is_skipped,default:false" json:"is_skipped" meta:"label:Is Skipped,help:Controls whether this occurrence is removed from the calendar entirely."`
	CompanyID              *int64     `bun:"company_id" json:"company_id" meta:"kind:many2one,model:tbl.company,ondelete:SET NULL,index,label:Company ID,help:Inherits the company scope from the parent recurrence; not settable independently."`
}

const BaseCalendarRecurrenceExceptionModelName = "base.calendar.recurrence.exception"

var BaseCalendarRecurrenceExceptionModel = orm.Define[BaseCalendarRecurrenceExceptionRecord](&Models, 331, orm.ModelSpec{
	Name:        BaseCalendarRecurrenceExceptionModelName,
	Label:       "Calendar Recurrence Exceptions",
	Description: "Per-occurrence overrides/skips for base.calendar.recurrence rules",
	Prepare:     prepareBaseCalendarRecurrenceException,
})

func init() {
	// The skip/override invariant needs the stored row: a write of only is_skipped=true on a row
	// that already carries an override_date must fail too, which the partial payload cannot show.
	orm.RegisterConstraint(BaseCalendarRecurrenceExceptionModelName, []string{"is_skipped", "override_date"}, validateBaseCalendarRecurrenceExceptionRows)
}

// parentRecordAccess checks ACL and record rules on a recurrence rule this package loads
// through the repository (which skips record rules); a variable so tests can observe it (T35).
var parentRecordAccess = orm.EnsureRecordAccess

// prepareBaseCalendarRecurrenceException runs inside the guarded create/write: a create always
// inherits company_id from its parent rule, and a write that moves the exception to another rule
// re-checks and re-inherits from the new parent. The company guards run after it, so the
// inherited company is still validated against the caller's allowed companies.
func prepareBaseCalendarRecurrenceException(ctx context.Context, op orm.WriteOp) error {
	if op.Create || hasRecordMapKey(op.Values, "recurrence_id") {
		return inheritBaseCalendarRecurrenceExceptionCompanyID(ctx, op.Values)
	}
	return nil
}

// inheritBaseCalendarRecurrenceExceptionCompanyID overwrites
// working["company_id"] with the parent base.calendar.recurrence row's own
// company_id, so an exception's company can never diverge from its rule's.
func inheritBaseCalendarRecurrenceExceptionCompanyID(ctx context.Context, working orm.RecordMap) error {
	recurrenceID, ok := anyconv.Many2OneID(working["recurrence_id"])
	if !ok || recurrenceID <= 0 {
		return orm.InvalidParamsf("base.calendar.recurrence.exception recurrence_id is required")
	}
	// The parent is read through the repository (no record rules): an exception
	// changes the series, so the caller must be allowed to write the parent rule.
	if err := parentRecordAccess(ctx, BaseCalendarRecurrenceModelName, "write", []string{fmt.Sprint(recurrenceID)}); err != nil {
		return err
	}
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(BaseCalendarRecurrenceModelName, nil), []string{fmt.Sprint(recurrenceID)}, []string{"company_id"})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return orm.NotFoundf("base.calendar.recurrence %d not found", recurrenceID)
	}
	working["company_id"] = companyIDValue(rows[0]["company_id"])
	return nil
}

// validateBaseCalendarRecurrenceExceptionRows enforces on the stored rows (inside the
// create/write transaction): is_skipped=true must not carry an override_date
// (SRS-F-005/006: a skip removes the occurrence, it does not also move it).
func validateBaseCalendarRecurrenceExceptionRows(ctx context.Context, records orm.RecordSet, _ orm.RecordMap) error {
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(BaseCalendarRecurrenceExceptionModelName, records.Model()), records.IDs(), []string{"is_skipped", "override_date"})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if isSkipped, _ := row["is_skipped"].(bool); isSkipped && row["override_date"] != nil {
			log.Errorf("base.calendar.recurrence.exception: is_skipped cannot be combined with override_date")
			return orm.InvalidParamsf("base.calendar.recurrence.exception: is_skipped cannot be combined with override_date")
		}
	}
	return nil
}

// loadBaseCalendarRecurrenceExceptions loads every exception for one
// recurrence rule (used by expandCalendarOccurrences's caller).
func loadBaseCalendarRecurrenceExceptions(ctx context.Context, recurrenceID int64) ([]BaseCalendarRecurrenceExceptionRecord, error) {
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(BaseCalendarRecurrenceExceptionModelName, nil), orm.RecordMap{"recurrence_id": recurrenceID}, nil)
	if err != nil {
		return nil, err
	}
	return baseCalendarExceptionsFromRows(rows), nil
}

// loadBaseCalendarRecurrenceExceptionsByRule fetches the exceptions of many
// rules in one query (an IN domain on recurrence_id) and groups them by rule,
// so expanding N rules costs one read instead of N.
func loadBaseCalendarRecurrenceExceptionsByRule(ctx context.Context, recurrenceIDs []int64) (map[int64][]BaseCalendarRecurrenceExceptionRecord, error) {
	grouped := make(map[int64][]BaseCalendarRecurrenceExceptionRecord, len(recurrenceIDs))
	if len(recurrenceIDs) == 0 {
		return grouped, nil
	}
	ids := make([]any, len(recurrenceIDs))
	for i, id := range recurrenceIDs {
		ids[i] = id
	}
	domain, err := orm.ParseDomain([]any{[]any{"recurrence_id", "in", ids}})
	if err != nil {
		return nil, err
	}
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(BaseCalendarRecurrenceExceptionModelName, nil), orm.RecordMap{"__domain": domain}, nil)
	if err != nil {
		return nil, err
	}
	for _, exception := range baseCalendarExceptionsFromRows(rows) {
		grouped[exception.RecurrenceID] = append(grouped[exception.RecurrenceID], exception)
	}
	return grouped, nil
}

func baseCalendarExceptionsFromRows(rows []orm.RecordMap) []BaseCalendarRecurrenceExceptionRecord {
	out := make([]BaseCalendarRecurrenceExceptionRecord, 0, len(rows))
	for _, row := range rows {
		exception := BaseCalendarRecurrenceExceptionRecord{}
		exception.ID, _ = int64FromAny(row["id"])
		exception.RecurrenceID, _ = anyconv.Many2OneID(row["recurrence_id"])
		if isSkipped, ok := row["is_skipped"].(bool); ok {
			exception.IsSkipped = isSkipped
		}
		if originalDate, ok := parseBaseCalendarDate(row["original_occurrence_date"]); ok {
			exception.OriginalOccurrenceDate = originalDate.Truncate(24 * time.Hour)
		}
		if overrideDate, ok := parseBaseCalendarDate(row["override_date"]); ok {
			exception.OverrideDate = &overrideDate
		}
		out = append(out, exception)
	}
	return out
}

// loadBaseCalendarRecurrenceExceptionByDate finds an existing exception for
// one recurrence rule + original occurrence date, so a repeated drag on the
// same occurrence updates in place rather than duplicating rows.
func loadBaseCalendarRecurrenceExceptionByDate(ctx context.Context, recurrenceID int64, originalDate time.Time) (*BaseCalendarRecurrenceExceptionRecord, error) {
	exceptions, err := loadBaseCalendarRecurrenceExceptions(ctx, recurrenceID)
	if err != nil {
		return nil, err
	}
	truncated := originalDate.Truncate(24 * time.Hour)
	for i := range exceptions {
		if exceptions[i].OriginalOccurrenceDate.Equal(truncated) {
			return &exceptions[i], nil
		}
	}
	return nil, nil
}
