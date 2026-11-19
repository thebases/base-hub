package orm

// File overview:
// - Responsibility: implements the `cohort_data` runtime RPC — a date-bucketed
//   retention/churn matrix aggregation distinct from `read_group`'s
//   field-value bucketing (see runtime_base_methods.go).
// - Key symbols: `runtimeCohortData` (RPC handler, DB-backed), `buildCohortMatrix`
//   (pure aggregation math, unit-testable without a DB).
// - Pattern: keeps the DB-fetching handler and the pure bucketing/offset math
//   in separate functions so the aggregation logic can be exercised by fast
//   unit tests, matching how `runtimeGroupRecords` is structured.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// CohortInterval enumerates the supported cohort bucket granularities.
type CohortInterval string

// Supported cohort bucket intervals.
const (
	CohortIntervalDay   CohortInterval = "day"
	CohortIntervalWeek  CohortInterval = "week"
	CohortIntervalMonth CohortInterval = "month"
	CohortIntervalYear  CohortInterval = "year"
)

// maxCohortOffsetColumns caps the number of +N offset columns cohort_data
// returns regardless of how wide the underlying date span is, so a
// multi-year dataset bucketed by day cannot blow up into thousands of
// columns the frontend has to render. Chosen as a round number comfortably
// above the common reporting horizons (12 months, ~2 years of weeks) while
// still bounding worst-case payload size.
const maxCohortOffsetColumns = 24

// CohortRow is one cohort bucket (one row) of the retention/churn matrix.
type CohortRow struct {
	Bucket string    `json:"bucket"` // canonical bucket-start label ("2026-01-02" for day/week, "2026-01" for month, "2026" for year)
	Total  float64   `json:"total"`  // count or measure-sum of every record in this cohort bucket, independent of retention
	Values []float64 `json:"values"` // per-offset retained count/measure-sum, aligned by offset index 0..column_count-1
}

// CohortResult is the full cohort_data RPC response payload.
type CohortResult struct {
	Interval    string      `json:"interval"`
	Timeline    string      `json:"timeline"`
	Measure     string      `json:"measure"`
	ColumnCount int         `json:"column_count"`
	Rows        []CohortRow `json:"rows"`
}

// cohortInputRecord is the minimal per-record shape the pure aggregation math
// needs, decoupled from RecordMap so buildCohortMatrix can be unit tested
// without touching the runtime/DB layer at all.
type cohortInputRecord struct {
	Start   time.Time
	Stop    time.Time
	HasStop bool
	Measure float64
}

