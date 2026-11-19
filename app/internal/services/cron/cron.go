package cron

// File overview:
// - Responsibility: implements the kernel-owned scheduler (SRS-F-015) that claims due sys.cron rows
//   per registered database and executes registered handlers with bounded concurrency.
// - Key symbols: `RegisterHandler`, `Start`, `Shutdown`, `claimDueJobs`, `markJobFailure`.
// - Pattern: Uses a FOR UPDATE SKIP LOCKED claim so concurrent scheduler processes never double-run
//   a job, and pushes next_call forward inside the claim so a crash mid-execution only delays a job
//   by one interval instead of losing or duplicating it.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope,
//   and transaction state can follow the work end to end.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"git.thebase.vn/base_hub/internal/worker"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/uptrace/bun"
)

// HandlerFunc is one registered cron job implementation. The payload carries
// the job's JSONB kwargs; the context is bound to the job's database with a
// sudo environment and a per-job timeout.
type HandlerFunc func(ctx context.Context, payload map[string]any) error

const (
	cronModelName = "sys.cron"
	// cronFallbackTable keeps the scheduler operable in tests or partial builds
	// where the sys.cron model metadata is not registered.
	cronFallbackTable = "sys_cron"
	// tickInterval is deliberately below the 30s minimum job interval so a due
	// job never waits a full extra interval just because of tick granularity.
	tickInterval = 15 * time.Second
	// jobTimeout bounds one handler execution; a hung handler must not occupy a
	// worker forever or block shutdown indefinitely.
	jobTimeout = 5 * time.Minute
	// claimBatchLimit caps how many jobs one tick claims per database so a
	// backlog cannot flood the bounded worker pool queue.
	claimBatchLimit = 16
	// failureBackoffCap bounds the exponential failure backoff per SRS-F-015.
	failureBackoffCap = time.Hour
	cronWorkerCount   = 4
	cronQueueSize     = 64
)

// handler registry: modules register their handlers at bootstrap (composition
// shape mirroring orm.RegisterRecordRule) so the kernel never imports modules.
var (
	handlersMu sync.RWMutex
	handlers   = map[string]HandlerFunc{}
)

// RegisterHandler binds a handler key to its implementation. Duplicate keys are
// rejected so two modules cannot silently fight over one job.
func RegisterHandler(key string, fn HandlerFunc) error {
	key = strings.TrimSpace(key)
	if key == "" {
		log.Errorf("cron: handler key is required")
		return fmt.Errorf("cron: handler key is required")
	}
	if fn == nil {
		log.Errorf("cron: handler %q requires a function", key)
		return fmt.Errorf("cron: handler %q requires a function", key)
	}
	handlersMu.Lock()
	defer handlersMu.Unlock()
	if _, exists := handlers[key]; exists {
		log.Errorf("cron: handler %q is already registered", key)
		return fmt.Errorf("cron: handler %q is already registered", key)
	}
	handlers[key] = fn
	return nil
}

// UnregisterHandler removes a handler registration so module shutdown/init
// cycles (tests, restarts) stay symmetric with RegisterHandler's duplicate
// guard. Jobs referencing the key are skipped until it is registered again.
func UnregisterHandler(key string) {
	handlersMu.Lock()
	defer handlersMu.Unlock()
	delete(handlers, strings.TrimSpace(key))
}

// lookupHandler resolves a registered handler by key.
func lookupHandler(key string) (HandlerFunc, bool) {
	handlersMu.RLock()
	defer handlersMu.RUnlock()
	fn, ok := handlers[strings.TrimSpace(key)]
	return fn, ok
}

// cronJobRow is the claimed snapshot of one due job. It carries the failure
// bookkeeping fields read at claim time so post-execution updates never need a
// second read of the row.
type cronJobRow struct {
	ID              int64  `bun:"id"`
	Name            string `bun:"name"`
	Handler         string `bun:"handler"`
	IntervalSeconds int64  `bun:"interval_seconds"`
	FailCount       int64  `bun:"fail_count"`
	MaxFailures     int64  `bun:"max_failures"`
	PayloadRaw      []byte `bun:"payload"`
	// ClaimedAt and LeaseUntil come from the claim: success reschedules from
	// ClaimedAt, and only while next_call still holds the lease (LeaseUntil),
	// so an operator's edit made during the run is never overwritten.
	ClaimedAt  time.Time `bun:"claimed_at"`
	LeaseUntil time.Time `bun:"lease_until"`
}

