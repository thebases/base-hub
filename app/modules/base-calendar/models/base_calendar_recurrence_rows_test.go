package models

// File overview:
// - Responsibility: locks down decoding of DB-shaped rows (time.Time dates, many2one pairs), the
//   DST-safe daysBetween and the "following" reschedule validation.

import (
	"context"
	"errors"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestRecurrenceRecordFromMapDecodesTimeValues(t *testing.T) {
	start := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC)
	rule := baseCalendarRecurrenceRecordFromMap(orm.RecordMap{
		"id": int64(7), "sys_model": "x.y", "record_id": [2]any{int64(9), "n"},
		"frequency": "daily", "start_date": start, "end_date": &end, "company_id": [2]any{int64(3), "c"},
	})
	if !rule.StartDate.Equal(start) || rule.EndDate == nil || !rule.EndDate.Equal(end) {
		t.Fatalf("dates not decoded: start=%v end=%v", rule.StartDate, rule.EndDate)
	}
	if rule.RecordID != 9 || rule.CompanyID == nil || *rule.CompanyID != 3 {
		t.Fatalf("many2one not decoded: %+v", rule)
	}
	// The rule must stop at its end date instead of expanding from year 1.
	got := expandCalendarOccurrences(rule, start, start.AddDate(0, 0, 60))
	if len(got) != 19 || !got[0].Equal(start) {
		t.Fatalf("occurrences = %d first=%v, want 19 from %v", len(got), got[0], start)
	}
}

func TestExceptionsFromRowsDecodeTimeAndMany2One(t *testing.T) {
	orig := time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC)
	over := time.Date(2026, 3, 6, 10, 0, 0, 0, time.UTC)
	got := baseCalendarExceptionsFromRows([]orm.RecordMap{{
		"id": int64(1), "recurrence_id": [2]any{int64(4), "r"},
		"original_occurrence_date": orig, "override_date": over,
	}})
	if len(got) != 1 || got[0].RecurrenceID != 4 || !got[0].OriginalOccurrenceDate.Equal(orig) ||
		got[0].OverrideDate == nil || !got[0].OverrideDate.Equal(over) {
		t.Fatalf("exception not decoded: %+v", got)
	}
}

func TestDaysBetweenAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	// 2026-03-08 is a 23h day in New York; Duration/24h would give 0 for a
	// midnight-to-midnight span that really is one calendar day.
	a := time.Date(2026, 3, 8, 0, 0, 0, 0, loc)
	b := time.Date(2026, 3, 9, 0, 0, 0, 0, loc)
	if got := daysBetween(a, b); got != 1 {
		t.Fatalf("spring-forward daysBetween = %d, want 1", got)
	}
	c := time.Date(2026, 11, 1, 0, 0, 0, 0, loc) // 25h day
	d := time.Date(2026, 11, 2, 0, 0, 0, 0, loc)
	if got := daysBetween(c, d); got != 1 || daysBetween(d, c) != -1 {
		t.Fatalf("fall-back daysBetween = %d", got)
	}
	// Daily rule across the change must still hit every day.
	rule := &BaseCalendarRecurrenceRecord{Frequency: "daily", Interval: 1, StartDate: time.Date(2026, 3, 1, 9, 0, 0, 0, loc)}
	got := expandCalendarOccurrences(rule, time.Date(2026, 3, 8, 0, 0, 0, 0, loc), time.Date(2026, 3, 11, 0, 0, 0, 0, loc))
	if len(got) != 3 {
		t.Fatalf("daily across DST = %d occurrences, want 3", len(got))
	}
}

func TestRescheduleFollowingRejectsEmptyOrInvertedSeries(t *testing.T) {
	start := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	rule := &BaseCalendarRecurrenceRecord{Frequency: "daily", Interval: 1, StartDate: start, EndDate: &end}

	// Dragging the first occurrence would end the old rule before it starts.
	_, err := rescheduleBaseCalendarFollowingOccurrences(context.Background(), nil, rule, start, start.AddDate(0, 0, 1))
	requireInvalidParams(t, err)
	// Moving past end_date would create a rule that ends before it starts.
	_, err = rescheduleBaseCalendarFollowingOccurrences(context.Background(), nil, rule, start.AddDate(0, 0, 3), end.AddDate(0, 0, 5))
	requireInvalidParams(t, err)
}

func requireInvalidParams(t *testing.T, err error) {
	t.Helper()
	var ormErr *orm.Error
	if !errors.As(err, &ormErr) || ormErr.Code != orm.ErrInvalidParams {
		t.Fatalf("want invalid params error, got %v", err)
	}
}
