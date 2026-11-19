package models

// File overview:
// - Responsibility: locks down the recurrence expansion date math (SRS-F-006 acceptance criteria)
//   independent of the database, since these are pure functions of a rule + a date range.
// - Key symbols: focused Test cases for expandCalendarOccurrences, clampCalendarMonthDay,
//   applyBaseCalendarException.

import (
	"testing"
	"time"
)

func mustParseCalendarDate(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		t.Fatalf("failed to parse test date %q: %v", value, err)
	}
	return parsed
}

func TestExpandCalendarOccurrencesWeeklyBiweekly(t *testing.T) {
	from := mustParseCalendarDate(t, "2026-08-01")
	to := mustParseCalendarDate(t, "2026-09-01")

	weekly := &BaseCalendarRecurrenceRecord{
		Frequency: "weekly",
		Interval:  2,
		StartDate: mustParseCalendarDate(t, "2026-08-03"),
	}
	occurrences := expandCalendarOccurrences(weekly, from, to)
	if len(occurrences) < 2 {
		t.Fatalf("expected at least 2 occurrences, got %d", len(occurrences))
	}
	if diff := occurrences[1].Sub(occurrences[0]); diff != 14*24*time.Hour {
		t.Fatalf("expected weekly interval=2 occurrences 14 days apart, got %v apart", diff)
	}

	biweekly := &BaseCalendarRecurrenceRecord{
		Frequency: "biweekly",
		Interval:  1,
		StartDate: mustParseCalendarDate(t, "2026-08-03"),
	}
	biOccurrences := expandCalendarOccurrences(biweekly, from, to)
	if len(biOccurrences) < 2 {
		t.Fatalf("expected at least 2 biweekly occurrences, got %d", len(biOccurrences))
	}
	if diff := biOccurrences[1].Sub(biOccurrences[0]); diff != 14*24*time.Hour {
		t.Fatalf("expected biweekly occurrences 14 days apart, got %v apart", diff)
	}
}

func TestExpandCalendarOccurrencesMonthdayClampsToLastDay(t *testing.T) {
	day := 31
	rule := &BaseCalendarRecurrenceRecord{
		Frequency:  "monthday",
		Interval:   1,
		ByMonthday: &day,
		StartDate:  mustParseCalendarDate(t, "2026-01-31"),
	}
	from := mustParseCalendarDate(t, "2026-01-01")
	to := mustParseCalendarDate(t, "2026-04-01")

	occurrences := expandCalendarOccurrences(rule, from, to)
	found := map[string]bool{}
	for _, occ := range occurrences {
		found[occ.Format("2006-01-02")] = true
	}
	if !found["2026-01-31"] {
		t.Fatalf("expected Jan 31 occurrence, got %v", occurrences)
	}
	if !found["2026-02-28"] {
		t.Fatalf("expected clamp to Feb 28 (non-leap year), got %v", occurrences)
	}
	if !found["2026-03-31"] {
		t.Fatalf("expected March 31 occurrence, got %v", occurrences)
	}
}

func TestExpandCalendarOccurrencesRespectsEndDate(t *testing.T) {
	endDate := mustParseCalendarDate(t, "2026-08-15")
	rule := &BaseCalendarRecurrenceRecord{
		Frequency: "daily",
		Interval:  1,
		StartDate: mustParseCalendarDate(t, "2026-08-01"),
		EndDate:   &endDate,
	}
	from := mustParseCalendarDate(t, "2026-08-01")
	to := mustParseCalendarDate(t, "2026-12-01") // far beyond end_date

	occurrences := expandCalendarOccurrences(rule, from, to)
	for _, occ := range occurrences {
		if occ.After(endDate) {
			t.Fatalf("expected no occurrence after end_date %v, got %v", endDate, occ)
		}
	}
	if got := len(occurrences); got != 15 {
		t.Fatalf("expected 15 daily occurrences from Aug 1 to Aug 15 inclusive, got %d", got)
	}
}

func TestExpandCalendarOccurrencesWeekdaySelection(t *testing.T) {
	rule := &BaseCalendarRecurrenceRecord{
		Frequency: "weekday",
		ByWeekday: []int{1, 3}, // Monday, Wednesday
		StartDate: mustParseCalendarDate(t, "2026-08-01"),
	}
	from := mustParseCalendarDate(t, "2026-08-01")
	to := mustParseCalendarDate(t, "2026-08-15")

	occurrences := expandCalendarOccurrences(rule, from, to)
	for _, occ := range occurrences {
		weekday := int(occ.Weekday())
		if weekday != 1 && weekday != 3 {
			t.Fatalf("expected only Monday/Wednesday occurrences, got %v (weekday=%d)", occ, weekday)
		}
	}
	if len(occurrences) == 0 {
		t.Fatalf("expected at least one Monday/Wednesday occurrence in range")
	}
}

func TestApplyBaseCalendarExceptionSkipAndOverride(t *testing.T) {
	occDate := mustParseCalendarDate(t, "2026-08-10")
	overrideDate := mustParseCalendarDate(t, "2026-08-11")

	skipped := []BaseCalendarRecurrenceExceptionRecord{{
		OriginalOccurrenceDate: occDate,
		IsSkipped:              true,
	}}
	if _, skip := applyBaseCalendarException(occDate, skipped); !skip {
		t.Fatalf("expected occurrence to be skipped")
	}

	overridden := []BaseCalendarRecurrenceExceptionRecord{{
		OriginalOccurrenceDate: occDate,
		OverrideDate:           &overrideDate,
	}}
	effective, skip := applyBaseCalendarException(occDate, overridden)
	if skip {
		t.Fatalf("expected occurrence not to be skipped")
	}
	if !effective.Equal(overrideDate) {
		t.Fatalf("expected effective date %v, got %v", overrideDate, effective)
	}
}

func TestValidateBaseCalendarRecurrencePatternRejectsInvalidFrequency(t *testing.T) {
	err := validateBaseCalendarRecurrencePattern(map[string]any{"frequency": "hourly"})
	if err == nil {
		t.Fatalf("expected error for invalid frequency")
	}
}

func TestValidateBaseCalendarRecurrencePatternRequiresWeekdayForWeekly(t *testing.T) {
	err := validateBaseCalendarRecurrencePattern(map[string]any{"frequency": "weekly"})
	if err == nil {
		t.Fatalf("expected error when weekly frequency is missing by_weekday")
	}
}

func TestValidateBaseCalendarRecurrencePatternRejectsEndBeforeStart(t *testing.T) {
	err := validateBaseCalendarRecurrencePattern(map[string]any{
		"start_date": "2026-08-10",
		"end_date":   "2026-08-01",
	})
	if err == nil {
		t.Fatalf("expected error when end_date is before start_date")
	}
}
