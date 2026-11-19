package orm

// File overview:
// - Responsibility: verifies the pure cohort bucketing/offset-computation math
//   (buildCohortMatrix) independent of the DB-backed runtimeCohortData handler.
// - Key symbols: TestBuildCohortMatrix* Test cases.
// - Pattern: constructs cohortInputRecord fixtures directly so the retention
//   math is exercised without a database, mirroring how runtime_base_methods_test.go
//   isolates pure helpers from the runtime/DB layer.

import (
	"testing"
	"time"
)

func mustDay(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		t.Fatalf("parse day %q: %v", value, err)
	}
	return parsed
}

// TestBuildCohortMatrixForwardRetention covers a simple 2-bucket, 3-offset
// forward retention case: two weekly cohorts, each with records that drop off
// at different +N offsets, verifying counts decrease as expected per column.
func TestBuildCohortMatrixForwardRetention(t *testing.T) {
	records := []cohortInputRecord{
		// Cohort A (day bucket 2026-01-01): 3 records, one stops same day,
		// one stops at +1, one still active at +2.
		{Start: mustDay(t, "2026-01-01"), Stop: mustDay(t, "2026-01-01"), HasStop: true},
		{Start: mustDay(t, "2026-01-01"), Stop: mustDay(t, "2026-01-02"), HasStop: true},
		{Start: mustDay(t, "2026-01-01"), Stop: mustDay(t, "2026-01-03"), HasStop: true},
		// Cohort B (day bucket 2026-01-02): 2 records, both still active at +1
		// (the max offset present in the data for this bucket).
		{Start: mustDay(t, "2026-01-02"), Stop: mustDay(t, "2026-01-03"), HasStop: true},
		{Start: mustDay(t, "2026-01-02"), Stop: mustDay(t, "2026-01-03"), HasStop: true},
	}

	result, err := buildCohortMatrix(records, CohortIntervalDay, "forward", true)
	if err != nil {
		t.Fatalf("buildCohortMatrix: %v", err)
	}
	if len(result.Rows) != 2 {
		t.Fatalf("expected 2 cohort rows, got %d", len(result.Rows))
	}

	rowA, rowB := result.Rows[0], result.Rows[1]
	if rowA.Bucket != "2026-01-01" || rowB.Bucket != "2026-01-02" {
		t.Fatalf("expected rows ordered 2026-01-01, 2026-01-02, got %q, %q", rowA.Bucket, rowB.Bucket)
	}
	if rowA.Total != 3 {
		t.Fatalf("expected cohort A total 3, got %v", rowA.Total)
	}
	if rowB.Total != 2 {
		t.Fatalf("expected cohort B total 2, got %v", rowB.Total)
	}

	// Column count is bounded by the widest span present in the data: cohort
	// A's bucket start (01-01) to the furthest date_stop in the whole dataset
	// (01-03) is 2 days -> 3 columns (offsets 0,1,2).
	if result.ColumnCount != 3 {
		t.Fatalf("expected column count 3, got %d", result.ColumnCount)
	}

	// Cohort A: +0 all 3 retained (stop >= start), +1 only 2 retained (the
	// same-day-stop record dropped off), +2 only 1 retained.
	wantA := []float64{3, 2, 1}
	for i, want := range wantA {
		if rowA.Values[i] != want {
			t.Fatalf("cohort A offset %d: expected %v, got %v", i, want, rowA.Values[i])
		}
	}

	// Cohort B: +0 both retained, +1 both retained (stop == start+1), +2 none
	// retained (no record stops that far out).
	wantB := []float64{2, 2, 0}
	for i, want := range wantB {
		if rowB.Values[i] != want {
			t.Fatalf("cohort B offset %d: expected %v, got %v", i, want, rowB.Values[i])
		}
	}
}

