package modules

// File overview:
// - Responsibility: lets the optional database module supply provisioning behavior to core bootstrap without a compile-time import.
// - Key symbols: DatabaseProvisioning, RegisterDatabaseProvisioning, DatabaseProvisioningHooks.
// - Pattern: Mirrors internal/orm's RegisterDatabaseProvisioner idiom so modules/database stays deletable like any other optional module.

import (
	"context"

	"git.thebase.vn/base_hub/internal/config"
	"github.com/uptrace/bun"
)

// DatabaseProvisioning is implemented by the database module (if installed) so
// core bootstrap can drive per-database module selection, template registry
// sync, and the full connect/setup/migrate boot sequence without importing
// modules/database directly.
type DatabaseProvisioning interface {
	DefaultEnabledModules() []string
	EnsureTemplateDatabaseRegistryRow(ctx context.Context) error

	// ConnectDatabase ensures a PostgreSQL database exists and connects/
	// registers it in the ORM registry, without migrating or seeding it.
	ConnectDatabase(ctx context.Context, logicalName string) error
	// SetupDatabase migrates schema, syncs the module catalog, and seeds
	// master data for a database already connected via ConnectDatabase.
	SetupDatabase(ctx context.Context, logicalName string) error
	// MigrateDatabase auto-migrates the ORM model registry onto db and
	// reports whether this was detected as a fresh install.
	MigrateDatabase(ctx context.Context, logicalName string, db *bun.DB) (freshInstall bool, err error)
	// CreateDatabaseIfNotExists creates dbName (or clones it from the
	// configured service template when eligible) if it doesn't already exist.
	CreateDatabaseIfNotExists(ctx context.Context, cfg config.PostgresqlConfig, dbName string) (created, cloned bool, err error)
	// InitDatabase combines ConnectDatabase + SetupDatabase for the HTTP
	// init-db API path, rolling back registration on failure.
	InitDatabase(ctx context.Context, logicalName string) error
	// ApplyModuleSelection reconciles sys_module_module install state on db
	// to match enabledModules.
	ApplyModuleSelection(ctx context.Context, db *bun.DB, enabledModules []string) error
	// DiscoverAndConnectServiceDatabases finds and connects every
	// known service (tenant) database registered under homeDBName's catalog.
	DiscoverAndConnectServiceDatabases(ctx context.Context, homeDBName string) ([]string, error)
	// BackfillServiceDBRegistry inserts catalog rows for service databases
	// discovered outside the catalog (e.g. via the postgresql.db_filter
	// fallback scan).
	BackfillServiceDBRegistry(ctx context.Context, homeDBName string, names []string)
	// ConfiguredServiceDatabaseNames returns non-reserved names from the
	// config file's explicitly configured database list.
	ConfiguredServiceDatabaseNames(ctx context.Context, homeDBName string) []string
	// EnsureServiceTemplateDatabase (re)provisions the clone-source template
	// database used to provision new tenant databases.
	EnsureServiceTemplateDatabase(ctx context.Context, forceRequested bool) error
}

var databaseProvisioningImpl DatabaseProvisioning

// RegisterDatabaseProvisioning lets the database module's init() supply its
// provisioning implementation once bootstrap wiring runs.
func RegisterDatabaseProvisioning(impl DatabaseProvisioning) {
	databaseProvisioningImpl = impl
}

// DatabaseProvisioningHooks returns the registered implementation. ok is false
// when the database module folder is absent, so callers can fall back safely.
func DatabaseProvisioningHooks() (DatabaseProvisioning, bool) {
	return databaseProvisioningImpl, databaseProvisioningImpl != nil
}
