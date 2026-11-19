package models

import (
	"context"
	"sort"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
)

// legacyExpandCalendarOccurrences generates every occurrence date for rule that
// falls within [from, to), clamped to rule.EndDate when set (SRS-F-006:
// lazy, range-bounded RFC 5545-style expansion — an indefinite rule never
// produces more than the requested range worth of dates).
func legacyExpandCalendarOccurrences(rule *BaseCalendarRecurrenceRecord, from, to time.Time) []time.Time {
	interval := rule.Interval
	if interval < 1 {
		interval = 1
	}
	rangeEnd := to
	if rule.EndDate != nil {
		ruleEnd := rule.EndDate.AddDate(0, 0, 1) // end_date is inclusive
		if ruleEnd.Before(rangeEnd) {
			rangeEnd = ruleEnd
		}
	}
	out := make([]time.Time, 0, 32)
	switch rule.Frequency {
	case "daily":
		legacyExpandCalendarStep(rule.StartDate, rangeEnd, from, 0, interval, &out)
	case "weekly":
		legacyExpandCalendarStep(rule.StartDate, rangeEnd, from, 7*interval, 0, &out)
	case "biweekly":
		legacyExpandCalendarStep(rule.StartDate, rangeEnd, from, 14, 0, &out)
	case "monthly":
		legacyExpandCalendarMonthStep(rule.StartDate, rangeEnd, from, interval, &out)
	case "yearly":
		legacyExpandCalendarYearStep(rule.StartDate, rangeEnd, from, interval, &out)
	case "weekday":
		legacyExpandCalendarWeekdayStep(rule.StartDate, rangeEnd, from, rule.ByWeekday, &out)
	case "monthday":
		day := 1
		if rule.ByMonthday != nil {
			day = *rule.ByMonthday
		}
		legacyExpandCalendarMonthdayStep(rule.StartDate, rangeEnd, from, interval, day, &out)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

// legacyExpandCalendarStep walks start forward by dayStep or interval days,
// appending every occurrence within [from, rangeEnd) to out.
func legacyExpandCalendarStep(start, rangeEnd, from time.Time, dayStep, dailyInterval int, out *[]time.Time) {
	step := dayStep
	if step == 0 {
		step = dailyInterval
	}
	if step < 1 {
		step = 1
	}
	cur := start
	// Walk forward one interval at a time so validation and side effects stay scoped to the current element.
	for cur.Before(rangeEnd) {
		if !cur.Before(from) {
			*out = append(*out, cur)
		}
		cur = cur.AddDate(0, 0, step)
	}
}

// legacyExpandCalendarMonthStep walks start forward by interval months.
func legacyExpandCalendarMonthStep(start, rangeEnd, from time.Time, interval int, out *[]time.Time) {
	cur := start
	// Walk forward one interval at a time so validation and side effects stay scoped to the current element.
	for cur.Before(rangeEnd) {
		if !cur.Before(from) {
			*out = append(*out, cur)
		}
		cur = cur.AddDate(0, interval, 0)
	}
}

// legacyExpandCalendarYearStep walks start forward by interval years.
func legacyExpandCalendarYearStep(start, rangeEnd, from time.Time, interval int, out *[]time.Time) {
	cur := start
	// Walk forward one interval at a time so validation and side effects stay scoped to the current element.
	for cur.Before(rangeEnd) {
		if !cur.Before(from) {
			*out = append(*out, cur)
		}
		cur = cur.AddDate(interval, 0, 0)
	}
}

// legacyExpandCalendarWeekdayStep walks day by day from start, keeping only dates
// whose weekday is in weekdays (0=Sunday..6=Saturday).
func legacyExpandCalendarWeekdayStep(start, rangeEnd, from time.Time, weekdays []int, out *[]time.Time) {
	allowed := make(map[int]bool, len(weekdays))
	for _, wd := range weekdays {
		allowed[wd] = true
	}
	cur := start
	// Walk forward one interval at a time so validation and side effects stay scoped to the current element.
	for cur.Before(rangeEnd) {
		if !cur.Before(from) && allowed[int(cur.Weekday())] {
			*out = append(*out, cur)
		}
		cur = cur.AddDate(0, 0, 1)
	}
}

// legacyExpandCalendarMonthdayStep walks month by month, clamping day to the last
// day of shorter months (SRS-F-006 acceptance criterion #2: clamp, not skip).
func legacyExpandCalendarMonthdayStep(start, rangeEnd, from time.Time, interval, day int, out *[]time.Time) {
	cur := time.Date(start.Year(), start.Month(), 1, start.Hour(), start.Minute(), start.Second(), 0, start.Location())
	// Walk forward one interval at a time so validation and side effects stay scoped to the current element.
	for cur.Before(rangeEnd) {
		occurrence := clampCalendarMonthDay(cur, day)
		if !occurrence.Before(start) && !occurrence.Before(from) && occurrence.Before(rangeEnd) {
			*out = append(*out, occurrence)
		}
		cur = cur.AddDate(0, interval, 0)
	}
}

func sameDates(a, b []time.Time) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].Equal(b[i]) {
			return false
		}
	}
	return true
}

