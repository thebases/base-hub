package models

import (
	"context"
	"errors"
	"testing"
)

func TestPurgeInBatchesStopsOnShortBatch(t *testing.T) {
	remaining := int64(1200)
	calls := 0
	total, err := purgeInBatches(context.Background(), 500, 50000, func(_ context.Context, limit int) (int64, error) {
		calls++
		n := min(remaining, int64(limit))
		remaining -= n
		return n, nil
	})
	if err != nil || total != 1200 || calls != 3 {
		t.Fatalf("total=%d calls=%d err=%v", total, calls, err)
	}
}

func TestPurgeInBatchesBoundedByMaxTotal(t *testing.T) {
	calls := 0
	total, _ := purgeInBatches(context.Background(), 500, 1500, func(_ context.Context, limit int) (int64, error) {
		calls++
		return int64(limit), nil
	})
	if total != 1500 || calls != 3 {
		t.Fatalf("total=%d calls=%d", total, calls)
	}
}

func TestPurgeInBatchesStopsAtDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	total, err := purgeInBatches(ctx, 10, 1000, func(_ context.Context, limit int) (int64, error) {
		calls++
		cancel()
		return int64(limit), nil
	})
	if err != nil || total != 10 || calls != 1 {
		t.Fatalf("total=%d calls=%d err=%v", total, calls, err)
	}
}

func TestPurgeInBatchesReturnsErrorWithPartialCount(t *testing.T) {
	boom := errors.New("boom")
	calls := 0
	total, err := purgeInBatches(context.Background(), 5, 100, func(_ context.Context, limit int) (int64, error) {
		calls++
		if calls == 2 {
			return 0, boom
		}
		return int64(limit), nil
	})
	if !errors.Is(err, boom) || total != 5 {
		t.Fatalf("total=%d err=%v", total, err)
	}
}

func TestRetentionStatusSelectionNeverIncludesResumable(t *testing.T) {
	for _, s := range []string{WorkflowRunStatusPending, WorkflowRunStatusRunning, WorkflowRunStatusRetryWaiting} {
		for _, term := range workflowRunTerminalStatuses {
			if s == term {
				t.Fatalf("resumable run status %q is purgeable", s)
			}
		}
	}
	for _, s := range []string{OutboundDeliveryStatusQueued, OutboundDeliveryStatusSending, OutboundDeliveryStatusRetryableFailed} {
		for _, term := range outboundTerminalStatuses {
			if s == term {
				t.Fatalf("in-flight delivery status %q is purgeable", s)
			}
		}
	}
	for _, s := range workflowRunTerminalStatuses {
		if !isValidWorkflowRunStatus(s) {
			t.Fatalf("unknown run status %q", s)
		}
	}
}

func TestParseRetentionDays(t *testing.T) {
	cases := map[string]int{"": 30, `"45"`: 45, "7": 7, "0": 0, `"0"`: 0, "-3": 0, "abc": 30}
	for raw, want := range cases {
		if got := parseRetentionDays(raw); got != want {
			t.Errorf("parseRetentionDays(%q)=%d want %d", raw, got, want)
		}
	}
}
