package models

// T65/T73: mark_done reads many2one values as [id, display_name], and done + delete + chain is one
// locked transaction (in-memory doubles stand in for the row lock and rollback).

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
)

// activityFixture is an in-memory tbl.discuss_activity table plus counters for what mark_done did.
type activityFixture struct {
	mu        sync.Mutex
	rows      map[string]orm.RecordMap
	chained   []orm.RecordMap
	messages  int
	failChain bool
}

func newActivityFixture(t *testing.T) *activityFixture {
	t.Helper()
	f := &activityFixture{rows: map[string]orm.RecordMap{
		"10": {
			"id": int64(10), "res_model": "tbl.partner", "res_id": int64(3),
			"activity_type_id": [2]any{int64(1), "Call"},
			"user_id":          [2]any{int64(5), "Alice"},
			"date_deadline":    time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC),
		},
	}}
	types := map[string]orm.RecordMap{
		"1": {"name": "Call", "chaining_type": "trigger", "triggered_next_type_id": [2]any{int64(2), "Meeting"}},
		"2": {"name": "Meeting", "delay_count": int64(3), "delay_unit": "days", "delay_from": "previous_activity"},
	}
	restore := []func(){
		orm.StubRuntimeRepository(DiscussActivityModelName, orm.RuntimeRepositoryStub{
			Read: func(_ context.Context, ids []string, _ []string) ([]orm.RecordMap, error) {
				if row, ok := f.rows[ids[0]]; ok {
					return []orm.RecordMap{row}, nil
				}
				return nil, nil
			},
			Unlink: func(_ context.Context, ids []string) (bool, error) {
				delete(f.rows, ids[0])
				return true, nil
			},
			Create: func(_ context.Context, values orm.RecordMap) (orm.RecordMap, error) {
				if f.failChain {
					return nil, errors.New("insert failed")
				}
				f.chained = append(f.chained, values)
				return orm.RecordMap{"id": int64(99)}, nil
			},
		}),
		orm.StubRuntimeRepository(DiscussActivityTypeModelName, orm.RuntimeRepositoryStub{
			Read: func(_ context.Context, ids []string, _ []string) ([]orm.RecordMap, error) {
				if row, ok := types[ids[0]]; ok {
					return []orm.RecordMap{row}, nil
				}
				return nil, nil
			},
		}),
		orm.StubRuntimeRepository(DiscussMessageModelName, orm.RuntimeRepositoryStub{
			Create: func(context.Context, orm.RecordMap) (orm.RecordMap, error) {
				f.mu.Lock()
				defer f.mu.Unlock()
				f.messages++
				return orm.RecordMap{"id": int64(f.messages)}, nil
			},
		}),
		orm.StubRuntimeRepository(DiscussFollowersModelName, orm.RuntimeRepositoryStub{
			SearchRead: func(context.Context, orm.RecordMap, []string) ([]orm.RecordMap, error) { return nil, nil },
		}),
	}
	previous := activityDoneTx
	// The double holds the fixture lock for the whole callback (SELECT ... FOR UPDATE), reports a
	// missing row as NotFound, and restores the table when the callback fails (rollback).
	activityDoneTx = func(ctx context.Context, activityID int64, fn func(context.Context) error) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		if _, ok := f.rows[fmt.Sprint(activityID)]; !ok {
			return orm.NotFoundf("tbl.discuss_activity %d not found", activityID)
		}
		snapshot := maps.Clone(f.rows)
		if err := fn(ctx); err != nil {
			f.rows = snapshot
			f.chained = nil
			return err
		}
		return nil
	}
	t.Cleanup(func() {
		activityDoneTx = previous
		for i := len(restore) - 1; i >= 0; i-- {
			restore[i]()
		}
	})
	return f
}

func TestMarkActivityDoneReadsMany2OneTuples(t *testing.T) {
	f := newActivityFixture(t)
	if err := MarkActivityDone(context.Background(), 10, nil); err != nil {
		t.Fatalf("MarkActivityDone: %v", err)
	}
	if len(f.rows) != 0 || f.messages != 1 || len(f.chained) != 1 {
		t.Fatalf("rows=%d messages=%d chained=%d, want 0/1/1", len(f.rows), f.messages, len(f.chained))
	}
	next := f.chained[0]
	if next["activity_type_id"] != int64(2) || next["user_id"] != int64(5) {
		t.Fatalf("chained activity = %v, want type 2 for user 5", next)
	}
	if want := time.Date(2026, 1, 13, 0, 0, 0, 0, time.UTC); !next["date_deadline"].(time.Time).Equal(want) {
		t.Fatalf("chained deadline = %v, want %v (previous deadline + 3 days)", next["date_deadline"], want)
	}
}

func TestMarkActivityDoneChainFailureKeepsActivity(t *testing.T) {
	f := newActivityFixture(t)
	f.failChain = true
	if err := MarkActivityDone(context.Background(), 10, nil); err == nil {
		t.Fatal("expected the chain failure to be returned")
	}
	if len(f.rows) != 1 || f.messages != 0 {
		t.Fatalf("rows=%d messages=%d: a rolled-back completion must keep the activity and post nothing", len(f.rows), f.messages)
	}
}

func TestMarkActivityDoneDoubleSubmitChainsOnce(t *testing.T) {
	f := newActivityFixture(t)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = MarkActivityDone(context.Background(), 10, nil)
		}()
	}
	wg.Wait()

	var notFound, ok int
	for _, err := range errs {
		var typed *orm.Error
		switch {
		case err == nil:
			ok++
		case errors.As(err, &typed) && typed.Code == orm.ErrNotFound:
			notFound++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 || notFound != 1 || len(f.chained) != 1 || f.messages != 1 {
		t.Fatalf("ok=%d notFound=%d chained=%d messages=%d, want 1/1/1/1", ok, notFound, len(f.chained), f.messages)
	}
}
