package cmd

// File overview:
// - Responsibility: startup bootstrap: per-database module data import, module catalog sync and server runtime/database initialisation.
// - Key symbols: `ensureBootstrapModuleData`, `startupBootstrapTargetNames`, `databaseBootstrapContext`, `bootstrapModuleDataIfEnabled`.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"git.thebase.vn/base_hub/internal/config"
	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/routes"
	"git.thebase.vn/base_hub/internal/services/log"
	"git.thebase.vn/base_hub/internal/services/masterdata"
	workflowbootstrap "git.thebase.vn/base_hub/modules/base-workflow/bootstrap"
	workflowservice "git.thebase.vn/base_hub/modules/base-workflow/service"
	"github.com/uptrace/bun"
)

// ensureBootstrapModuleData ensures the bootstrap module data handled by this package.
func ensureBootstrapModuleData(ctx context.Context, enabledModules []string) (*masterdata.EnsureReport, string, error) {
	service := masterdata.NewService()
	const diskPath = kernelmodules.DefaultRoot
	if _, err := os.Stat(diskPath); err == nil {
		roots, discoverErr := kernelmodules.DiscoverImportRootsFromDisk(diskPath)
		if discoverErr != nil {
			return nil, diskPath, discoverErr
		}
		roots = kernelmodules.FilterImportRootsByModuleNames(roots, enabledModules)
		// Validate the invariants for this stage before the function commits to the next step.
		report, ensureErr := service.EnsurePathsImported(ctx, roots)
		return report, diskPath, ensureErr
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Errorf("stat %s: %v", diskPath, err)
		return nil, diskPath, fmt.Errorf("stat %s: %w", diskPath, err)
	}

	if embeddedAssets.Modules != nil {
		roots, discoverErr := kernelmodules.DiscoverImportRootsFromFS(embeddedAssets.Modules, ".")
		if discoverErr != nil {
			return nil, "embedded modules", discoverErr
		}
		roots = kernelmodules.FilterImportRootsByModuleNames(roots, enabledModules)
		// Validate the invariants for this stage before the function commits to the next step.
		report, ensureErr := service.EnsureFSRootsImported(ctx, embeddedAssets.Modules, roots)
		return report, "embedded modules", ensureErr
	}
	log.Errorf("%s not found and no embedded modules are available", diskPath)
	return nil, diskPath, fmt.Errorf("%s not found and no embedded modules are available", diskPath)
}

func startupBootstrapTargetNames() []string {
	// Only the home control-plane DB is a startup bootstrap target. Data-plane
	// databases are provisioned on demand by cloning the service template and
	// rediscovered on later startups through the home base_database registry.
	// (A single-name dedupe, not delegated to modules/database's
	// UniqueDatabaseNames — internal/cmd must not import that module directly.)
	homeName := strings.TrimSpace(config.HomeDBName())
	if homeName == "" {
		return nil
	}
	return []string{homeName}
}

func databaseBootstrapContext(ctx context.Context, name string) (context.Context, *bun.DB, error) {
	logicalName := strings.TrimSpace(name)
	if logicalName == "" {
		return nil, nil, fmt.Errorf("database name is empty")
	}
	registeredDB, err := orm.EnsureNamedBunDB(logicalName)
	if err != nil {
		return nil, nil, fmt.Errorf("connect database %q: %w", logicalName, err)
	}
	if registeredDB == nil || registeredDB.DB == nil {
		return nil, nil, fmt.Errorf("database %q is not initialized", logicalName)
	}
	return orm.ContextWithEnvironment(ctx, orm.NewEnvironmentWithDBName(
		registeredDB.DB,
		logicalName,
		0,
		nil,
		true,
	)), registeredDB.DB, nil
}

