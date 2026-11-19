package models

// File overview:
// - Responsibility: the discuss module's database transaction seams (plan T73): one generic
//   environment-bound transaction (inbound email routing) and one that also locks a
//   tbl.discuss_activity row (mark_done). Ownership: discuss module.
// - Pattern: same environment-bound convention as base-workflow's workflow_tx.go and
//   website's website_site_tx.go: the transaction is bound to the ORM environment in ctx, so
//   every orm.RuntimeRepository* / ExecuteRuntimeMethod call made with the callback's context
//   joins it. A caller already inside a transaction joins that one instead of nesting.
//   The seams are package vars because the in-memory repository stubs used by unit tests have
//   no database.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/uptrace/bun"
)

// discussActivityTable is tbl.discuss_activity's storage table. A literal, not
// ResolveStorageName(): the model spec's RuntimeMethods reach this file (initialization cycle).
const discussActivityTable = "tbl_discuss_activity"

var (
	// discussTx runs fn in one transaction.
	discussTx = runInDiscussTxDB
	// activityDoneTx runs fn in one transaction after locking the activity row; a missing row
	// is NotFound.
	activityDoneTx = runActivityLockedTxDB
)

// runInDiscussTxDB opens (or joins) a transaction and runs fn with it bound to the environment.
func runInDiscussTxDB(ctx context.Context, fn func(context.Context) error) error {
	env, handle := discussTxHandle(ctx)
	if handle == nil {
		log.Errorf("discuss transaction requires a database handle")
		return fmt.Errorf("discuss transaction requires a database handle")
	}
	db, ok := handle.(*bun.DB)
	if !ok {
		// Already inside a transaction (bun.Tx from a nested runtime call): join it.
		return fn(orm.ContextWithEnvironment(ctx, env.WithDB(handle)))
	}
	// This call opens the transaction, so it owns the after-commit scope: record events of
	// writes inside fn fire only once it commits (dropped on rollback). The scope flushes on
	// the environment's DB, so bind db there first.
	scopeCtx := orm.ContextWithEnvironment(ctx, env.WithDB(db))
	return orm.WithAfterCommitScope(scopeCtx, func(scopeCtx context.Context) error {
		return db.RunInTx(scopeCtx, nil, func(txCtx context.Context, tx bun.Tx) error {
			return fn(orm.ContextWithEnvironment(txCtx, env.WithDB(tx)))
		})
	})
}

// discussTxHandle returns the environment (a minimal one when ctx carries none, as
// orm.withRuntimeTxDB does) and the DB handle to run the transaction on.
func discussTxHandle(ctx context.Context) (*orm.Environment, bun.IDB) {
	env := orm.EnvironmentFromContext(ctx)
	var handle bun.IDB
	if env != nil && env.DB() != nil {
		handle = env.DB()
	} else if db := orm.BunDBFromContext(ctx); db != nil {
		handle = db
	}
	if env == nil && handle != nil {
		env = orm.NewEnvironment(handle, 0, nil, false)
	}
	return env, handle
}

// runActivityLockedTxDB takes SELECT ... FOR UPDATE on the activity row inside the transaction.
// A second concurrent mark_done of the same activity blocks here until the first commits, then
// finds the row deleted and gets NotFound, so the completion is never chained twice.
func runActivityLockedTxDB(ctx context.Context, activityID int64, fn func(context.Context) error) error {
	return runInDiscussTxDB(ctx, func(txCtx context.Context) error {
		var lockedID int64
		err := orm.EnvironmentFromContext(txCtx).DB().NewSelect().
			TableExpr(discussActivityTable).
			Column("id").
			Where("id = ?", activityID).
			For("UPDATE").
			Scan(txCtx, &lockedID)
		if errors.Is(err, sql.ErrNoRows) {
			log.Errorf("tbl.discuss_activity %d not found", activityID)
			return orm.NotFoundf("tbl.discuss_activity %d not found", activityID)
		}
		if err != nil {
			log.Errorf("tbl.discuss_activity %d: lock row: %v", activityID, err)
			return fmt.Errorf("tbl.discuss_activity %d: lock row: %w", activityID, err)
		}
		return fn(txCtx)
	})
}

// isUniqueViolation reports a Postgres unique_violation (SQLSTATE 23505) anywhere in the chain.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