// TestExpandMatchesLegacyWhereSemanticsAgree is the golden check: for every
// frequency and for starts that do not hit the legacy drift/ignored-interval
// bugs, the iterator expansion must equal the old step-by-step walk.
func TestExpandMatchesLegacyWhereSemanticsAgree(t *testing.T) {
	day := 17
	end := mustParseCalendarDate(t, "2027-03-20")
	starts := []string{"2026-01-05", "2026-03-14", "2025-11-28"} // all day <= 28
	windows := [][2]string{
		{"2026-01-01", "2026-02-01"}, {"2026-02-10", "2026-06-30"}, {"2026-07-01", "2027-01-01"}, {"2024-01-01", "2025-06-01"},
	}
	for _, start := range starts {
		for _, w := range windows {
			for _, cfg := range []BaseCalendarRecurrenceRecord{
				{Frequency: "daily", Interval: 1}, {Frequency: "daily", Interval: 3},
				{Frequency: "weekly", Interval: 1}, {Frequency: "weekly", Interval: 2},
				{Frequency: "biweekly", Interval: 1},
				{Frequency: "monthly", Interval: 1}, {Frequency: "monthly", Interval: 4},
				{Frequency: "yearly", Interval: 1},
				{Frequency: "weekday", Interval: 1, ByWeekday: []int{1, 3, 6}},
				{Frequency: "monthday", Interval: 1, ByMonthday: &day}, {Frequency: "monthday", Interval: 2, ByMonthday: &day},
				{Frequency: "daily", Interval: 2, EndDate: &end},
			} {
				rule := cfg
				rule.StartDate = mustParseCalendarDate(t, start)
				from, to := mustParseCalendarDate(t, w[0]), mustParseCalendarDate(t, w[1])
				want := legacyExpandCalendarOccurrences(&rule, from, to)
				got := expandCalendarOccurrences(&rule, from, to)
				if !sameDates(want, got) {
					t.Errorf("%s/%d start=%s window=%v\n legacy=%v\n new   =%v", rule.Frequency, rule.Interval, start, w, want, got)
				}
			}
		}
	}
}

func TestExpandBiweeklyAndWeekdayHonourInterval(t *testing.T) {
	from, to := mustParseCalendarDate(t, "2026-08-03"), mustParseCalendarDate(t, "2026-10-01")
	biweekly := &BaseCalendarRecurrenceRecord{Frequency: "biweekly", Interval: 2, StartDate: from}
	got := expandCalendarOccurrences(biweekly, from, to)
	if len(got) < 2 || got[1].Sub(got[0]) != 28*24*time.Hour {
		t.Fatalf("biweekly interval 2 must be 28 days apart, got %v", got)
	}

	weekday := &BaseCalendarRecurrenceRecord{Frequency: "weekday", Interval: 2, ByWeekday: []int{1}, StartDate: from} // Mondays, every 2nd week
	mondays := expandCalendarOccurrences(weekday, from, to)
	if len(mondays) < 2 || mondays[1].Sub(mondays[0]) != 14*24*time.Hour {
		t.Fatalf("weekday interval 2 must skip alternate weeks, got %v", mondays)
	}
}

func TestExpandMonthlyClampsInsteadOfDrifting(t *testing.T) {
	rule := &BaseCalendarRecurrenceRecord{Frequency: "monthly", Interval: 1, StartDate: mustParseCalendarDate(t, "2026-01-31")}
	got := expandCalendarOccurrences(rule, mustParseCalendarDate(t, "2026-01-01"), mustParseCalendarDate(t, "2026-05-01"))
	want := []string{"2026-01-31", "2026-02-28", "2026-03-31", "2026-04-30"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i].Format("2006-01-02") != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestExpandFarFutureWindowIsCheapAndBounded(t *testing.T) {
	rule := &BaseCalendarRecurrenceRecord{Frequency: "daily", Interval: 1, StartDate: mustParseCalendarDate(t, "1990-01-01")}
	got := expandCalendarOccurrences(rule, mustParseCalendarDate(t, "2026-08-01"), mustParseCalendarDate(t, "2026-08-08"))
	if len(got) != 7 || got[0].Format("2006-01-02") != "2026-08-01" {
		t.Fatalf("got %v", got)
	}
	if n := len(expandCalendarOccurrences(rule, mustParseCalendarDate(t, "1990-01-01"), mustParseCalendarDate(t, "2100-01-01"))); n != maxCalendarOccurrencesPerRule {
		t.Fatalf("cap not applied: %d", n)
	}
	bad := &BaseCalendarRecurrenceRecord{Frequency: "weekday", ByWeekday: []int{9}, StartDate: rule.StartDate}
	if got := expandCalendarOccurrences(bad, rule.StartDate, rule.StartDate.AddDate(1, 0, 0)); len(got) != 0 {
		t.Fatalf("invalid weekdays must yield nothing, got %v", got)
	}
}

func TestLoadExceptionsByRuleGroupsInOneQuery(t *testing.T) {
	calls := 0
	restore := orm.StubRuntimeRepository(BaseCalendarRecurrenceExceptionModelName, orm.RuntimeRepositoryStub{
		SearchRead: func(_ context.Context, filter orm.RecordMap, _ []string) ([]orm.RecordMap, error) {
			calls++
			if _, ok := filter["__domain"].(orm.DomainNode); !ok {
				t.Fatalf("expected one __domain IN filter, got %v", filter)
			}
			return []orm.RecordMap{
				{"id": int64(1), "recurrence_id": int64(10), "is_skipped": true, "original_occurrence_date": "2026-08-10"},
				{"id": int64(2), "recurrence_id": int64(11), "override_date": "2026-08-12", "original_occurrence_date": "2026-08-11"},
				{"id": int64(3), "recurrence_id": int64(10), "is_skipped": true, "original_occurrence_date": "2026-08-17"},
			}, nil
		},
	})
	defer restore()

	got, err := loadBaseCalendarRecurrenceExceptionsByRule(context.Background(), []int64{10, 11, 12})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(got[10]) != 2 || len(got[11]) != 1 || len(got[12]) != 0 {
		t.Fatalf("calls=%d grouped=%v", calls, got)
	}
	if got[11][0].OverrideDate == nil {
		t.Fatal("override date must be parsed")
	}
}
