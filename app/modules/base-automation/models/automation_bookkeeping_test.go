package models

// T71/T77: client writes cannot set cron bookkeeping; run survives a cleared executor hook.

import (
	"context"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestNormalizeAutomationCronValuesStripsBookkeeping(t *testing.T) {
	future := time.Now().Add(time.Hour)
	created, err := normalizeAutomationCronValues(orm.RecordMap{
		"name": "n", "interval_seconds": 60, "action_id": 1,
		"fail_count": 9, "last_run_status": "ok", "next_call": future,
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if created["fail_count"] != int64(0) || created["last_run_status"] != nil {
		t.Fatalf("create kept client bookkeeping: %v", created)
	}
	if created["next_call"] != future {
		t.Fatalf("create must keep a client-chosen first run time, got %v", created["next_call"])
	}

	written, err := normalizeAutomationCronValues(orm.RecordMap{
		"name": "renamed", "fail_count": 0, "last_run_status": "ok", "next_call": future,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"fail_count", "last_run_status", "next_call"} {
		if _, ok := written[field]; ok {
			t.Fatalf("write kept %s: %v", field, written)
		}
	}
	if written["name"] != "renamed" {
		t.Fatalf("legitimate field lost: %v", written)
	}
}

func TestNormalizeAutomationCronValuesReenableResetsFailCount(t *testing.T) {
	enabled, err := normalizeAutomationCronValues(orm.RecordMap{"active": true, "fail_count": 4}, false)
	if err != nil {
		t.Fatal(err)
	}
	if enabled["active"] != true || enabled["fail_count"] != int64(0) {
		t.Fatalf("re-enable must reset fail_count: %v", enabled)
	}
	paused, err := normalizeAutomationCronValues(orm.RecordMap{"active": false}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := paused["fail_count"]; ok || paused["active"] != false {
		t.Fatalf("pause must only write active: %v", paused)
	}
}

func TestRunActionWithoutExecutorReturnsError(t *testing.T) {
	previousAccess, previousHook := parentRecordAccess, actionExecutorHook
	t.Cleanup(func() { parentRecordAccess, actionExecutorHook = previousAccess, previousHook })
	parentRecordAccess = func(context.Context, string, string, []string) error { return nil }
	actionExecutorHook = nil
	if _, err := runAutomationAction(context.Background(), nil, []string{"4"}, nil, nil); err == nil {
		t.Fatal("run after shutdown must return an error, not panic")
	}
}
