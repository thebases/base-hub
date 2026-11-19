package models

import (
	"context"
	"fmt"

	"git.thebase.vn/base_hub/internal/orm"
	"github.com/uptrace/bun"
)

// runInWebsiteSiteTx serializes one site's publish/unpublish lifecycle inside
// a single database transaction locked on the website_site row. Reusing the
// ORM's environment-bound transaction convention (see internal/orm/runtime_tx.go
// and sys.sequence's no-gap claim) means every RuntimeRepository* call made
// with the returned context automatically joins this same transaction.
func runInWebsiteSiteTx(ctx context.Context, siteID int64, fn func(context.Context) error) error {
	return runInWebsiteTx(ctx, func(txCtx context.Context, tx bun.IDB) error {
		return lockWebsiteSiteRowAndRun(txCtx, tx, siteID, fn)
	})
}

// runInWebsiteTx runs fn in one transaction on the request DB, joining the active one when the
// environment already carries a transaction (nested runtime call). The context handed to fn has its
// environment rebound to tx, so RuntimeRepository* calls inside fn commit or roll back together.
func runInWebsiteTx(ctx context.Context, fn func(context.Context, bun.IDB) error) error {
	handle := websiteSiteTxHandle(ctx)
	if handle == nil {
		return fmt.Errorf("website transaction requires a database handle")
	}
	switch current := handle.(type) {
	case bun.Tx:
		return fn(websiteContextWithDB(ctx, current), current)
	case *bun.Tx:
		return fn(websiteContextWithDB(ctx, current), current)
	case *bun.DB:
		if env := orm.EnvironmentFromContext(ctx); env == nil || env.DB() == nil {
			return runWebsiteTxOn(ctx, current, fn)
		}
		// This transaction is opened here, not by the ORM: the after-commit scope holds record
		// events raised inside it until the commit, instead of firing them before it.
		return orm.WithAfterCommitScope(ctx, func(scopeCtx context.Context) error {
			return runWebsiteTxOn(scopeCtx, current, fn)
		})
	default:
		// Unknown Bun-compatible handles are treated as already-managed scopes.
		return fn(ctx, current)
	}
}

// runWebsiteTxOn opens the transaction on db and hands fn a context bound to it.
func runWebsiteTxOn(ctx context.Context, db *bun.DB, fn func(context.Context, bun.IDB) error) error {
	return db.RunInTx(ctx, nil, func(txCtx context.Context, tx bun.Tx) error {
		return fn(websiteContextWithDB(txCtx, tx), tx)
	})
}

// websiteContextWithDB rebinds the request environment to db; without an environment the context
// is returned unchanged (lockWebsiteSiteRowAndRun reports that case).
func websiteContextWithDB(ctx context.Context, db bun.IDB) context.Context {
	if env := orm.EnvironmentFromContext(ctx); env != nil {
		return orm.ContextWithEnvironment(ctx, env.WithDB(db))
	}
	return ctx
}

func websiteSiteTxHandle(ctx context.Context) bun.IDB {
	if env := orm.EnvironmentFromContext(ctx); env != nil && env.DB() != nil {
		return env.DB()
	}
	return orm.BunDBFromContext(ctx)
}

// websiteSiteTable is tbl.website_site's storage table; a literal (not WebsiteSiteModelSpec.ResolveStorageName())
// because the spec's RuntimeMethods reference this file, which would make an initialization cycle.
const websiteSiteTable = "tbl_website_site"

// lockWebsiteSiteRowAndRun takes SELECT ... FOR UPDATE on the site row before
// running fn, so concurrent publish/unpublish calls for the same site block on
// this lock instead of interleaving writes.
func lockWebsiteSiteRowAndRun(ctx context.Context, tx bun.IDB, siteID int64, fn func(context.Context) error) error {
	var lockedID int64
	if err := tx.NewSelect().
		TableExpr(websiteSiteTable).
		Column("id").
		Where("id = ?", siteID).
		For("UPDATE").
		Scan(ctx, &lockedID); err != nil {
		return fmt.Errorf("tbl.website_site transaction: lock site %d: %w", siteID, err)
	}
	env := orm.EnvironmentFromContext(ctx)
	if env == nil {
		return fmt.Errorf("tbl.website_site transaction requires a runtime environment")
	}
	return fn(orm.ContextWithEnvironment(ctx, env.WithDB(tx)))
}
