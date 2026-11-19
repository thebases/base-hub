package orm

import "context"

// DatabaseBackupArtifact describes one completed pg_dump artifact so the
// control-plane row can persist path + checksum + size (SRS-F-016) without
// coupling module packages to the filesystem layout owned by internal/cmd.
type DatabaseBackupArtifact struct {
	// Path is the absolute or backup.root-relative artifact location on disk.
	Path string
	// Checksum is the hex SHA-256 of the artifact bytes, verified after write.
	Checksum string
	// Size is the artifact byte size recorded at write time.
	Size int64
}

// DatabaseProvisioner bridges runtime model methods to the backend database
// lifecycle helpers without coupling module packages to internal/cmd.
type DatabaseProvisioner interface {
	Connect(ctx context.Context, name string) error
	Setup(ctx context.Context, name string, enabledModules []string) error
	Provision(ctx context.Context, name string, enabledModules []string) error
	Stop(ctx context.Context, name string) error
	Drop(ctx context.Context, name string) error
	// Reinit reconnects name (if needed) and forces a master-data reimport,
	// replaying every seed regardless of whether the database already has data
	// — unlike Setup, which only seeds on first provision.
	Reinit(ctx context.Context, name string, enabledModules []string) error
	// Backup streams a pg_dump custom-format archive of name into the
	// configured backup root and returns the verified artifact metadata.
	Backup(ctx context.Context, name string) (*DatabaseBackupArtifact, error)
	// Duplicate clones sourceName into newName via CREATE DATABASE … TEMPLATE,
	// quiescing the source first so the copy is never torn (SRS-F-016).
	Duplicate(ctx context.Context, sourceName, newName string) error
	// Restore replaces name's contents from a backup artifact; the checksum is
	// re-verified before any destructive step when the artifact carries one.
	Restore(ctx context.Context, name string, artifact DatabaseBackupArtifact) error
}

var databaseProvisioner DatabaseProvisioner

// RegisterDatabaseProvisioner exposes one process-wide lifecycle implementation
// for runtime methods that manage service databases.
func RegisterDatabaseProvisioner(p DatabaseProvisioner) {
	databaseProvisioner = p
}

// GetDatabaseProvisioner returns the currently registered lifecycle bridge.
func GetDatabaseProvisioner() DatabaseProvisioner {
	return databaseProvisioner
}

// ModuleCatalogSyncFunc syncs the runtime module manifest catalog (from disk
// or the embedded build asset) and reports where it was read from.
type ModuleCatalogSyncFunc func(ctx context.Context) (source string, count int, err error)

var moduleCatalogSyncFn ModuleCatalogSyncFunc

// RegisterModuleCatalogSync lets the binary composition layer (internal/cmd)
// supply the manifest-catalog sync implementation, since it alone knows
// whether manifests come from disk or an embedded build asset.
func RegisterModuleCatalogSync(fn ModuleCatalogSyncFunc) {
	moduleCatalogSyncFn = fn
}

// SyncModuleCatalog invokes the registered ModuleCatalogSyncFunc.
func SyncModuleCatalog(ctx context.Context) (string, int, error) {
	if moduleCatalogSyncFn == nil {
		return "", 0, nil
	}
	return moduleCatalogSyncFn(ctx)
}

// ProvisionSeedResult reports the outcome of a master-data seed bootstrap
// pass triggered during database provisioning.
type ProvisionSeedResult struct {
	Imported bool
	Source   string
	Created  int
	Updated  int
	Skipped  int
}

// ProvisionSeedFunc runs the master-data bootstrap pass for a just-migrated
// database. forceImport replays every seed regardless of the persisted
// init_import gate (used for freshly created databases and explicit reinit).
type ProvisionSeedFunc func(ctx context.Context, forceImport bool, enabledModules []string) (*ProvisionSeedResult, error)

var provisionSeedFn ProvisionSeedFunc

// RegisterProvisionSeedBootstrap lets internal/cmd supply the master-data
// bootstrap implementation (it owns the embedded/disk asset resolution that
// the seed importer reads from).
func RegisterProvisionSeedBootstrap(fn ProvisionSeedFunc) {
	provisionSeedFn = fn
}

// BootstrapProvisionSeed invokes the registered ProvisionSeedFunc.
func BootstrapProvisionSeed(ctx context.Context, forceImport bool, enabledModules []string) (*ProvisionSeedResult, error) {
	if provisionSeedFn == nil {
		return nil, nil
	}
	return provisionSeedFn(ctx, forceImport, enabledModules)
}

var forceConfigSyncFn func() bool

// RegisterForceConfigSyncFlag exposes the process's --force-config /
// --fc CLI flag value so provisioning-triggered migrations observe the same
// override behavior as the boot-time migration path.
func RegisterForceConfigSyncFlag(fn func() bool) {
	forceConfigSyncFn = fn
}

// ForceConfigSyncFlag reports the current --force-config flag value, or
// false if nothing registered it (e.g. in tests).
func ForceConfigSyncFlag() bool {
	if forceConfigSyncFn == nil {
		return false
	}
	return forceConfigSyncFn()
}
