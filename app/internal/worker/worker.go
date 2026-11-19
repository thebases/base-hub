package worker

// File overview:
// - Responsibility: implements reusable worker-pool primitives so background jobs run with bounded concurrency instead of ad hoc goroutines.
// - Key symbols: `NewPool`, `Option`, `WorkerFunc`.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.
// - Pattern: Protects shared mutable state with explicit synchronization or worker coordination rather than relying on unsupervised goroutine side effects.

import (
	"context"
	"errors"
	"fmt"
	servicelog "git.thebase.vn/base_hub/internal/services/log"
	"runtime/debug"
	"sync"
	"time"
)

// ErrPoolClosed is returned by Submit* once the pool is shutting down or shut down.
var ErrPoolClosed = errors.New("worker pool closed")

// Default timeout for job submission
const DefaultSubmitTimeout = 3 * time.Second

// WorkerFunc defines a function that processes a job of type `T` and returns `R`
type WorkerFunc[T any, R any] func(ctx context.Context, job T) (R, error)

// Option configures a Pool at construction.
type Option func(*poolConfig)

type poolConfig struct {
	workers       int
	queue         int
	results       int
	submitTimeout time.Duration
}

// WithWorkers sets the number of concurrent workers (minimum 1).
func WithWorkers(n int) Option { return func(c *poolConfig) { c.workers = n } }

// WithQueue sets the job queue capacity.
func WithQueue(n int) Option { return func(c *poolConfig) { c.queue = n } }

// WithResults opts in to the Results and Errors channels with the given
// buffer. The caller must consume them; a full channel drops and logs. Without
// this option results are discarded and errors are only logged.
func WithResults(buffer int) Option { return func(c *poolConfig) { c.results = buffer } }

// WithSubmitTimeout overrides DefaultSubmitTimeout for Submit.
func WithSubmitTimeout(d time.Duration) Option { return func(c *poolConfig) { c.submitTimeout = d } }

// Pool manages concurrent workers
type Pool[T any, R any] struct {
	workerFunc WorkerFunc[T, R]
	jobs       chan T
	// results and errors are nil unless WithResults was given.
	results      chan R
	errors       chan error
	wg           sync.WaitGroup
	shutdownOnce sync.Once
	// closing is closed first on shutdown to unblock submitters parked on a full
	// queue; mu/closed then guarantee no send can race close(jobs).
	closing       chan struct{}
	mu            sync.RWMutex
	closed        bool
	submitTimeout time.Duration
	ctx           context.Context
	cancel        context.CancelFunc
}

// NewPool starts a worker pool. Defaults: 1 worker, queue of 1, no result
// channels, DefaultSubmitTimeout.
func NewPool[T any, R any](workerFunc WorkerFunc[T, R], opts ...Option) *Pool[T, R] {
	cfg := poolConfig{workers: 1, queue: 1, submitTimeout: DefaultSubmitTimeout}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.workers < 1 {
		cfg.workers = 1
	}
	if cfg.queue < 0 {
		cfg.queue = 0
	}
	ctx, cancel := context.WithCancel(context.Background())

	pool := &Pool[T, R]{
		workerFunc:    workerFunc,
		jobs:          make(chan T, cfg.queue),
		closing:       make(chan struct{}),
		submitTimeout: cfg.submitTimeout,
		ctx:           ctx,
		cancel:        cancel,
	}
	if cfg.results > 0 {
		pool.results = make(chan R, cfg.results)
		pool.errors = make(chan error, cfg.results)
	}

	for i := 0; i < cfg.workers; i++ {
		pool.wg.Add(1)
		// Launch the background work separately so the current request path can keep moving.
		go pool.runWorker(i)
	}
	return pool
}

// runWorker processes jobs until jobs channel is closed
func (p *Pool[T, R]) runWorker(workerID int) {
	defer p.wg.Done()

	for job := range p.jobs {
		// Check if context is cancelled
		if p.ctx.Err() != nil {
			return
		}

		result, err := p.runJob(job)
		if err != nil {
			servicelog.Errorf("worker %d: error processing job: %v", workerID, err)
			if p.errors != nil {
				select {
				case p.errors <- err:
				default:
					servicelog.Warnf("worker %d: errors channel full, dropping error: %v", workerID, err)
				}
			}
			continue
		}

		if p.results != nil {
			select {
			case p.results <- result:
			default:
				servicelog.Warnf("worker %d: results channel full, dropping result", workerID)
			}
		}
	}
}

// runJob runs workerFunc with a recover: an unrecovered panic in a pool
// goroutine would kill the whole process, and losing the worker instead would
// silently shrink the pool. A panic becomes an ordinary job error.
func (p *Pool[T, R]) runJob(job T) (result R, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("worker: job panicked: %v\n%s", recovered, debug.Stack())
		}
	}()
	return p.workerFunc(p.ctx, job)
}

// Submit a job to the worker pool with default timeout
func (p *Pool[T, R]) Submit(job T) error {
	return p.SubmitWithTimeout(job, p.submitTimeout)
}

// SubmitWithTimeout submits a job with custom timeout
func (p *Pool[T, R]) SubmitWithTimeout(job T, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(p.ctx, timeout)
	defer cancel()
	return p.SubmitWithContext(ctx, job)
}

// SubmitWithContext submits a job with context for cancellation
func (p *Pool[T, R]) SubmitWithContext(ctx context.Context, job T) error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed {
		return ErrPoolClosed
	}
	// Wait on the competing asynchronous outcomes and handle whichever event arrives first.
	select {
	case p.jobs <- job:
		return nil
	case <-p.closing:
		return ErrPoolClosed
	case <-ctx.Done():
		servicelog.Errorf("submit failed: %v", ctx.Err())
		return fmt.Errorf("submit failed: %w", ctx.Err())
	}
}

// closeJobs stops accepting work and closes the jobs channel. Signalling
// closing first releases submitters blocked on a full queue, so taking the
// write lock cannot wait on them; after that no Submit can reach close(jobs).
func (p *Pool[T, R]) closeJobs() {
	close(p.closing)
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	close(p.jobs)
}

// GetResultsChannel returns the results channel, or nil without WithResults.
func (p *Pool[T, R]) GetResultsChannel() <-chan R {
	return p.results
}

// Errors returns the errors channel, or nil without WithResults.
func (p *Pool[T, R]) Errors() <-chan error {
	return p.errors
}

// Shutdown gracefully stops workers after draining remaining jobs
func (p *Pool[T, R]) Shutdown() {
	// Tear down the resource cleanly so later calls do not inherit stale state.
	p.ShutdownWithTimeout(30 * time.Second)
}

// ShutdownWithTimeout gracefully stops workers with a timeout
func (p *Pool[T, R]) ShutdownWithTimeout(timeout time.Duration) {
	p.shutdownOnce.Do(func() {
		p.closeJobs()

		done := make(chan struct{})
		go func() {
			p.wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			servicelog.Infof("worker pool shut down gracefully")
		case <-time.After(timeout):
			servicelog.Warnf("worker pool shutdown timed out, cancelling context")
			p.cancel()
			p.wg.Wait()
		}

		p.closeOutputs()
	})
}

// ShutdownNow immediately stops all workers without draining
func (p *Pool[T, R]) ShutdownNow() {
	p.shutdownOnce.Do(func() {
		p.cancel()
		p.closeJobs()
		p.wg.Wait()
		p.closeOutputs()
		servicelog.Infof("worker pool shut down immediately")
	})
}

func (p *Pool[T, R]) closeOutputs() {
	if p.results != nil {
		close(p.results)
		close(p.errors)
	}
}
