package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestSubmitConcurrentWithShutdownDoesNotPanic(t *testing.T) {
	for i := 0; i < 50; i++ {
		pool := NewPool(func(ctx context.Context, n int) (int, error) { return n, nil }, WithWorkers(2), WithQueue(1))
		var wg sync.WaitGroup
		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 20; j++ {
					if err := pool.Submit(j); err != nil && !errors.Is(err, ErrPoolClosed) {
						t.Errorf("unexpected submit error: %v", err)
						return
					}
				}
			}()
		}
		pool.Shutdown()
		wg.Wait()
		if err := pool.Submit(1); !errors.Is(err, ErrPoolClosed) {
			t.Fatalf("expected ErrPoolClosed, got %v", err)
		}
	}
}

func TestResultsAreOptIn(t *testing.T) {
	plain := NewPool(func(ctx context.Context, n int) (int, error) { return n, nil })
	if plain.GetResultsChannel() != nil || plain.Errors() != nil {
		t.Fatal("result channels must be nil without WithResults")
	}
	plain.Shutdown()

	pool := NewPool(func(ctx context.Context, n int) (int, error) { return n * 2, nil }, WithResults(4))
	if err := pool.Submit(21); err != nil {
		t.Fatal(err)
	}
	if got := <-pool.GetResultsChannel(); got != 42 {
		t.Fatalf("result = %d, want 42", got)
	}
	pool.Shutdown()
}

// TestPanickingJobBecomesErrorAndWorkerSurvives covers K-14: a panic in one
// job must not crash the process nor kill the worker goroutine.
func TestPanickingJobBecomesErrorAndWorkerSurvives(t *testing.T) {
	pool := NewPool(func(ctx context.Context, n int) (int, error) {
		if n == 0 {
			panic("boom")
		}
		return n, nil
	}, WithWorkers(1), WithResults(4))
	defer pool.Shutdown()
	if err := pool.Submit(0); err != nil {
		t.Fatal(err)
	}
	if err := <-pool.Errors(); err == nil {
		t.Fatal("expected the panic as a job error")
	}
	if err := pool.Submit(7); err != nil {
		t.Fatal(err)
	}
	if got := <-pool.GetResultsChannel(); got != 7 {
		t.Fatalf("result = %d, want 7 from the same (surviving) worker", got)
	}
}
