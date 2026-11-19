package routes

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
)

type memIdemStore struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (s *memIdemStore) Get(k string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[k], nil
}
func (s *memIdemStore) Set(k string, v []byte, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[k] = v
	return nil
}
func (s *memIdemStore) Delete(k string) error { return nil }

func TestMCPDispatchWithIdempotencyCollapsesOverlappingCalls(t *testing.T) {
	prevStore, prevDispatch := mcpIdempotencyStore, mcpIdempotentDispatch
	t.Cleanup(func() { mcpIdempotencyStore, mcpIdempotentDispatch = prevStore, prevDispatch })
	mcpIdempotencyStore = &memIdemStore{m: map[string][]byte{}}
	var calls atomic.Int32
	release := make(chan struct{})
	mcpIdempotentDispatch = func(context.Context, *orm.RBACIdentity, string, map[string]any) (any, error) {
		calls.Add(1)
		<-release
		return map[string]any{"id": 1}, nil
	}
	identity := &orm.RBACIdentity{DBName: "d", UserID: 1}
	args := map[string]any{"idempotency_key": "k1"}

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := mcpDispatchWithIdempotency(context.Background(), identity, "base.record.create", args); err != nil {
				t.Error(err)
			}
		}()
	}
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected exactly one dispatch, got %d", got)
	}
}

// A retried base.batch.execute must replay the first batch result instead of re-running its create steps.
func TestMCPBatchExecuteRequiresKeyAndReplaysWholeResult(t *testing.T) {
	prevStore, prevDispatch := mcpIdempotencyStore, mcpIdempotentDispatch
	t.Cleanup(func() { mcpIdempotencyStore, mcpIdempotentDispatch = prevStore, prevDispatch })
	mcpIdempotencyStore = &memIdemStore{m: map[string][]byte{}}
	var calls atomic.Int32
	mcpIdempotentDispatch = func(_ context.Context, _ *orm.RBACIdentity, name string, _ map[string]any) (any, error) {
		calls.Add(1)
		return map[string]any{"steps": []any{map[string]any{"name": "base.record.create", "result": map[string]any{"id": calls.Load()}}}}, nil
	}
	identity := &orm.RBACIdentity{DBName: "d", UserID: 1}
	steps := []any{map[string]any{"name": "base.record.create", "arguments": map[string]any{"model": "tbl.partner"}}}

	_, err := mcpDispatchWithIdempotency(t.Context(), identity, "base.batch.execute", map[string]any{"steps": steps})
	var toolErr *mcpToolError
	if !errors.As(err, &toolErr) || toolErr.Code != "invalid_params" {
		t.Fatalf("batch without idempotency_key: err = %v, want invalid_params", err)
	}
	if calls.Load() != 0 {
		t.Fatal("batch without a key must not execute")
	}

	args := map[string]any{"steps": steps, "idempotency_key": "batch-1"}
	first, err := mcpDispatchWithIdempotency(t.Context(), identity, "base.batch.execute", args)
	if err != nil {
		t.Fatal(err)
	}
	second, err := mcpDispatchWithIdempotency(t.Context(), identity, "base.batch.execute", args)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("batch executed %d times, want 1", calls.Load())
	}
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("replay = %s, want %s", secondJSON, firstJSON)
	}
}

// One caller cancelling must not fail another caller collapsed onto the same key.
func TestMCPDispatchWithIdempotencyCancelledLeaderDoesNotFailWaiter(t *testing.T) {
	prevStore, prevDispatch := mcpIdempotencyStore, mcpIdempotentDispatch
	t.Cleanup(func() { mcpIdempotencyStore, mcpIdempotentDispatch = prevStore, prevDispatch })
	mcpIdempotencyStore = &memIdemStore{m: map[string][]byte{}}
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	mcpIdempotentDispatch = func(ctx context.Context, _ *orm.RBACIdentity, _ string, _ map[string]any) (any, error) {
		calls.Add(1)
		close(started)
		select {
		case <-release:
			return map[string]any{"id": 7}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	identity := &orm.RBACIdentity{DBName: "d", UserID: 1}
	args := map[string]any{"idempotency_key": "cancel-k"}

	leaderCtx, cancelLeader := context.WithCancel(t.Context())
	leaderErr := make(chan error, 1)
	go func() {
		_, err := mcpDispatchWithIdempotency(leaderCtx, identity, "base.record.create", args)
		leaderErr <- err
	}()
	<-started
	waiter := make(chan any, 1)
	go func() {
		result, err := mcpDispatchWithIdempotency(t.Context(), identity, "base.record.create", args)
		if err != nil {
			t.Error(err)
		}
		waiter <- result
	}()
	time.Sleep(50 * time.Millisecond) // let the waiter join the in-flight call
	cancelLeader()
	if err := <-leaderErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("leader err = %v, want its own cancellation", err)
	}
	close(release)
	result := <-waiter
	if got, _ := result.(map[string]any); got["id"] != 7 {
		t.Fatalf("waiter result = %#v, want the shared result", result)
	}
	if calls.Load() != 1 {
		t.Fatalf("dispatch ran %d times, want 1", calls.Load())
	}
}
