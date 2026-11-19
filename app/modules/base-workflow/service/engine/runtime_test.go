package engine

// File overview:
// - Responsibility: verifies runtime bootstrap helpers for the workflow module.
// - Key symbols: focused Test cases for cron payload parsing.
// - Pattern: Locks down the scheduler-facing contract so tuning the sys.cron
//   payload keeps steering ResumeDueRuns batches predictably.

import "testing"

func TestResumeDueRunsLimitReadsPayload(t *testing.T) {
	tests := []struct {
		name    string
		payload map[string]any
		want    int64
	}{
		{name: "nil payload uses default", payload: nil, want: 50},
		{name: "missing key uses default", payload: map[string]any{}, want: 50},
		// JSONB numbers decode as float64 — the common runtime shape.
		{name: "jsonb float value", payload: map[string]any{"limit": float64(7)}, want: 7},
		{name: "int value", payload: map[string]any{"limit": 25}, want: 25},
		{name: "int64 value", payload: map[string]any{"limit": int64(9)}, want: 9},
		{name: "zero falls back to default", payload: map[string]any{"limit": float64(0)}, want: 50},
		{name: "negative falls back to default", payload: map[string]any{"limit": -3}, want: 50},
		{name: "non-numeric falls back to default", payload: map[string]any{"limit": "ten"}, want: 50},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resumeDueRunsLimit(tt.payload); got != tt.want {
				t.Fatalf("resumeDueRunsLimit(%#v) = %d, want %d", tt.payload, got, tt.want)
			}
		})
	}
}
