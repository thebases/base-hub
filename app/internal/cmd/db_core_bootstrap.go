package cmd

// File overview:
// - Responsibility: creates and migrates the home (and, in multi-tenant mode,
//   template) control-plane database using only kernel packages. The home DB
//   is core infrastructure the process cannot boot without, so this path must
//   not depend on the optional `database` module (or any other non-`base-`
//   prefixed module) being compiled into the binary via modules/all.
// - Key symbols: `ensureCoreDatabaseExists`, `migrateCoreDatabase`.
// - Pattern: Mirrors the create/migrate primitives modules/database implements
//   for the full tenant-provisioning lifecycle (cloning, backup, drop, ...),
//   but keeps only the subset core startup actually needs.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/models"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

// ensureCoreDatabaseExists creates dbName if it does not already exist yet,
// using an admin connection to the home DB (or the pg-native "postgres"
// database as a last resort on a completely fresh cluster), then grants
// schema-public ownership to the configured app user so migrations can run.
// It never clones from a tenant-provisioning template: that behavior belongs
// to the optional database module's tenant lifecycle, not core bootstrap.
func ensureCoreDatabaseExists(ctx context.Context, cfg config.PostgresqlConfig, dbName string) (created bool, err error) {
	dbName = strings.TrimSpace(dbName)
	if dbName == "" {
		return false, fmt.Errorf("database name is empty")
	}

	adminDB, err := openCoreAdminDB(cfg, dbName)
	if err != nil {
		return false, fmt.Errorf("connect to admin database: %w", err)
	}
	defer adminDB.Close()

	exists, err := coreDatabaseExists(ctx, adminDB.DB, dbName)
	if err != nil {
		return false, fmt.Errorf("check pg_database: %w", err)
	}
	if exists {
		log.Infof("bootstrap: database %q already exists", dbName)
		return false, nil
	}

	log.Infof("bootstrap: creating database %q", dbName)
	quotedName := `"` + strings.ReplaceAll(dbName, `"`, `""`) + `"`
	if _, err := adminDB.DB.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE %s", quotedName)); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			return false, nil
		}
		return false, fmt.Errorf("CREATE DATABASE %q: %w", dbName, err)
	}
	if err := grantCoreSchemaPublicOwnership(cfg, dbName); err != nil {
		return false, fmt.Errorf("grant public schema privileges on %q: %w", dbName, err)
	}
	log.Infof("bootstrap: database %q created", dbName)
	return true, nil
}

