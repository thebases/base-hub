package models

// Workflow graph transactions and revision check (plan T59).
//
// The canvas save (`replace_action_sequence` + layout), `replace_workflow_edges`, `copy` and
// `import` used to be search, unlink, then N creates, each in its own transaction: a failed create
// left a workflow with fewer steps, a run during the save saw none and two concurrent saves
// duplicated steps. They now run in one database transaction. For an existing workflow the
// transaction first takes `SELECT ... FOR UPDATE` on the base.workflow row, so concurrent saves of
// the same workflow queue behind each other, and the mutation bumps `revision` before commit. A
// caller that sends `expected_revision` gets a Conflict when someone saved in between.
//
// Same environment-bound convention as tbl.website_site (modules/website/models/website_site_tx.go):
// the transaction is bound to the ORM environment in ctx, so every RuntimeRepository* call (and the
// nested create/unlink/flow_map helpers) made with the returned context joins it. The trigger
// registry hook called by createWorkflowTrigger is in-process and is not rolled back; copy and
// import create non-webhook triggers inactive (nothing registered), and a webhook row registers no
// listener, so a rolled-back copy leaves no live subscription behind.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/uptrace/bun"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// workflowFlowTable is base.workflow's storage table. A literal, not FlowModelSpec.ResolveStorageName():
// FlowModelSpec's RuntimeMethods reach this file, which would make an initialization cycle.
const workflowFlowTable = "base_workflow"

// workflowTxOps is the database seam of the workflow transaction: run opens (or joins) the
// transaction and locks the workflow row, bump increments the revision inside it. Tests replace it
// because the in-memory repository stubs have no database.
type workflowTxOps struct {
	run  func(ctx context.Context, workflowID int64, fn func(txCtx context.Context, revision int64) error) error
	bump func(ctx context.Context, workflowID int64) (int64, error)
}

var workflowTx = workflowTxOps{run: runWorkflowLockedTxDB, bump: bumpWorkflowRevisionDB}

// runInWorkflowTx runs fn in one transaction. workflowID > 0 locks that base.workflow row first and
// passes its current revision to fn; workflowID <= 0 (import of a brand-new workflow) opens the
// transaction without a lock and passes 0.
func runInWorkflowTx(ctx context.Context, workflowID int64, fn func(txCtx context.Context, revision int64) error) error {
	return workflowTx.run(ctx, workflowID, fn)
}

// runWorkflowMutation is the shared body of every graph mutation on an existing workflow: lock,
// compare expected_revision (nil = not sent, no check), run fn, mark the workflow draft, bump the
// revision. It returns the revision after the bump.
func runWorkflowMutation(ctx context.Context, method string, workflowID int64, expected *int64, fn func(txCtx context.Context) error) (int64, error) {
	var next int64
	err := runInWorkflowTx(ctx, workflowID, func(txCtx context.Context, current int64) error {
		if expected != nil && *expected != current {
			log.Warnf("%s: workflow %d revision conflict (expected %d, stored %d)", method, workflowID, *expected, current)
			return orm.Conflictf("%s: workflow %d revision conflict: it was saved by someone else (expected revision %d, current %d); reload it and apply your changes again", method, workflowID, *expected, current)
		}
		if err := fn(txCtx); err != nil {
			return err
		}
		// The saved graph now differs from the published snapshot; execution keeps the published
		// version until the next publish (plan T97).
		if err := markWorkflowDraft(txCtx, workflowID); err != nil {
			return err
		}
		bumped, err := workflowTx.bump(txCtx, workflowID)
		if err != nil {
			return err
		}
		next = bumped
		return nil
	})
	if err != nil {
		return 0, err
	}
	return next, nil
}

// expectedRevisionFromKwargs reads the optional expected_revision argument. Absent, null or false
// means "no check"; anything else must be a non-negative integer.
func expectedRevisionFromKwargs(method string, kwargs map[string]any) (*int64, error) {
	raw, exists := kwargs["expected_revision"]
	if !exists || raw == nil || raw == false {
		return nil, nil
	}
	value, ok := int64FromNonNegativeAny(raw)
	if !ok {
		log.Errorf("%s expected_revision must be a non-negative integer", method)
		return nil, orm.InvalidParamsf("%s expected_revision must be a non-negative integer", method)
	}
	return &value, nil
}