// bootstrapModuleDataIfEnabled imports startup master data only when the
// persisted gate is enabled or the target database is known to be fresh.
// Existing populated databases must not replay seed data unless operators
// explicitly request it through init_import/force mode.
func bootstrapModuleDataIfEnabled(ctx context.Context, forceGate, importFresh bool, enabledModules []string) (*masterdata.EnsureReport, string, bool, error) {
	db := orm.BunDBFromContext(ctx)
	if db == nil {
		log.Errorf("default bun db is not initialized")
		return nil, "", false, fmt.Errorf("default bun db is not initialized")
	}

	// Force mode flips the persisted gate before the bootstrap check runs so the
	// rest of the startup flow observes one source of truth for this decision.
	if forceGate {
		if err := config.SetInitialMasterDataImportEnabled(ctx, db, true); err != nil {
			log.Errorf("enable sys.config_param init_import: %v", err)
			return nil, "", false, fmt.Errorf("enable sys.config_param init_import: %w", err)
		}
	}

	enabled, err := config.InitialMasterDataImportEnabled(ctx, db)
	if err != nil {
		log.Errorf("read sys.config_param init_import: %v", err)
		return nil, "", false, fmt.Errorf("read sys.config_param init_import: %w", err)
	}

	// Fresh databases need one bootstrap pass even when the persisted gate is
	// false because migration just created the schema but not the seed rows yet.
	if !enabled && !importFresh {
		return nil, "", false, nil
	}

	// "Fresh" is only observable on the boot that found zero tables: once
	// migration has run, a killed/failed first import would never be retried.
	// Persist it into the gate up front so the reset-after-success path below
	// owns the retry, exactly like an operator-requested import.
	if !enabled && importFresh {
		if err := config.SetInitialMasterDataImportEnabled(ctx, db, true); err != nil {
			log.Errorf("enable sys.config_param init_import for fresh database: %v", err)
			return nil, "", false, fmt.Errorf("enable sys.config_param init_import for fresh database: %w", err)
		}
		enabled = true
	}

	report, source, err := ensureBootstrapModuleData(ctx, enabledModules)
	if err != nil {
		return nil, source, true, err
	}

	// Reset the gate only after the bootstrap path completes successfully so a
	// failed import can be retried on the next process start.
	if err := config.SetInitialMasterDataImportEnabled(ctx, db, false); err != nil {
		log.Errorf("disable sys.config_param init_import: %v", err)
		return nil, source, true, fmt.Errorf("disable sys.config_param init_import: %w", err)
	}
	return report, source, true, nil
}

func bootstrapStartupTargetDatabase(ctx context.Context, name string, forceGate, importFresh bool) error {
	dbCtx, db, err := databaseBootstrapContext(ctx, name)
	if err != nil {
		return err
	}
	effectiveEnabledModules := startupBootstrapEnabledModules(name)

	moduleCatalogSource, moduleCatalogCount, err := syncModuleCatalog(dbCtx)
	if err != nil {
		log.Errorf("failed to sync module catalog on %s from %s: %v", name, moduleCatalogSource, err)
		return fmt.Errorf("sync module catalog on %s from %s: %w", name, moduleCatalogSource, err)
	}
	log.Infof("module catalog synced: db=%s source=%s manifests=%d", name, moduleCatalogSource, moduleCatalogCount)

	if len(effectiveEnabledModules) > 0 {
		// Apply the bootstrap-time module selection before any seed replay so a
		// single-tenant home DB never imports or exposes the control-plane database module.
		if hooks, ok := kernelmodules.DatabaseProvisioningHooks(); ok {
			if err := hooks.ApplyModuleSelection(dbCtx, db, effectiveEnabledModules); err != nil {
				log.Warnf("apply bootstrap module selection on %q: %v", name, err)
			}
		} else if err := applyCoreModuleSelection(dbCtx, db, effectiveEnabledModules); err != nil {
			// Kernel-only fallback: without the optional database module's hook,
			// modules like base-auth would otherwise stay state=not_installed
			// forever (auto_install cascade never fires because "base" itself
			// has auto_install=false), 404ing every HTTPRouteRegistrar route
			// behind moduleInstallGuard.
			log.Warnf("apply core bootstrap module selection on %q: %v", name, err)
		}
	}

	ensureReport, moduleDataSource, importExecuted, err := bootstrapModuleDataIfEnabled(dbCtx, forceGate, importFresh, effectiveEnabledModules)
	if err != nil {
		log.Errorf("failed to verify module seed metadata on %s from %s: %v", name, moduleDataSource, err)
		return fmt.Errorf("verify module seed metadata on %s from %s: %w", name, moduleDataSource, err)
	}
	if !importExecuted {
		log.Infof("module bootstrap verification skipped: db=%s", name)
	} else if ensureReport.Imported {
		log.Infof(
			"module bootstrap import completed: db=%s source=%s checked=%d missing=%d created=%d updated=%d skipped=%d",
			name,
			moduleDataSource,
			ensureReport.Checked,
			len(ensureReport.Missing),
			ensureReport.ImportReport.Created,
			ensureReport.ImportReport.Updated,
			ensureReport.ImportReport.Skipped,
		)
	} else {
		log.Infof(
			"module bootstrap check completed: db=%s source=%s checked=%d missing=0 init_import_requested=%t database_fresh=%t",
			name,
			moduleDataSource,
			ensureReport.Checked,
			forceGate,
			importFresh,
		)
	}

	if strings.TrimSpace(name) != strings.TrimSpace(config.HomeDBName()) {
		if hooks, ok := kernelmodules.DatabaseProvisioningHooks(); ok {
			if err := hooks.ApplyModuleSelection(dbCtx, db, defaultEnabledModulesOrEmpty()); err != nil {
				log.Warnf("apply default service module selection on %q during startup bootstrap: %v", name, err)
			}
		}
	}
	return nil
}