// openCoreAdminDB opens a maintenance connection for CREATE DATABASE /
// pg_database checks. It rides the home control-plane DB first (never a
// data-plane target) and falls back to the pg-native "postgres" database so a
// fresh cluster can still create home itself.
func openCoreAdminDB(cfg config.PostgresqlConfig, targetDBName string) (*orm.BunDB, error) {
	homeName := strings.TrimSpace(cfg.ResolvedHomeDBName())
	seen := map[string]struct{}{}
	candidates := make([]string, 0, 2)
	for _, name := range []string{homeName, "postgres"} {
		name = strings.TrimSpace(name)
		if name == "" || name == targetDBName {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		candidates = append(candidates, name)
	}
	if len(candidates) == 0 {
		candidates = []string{"postgres"}
	}

	var lastErr error
	for _, candidate := range candidates {
		adminCfg := cfg
		adminCfg.Name = candidate
		db, err := orm.NewBunDB(adminCfg)
		if err != nil {
			lastErr = err
			log.Warnf("bootstrap: open admin database %q for target %q: %v", candidate, targetDBName, err)
			continue
		}
		if pingErr := db.DB.PingContext(context.Background()); pingErr != nil {
			_ = db.Close()
			lastErr = pingErr
			log.Warnf("bootstrap: open admin database %q for target %q: %v", candidate, targetDBName, pingErr)
			continue
		}
		return db, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no admin database candidates available for %q", targetDBName)
	}
	return nil, lastErr
}

func coreDatabaseExists(ctx context.Context, db *bun.DB, dbName string) (bool, error) {
	var exists bool
	if err := db.NewRaw(`SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = ?)`, dbName).Scan(ctx, &exists); err != nil {
		return false, err
	}
	return exists, nil
}

// grantCoreSchemaPublicOwnership connects to dbName as the configured app
// user and ensures that user owns schema public. A freshly created database
// can end up with a public schema owner that differs from the connecting
// role depending on cluster defaults; on Postgres 15+ only the owner (or a
// superuser) has CREATE there, so anything less leaves AutoMigrate unable to
// create its tables.
func grantCoreSchemaPublicOwnership(cfg config.PostgresqlConfig, dbName string) error {
	targetCfg := cfg
	targetCfg.Name = dbName
	db, err := orm.NewBunDB(targetCfg)
	if err != nil {
		return fmt.Errorf("connect to %q: %w", dbName, err)
	}
	defer db.Close()

	quotedUser := `"` + strings.ReplaceAll(cfg.User, `"`, `""`) + `"`
	if _, err := db.DB.ExecContext(context.Background(), fmt.Sprintf("ALTER SCHEMA public OWNER TO %s", quotedUser)); err != nil {
		return fmt.Errorf("alter schema public owner: %w", err)
	}
	return nil
}

// migrateCoreDatabase auto-migrates the runtime model registry onto db, syncs
// sys.model.relation/operation, reconciles sys.model.constraint, and (home DB
// only) syncs the runtime config snapshot. It reports whether db was detected
// as a fresh install (no tables) before migration. This is the same sequence
// the optional database module runs for the home DB, reimplemented here with
// only kernel packages (internal/orm, internal/models, internal/config) so
// core bootstrap never depends on modules/database being compiled in.
func migrateCoreDatabase(ctx context.Context, name string, db *bun.DB) (freshInstall bool, err error) {
	if db == nil {
		return false, nil
	}

	isEmpty, err := coreDatabaseIsEmpty(ctx, db)
	if err != nil {
		return false, fmt.Errorf("check schema tables on %q: %w", name, err)
	}
	if isEmpty {
		log.Infof("database %q has no tables — bootstrapping fresh schema", name)
	}
	freshInstall = isEmpty

	if err := orm.AutoMigrateRegisteredModelsToDB(ctx, db, orm.DefaultModelRegistry()); err != nil {
		return false, fmt.Errorf("auto-migrate database %q: %w", name, err)
	}
	if err := models.SyncSysModelRelations(ctx, db, orm.RegisteredModels()); err != nil {
		return false, fmt.Errorf("sync sys.model.relation on %q: %w", name, err)
	}
	if err := models.SyncSysModelOperations(ctx, db, orm.RegisteredModels()); err != nil {
		return false, fmt.Errorf("sync sys.model.operation on %q: %w", name, err)
	}
	// Per-row constraint failures are logged (row flips to state=error), never
	// fatal to boot — one bad constraint must not block the whole database.
	if err := models.ReconcileModelConstraints(ctx, db, name); err != nil {
		log.Errorf("reconcile sys.model.constraint on %q: %v", name, err)
	}
	// Keep the process-wide runtime config anchored to the control-plane DB.
	// Service DBs carry cloned sys.config_param rows for local inspection, but
	// letting them overwrite config.Get() would misroute later provisioning.
	if strings.TrimSpace(name) == strings.TrimSpace(config.HomeDBName()) {
		if err := config.SyncRuntimeConfig(ctx, db, orm.ForceConfigSyncFlag()); err != nil {
			return false, fmt.Errorf("sync runtime config on %q: %w", name, err)
		}
	}
	return freshInstall, nil
}

// applyCoreModuleSelection force-installs enabledModules on db using only a
// plain sys_module_module update. It exists so single-tenant home-DB boot can
// mark its default module set (base, base-auth, ...) installed even when the
// optional database module isn't compiled in and its
// ApplyServiceDatabaseModuleSelection (tenant-selection reconciliation,
// uninstall-on-disable) hook is unavailable — see bootstrapStartupTargetDatabase.
// Unlike that full reconciliation path, this never uninstalls a module that
// fell out of enabledModules: it is a boot-time "ensure these are on" primitive,
// not a module-selection editor. manual_uninstall = false excludes any module
// an admin explicitly uninstalled via sys.module.module's own row action — this
// runs on every server boot, so without the guard it silently reverted a manual
// uninstall on the very next restart.
func applyCoreModuleSelection(ctx context.Context, db *bun.DB, enabledModules []string) error {
	if db == nil || len(enabledModules) == 0 {
		return nil
	}
	_, err := db.NewUpdate().
		TableExpr("sys_module_module").
		Set("state = 'installed'").
		Where("name IN (?)", bun.In(enabledModules)).
		Where("manual_uninstall = false").
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("mark modules installed: %w", err)
	}
	return nil
}