// runtimeCohortData handles the `cohort_data` runtime RPC. It buckets records
// by a `date_start`-style field truncated to an interval (the cohort row),
// then for each bucket computes retention/churn at each +N interval offset
// using a `date_stop`-style field — a genuinely different aggregation shape
// than read_group's field-value bucketing (runtimeGroupRecords).
func runtimeCohortData(ctx context.Context, model *ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	if model == nil {
		return nil, fmt.Errorf("orm: model metadata is required")
	}

	// All cohort-specific parameters are kwargs-only (not positional) because
	// args[0] is already reserved for the domain by runtimeSearchFilter's
	// convention (shared with read_group/search_read).
	dateStartField := strings.TrimSpace(runtimeStringArg(nil, kwargs, "date_start", -1))
	if dateStartField == "" {
		return nil, fmt.Errorf("orm: cohort_data requires a non-empty date_start field")
	}
	dateStopField := strings.TrimSpace(runtimeStringArg(nil, kwargs, "date_stop", -1))
	if dateStopField == "" {
		return nil, fmt.Errorf("orm: cohort_data requires a non-empty date_stop field")
	}

	intervalRaw := strings.ToLower(strings.TrimSpace(runtimeStringArg(nil, kwargs, "interval", -1)))
	if intervalRaw == "" {
		intervalRaw = string(CohortIntervalDay)
	}
	interval := CohortInterval(intervalRaw)
	switch interval {
	case CohortIntervalDay, CohortIntervalWeek, CohortIntervalMonth, CohortIntervalYear:
	default:
		return nil, fmt.Errorf("orm: cohort_data interval must be one of day|week|month|year, got %q", intervalRaw)
	}

	timeline := strings.ToLower(strings.TrimSpace(runtimeStringArg(nil, kwargs, "timeline", -1)))
	if timeline == "" {
		timeline = "forward"
	}
	if timeline != "forward" && timeline != "backward" {
		return nil, fmt.Errorf("orm: cohort_data timeline must be forward or backward, got %q", timeline)
	}

	measure := strings.TrimSpace(runtimeStringArg(nil, kwargs, "measure", -1))
	if measure == "" {
		measure = "__count"
	}

	// Validate the requested fields actually exist and are the right kind so
	// a typo in the arch produces a clear error instead of a silently empty
	// or nonsensical matrix.
	startField, ok := runtimeLookupField(model, dateStartField)
	if !ok {
		return nil, fmt.Errorf("orm: cohort_data date_start field %q does not exist on model %q", dateStartField, model.Name)
	}
	if startField.Kind != FieldKindDate && startField.Kind != FieldKindDateTime {
		return nil, fmt.Errorf("orm: cohort_data date_start field %q must be a date/datetime field", dateStartField)
	}
	stopField, ok := runtimeLookupField(model, dateStopField)
	if !ok {
		return nil, fmt.Errorf("orm: cohort_data date_stop field %q does not exist on model %q", dateStopField, model.Name)
	}
	if stopField.Kind != FieldKindDate && stopField.Kind != FieldKindDateTime {
		return nil, fmt.Errorf("orm: cohort_data date_stop field %q must be a date/datetime field", dateStopField)
	}
	if measure != "__count" {
		measureField, ok := runtimeLookupField(model, measure)
		if !ok {
			return nil, fmt.Errorf("orm: cohort_data measure field %q does not exist on model %q", measure, model.Name)
		}
		if measureField.Kind != FieldKindFloat && measureField.Kind != FieldKindInteger && measureField.Kind != FieldKindID {
			return nil, fmt.Errorf("orm: cohort_data measure field %q must be a numeric field", measure)
		}
	}

	filter, err := runtimeSearchFilter(ctx, model, args, kwargs)
	if err != nil {
		return nil, err
	}

	fetchFields := []string{dateStartField, dateStopField}
	if measure != "__count" {
		fetchFields = append(fetchFields, measure)
	}
	records, err := runtimeRepositorySearchRead(ctx, model, filter, fetchFields)
	if err != nil {
		return nil, err
	}

	inputRecords := runtimeCohortRecords(records, dateStartField, dateStopField, measure)
	result, err := buildCohortMatrix(inputRecords, interval, timeline, measure == "__count")
	if err != nil {
		return nil, err
	}
	result.Measure = measure
	return result, nil
}

// runtimeCohortRecords extracts the minimal (start, stop, measure) shape the
// pure aggregation math needs from raw runtime RecordMaps. Records with no
// usable date_start value are skipped rather than failing the whole request,
// since a sparse/optional date field is a normal data condition rather than
// a malformed request.
func runtimeCohortRecords(records []RecordMap, dateStartField, dateStopField, measure string) []cohortInputRecord {
	useCount := measure == "__count"
	out := make([]cohortInputRecord, 0, len(records))
	for _, record := range records {
		start, ok := runtimeCoerceCohortTime(record[dateStartField])
		if !ok {
			continue
		}
		stop, hasStop := runtimeCoerceCohortTime(record[dateStopField])
		rec := cohortInputRecord{Start: start, Stop: stop, HasStop: hasStop}
		if !useCount {
			rec.Measure = toFloat64(record[measure])
		}
		out = append(out, rec)
	}
	return out
}

// runtimeCoerceCohortTime normalizes a runtime field value into a time.Time,
// accepting the concrete shapes the search/read layer may hand back (native
// time.Time from Bun/pgx scanning, or an RFC3339/date-only string when a
// value arrives pre-serialized).
func runtimeCoerceCohortTime(value any) (time.Time, bool) {
	switch typed := value.(type) {
	case time.Time:
		if typed.IsZero() {
			return time.Time{}, false
		}
		return typed, true
	case *time.Time:
		if typed == nil || typed.IsZero() {
			return time.Time{}, false
		}
		return *typed, true
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return time.Time{}, false
		}
		layouts := []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"}
		for _, layout := range layouts {
			if parsed, err := time.Parse(layout, text); err == nil {
				return parsed, true
			}
		}
		return time.Time{}, false
	default:
		return time.Time{}, false
	}
}