// startupBootstrapEnabledModules resolves the module selection applied at
// startup. In multi-tenant mode (config.MultiTenantEnabled), the home DB never
// gets a default-enabled-modules selection applied at boot (its feature set
// is per-tenant, resolved when each tenant database is provisioned). In
// single-tenant mode the home DB is the only database, so it still imports
// every other module's seed data but excludes "database" — there is no
// control-plane multi-database management to expose.
func startupBootstrapEnabledModules(dbName string) []string {
	if strings.TrimSpace(dbName) != strings.TrimSpace(config.HomeDBName()) {
		return defaultEnabledModulesOrEmpty()
	}
	if config.MultiTenantEnabled() {
		return nil
	}
	return singleTenantHomeModuleSelection()
}

// singleTenantHomeModuleSelection resolves the discovered "base"/"base-"
// prefixed module names for the single-tenant home DB bootstrap: mirrors the
// disk/embedded discovery fallback used elsewhere in this file (syncModuleCatalog,
// ensureBootstrapModuleData) but only needs manifest names, not full import.
// Non-base modules are excluded so only first-party base modules are ever
// auto-installed at boot, matching DefaultEnabledModules in the database
// module and IsCoreModuleName's uninstall protection.
func singleTenantHomeModuleSelection() []string {
	const diskPath = kernelmodules.DefaultRoot
	var manifests []kernelmodules.Manifest
	var err error
	if _, statErr := os.Stat(diskPath); statErr == nil {
		manifests, err = kernelmodules.DiscoverManifestsFromDisk(diskPath)
	} else if embeddedAssets.Modules != nil {
		manifests, err = kernelmodules.DiscoverManifestsFromFS(embeddedAssets.Modules, ".")
	} else {
		err = fmt.Errorf("%s not found and no embedded modules are available", diskPath)
	}
	if err != nil {
		log.Errorf("resolve single-tenant home module selection: %v", err)
		return nil
	}

	names := make([]string, 0, len(manifests))
	for _, manifest := range manifests {
		name := strings.TrimSpace(manifest.Name)
		if name == "" || name == "database" {
			continue
		}
		if name != "base" && !strings.HasPrefix(name, "base-") {
			continue
		}
		names = append(names, name)
	}
	return names
}

// defaultEnabledModulesOrEmpty resolves the default module selection through
// the database module's provisioning hooks. It returns an empty selection when
// the database module isn't installed rather than failing to compile.
func defaultEnabledModulesOrEmpty() []string {
	hooks, ok := kernelmodules.DatabaseProvisioningHooks()
	if !ok {
		return nil
	}
	return hooks.DefaultEnabledModules()
}