func coreDatabaseIsEmpty(ctx context.Context, db *bun.DB) (bool, error) {
	var count int
	err := db.NewRaw(`
		SELECT COUNT(*)
		FROM information_schema.tables
		WHERE table_schema = current_schema()
		  AND table_type = 'BASE TABLE'
	`).Scan(ctx, &count)
	if err != nil {
		return false, err
	}
	return count == 0, nil
}

// lazyMigratedDatabases records DBs the first-open hook already migrated in
// this process, so an evict-and-reconnect of the same DB doesn't re-run DDL.
var lazyMigratedDatabases sync.Map

// migrateLazilyOpenedDatabase is the orm first-open hook: a tenant DB that was
// not connected at boot (selected later by cookie/header/host) would otherwise
// serve queries against a schema older than the registered models, failing
// with SQLSTATE 42703 for any newly added column. Only DBs that already carry
// the hub schema are touched — request input must never bootstrap a foreign or
// empty database.
func migrateLazilyOpenedDatabase(ctx context.Context, name string, db *bun.DB) error {
	if db == nil {
		return nil
	}
	if _, done := lazyMigratedDatabases.Load(name); done {
		return nil
	}
	isHub, err := coreDatabaseHasHubSchema(ctx, db)
	if err != nil {
		return fmt.Errorf("check hub schema on %q: %w", name, err)
	}
	if !isHub {
		return nil
	}
	log.Infof("database %q opened after boot — migrating schema to registered models", name)
	if _, err := migrateCoreDatabase(ctx, name, db); err != nil {
		return err
	}
	lazyMigratedDatabases.Store(name, struct{}{})
	// Keep the module catalog (sys_module_module) in step with app/modules for
	// this DB too, not just the home DB synced at boot. Runs off the hook
	// goroutine: the named-DB registry is still mid-open here, and the sync
	// resolves the pool through it.
	lazyCatalogSyncs.Add(1)
	go syncLazilyOpenedModuleCatalog(name)
	return nil
}

// lazyCatalogSyncs tracks the background catalog syncs started above, and
// lazyCatalogCtx cancels them, so shutdown can stop them and wait before the
// DB pools they use are closed (stopLazyCatalogSyncs).
var (
	lazyCatalogSyncs                  sync.WaitGroup
	lazyCatalogCtx, lazyCatalogCancel = context.WithCancel(context.Background())
)

// stopLazyCatalogSyncs cancels in-flight lazy catalog syncs and waits for them
// within the shutdown budget. Registered after the database stop step so it
// runs before the pools close (the shutdown stack runs newest first).
func stopLazyCatalogSyncs(ctx context.Context) error {
	lazyCatalogCancel()
	done := make(chan struct{})
	go func() {
		lazyCatalogSyncs.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func syncLazilyOpenedModuleCatalog(name string) {
	defer lazyCatalogSyncs.Done()
	ctx, cancel := context.WithTimeout(lazyCatalogCtx, 2*time.Minute)
	defer cancel()
	dbCtx, _, err := databaseBootstrapContext(ctx, name)
	if err != nil {
		log.Warnf("module catalog sync on lazily opened database %q: %v", name, err)
		return
	}
	source, count, err := syncModuleCatalog(dbCtx)
	if err != nil {
		log.Warnf("module catalog sync on lazily opened database %q from %s: %v", name, source, err)
		return
	}
	log.Infof("module catalog synced: db=%s source=%s manifests=%d", name, source, count)
}

// coreDatabaseHasHubSchema reports whether db already holds the model
// registry table, i.e. it was provisioned by this hub at some point.
func coreDatabaseHasHubSchema(ctx context.Context, db *bun.DB) (bool, error) {
	var exists bool
	err := db.NewRaw(`SELECT to_regclass('sys_model') IS NOT NULL`).Scan(ctx, &exists)
	return exists, err
}