// cohortBucketStart truncates a timestamp to the start of its interval
// bucket (UTC), matching the truncation semantics of a Postgres
// `date_trunc` call without needing a round-trip through SQL.
func cohortBucketStart(t time.Time, interval CohortInterval) (time.Time, error) {
	t = t.UTC()
	switch interval {
	case CohortIntervalDay:
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), nil
	case CohortIntervalWeek:
		day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		// ISO week starts Monday; Go's Weekday() returns Sunday=0, so shift it
		// into a Monday-based offset before subtracting back to the week start.
		offset := (int(day.Weekday()) + 6) % 7
		return day.AddDate(0, 0, -offset), nil
	case CohortIntervalMonth:
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC), nil
	case CohortIntervalYear:
		return time.Date(t.Year(), 1, 1, 0, 0, 0, 0, time.UTC), nil
	default:
		return time.Time{}, fmt.Errorf("orm: unsupported cohort interval %q", interval)
	}
}

// cohortAddInterval advances (or, with a negative n, retreats) a bucket-start
// timestamp by n whole intervals.
func cohortAddInterval(t time.Time, interval CohortInterval, n int) time.Time {
	switch interval {
	case CohortIntervalDay:
		return t.AddDate(0, 0, n)
	case CohortIntervalWeek:
		return t.AddDate(0, 0, 7*n)
	case CohortIntervalMonth:
		return t.AddDate(0, n, 0)
	case CohortIntervalYear:
		return t.AddDate(n, 0, 0)
	default:
		return t
	}
}

// cohortIntervalsBetween counts how many whole intervals separate start from
// end (0 when end is not after start). Used only to size the offset-column
// cap from the actual data span, not to compute per-cell values.
func cohortIntervalsBetween(start, end time.Time, interval CohortInterval) int {
	if !end.After(start) {
		return 0
	}
	count := 0
	cursor := start
	for cursor.Before(end) {
		cursor = cohortAddInterval(cursor, interval, 1)
		count++
		// Safety valve against pathological inputs (e.g. a decades-wide span
		// bucketed by day) — precision beyond the column cap is never used.
		if count > maxCohortOffsetColumns*4 {
			break
		}
	}
	return count
}

// cohortBucketLabel renders a bucket-start timestamp into the row label the
// frontend displays, matching the granularity of the interval.
func cohortBucketLabel(t time.Time, interval CohortInterval) string {
	switch interval {
	case CohortIntervalMonth:
		return t.Format("2006-01")
	case CohortIntervalYear:
		return t.Format("2006")
	default: // day, week: label by the bucket's start date
		return t.Format("2006-01-02")
	}
}