func consumeConfigInitialImportFlag(ctx context.Context, db *bun.DB) error {
	if db == nil {
		return fmt.Errorf("default bun db is not initialized")
	}
	if !config.Get().InitialImportEnabled() {
		return nil
	}
	config.Update(func(c *config.AppConfig) { c.App.InitImport = false })
	if err := config.SetInitialMasterDataImportEnabled(ctx, db, false); err != nil {
		log.Errorf("disable sys.config_param init_import: %v", err)
		return fmt.Errorf("disable sys.config_param init_import: %w", err)
	}
	if err := config.PersistRuntimeConfig(ctx, db); err != nil {
		log.Errorf("persist runtime config after init_import bootstrap: %v", err)
		return fmt.Errorf("persist runtime config after init_import bootstrap: %w", err)
	}
	log.Infof("module bootstrap config consumed: init_import=false persisted to %s", configPath)
	return nil
}

// syncModuleCatalog synchronizes the module catalog handled by this package.
func syncModuleCatalog(ctx context.Context) (string, int, error) {
	const diskPath = kernelmodules.DefaultRoot
	if _, err := os.Stat(diskPath); err == nil {
		manifests, discoverErr := kernelmodules.DiscoverManifestsFromDisk(diskPath)
		if discoverErr != nil {
			return diskPath, 0, discoverErr
		}
		if err := kernelmodules.SyncCatalog(ctx, manifests); err != nil {
			return diskPath, 0, err
		}
		return diskPath, len(manifests), nil
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Errorf("stat %s: %v", diskPath, err)
		return diskPath, 0, fmt.Errorf("stat %s: %w", diskPath, err)
	}

	if embeddedAssets.Modules != nil {
		manifests, discoverErr := kernelmodules.DiscoverManifestsFromFS(embeddedAssets.Modules, ".")
		if discoverErr != nil {
			return "embedded modules", 0, discoverErr
		}
		if err := kernelmodules.SyncCatalog(ctx, manifests); err != nil {
			return "embedded modules", 0, err
		}
		return "embedded modules", len(manifests), nil
	}
	log.Errorf("%s not found and no embedded modules are available", diskPath)
	return diskPath, 0, fmt.Errorf("%s not found and no embedded modules are available", diskPath)
}

func initializeServerRuntime() error {
	// Register every active runtime-backed model up front so auto-migration,
	// generic RPC calls, and metadata-driven imports all see the same model set.
	if err := orm.ValidateComputeHandlers(); err != nil {
		log.Errorf("compute handler validation failed: %v", err)
		return fmt.Errorf("compute handler validation failed: %w", err)
	}
	orm.InitSharedModelWorker(orm.SharedWorkerConfig{
		WorkerCount:  4,
		JobQueueSize: 64,
	})

	// Own the slow outbound side-effect worker at process bootstrap so its
	// lifecycle is explicit rather than hidden behind lazy first use.
	workflowservice.InitOutboundDeliveryWorker(workflowservice.OutboundDeliveryWorkerConfig{
		WorkerCount:  2,
		JobQueueSize: 64,
	})
	return nil
}

func migrateDiscoveredServiceDatabases(ctx context.Context, freshConfiguredDBs map[string]bool) error {
	hooks, ok := kernelmodules.DatabaseProvisioningHooks()
	if !ok {
		log.Warnf("database module not installed: skipping service database discovery")
		return nil
	}
	connectedServiceDBs, err := hooks.DiscoverAndConnectServiceDatabases(ctx, config.HomeDBName())
	if err != nil {
		log.Errorf("service database discovery failed before server startup: %v", err)
		return fmt.Errorf("service database discovery failed before server startup: %w", err)
	}
	if len(connectedServiceDBs) == 0 {
		return nil
	}
	serviceDBInstances := make(map[string]*orm.BunDB, len(connectedServiceDBs))
	for _, name := range connectedServiceDBs {
		if db := orm.ResolveRegisteredDatabase(name); db != nil {
			serviceDBInstances[name] = db
		}
	}
	if len(serviceDBInstances) == 0 {
		return nil
	}
	freshServiceDBs, err := migrateConfiguredDatabases(ctx, serviceDBInstances)
	if err != nil {
		log.Errorf("service database bootstrap failed before server startup: %v", err)
		return fmt.Errorf("service database bootstrap failed before server startup: %w", err)
	}
	for name, fresh := range freshServiceDBs {
		if fresh {
			freshConfiguredDBs[name] = true
		}
	}
	return nil
}

