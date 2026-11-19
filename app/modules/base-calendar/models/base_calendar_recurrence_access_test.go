package models

// T35: reschedule and exception writes check the parent rule; interval is bounded.

import (
	"context"
	"errors"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
)

type recordedAccess struct {
	model, operation string
	ids              []string
}

// denyParentAccess replaces the parent check with one that records its call and refuses.
func denyParentAccess(t *testing.T) *recordedAccess {
	t.Helper()
	previous := parentRecordAccess
	t.Cleanup(func() { parentRecordAccess = previous })
	got := &recordedAccess{}
	parentRecordAccess = func(_ context.Context, model, operation string, ids []string) error {
		got.model, got.operation, got.ids = model, operation, ids
		return orm.Forbiddenf("denied")
	}
	return got
}

func failOnRepository(t *testing.T, modelName string) {
	t.Helper()
	fail := func(string) { t.Errorf("%s repository must not be reached after a denied check", modelName) }
	t.Cleanup(orm.StubRuntimeRepository(modelName, orm.RuntimeRepositoryStub{
		Create: func(context.Context, orm.RecordMap) (orm.RecordMap, error) { fail("create"); return nil, nil },
		Write:  func(context.Context, []string, orm.RecordMap) (bool, error) { fail("write"); return false, nil },
		Read:   func(context.Context, []string, []string) ([]orm.RecordMap, error) { fail("read"); return nil, nil },
	}))
}

func isForbiddenError(err error) bool {
	var typed *orm.Error
	return errors.As(err, &typed) && typed.Code == orm.ErrForbidden
}

func TestRescheduleOccurrenceChecksRuleWriteAccess(t *testing.T) {
	got := denyParentAccess(t)
	failOnRepository(t, BaseCalendarRecurrenceModelName)
	_, err := rescheduleOccurrenceBaseCalendarRecurrence(context.Background(), nil, []string{"5"}, nil, map[string]any{
		"original_occurrence_date": "2026-08-03", "new_datetime": "2026-08-04", "scope": "all",
	})
	if !isForbiddenError(err) {
		t.Fatalf("expected forbidden, got %v", err)
	}
	if got.model != BaseCalendarRecurrenceModelName || got.operation != "write" || len(got.ids) != 1 || got.ids[0] != "5" {
		t.Fatalf("unexpected check %+v", got)
	}
}

func TestExceptionCreateChecksParentWriteAccess(t *testing.T) {
	got := denyParentAccess(t)
	failOnRepository(t, BaseCalendarRecurrenceModelName)
	failOnRepository(t, BaseCalendarRecurrenceExceptionModelName)
	err := prepareBaseCalendarRecurrenceException(context.Background(), orm.WriteOp{Create: true, Values: orm.RecordMap{
		"recurrence_id": 9, "original_occurrence_date": "2026-08-03", "is_skipped": true,
	}})
	if !isForbiddenError(err) || got.operation != "write" || got.ids[0] != "9" {
		t.Fatalf("expected forbidden write check on rule 9, got %v (%+v)", err, got)
	}
}

func TestExceptionWriteRechecksNewParent(t *testing.T) {
	got := denyParentAccess(t)
	failOnRepository(t, BaseCalendarRecurrenceModelName)
	failOnRepository(t, BaseCalendarRecurrenceExceptionModelName)
	err := prepareBaseCalendarRecurrenceException(context.Background(), orm.WriteOp{IDs: []string{"1"}, Values: orm.RecordMap{"recurrence_id": 11}})
	if !isForbiddenError(err) || got.ids[0] != "11" {
		t.Fatalf("moving an exception must check the new rule, got %v (%+v)", err, got)
	}
}