// payload decodes the JSONB kwargs; malformed or empty payloads degrade to an
// empty map so handlers never receive nil.
func (r cronJobRow) payload() map[string]any {
	if len(r.PayloadRaw) == 0 {
		return map[string]any{}
	}
	decoded := map[string]any{}
	if err := json.Unmarshal(r.PayloadRaw, &decoded); err != nil {
		log.Warnf("cron: job %q has malformed payload, running with empty kwargs: %v", r.Name, err)
		return map[string]any{}
	}
	return decoded
}

// claimedJob pairs a claimed row with the database it was claimed on so the
// worker can rebuild the right environment context.
type claimedJob struct {
	dbName string
	row    cronJobRow
}

// Service owns the tick loop and the bounded execution pool.
// jobSubmitter narrows worker.Pool[T, R] down to the lifecycle/submit methods
// the scheduler actually uses, so Service can be tested against a fake pool
// instead of spinning up real goroutines.
type jobSubmitter[T any] interface {
	Submit(job T) error
	Shutdown()
}

type Service struct {
	mu sync.Mutex
	// missingDBs remembers names already warned about so a dropped tenant DB logs once.
	missingDBs sync.Map
	started    bool
	stop       chan struct{}
	done       chan struct{}
	baseCtx    context.Context
	pool       jobSubmitter[claimedJob]
}

// defaultService is the process-wide scheduler started from server.go.
var defaultService = &Service{}

// Start launches the process-wide scheduler tick loop.
func Start(ctx context.Context) { defaultService.Start(ctx) }

// Shutdown stops the process-wide scheduler and drains in-flight jobs.
func Shutdown() { defaultService.Shutdown() }

// Start launches the tick loop and the bounded worker pool. Calling Start on a
// running service is a no-op so lifecycle wiring stays idempotent.
func (s *Service) Start(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.baseCtx = ctx
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	s.pool = worker.NewPool[claimedJob, struct{}](s.executeJob, worker.WithWorkers(cronWorkerCount), worker.WithQueue(cronQueueSize))
	s.started = true
	// Launch the background work separately so the current request path can keep moving.
	go s.run()
	log.Infof("cron: scheduler started (tick %s, %d workers)", tickInterval, cronWorkerCount)
}

// Shutdown stops the tick loop first, then drains the pool, so no new claims
// happen while in-flight jobs finish.
func (s *Service) Shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return
	}
	close(s.stop)
	<-s.done
	// Tear down the resource cleanly so later calls do not inherit stale state.
	s.pool.Shutdown()
	s.started = false
	log.Infof("cron: scheduler stopped")
}

// run is the tick loop: every tick it claims due jobs on every registered
// database and hands them to the bounded pool.
func (s *Service) run() {
	defer close(s.done)
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for {
		// Wait on the competing asynchronous outcomes and handle whichever event arrives first.
		select {
		case <-s.stop:
			return
		case <-s.baseCtx.Done():
			return
		case <-ticker.C:
			s.tickAll()
		}
	}
}

// tickAll claims due jobs across every registered database. Per-database
// errors are logged and skipped so one broken tenant DB never starves the rest.
func (s *Service) tickAll() {
	// Walk each registered database in order so validation and side effects stay scoped to the current element.
	for _, name := range orm.RegisteredDBNames() {
		jobs, err := s.claimDueJobsOnDB(name)
		if err != nil {
			// A registered name whose Postgres database was dropped is not a
			// per-tick error: warn once, skip it, and re-arm when it connects again.
			if isMissingDatabase(err) {
				if _, seen := s.missingDBs.LoadOrStore(name, struct{}{}); !seen {
					log.Warnf("cron: skipping %q, database does not exist (unregister it or recreate it): %v", name, err)
				}
				continue
			}
			log.Errorf("cron: claim due jobs on %q: %v", name, err)
			continue
		}
		s.missingDBs.Delete(name)
		// Walk each claimed job in order so validation and side effects stay scoped to the current element.
		for _, job := range jobs {
			// A failed submit is safe to drop: the claim already pushed next_call
			// forward (the lease), so the job runs again once the lease expires.
			if err := s.pool.Submit(job); err != nil {
				log.Errorf("cron: submit job %q on %q: %v", job.row.Name, name, err)
			}
		}
	}
}