// buildCohortMatrix is the pure aggregation core of cohort_data: given a flat
// set of (date_start, date_stop, measure) records, it buckets rows into
// cohorts and computes the +N offset retention/churn matrix. Kept free of any
// DB/runtime dependency so it can be unit tested directly.
//
// Retention semantics:
//   - timeline=forward (default): rows bucket by date_start; a record is
//     "retained" at offset N if date_stop >= bucket_start + N*interval (still
//     active N periods after joining the cohort).
//   - timeline=backward: rows bucket by date_stop instead (the cohort is
//     "when did this record stop"); a record counts at offset N if
//     date_start <= bucket_start - N*interval (it already existed N periods
//     before the stop date), i.e. churn measured backward in time.
//
// Column count is the widest span, in intervals, between any cohort's bucket
// start and the furthest boundary date actually present in the data,
// capped at maxCohortOffsetColumns.
func buildCohortMatrix(records []cohortInputRecord, interval CohortInterval, timeline string, useCount bool) (CohortResult, error) {
	switch interval {
	case CohortIntervalDay, CohortIntervalWeek, CohortIntervalMonth, CohortIntervalYear:
	default:
		return CohortResult{}, fmt.Errorf("orm: unsupported cohort interval %q", interval)
	}
	if timeline != "forward" && timeline != "backward" {
		return CohortResult{}, fmt.Errorf("orm: unsupported cohort timeline %q", timeline)
	}

	type bucketAgg struct {
		start   time.Time
		total   float64
		members []cohortInputRecord
	}
	buckets := map[int64]*bucketAgg{}
	var globalBoundary time.Time
	haveBoundary := false

	for _, record := range records {
		anchor := record.Start
		if timeline == "backward" {
			// A backward cohort is anchored on when the record stopped; skip
			// records with no stop value since they have no bucket to join.
			if !record.HasStop {
				continue
			}
			anchor = record.Stop
		}
		bucketStart, err := cohortBucketStart(anchor, interval)
		if err != nil {
			return CohortResult{}, err
		}
		key := bucketStart.Unix()
		agg, ok := buckets[key]
		if !ok {
			agg = &bucketAgg{start: bucketStart}
			buckets[key] = agg
		}
		agg.members = append(agg.members, record)
		if useCount {
			agg.total++
		} else {
			agg.total += record.Measure
		}

		// Track the boundary date actually present in the data (the field
		// opposite the anchor) so the column count reflects real span rather
		// than an arbitrary/unbounded value. Forward offsets extend into the
		// future from the bucket start, so the relevant boundary is the
		// latest date_stop seen; backward offsets extend into the past from
		// the bucket start, so the relevant boundary is the earliest
		// date_start seen.
		boundary := record.Stop
		hasBoundary := record.HasStop
		if timeline == "backward" {
			boundary = record.Start
			hasBoundary = true
		}
		if !hasBoundary {
			continue
		}
		if !haveBoundary {
			globalBoundary = boundary
			haveBoundary = true
		} else if timeline == "forward" && boundary.After(globalBoundary) {
			globalBoundary = boundary
		} else if timeline == "backward" && boundary.Before(globalBoundary) {
			globalBoundary = boundary
		}
	}

	if len(buckets) == 0 {
		return CohortResult{Interval: string(interval), Timeline: timeline, ColumnCount: 0, Rows: []CohortRow{}}, nil
	}

	// Size the offset-column count from the widest real data span, capped so
	// the matrix cannot grow unbounded for a long-running dataset bucketed at
	// fine granularity (e.g. years of daily buckets).
	columnCount := 1
	for _, agg := range buckets {
		span := 1
		if haveBoundary {
			if timeline == "forward" {
				span = cohortIntervalsBetween(agg.start, globalBoundary, interval) + 1
			} else {
				span = cohortIntervalsBetween(globalBoundary, agg.start, interval) + 1
			}
		}
		if span > columnCount {
			columnCount = span
		}
	}
	if columnCount > maxCohortOffsetColumns {
		columnCount = maxCohortOffsetColumns
	}

	// Sort bucket keys (unix seconds of bucket start) ascending so rows render
	// in chronological cohort order regardless of Go map iteration order.
	keys := make([]int64, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	rows := make([]CohortRow, 0, len(keys))
	for _, key := range keys {
		agg := buckets[key]
		values := make([]float64, columnCount)
		for offset := 0; offset < columnCount; offset++ {
			var edge time.Time
			if timeline == "forward" {
				edge = cohortAddInterval(agg.start, interval, offset)
			} else {
				edge = cohortAddInterval(agg.start, interval, -offset)
			}
			var value float64
			for _, member := range agg.members {
				var retained bool
				if timeline == "forward" {
					retained = member.HasStop && !member.Stop.Before(edge)
				} else {
					retained = !member.Start.After(edge)
				}
				if !retained {
					continue
				}
				if useCount {
					value++
				} else {
					value += member.Measure
				}
			}
			values[offset] = value
		}
		rows = append(rows, CohortRow{
			Bucket: cohortBucketLabel(agg.start, interval),
			Total:  agg.total,
			Values: values,
		})
	}

	return CohortResult{
		Interval:    string(interval),
		Timeline:    timeline,
		ColumnCount: columnCount,
		Rows:        rows,
	}, nil
}