// A write that does not move the exception must not touch the parent rule.
func TestExceptionWriteWithoutParentSkipsParentCheck(t *testing.T) {
	got := denyParentAccess(t)
	failOnRepository(t, BaseCalendarRecurrenceModelName)
	if err := prepareBaseCalendarRecurrenceException(context.Background(), orm.WriteOp{IDs: []string{"1"}, Values: orm.RecordMap{"is_skipped": true}}); err != nil || got.model != "" {
		t.Fatalf("unexpected parent check %+v (err %v)", got, err)
	}
}

// The skip/override invariant is checked on the stored row, so a write of only is_skipped=true
// on a row that already has an override_date is refused (the partial payload looked valid).
func TestExceptionConstraintChecksStoredRow(t *testing.T) {
	override := time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC)
	rows := map[string]orm.RecordMap{
		"1": {"id": int64(1), "is_skipped": true, "override_date": override},
		"2": {"id": int64(2), "is_skipped": true, "override_date": nil},
		"3": {"id": int64(3), "is_skipped": false, "override_date": override},
	}
	t.Cleanup(orm.StubRuntimeRepository(BaseCalendarRecurrenceExceptionModelName, orm.RuntimeRepositoryStub{
		Read: func(_ context.Context, ids []string, _ []string) ([]orm.RecordMap, error) {
			out := make([]orm.RecordMap, 0, len(ids))
			for _, id := range ids {
				out = append(out, rows[id])
			}
			return out, nil
		},
	}))
	check := func(id string) error {
		return validateBaseCalendarRecurrenceExceptionRows(context.Background(), orm.NewRecordSet(nil, nil, []string{id}), orm.RecordMap{"is_skipped": true})
	}
	requireInvalidParams(t, check("1"))
	for _, id := range []string{"2", "3"} {
		if err := check(id); err != nil {
			t.Fatalf("row %s should pass: %v", id, err)
		}
	}
}

// Prepare re-validates the target only when the payload names it.
func TestRecurrencePrepareValidatesTargetOnlyWhenPresent(t *testing.T) {
	if err := prepareBaseCalendarRecurrence(context.Background(), orm.WriteOp{IDs: []string{"1"}, Values: orm.RecordMap{"end_date": "2026-09-01"}}); err != nil {
		t.Fatalf("partial write should pass: %v", err)
	}
	requireInvalidParams(t, prepareBaseCalendarRecurrence(context.Background(), orm.WriteOp{Create: true, Values: orm.RecordMap{"frequency": "daily"}}))
	requireInvalidParams(t, prepareBaseCalendarRecurrence(context.Background(), orm.WriteOp{IDs: []string{"1"}, Values: orm.RecordMap{"sys_model": "no.such.model", "record_id": 1}}))
}

func TestRecurrenceIntervalBounded(t *testing.T) {
	for _, interval := range []any{0, -1, maxCalendarRecurrenceInterval + 1, "x"} {
		if err := validateBaseCalendarRecurrencePattern(orm.RecordMap{"interval": interval}); err == nil {
			t.Fatalf("interval %v should be refused", interval)
		}
	}
	if err := validateBaseCalendarRecurrencePattern(orm.RecordMap{"interval": maxCalendarRecurrenceInterval}); err != nil {
		t.Fatalf("interval %d should pass: %v", maxCalendarRecurrenceInterval, err)
	}
}

// A stored rule with a huge interval used to walk day by day forever in the weekday iterator.
func TestExpandStoredHugeIntervalReturnsQuickly(t *testing.T) {
	rule := &BaseCalendarRecurrenceRecord{
		Frequency: "weekday", Interval: 1 << 30, ByWeekday: []int{1},
		StartDate: mustParseCalendarDate(t, "2026-01-01"),
	}
	done := make(chan []time.Time, 1)
	go func() {
		done <- expandCalendarOccurrences(rule, mustParseCalendarDate(t, "2026-02-01"), mustParseCalendarDate(t, "2026-03-01"))
	}()
	select {
	case got := <-done:
		if len(got) != 0 {
			t.Fatalf("expected no occurrences, got %d", len(got))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expansion did not finish")
	}
}