func workflowTxHandle(ctx context.Context) bun.IDB {
	if env := orm.EnvironmentFromContext(ctx); env != nil && env.DB() != nil {
		return env.DB()
	}
	if db := orm.BunDBFromContext(ctx); db != nil {
		return db
	}
	// Explicit nil interface: a typed nil *bun.DB would pass a `!= nil` check.
	return nil
}

func runWorkflowLockedTxDB(ctx context.Context, workflowID int64, fn func(context.Context, int64) error) error {
	handle := workflowTxHandle(ctx)
	if handle == nil {
		log.Errorf("base.workflow transaction requires a database handle")
		return fmt.Errorf("base.workflow transaction requires a database handle")
	}
	switch current := handle.(type) {
	case *bun.DB:
		run := func(scopeCtx context.Context) error {
			return current.RunInTx(scopeCtx, nil, func(txCtx context.Context, tx bun.Tx) error {
				return lockWorkflowRowAndRun(txCtx, tx, workflowID, fn)
			})
		}
		// This call opens the transaction, so it owns the after-commit scope: record events from
		// runtime writes inside fn fire only once it commits (dropped on rollback). The scope
		// flushes on the Environment's DB, so it is used only when that is the handle opened here.
		if env := orm.EnvironmentFromContext(ctx); env != nil && env.DB() == bun.IDB(current) {
			return orm.WithAfterCommitScope(ctx, run)
		}
		return run(ctx)
	default:
		// Already inside a transaction (bun.Tx / *bun.Tx from a nested runtime call) or another
		// managed handle: reuse it and still take the row lock so this call serializes with siblings.
		return lockWorkflowRowAndRun(ctx, current, workflowID, fn)
	}
}

// lockWorkflowRowAndRun takes SELECT ... FOR UPDATE on the workflow row (when workflowID > 0) and
// runs fn with the transaction bound to the environment.
func lockWorkflowRowAndRun(ctx context.Context, tx bun.IDB, workflowID int64, fn func(context.Context, int64) error) error {
	var revision int64
	if workflowID > 0 {
		err := tx.NewSelect().
			TableExpr(workflowFlowTable).
			ColumnExpr("COALESCE(revision, 0)").
			Where("id = ?", workflowID).
			For("UPDATE").
			Scan(ctx, &revision)
		if errors.Is(err, sql.ErrNoRows) {
			return orm.NotFoundf("base.workflow %d not found", workflowID)
		}
		if err != nil {
			log.Errorf("base.workflow transaction: lock workflow %d: %v", workflowID, err)
			return fmt.Errorf("base.workflow transaction: lock workflow %d: %w", workflowID, err)
		}
	}
	env := orm.EnvironmentFromContext(ctx)
	if env == nil {
		log.Errorf("base.workflow transaction requires a runtime environment")
		return fmt.Errorf("base.workflow transaction requires a runtime environment")
	}
	return fn(orm.ContextWithEnvironment(ctx, env.WithDB(tx)), revision)
}

// bumpWorkflowRevisionDB increments base.workflow.revision on the transaction bound to ctx.
func bumpWorkflowRevisionDB(ctx context.Context, workflowID int64) (int64, error) {
	handle := workflowTxHandle(ctx)
	if handle == nil {
		return 0, fmt.Errorf("base.workflow revision bump requires a database handle")
	}
	var revision int64
	err := handle.NewRaw("UPDATE ? SET revision = COALESCE(revision, 0) + 1 WHERE id = ? RETURNING revision",
		bun.Ident(workflowFlowTable), workflowID).Scan(ctx, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, orm.NotFoundf("base.workflow %d not found", workflowID)
	}
	if err != nil {
		log.Errorf("base.workflow revision bump workflow %d: %v", workflowID, err)
		return 0, fmt.Errorf("base.workflow revision bump workflow %d: %w", workflowID, err)
	}
	return revision, nil
}
