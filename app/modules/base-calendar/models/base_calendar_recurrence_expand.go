package models

// File overview:
// - Responsibility: expands a recurrence rule into occurrence dates for a [from, to) window
//   (SRS-F-006). Pure date math, no database access.
// - Key symbols: `expandCalendarOccurrences`, `calendarOccurrences`.
// - Pattern: each frequency is a lazy `iter.Seq[time.Time]` that jumps straight to the window
//   start instead of walking every step from the rule's start date, and stops at the window end.

import (
	"iter"
	"time"
)

// maxCalendarOccurrencesPerRule caps one expansion so an absurd range or a
// one-day rule cannot allocate an unbounded slice.
const maxCalendarOccurrencesPerRule = 10000

// maxCalendarRecurrenceInterval bounds `interval`: the weekday walk skips whole
// weeks day by day until one matches, so an unbounded interval would spin a
// core before the first occurrence. Create/write refuse larger values; rows
// stored before that check expand to nothing.
const maxCalendarRecurrenceInterval = 1000

// expandCalendarOccurrences generates every occurrence date for rule that
// falls within [from, to), clamped to rule.EndDate when set (end_date is
// inclusive). An indefinite rule never produces more than the requested range
// worth of dates.
func expandCalendarOccurrences(rule *BaseCalendarRecurrenceRecord, from, to time.Time) []time.Time {
	rangeEnd := to
	if rule.EndDate != nil {
		if ruleEnd := rule.EndDate.AddDate(0, 0, 1); ruleEnd.Before(rangeEnd) {
			rangeEnd = ruleEnd
		}
	}
	out := make([]time.Time, 0, 32)
	for occurrence := range calendarOccurrences(rule, from) {
		if !occurrence.Before(rangeEnd) || len(out) >= maxCalendarOccurrencesPerRule {
			break
		}
		out = append(out, occurrence)
	}
	return out
}

// calendarOccurrences yields rule's occurrences at or after from, in
// ascending order and without an upper bound; the caller stops the loop.
func calendarOccurrences(rule *BaseCalendarRecurrenceRecord, from time.Time) iter.Seq[time.Time] {
	interval := rule.Interval
	if interval < 1 {
		interval = 1
	}
	if interval > maxCalendarRecurrenceInterval {
		return func(func(time.Time) bool) {}
	}
	switch rule.Frequency {
	case "daily":
		return calendarDaySteps(rule.StartDate, from, interval)
	case "weekly":
		return calendarDaySteps(rule.StartDate, from, 7*interval)
	case "biweekly":
		return calendarDaySteps(rule.StartDate, from, 14*interval)
	case "monthly":
		return calendarMonthSteps(rule.StartDate, from, interval)
	case "yearly":
		return calendarMonthSteps(rule.StartDate, from, 12*interval)
	case "weekday":
		return calendarWeekdaySteps(rule.StartDate, from, rule.ByWeekday, interval)
	case "monthday":
		day := 1
		if rule.ByMonthday != nil {
			day = *rule.ByMonthday
		}
		return calendarMonthdaySteps(rule.StartDate, from, interval, day)
	default:
		return func(func(time.Time) bool) {}
	}
}

// daysBetween is the calendar-day distance from a to b (negative when b is
// earlier), computed on each side's own y/m/d at UTC midnight so a DST shift
// (a 23h or 25h day) can never round a whole day away. Time-of-day is ignored;
// callers step back one unit and re-check against the real instants.
func daysBetween(a, b time.Time) int {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return int(time.Date(by, bm, bd, 0, 0, 0, 0, time.UTC).Sub(time.Date(ay, am, ad, 0, 0, 0, 0, time.UTC)) / (24 * time.Hour))
}

// calendarDaySteps yields start + n*stepDays for n >= 0, skipping those before from.
func calendarDaySteps(start, from time.Time, stepDays int) iter.Seq[time.Time] {
	return func(yield func(time.Time) bool) {
		n := 0
		if days := daysBetween(start, from); days > 0 {
			n = max(days/stepDays-1, 0) // one step back; the loop below corrects
		}
		for ; ; n++ {
			cur := start.AddDate(0, 0, n*stepDays)
			if cur.Before(from) {
				continue
			}
			if !yield(cur) {
				return
			}
		}
	}
}

// addMonthsClamped returns t shifted by months, clamping the day to the last
// day of a shorter target month (Jan 31 + 1 month = Feb 28) instead of rolling
// over into the next month. Each result is computed from the anchor, so
// clamping never drifts later occurrences.
func addMonthsClamped(t time.Time, months int) time.Time {
	first := time.Date(t.Year(), t.Month(), 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location()).AddDate(0, months, 0)
	return clampCalendarMonthDay(first, t.Day())
}

// calendarMonthSteps yields start + n*stepMonths months for n >= 0.
func calendarMonthSteps(start, from time.Time, stepMonths int) iter.Seq[time.Time] {
	return func(yield func(time.Time) bool) {
		n := 0
		if months := (from.Year()-start.Year())*12 + int(from.Month()-start.Month()); months > 0 {
			n = max(months/stepMonths-1, 0)
		}
		for ; ; n++ {
			cur := addMonthsClamped(start, n*stepMonths)
			if cur.Before(from) {
				continue
			}
			if !yield(cur) {
				return
			}
		}
	}
}

// calendarWeekdaySteps yields each day from start whose weekday is in
// weekdays (0=Sunday..6=Saturday). With interval N it keeps only every Nth
// Sunday-based week, counted from the week containing start.
func calendarWeekdaySteps(start, from time.Time, weekdays []int, interval int) iter.Seq[time.Time] {
	allowed := make(map[int]bool, len(weekdays))
	for _, wd := range weekdays {
		allowed[wd] = true
	}
	valid := false
	for wd := range allowed {
		valid = valid || (wd >= 0 && wd <= 6)
	}
	if !valid {
		// No usable weekday would make the day-by-day walk run forever.
		return func(func(time.Time) bool) {}
	}
	weekZero := start.AddDate(0, 0, -int(start.Weekday()))
	return func(yield func(time.Time) bool) {
		n := 0
		if days := daysBetween(start, from); days > 1 {
			n = days - 1 // one day back; the loop below skips anything before from
		}
		for ; ; n++ {
			cur := start.AddDate(0, 0, n)
			if cur.Before(from) || !allowed[int(cur.Weekday())] {
				continue
			}
			if interval > 1 {
				weekIndex := daysBetween(weekZero, cur.AddDate(0, 0, -int(cur.Weekday()))) / 7
				if weekIndex%interval != 0 {
					continue
				}
			}
			if !yield(cur) {
				return
			}
		}
	}
}

// calendarMonthdaySteps yields day-of-month `day` every stepMonths months,
// clamped to the month's last day (SRS-F-006 acceptance criterion #2: clamp,
// not skip). The first occurrence is never before start.
func calendarMonthdaySteps(start, from time.Time, stepMonths, day int) iter.Seq[time.Time] {
	monthStart := time.Date(start.Year(), start.Month(), 1, start.Hour(), start.Minute(), start.Second(), 0, start.Location())
	return func(yield func(time.Time) bool) {
		n := 0
		if months := (from.Year()-start.Year())*12 + int(from.Month()-start.Month()); months > 0 {
			n = max(months/stepMonths-1, 0)
		}
		for ; ; n++ {
			occurrence := clampCalendarMonthDay(monthStart.AddDate(0, n*stepMonths, 0), day)
			if occurrence.Before(start) || occurrence.Before(from) {
				continue
			}
			if !yield(occurrence) {
				return
			}
		}
	}
}