func bootstrapServerDatabases(ctx context.Context, dbInstances map[string]*orm.BunDB) error {
	configRequestedImport := config.Get().InitialImportEnabled()
	freshConfiguredDBs, err := migrateConfiguredDatabases(ctx, dbInstances)
	if err != nil {
		log.Errorf("database bootstrap failed before server startup: %v", err)
		return fmt.Errorf("database bootstrap failed before server startup: %w", err)
	}
	if err := migrateDiscoveredServiceDatabases(ctx, freshConfiguredDBs); err != nil {
		return err
	}
	// DBs opened after boot (request-time selection) get the same schema
	// migration on first open instead of serving with a stale schema.
	orm.SetNamedDBOpenHook(migrateLazilyOpenedDatabase)
	if hooks, ok := kernelmodules.DatabaseProvisioningHooks(); ok {
		hooks.BackfillServiceDBRegistry(ctx, config.HomeDBName(), hooks.ConfiguredServiceDatabaseNames(ctx, config.HomeDBName()))
		routes.SetDBInitFunc(hooks.InitDatabase)
	} else {
		log.Warnf("database module not installed: skipping service DB registry backfill and init-db route wiring")
	}
	for _, name := range startupBootstrapTargetNames() {
		if err := bootstrapStartupTargetDatabase(
			ctx,
			name,
			forceMasterDataImport || configRequestedImport,
			freshConfiguredDBs[strings.TrimSpace(name)],
		); err != nil {
			return err
		}
	}
	if err := workflowbootstrap.ReconcileWorkflowUIShell(ctx); err != nil {
		log.Errorf("failed to reconcile workflow UI shell seed rows: %v", err)
		return fmt.Errorf("failed to reconcile workflow UI shell seed rows: %w", err)
	}
	// A failed Lua step migration must not stop the server: affected steps fail loudly at run time
	// ("select an automation action") and the migration retries on the next boot.
	if err := workflowbootstrap.ReconcileLuaScriptSteps(ctx); err != nil {
		log.Warnf("failed to migrate inline Lua workflow steps to automation actions: %v", err)
	}
	// Module bootstrap only saw the databases connected at that time and ran before auto-migrate;
	// register workflow triggers again now that every tenant database is migrated and imported.
	workflowservice.SyncWorkflowTriggers(ctx)
	// Single-tenant mode never provisions a clone-source template database and
	// does not install modules/database on the home DB, so skip both steps
	// entirely rather than let them no-op deeper in the call stack.
	if config.MultiTenantEnabled() {
		if hooks, ok := kernelmodules.DatabaseProvisioningHooks(); ok {
			if err := hooks.EnsureServiceTemplateDatabase(ctx, configRequestedImport); err != nil {
				log.Warnf("failed to ensure service template database: %v", err)
			}
		} else {
			log.Warnf("database module not installed: skipping service template database provisioning")
		}
		// Mirror the configured template DB into base.database after bootstrap so
		// the control-plane UI can duplicate from it without exposing live actions.
		// Skipped when the database module isn't installed.
		if hooks, ok := kernelmodules.DatabaseProvisioningHooks(); ok {
			if err := hooks.EnsureTemplateDatabaseRegistryRow(ctx); err != nil {
				log.Errorf("failed to sync template database registry row: %v", err)
				return fmt.Errorf("sync template database registry row: %w", err)
			}
		} else {
			log.Warnf("database module not installed: skipping template database registry sync")
		}
	}
	if configRequestedImport {
		homeDB := orm.BunDBFromContext(ctx)
		if err := consumeConfigInitialImportFlag(ctx, homeDB); err != nil {
			return err
		}
	}
	return nil
}
