package orm

// File overview:
// - Responsibility: after-commit callback queue for runtime transactions, so side effects (record
//   events, workflow model triggers) run only once the outermost transaction really commits.
// - Key symbols: `AfterCommit`, `WithAfterCommitScope`, `beginAfterCommitScope`.

import (
	"context"
	"sync"

	"github.com/uptrace/bun"
)

// afterCommitQueueKey carries the *afterCommitQueue of the transaction that is open on ctx.
// It lives on the Go context, not on the Environment, because callers such as the MCP request
// path rebuild the Environment from an identity while keeping the ctx (and the tx handle).
type afterCommitQueueKey struct{}

type afterCommitEntry struct {
	ctx context.Context
	fn  func(context.Context)
}

// afterCommitQueue collects callbacks registered while one transaction is open. It is owned by
// whoever opened that transaction (beginAfterCommitScope): flushed after a successful commit,
// dropped on rollback.
type afterCommitQueue struct {
	mu      sync.Mutex
	entries []afterCommitEntry
}

// AfterCommit runs fn once the transaction bound to ctx has committed, or right away when no
// transaction is open (or the open one was started outside an after-commit scope, the legacy
// behaviour). Callbacks of one transaction run in registration order, outside the transaction,
// with ctx's values and Environment (user, sudo, tenant DB name) but the Environment's DB
// rebound to the non-transactional handle the transaction was opened from. A rolled-back
// transaction drops its callbacks.
func AfterCommit(ctx context.Context, fn func(context.Context)) {
	if fn == nil {
		return
	}
	if q, _ := ctx.Value(afterCommitQueueKey{}).(*afterCommitQueue); q != nil && environmentInTx(ctx) {
		q.mu.Lock()
		q.entries = append(q.entries, afterCommitEntry{ctx: ctx, fn: fn})
		q.mu.Unlock()
		return
	}
	fn(ctx)
}

// WithAfterCommitScope is for code outside the ORM that opens its own transaction and binds it
// to the Environment (env.WithDB(tx)): wrap the RunInTx call in it so runtime writes inside
// defer their record events to the real commit. ctx's Environment DB must be the
// non-transactional handle the transaction is opened from; callbacks run on it after fn
// returns nil and are dropped when fn returns an error.
func WithAfterCommitScope(ctx context.Context, fn func(context.Context) error) error {
	scopeCtx, q := beginAfterCommitScope(ctx)
	if err := fn(scopeCtx); err != nil {
		return err
	}
	q.flush(EnvironmentFromContext(ctx).DB())
	return nil
}

// beginAfterCommitScope installs a fresh queue for a transaction about to be opened. A fresh
// queue (not the parent's) is right even when ctx already carries one: a new transaction on a
// *bun.DB commits independently of any outer one.
func beginAfterCommitScope(ctx context.Context) (context.Context, *afterCommitQueue) {
	q := &afterCommitQueue{}
	return context.WithValue(ctx, afterCommitQueueKey{}, q), q
}

// flush runs the queued callbacks after commit. Each runs on its own registration ctx with the
// queue removed and the Environment DB rebound to baseDB: the committed tx handle is unusable,
// and a callback that writes must open (and own) its own transaction.
func (q *afterCommitQueue) flush(baseDB bun.IDB) {
	q.mu.Lock()
	entries := q.entries
	q.entries = nil
	q.mu.Unlock()
	for _, entry := range entries {
		entry.fn(afterCommitContext(entry.ctx, baseDB))
	}
}

func afterCommitContext(ctx context.Context, baseDB bun.IDB) context.Context {
	ctx = context.WithValue(ctx, afterCommitQueueKey{}, (*afterCommitQueue)(nil))
	if env := EnvironmentFromContext(ctx); env != nil && baseDB != nil {
		ctx = ContextWithEnvironment(ctx, env.WithDB(baseDB))
	}
	return ctx
}

func environmentInTx(ctx context.Context) bool {
	switch EnvironmentFromContext(ctx).DB().(type) {
	case bun.Tx, *bun.Tx:
		return true
	}
	return false
}

// recordEvent is one record-lifecycle event queued for after-commit dispatch.
type recordEvent struct {
	name   string
	id     string
	record RecordMap
}

// fireRecordEventsAfterCommit dispatches events, in order, once the transaction bound to ctx
// commits (immediately when none is open): an outer transaction reused by a runtime write may
// still roll back, and a workflow must never be notified about a write that did not persist.
func fireRecordEventsAfterCommit(ctx context.Context, modelName string, events []recordEvent) {
	if len(events) == 0 {
		return
	}
	AfterCommit(ctx, func(cbCtx context.Context) {
		for _, ev := range events {
			fireRecordEvent(cbCtx, ev.name, modelName, ev.id, ev.record)
		}
	})
}