// isMissingDatabase reports whether err is Postgres "invalid_catalog_name" (3D000).
// The text match stays as a fallback: the error can surface from pool
// resolution wrapped as text rather than as a *pgconn.PgError.
func isMissingDatabase(err error) bool {
	return hasSQLState(err, "3D000") || (err != nil && strings.Contains(err.Error(), "SQLSTATE 3D000"))
}

// hasSQLState matches a Postgres error by its SQLSTATE, never by message text.
func hasSQLState(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}

// claimDueJobsOnDB resolves the database handle and delegates to the claim query.
func (s *Service) claimDueJobsOnDB(dbName string) ([]claimedJob, error) {
	ctx, db, err := s.jobContext(dbName)
	if err != nil {
		return nil, err
	}
	rows, err := claimDueJobs(ctx, db, cronStorageTable(), claimBatchLimit)
	if err != nil {
		// A database that has not migrated sys_cron yet simply has no jobs; treat
		// it as empty instead of logging a hard error every tick. Only
		// undefined_table (42P01): a missing column or function is a real error.
		if hasSQLState(err, "42P01") {
			return nil, nil
		}
		return nil, err
	}
	jobs := make([]claimedJob, 0, len(rows))
	for _, row := range rows {
		jobs = append(jobs, claimedJob{dbName: dbName, row: row})
	}
	return jobs, nil
}

