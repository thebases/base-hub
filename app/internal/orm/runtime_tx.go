package orm

// File overview:
// - Responsibility: implements the metadata-driven ORM/runtime kernel for runtime tx, where storage, runtime dispatch, and security rules stay centralized.
// - Key symbols: `runtimeDB`, `withRuntimeTx`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Uses Bun-facing model or repository patterns so schema mapping stays explicit and transaction-aware inside the backend data layer.

import (
	"context"
	"fmt"

	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

// ReadOnlyEnvContextKey marks an environment as read-only. A context override
// carrying this key (SRS-F-020 report rendering) makes every runtime mutation
// fail before opening a transaction — templates must never trigger writes.
const ReadOnlyEnvContextKey = "readonly_env"

// EnsureWritableEnvironment rejects mutations attempted under a read-only
// environment. A nil environment stays writable: bootstrap flows attach their
// own environment later and are never render contexts.
func EnsureWritableEnvironment(env *Environment) error {
	if env == nil {
		return nil
	}
	if raw, ok := env.ContextValue(ReadOnlyEnvContextKey); ok {
		if readonly, _ := raw.(bool); readonly {
			log.Errorf("orm: mutation rejected: environment is read-only")
			return fmt.Errorf("orm: mutation rejected: environment is read-only")
		}
	}
	return nil
}

// runtimeDB returns the environment-bound DB handle when one exists so every
// nested runtime helper can reuse the same request or transaction scope.
func runtimeDB[T any](ctx context.Context, h *Handler[T]) bun.IDB {
	if h == nil || h.repo == nil {
		return runtimeDBFallback(ctx, nil)
	}
	return runtimeDBFallback(ctx, h.repo.db)
}

// runtimeDBFallback is the shared, non-generic implementation behind
// runtimeDB (Handler[T], static models) and dynamicRuntimeExecutor's
// equivalent (published custom models) -- it never actually needed a
// *Handler[T], only a fallback *bun.DB to use outside an active transaction.
func runtimeDBFallback(ctx context.Context, fallback *bun.DB) bun.IDB {
	if env := EnvironmentFromContext(ctx); env != nil && env.DB() != nil {
		// Prefer the request-bound handle so nested reads and writes share the
		// same transaction when one is already active.
		return env.DB()
	}
	if fallback == nil {
		// Return an explicit typed nil so callers can safely do db == nil.
		// Returning a nil *bun.DB directly would produce a non-nil bun.IDB
		// interface value and defeat nil checks downstream.
		return nil
	}
	return fallback
}

// withRuntimeTx wraps one logical runtime mutation in a Bun transaction and
// rebinds the runtime environment so nested calls keep using the same unit.
func withRuntimeTx[T any](ctx context.Context, h *Handler[T], fn func(context.Context) error) error {
	if h == nil || h.repo == nil {
		log.Errorf("orm: BunRepository is not initialized")
		return fmt.Errorf("orm: BunRepository is not initialized")
	}
	return withRuntimeTxDB(ctx, h.repo.db, fn)
}

// withRuntimeTxDB is the shared, non-generic implementation behind
// withRuntimeTx and dynamicRuntimeExecutor's equivalent.
func withRuntimeTxDB(ctx context.Context, fallbackDB *bun.DB, fn func(context.Context) error) error {
	env := EnvironmentFromContext(ctx)
	// Read-only environments (report rendering, SRS-F-020) must never mutate:
	// every runtime mutation funnels through this transaction helper, so one
	// guard here blocks create/write/unlink/toggle/sequence claims wholesale.
	if err := EnsureWritableEnvironment(env); err != nil {
		return err
	}
	if env == nil {
		// Bootstrap a minimal environment when lower-level callers invoke
		// runtime writes without pre-attaching one.
		env = NewEnvironment(fallbackDB, 0, nil, false)
		ctx = ContextWithEnvironment(ctx, env)
	}
	// Prefer the context-bound DB (e.g. injected by init-db CLI via dbCtx)
	// and fall back to the repo's captured handle. If neither is available the
	// operation cannot run.
	effectiveDB := env.DB()
	if effectiveDB == nil {
		effectiveDB = fallbackDB
	}
	if effectiveDB == nil {
		log.Errorf("orm: BunRepository is not initialized")
		return fmt.Errorf("orm: BunRepository is not initialized")
	}
	switch current := effectiveDB.(type) {
	case bun.Tx:
		// Reuse value transactions directly to avoid opening nested transactions.
		return fn(ContextWithEnvironment(ctx, env.WithDB(current)))
	case *bun.Tx:
		// Reuse pointer transactions for the same reason as bun.Tx above.
		return fn(ContextWithEnvironment(ctx, env.WithDB(current)))
	case *bun.DB:
		// Convert the shared DB handle into a transaction for the duration of
		// this mutation and propagate it through the environment. This call
		// opened the transaction, so it owns the after-commit queue: nested
		// runtime calls (the reuse branches above) only enqueue, and the queue
		// runs here once RunInTx has committed, or is dropped on rollback.
		scopeCtx, afterCommit := beginAfterCommitScope(ctx)
		err := current.RunInTx(scopeCtx, nil, func(txCtx context.Context, tx bun.Tx) error {
			return fn(ContextWithEnvironment(txCtx, env.WithDB(tx)))
		})
		if err != nil {
			return err
		}
		afterCommit.flush(current)
		return nil
	default:
		// Unknown Bun-compatible handles are treated as already-managed scopes.
		return fn(ctx)
	}
}
