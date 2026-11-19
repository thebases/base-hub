package orm

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

var afterCommitTestModelSeq atomic.Int64

// afterCommitRecorder registers a record-event handler on a fresh model name (handlers cannot be
// unregistered) and records "<event>:<id>" plus whether the handler ran inside a transaction.
type afterCommitRecorder struct {
	model  string
	events []string
	inTx   []bool
}

func newAfterCommitRecorder() *afterCommitRecorder {
	r := &afterCommitRecorder{model: fmt.Sprintf("test.after_commit.%d", afterCommitTestModelSeq.Add(1))}
	RegisterRecordEventHandler(r.model, func(ctx context.Context, event, _ string, id string, _ RecordMap) {
		r.events = append(r.events, event+":"+id)
		r.inTx = append(r.inTx, environmentInTx(ctx))
	})
	return r
}

// writeRow stands in for RuntimeCreate inside a reused outer transaction: it joins the tx through
// withRuntimeTxDB's reuse branch, then queues its events exactly like the runtime paths do.
func (r *afterCommitRecorder) writeRow(ctx context.Context, id string) error {
	err := withRuntimeTxDB(ctx, nil, func(context.Context) error { return nil })
	if err != nil {
		return err
	}
	fireRecordEventsAfterCommit(ctx, r.model, []recordEvent{
		{name: RecordEventBeforeCreate, id: id},
		{name: RecordEventAfterCreate, id: id},
	})
	return nil
}

func fakeTxContext(ctx context.Context, base bun.IDB) context.Context {
	env := NewEnvironmentWithDBName(base, "tenant_a", 7, nil, false)
	return ContextWithEnvironment(ctx, env.WithDB(bun.Tx{}))
}

func TestAfterCommitScopeRollbackFiresNothing(t *testing.T) {
	rec := newAfterCommitRecorder()
	base := bun.NewDB(nil, pgdialect.New())
	ctx := ContextWithEnvironment(context.Background(), NewEnvironmentWithDBName(base, "tenant_a", 7, nil, false))
	errBatch := errors.New("row 3 failed")

	err := WithAfterCommitScope(ctx, func(scopeCtx context.Context) error {
		txCtx := fakeTxContext(scopeCtx, base)
		for _, id := range []string{"1", "2"} {
			if err := rec.writeRow(txCtx, id); err != nil {
				return err
			}
		}
		return errBatch
	})
	if !errors.Is(err, errBatch) {
		t.Fatalf("err = %v, want %v", err, errBatch)
	}
	if len(rec.events) != 0 {
		t.Fatalf("rolled-back batch fired %v, want nothing", rec.events)
	}
}

func TestAfterCommitScopeCommitFiresOnceInOrderOutsideTx(t *testing.T) {
	rec := newAfterCommitRecorder()
	base := bun.NewDB(nil, pgdialect.New())
	ctx := ContextWithEnvironment(context.Background(), NewEnvironmentWithDBName(base, "tenant_a", 7, nil, false))

	var dbNames []string
	err := WithAfterCommitScope(ctx, func(scopeCtx context.Context) error {
		txCtx := fakeTxContext(scopeCtx, base)
		for _, id := range []string{"1", "2"} {
			if err := rec.writeRow(txCtx, id); err != nil {
				return err
			}
		}
		AfterCommit(txCtx, func(cbCtx context.Context) {
			env := EnvironmentFromContext(cbCtx)
			dbNames = append(dbNames, env.DBName())
			if env.DB() != base {
				t.Errorf("callback DB = %T, want the base *bun.DB", env.DB())
			}
		})
		if len(rec.events) != 0 {
			t.Fatalf("events fired before commit: %v", rec.events)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scope: %v", err)
	}
	want := []string{"before_create:1", "after_create:1", "before_create:2", "after_create:2"}
	if !slices.Equal(rec.events, want) {
		t.Fatalf("events = %v, want %v", rec.events, want)
	}
	if slices.Contains(rec.inTx, true) {
		t.Fatalf("a handler ran inside the transaction: %v", rec.inTx)
	}
	if !slices.Equal(dbNames, []string{"tenant_a"}) {
		t.Fatalf("callback tenant DB names = %v, want [tenant_a]", dbNames)
	}
}

func TestAfterCommitWithoutTransactionRunsImmediately(t *testing.T) {
	rec := newAfterCommitRecorder()
	ctx := ContextWithEnvironment(context.Background(), NewEnvironment(bun.NewDB(nil, pgdialect.New()), 0, nil, false))
	fireRecordEventsAfterCommit(ctx, rec.model, []recordEvent{{name: RecordEventAfterWrite, id: "9"}})
	if !slices.Equal(rec.events, []string{"after_write:9"}) {
		t.Fatalf("events = %v, want immediate after_write:9", rec.events)
	}
}

// TestWithRuntimeTxDBDefersEventsToOutermostCommit runs the real opener (withRuntimeTxDB on a
// *bun.DB) against the local test Postgres: nested runtime writes reuse its transaction, and their
// events fire only after the outer RunInTx commits, never on rollback.
func TestWithRuntimeTxDBDefersEventsToOutermostCommit(t *testing.T) {
	bunDB, err := NewBunDB(runtimeGraphEnvPostgresConfig())
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	t.Cleanup(func() { _ = bunDB.Close() })
	if err := pingRegisteredBunDB(context.Background(), bunDB); err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	ctx := ContextWithEnvironment(context.Background(), NewEnvironmentWithDBName(bunDB.DB, "tenant_a", 1, nil, true))

	rollback := newAfterCommitRecorder()
	errBatch := errors.New("row 2 failed")
	err = withRuntimeTxDB(ctx, nil, func(txCtx context.Context) error {
		if err := rollback.writeRow(txCtx, "1"); err != nil {
			return err
		}
		return errBatch
	})
	if !errors.Is(err, errBatch) || len(rollback.events) != 0 {
		t.Fatalf("rollback: err=%v events=%v, want %v and no events", err, rollback.events, errBatch)
	}

	commit := newAfterCommitRecorder()
	err = withRuntimeTxDB(ctx, nil, func(txCtx context.Context) error {
		for _, id := range []string{"1", "2"} {
			if err := commit.writeRow(txCtx, id); err != nil {
				return err
			}
		}
		if len(commit.events) != 0 {
			t.Fatalf("events fired before commit: %v", commit.events)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	want := []string{"before_create:1", "after_create:1", "before_create:2", "after_create:2"}
	if !slices.Equal(commit.events, want) || slices.Contains(commit.inTx, true) {
		t.Fatalf("commit events = %v (inTx %v), want %v outside the tx", commit.events, commit.inTx, want)
	}
}

func TestCreateReadBackResult(t *testing.T) {
	inserted := RecordMap{"id": int64(5)}
	pgErr := fmt.Errorf("select: %w", &pgconn.PgError{Code: "42703", Message: "column does not exist"})
	if got, err := createReadBackResult("m", "5", nil, pgErr, inserted); err == nil || got != nil {
		t.Fatalf("postgres read-back error: got %v, err %v; want the error (tx is aborted)", got, err)
	}
	got, err := createReadBackResult("m", "5", nil, errors.New("enrich: bad shape"), inserted)
	if err != nil || got["id"] != int64(5) {
		t.Fatalf("Go-side read-back error: got %v, err %v; want the insert result", got, err)
	}
	read := RecordMap{"id": int64(5), "name": "x"}
	if got, err := createReadBackResult("m", "5", []RecordMap{read}, nil, inserted); err != nil || got["name"] != "x" {
		t.Fatalf("read-back row: got %v, err %v", got, err)
	}
}