// claimDueJobs atomically claims due rows using FOR UPDATE SKIP LOCKED and
// pushes next_call forward inside the same statement, by at least jobTimeout.
// Three properties matter here:
//   - SKIP LOCKED means two concurrent scheduler processes can never claim the
//     same row, so a job is never double-run;
//   - next_call doubles as a lease: a job may run up to jobTimeout, so pushing
//     it only by the interval (30 s minimum) would let the next tick claim the
//     same job again while it is still running;
//   - rescheduling inside the claim means a process crash after the claim only
//     delays the job by one lease — it is never lost and never duplicated.
//
// markJobSuccess/markJobFailure replace the lease with the real next run.
func claimDueJobs(ctx context.Context, db bun.IDB, table string, limit int) ([]cronJobRow, error) {
	if db == nil {
		log.Errorf("cron: claim requires a database handle")
		return nil, fmt.Errorf("cron: claim requires a database handle")
	}
	if limit <= 0 {
		limit = claimBatchLimit
	}
	rows := make([]cronJobRow, 0, limit)
	query := fmt.Sprintf(`
		UPDATE %s AS c
		SET next_call = now() + make_interval(secs => GREATEST(c.interval_seconds, %d, %d))
		WHERE c.id IN (
			SELECT t.id FROM %s AS t
			WHERE t.active = TRUE AND t.next_call <= now()
			ORDER BY t.next_call ASC
			LIMIT ?
			FOR UPDATE SKIP LOCKED
		)
		RETURNING c.id, c.name, c.handler, c.interval_seconds, c.fail_count, c.max_failures, c.payload,
			now() AS claimed_at, c.next_call AS lease_until
	`, table, minIntervalSeconds, int64(jobTimeout/time.Second), table)
	if err := db.NewRaw(query, limit).Scan(ctx, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// minIntervalSeconds mirrors the model-level floor so a legacy row that
// predates validation still cannot hot-loop the claim query.
const minIntervalSeconds = 30

// executeJob is the worker function: it resolves the handler, runs it with
// panic recovery and a timeout, then records success or failure bookkeeping.
// It always returns a nil error because failures are persisted on the job row
// rather than surfaced through the pool's error channel.
func (s *Service) executeJob(poolCtx context.Context, job claimedJob) (struct{}, error) {
	handler, ok := lookupHandler(job.row.Handler)
	if !ok {
		// Unknown handlers are skipped, not failed: handler registration is
		// per-build (a module may be compiled out or register later), so a job
		// must not burn fail_count and self-deactivate over deployment topology.
		log.Warnf("cron: job %q on %q references unregistered handler %q; skipping", job.row.Name, job.dbName, job.row.Handler)
		return struct{}{}, nil
	}
	err := s.runHandler(poolCtx, job, handler)
	if err != nil {
		// A handler cut short by pool shutdown is not a job failure: skip the
		// bookkeeping so fail_count is not burned and the job is retried on the
		// next tick after restart (its next_run was already advanced at claim).
		if poolCtx.Err() != nil && errors.Is(err, poolCtx.Err()) {
			log.Warnf("cron: job %q on %q interrupted by shutdown", job.row.Name, job.dbName)
			return struct{}{}, nil
		}
		s.recordJobFailure(job, err)
		return struct{}{}, nil
	}
	s.recordJobSuccess(job)
	return struct{}{}, nil
}

// runHandler executes one handler with panic recovery and a per-job timeout so
// a broken job can neither kill the scheduler process nor wedge a worker.
func (s *Service) runHandler(poolCtx context.Context, job claimedJob, handler HandlerFunc) (err error) {
	// Convert panics into ordinary job failures so the failure bookkeeping and
	// deactivation path applies uniformly.
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("cron: job %q panicked: %v\n%s", job.row.Name, recovered, debug.Stack())
		}
	}()
	ctx, _, ctxErr := s.jobContextFrom(poolCtx, job.dbName)
	if ctxErr != nil {
		return ctxErr
	}
	// The lease (next_call) runs from the claim, so the deadline does too: a job that
	// waited in the worker queue must not outlive its lease and overlap a re-claim.
	deadline := time.Now().Add(jobTimeout)
	if !job.row.ClaimedAt.IsZero() {
		deadline = job.row.ClaimedAt.Add(jobTimeout)
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	return handler(ctx, job.row.payload())
}

// recordJobSuccess resets the consecutive failure counter after a clean run.
func (s *Service) recordJobSuccess(job claimedJob) {
	ctx, db, err := s.jobContext(job.dbName)
	if err != nil {
		log.Errorf("cron: record success for job %q on %q: %v", job.row.Name, job.dbName, err)
		return
	}
	if err := markJobSuccess(ctx, db, cronStorageTable(), job.row); err != nil {
		log.Errorf("cron: record success for job %q on %q: %v", job.row.Name, job.dbName, err)
	}
}

// recordJobFailure applies the failure bookkeeping and, when the job crosses
// max_failures, announces the deactivation on the service bus.
func (s *Service) recordJobFailure(job claimedJob, cause error) {
	log.Errorf("cron: job %q on %q failed: %v", job.row.Name, job.dbName, cause)
	ctx, db, err := s.jobContext(job.dbName)
	if err != nil {
		log.Errorf("cron: record failure for job %q on %q: %v", job.row.Name, job.dbName, err)
		return
	}
	deactivated, err := markJobFailure(ctx, db, cronStorageTable(), job.row)
	if err != nil {
		log.Errorf("cron: record failure for job %q on %q: %v", job.row.Name, job.dbName, err)
		return
	}
	if deactivated {
		publishCronDeactivationAlert(ctx, job, cause)
	}
}

// markJobSuccess resets fail_count after a successful run so backoff always
// reflects the current consecutive streak, not history, and replaces the claim
// lease with the regular schedule: one interval after the claim (so the period
// does not drift by the run time), or now when the run outlasted the interval.
// A next_call that no longer equals the lease was changed during the run (an
// operator edit) and is kept.
func markJobSuccess(ctx context.Context, db bun.IDB, table string, row cronJobRow) error {
	_, err := db.NewRaw(
		fmt.Sprintf(`UPDATE %s SET fail_count = 0,
			next_call = CASE WHEN next_call = ? THEN GREATEST(?::timestamptz + make_interval(secs => GREATEST(interval_seconds, %d)), now()) ELSE next_call END
			WHERE id = ?`, table, minIntervalSeconds),
		row.LeaseUntil, row.ClaimedAt, row.ID,
	).Exec(ctx)
	return err
}

// markJobFailure increments fail_count, applies exponential backoff to
// next_call (interval * 2^fail_count, capped), and deactivates the job once it
// reaches max_failures. It reports whether this call deactivated the job.
func markJobFailure(ctx context.Context, db bun.IDB, table string, row cronJobRow) (bool, error) {
	failCount := row.FailCount + 1
	maxFailures := row.MaxFailures
	if maxFailures <= 0 {
		maxFailures = 5
	}
	deactivate := failCount >= maxFailures

	interval := time.Duration(row.IntervalSeconds) * time.Second
	if interval < time.Duration(minIntervalSeconds)*time.Second {
		interval = time.Duration(minIntervalSeconds) * time.Second
	}
	// Clamp the exponent before shifting so a large fail_count cannot overflow
	// the duration arithmetic; the cap below bounds the result anyway.
	exponent := failCount
	if exponent > 12 {
		exponent = 12
	}
	backoff := interval << exponent
	if backoff > failureBackoffCap || backoff <= 0 {
		backoff = failureBackoffCap
	}

	// `active = active AND NOT deactivate` preserves an operator's concurrent
	// manual deactivation while never re-activating a row from this path.
	res, err := db.NewRaw(
		// next_call moves only while it still holds this run's lease: an operator edit or a
		// newer claim made during the run wins (same guard as markJobSuccess).
		fmt.Sprintf("UPDATE %s SET fail_count = ?, next_call = CASE WHEN next_call = ? THEN now() + ?::interval ELSE next_call END, active = active AND ? WHERE id = ?", table),
		failCount,
		row.LeaseUntil,
		fmt.Sprintf("%d seconds", int64(backoff/time.Second)),
		!deactivate,
		row.ID,
	).Exec(ctx)
	if err != nil {
		return false, err
	}
	if rows, rowsErr := res.RowsAffected(); rowsErr == nil && rows == 0 {
		// The row vanished (unlinked mid-run); nothing to bookkeep.
		return false, nil
	}
	return deactivate, nil
}

// publishCronDeactivationAlert announces a self-deactivated job on the global
// service-bus channel so operators see it without tailing logs. Swappable for
// tests; failures are logged only — the deactivation itself is already durable.
var publishCronDeactivationAlert = func(ctx context.Context, job claimedJob, cause error) {
	payload := map[string]any{
		"job_id":     job.row.ID,
		"name":       job.row.Name,
		"handler":    job.row.Handler,
		"database":   job.dbName,
		"fail_count": job.row.FailCount + 1,
		"error":      cause.Error(),
	}
	// Reuse the same runtime publish path as base.database.create so the alert
	// lands in the durable bus log and reaches connected frontends.
	if _, err := orm.ExecuteRuntimeMethod(ctx, "base.service.bus", "publish", nil, []any{[]string{"servicebus:global"}, "cron.job.deactivated", payload}, nil); err != nil {
		log.Warnf("cron: publish deactivation alert for job %q: %v", job.row.Name, err)
	}
	log.Errorf("cron: job %q on %q deactivated after %d consecutive failures", job.row.Name, job.dbName, job.row.FailCount+1)
}

// jobContext builds a database-bound sudo environment context for one job,
// mirroring the shape used by startup bootstrap (databaseBootstrapContext).
func (s *Service) jobContext(dbName string) (context.Context, bun.IDB, error) {
	return s.jobContextFrom(s.baseCtx, dbName)
}

// jobContextFrom is jobContext with an explicit parent, so handlers can run
// under the worker pool's ctx and be cancelled by shutdown.
func (s *Service) jobContextFrom(base context.Context, dbName string) (context.Context, bun.IDB, error) {
	if base == nil {
		base = context.Background()
	}
	name := strings.TrimSpace(dbName)
	if name == "" {
		// The default (unnamed) handle keeps tests and single-DB setups working
		// without the named registry.
		db := orm.GetBunDB()
		if db == nil {
			log.Errorf("cron: default database is not initialized")
			return nil, nil, fmt.Errorf("cron: default database is not initialized")
		}
		return orm.ContextWithEnvironment(base, orm.NewEnvironment(db, 0, nil, true)), db, nil
	}
	registered, err := orm.EnsureNamedBunDBNoCreate(name)
	if err != nil {
		return nil, nil, fmt.Errorf("cron: connect database %q: %w", name, err)
	}
	if registered == nil || registered.DB == nil {
		log.Errorf("cron: database %q is not initialized", name)
		return nil, nil, fmt.Errorf("cron: database %q is not initialized", name)
	}
	return orm.ContextWithEnvironment(base, orm.NewEnvironmentWithDBName(registered.DB, name, 0, nil, true)), registered.DB, nil
}

// cronStorageTable resolves the physical table through model metadata per the
// coding rules, falling back to the canonical name when the model is not
// registered (tests, partial builds).
func cronStorageTable() string {
	if meta, ok := orm.LookupModel(cronModelName); ok && meta != nil {
		if table := strings.TrimSpace(meta.StorageName); table != "" {
			return table
		}
	}
	return cronFallbackTable
}