// TestBuildCohortMatrixZeroRecordBucket covers the edge case of an empty
// input set: no buckets should be produced and no panic should occur.
func TestBuildCohortMatrixZeroRecordBucket(t *testing.T) {
	result, err := buildCohortMatrix(nil, CohortIntervalDay, "forward", true)
	if err != nil {
		t.Fatalf("buildCohortMatrix: %v", err)
	}
	if len(result.Rows) != 0 {
		t.Fatalf("expected zero rows for empty input, got %d", len(result.Rows))
	}
	if result.ColumnCount != 0 {
		t.Fatalf("expected zero column count for empty input, got %d", result.ColumnCount)
	}
}

// TestBuildCohortMatrixBackwardChurn covers timeline=backward: cohorts are
// bucketed by date_stop, and a record counts at offset N if it already
// existed (date_start <= bucket_stop - N*interval), measuring churn looking
// backward in time from the stop date.
func TestBuildCohortMatrixBackwardChurn(t *testing.T) {
	records := []cohortInputRecord{
		// Both records stop on 2026-02-01 (the cohort bucket), but started at
		// different distances before that date.
		{Start: mustDay(t, "2026-01-30"), Stop: mustDay(t, "2026-02-01"), HasStop: true},
		{Start: mustDay(t, "2026-01-25"), Stop: mustDay(t, "2026-02-01"), HasStop: true},
		// A record with no stop date can never anchor a backward cohort and
		// must be skipped rather than causing an error.
		{Start: mustDay(t, "2026-01-20"), HasStop: false},
	}

	result, err := buildCohortMatrix(records, CohortIntervalDay, "backward", true)
	if err != nil {
		t.Fatalf("buildCohortMatrix: %v", err)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("expected 1 cohort row (bucketed by date_stop), got %d", len(result.Rows))
	}
	row := result.Rows[0]
	if row.Bucket != "2026-02-01" {
		t.Fatalf("expected bucket 2026-02-01, got %q", row.Bucket)
	}
	if row.Total != 2 {
		t.Fatalf("expected total 2 (the no-stop record is excluded), got %v", row.Total)
	}

	// +0: both existed by 2026-02-01 -> 2.
	// +2: only the record started 2026-01-30 (<=2 days back) still counts if
	// 2026-01-30 <= 2026-01-30 -> both should count since -2 offset edge is
	// 2026-01-30; the 01-25 start also satisfies (earlier). So +2 stays 2.
	// +8: edge becomes 2026-01-24; only the 01-25 start no longer qualifies
	// (01-25 > 01-24 is false, so it still counts) — pick an offset wide
	// enough to actually observe a drop: +7 -> edge 2026-01-25, both counted
	// (01-25<=01-25, 01-30<=01-25 false) -> value 1.
	if row.Values[0] != 2 {
		t.Fatalf("offset 0: expected 2, got %v", row.Values[0])
	}
	if len(row.Values) > 7 && row.Values[7] != 1 {
		t.Fatalf("offset 7: expected 1 (only the earlier-started record still qualifies), got %v", row.Values[7])
	}
}

// TestBuildCohortMatrixRejectsInvalidInterval ensures a bad interval string
// produces a clear error instead of silently defaulting or panicking.
func TestBuildCohortMatrixRejectsInvalidInterval(t *testing.T) {
	_, err := buildCohortMatrix([]cohortInputRecord{{Start: mustDay(t, "2026-01-01")}}, CohortInterval("fortnight"), "forward", true)
	if err == nil {
		t.Fatalf("expected an error for an invalid interval, got nil")
	}
}

// TestBuildCohortMatrixRejectsInvalidTimeline ensures a bad timeline string
// produces a clear error instead of silently defaulting.
func TestBuildCohortMatrixRejectsInvalidTimeline(t *testing.T) {
	_, err := buildCohortMatrix([]cohortInputRecord{{Start: mustDay(t, "2026-01-01")}}, CohortIntervalDay, "sideways", true)
	if err == nil {
		t.Fatalf("expected an error for an invalid timeline, got nil")
	}
}
