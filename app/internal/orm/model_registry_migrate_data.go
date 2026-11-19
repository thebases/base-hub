package orm

// File overview:
// - Responsibility: lets a module declare an idempotent data migration that runs on every database
//   the ORM migrates (configured, discovered and lazily opened tenant databases alike), after its
//   tables are reconciled. Example: re-encrypting rows written under an old key.
// - Key symbols: `RegisterLegacyDataMigration`, `runModuleDataMigrations`.
// - Pattern: registered from a module models package's init(); each migration runs in its own
//   transaction. A failure is logged and rolled back without failing the schema migration, so a
//   data fix can never stop a database from booting; the migration retries on the next run.

import (
	"context"
	"strings"
	"sync"

	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

// LegacyDataMigration rewrites rows inside tx. It must be idempotent (rows already migrated are
// skipped) and must tolerate its tables being absent (module never installed on this database).
type LegacyDataMigration func(ctx context.Context, tx bun.IDB) error

type moduleDataMigration struct {
	name string
	fn   LegacyDataMigration
}

var moduleDataMigrations = struct {
	mu    sync.RWMutex
	items []moduleDataMigration
}{}

// RegisterLegacyDataMigration registers fn under name; a second registration of the same name
// replaces the first so a re-run init (tests) does not run it twice.
func RegisterLegacyDataMigration(name string, fn LegacyDataMigration) {
	name = strings.TrimSpace(name)
	if name == "" || fn == nil {
		return
	}
	moduleDataMigrations.mu.Lock()
	defer moduleDataMigrations.mu.Unlock()
	for index, existing := range moduleDataMigrations.items {
		if existing.name == name {
			moduleDataMigrations.items[index].fn = fn
			return
		}
	}
	moduleDataMigrations.items = append(moduleDataMigrations.items, moduleDataMigration{name: name, fn: fn})
}

// runModuleDataMigrations runs every registered data migration in registration order, each in
// its own transaction.
func runModuleDataMigrations(ctx context.Context, db *bun.DB) {
	if db == nil {
		return
	}
	moduleDataMigrations.mu.RLock()
	items := append([]moduleDataMigration(nil), moduleDataMigrations.items...)
	moduleDataMigrations.mu.RUnlock()

	for _, item := range items {
		err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			return item.fn(ctx, tx)
		})
		if err != nil {
			log.Errorf("orm: data migration %s failed (rolled back, retried on next migration): %v", item.name, err)
		}
	}
}
