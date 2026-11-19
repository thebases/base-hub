package cmd

// File overview:
// - Responsibility: implements command-line workflow for db bootstrap so operational tasks reuse the same config and bootstrap rules as the main service.
// - Key symbols: `initConfiguredDatabases`, `migrateConfiguredDatabases`.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.
// - Pattern: Reads operational behavior from shared config so environments can tune the same code paths without forking implementation logic.
// - Schema migration and database create/exists helpers live in modules/database (the
//   control-plane owner); this file only sequences them for the boot path, reached
//   through the DatabaseProvisioning hook (never a direct import — see
//   internal/modules/database_hooks.go) so the database module stays a removable plugin.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

type configuredDatabaseCleanup struct {
	instances map[string]*orm.BunDB
}

// initConfiguredDatabases opens the home control-plane database plus any
// explicitly configured named databases and registers them in the ORM
// registry. Data-plane databases are attached later via service-DB discovery
// or template-clone provisioning.
func initConfiguredDatabases(ctx context.Context) (map[string]*orm.BunDB, *configuredDatabaseCleanup, error) {
	instances := map[string]*orm.BunDB{}
	// HomeDBName always resolves (defaulting to "home"), so startup never
	// bootstraps an unnamed database.
	homeName := strings.TrimSpace(config.HomeDBName())

	// The home DB is the bootstrap/admin + control-plane database and is the
	// only one startup may create eagerly. Data-plane DBs must NOT be ensured
	// here: they are provisioned after the service template is ready, by cloning
	// it like any tenant — creating one empty at this point would bypass the
	// clone contract on fresh installs.
	//
	// This uses the kernel-only create primitive (not the optional database
	// module's hook) so the process can always bootstrap its own control-plane
	// database regardless of which optional modules are compiled in.
	if _, err := ensureCoreDatabaseExists(ctx, config.Get().PostgresqlDb, homeName); err != nil {
		return nil, nil, fmt.Errorf("ensure configured database %q: %w", homeName, err)
	}

	if err := openConfiguredDatabase(instances, homeName, config.Get().PostgresqlDb, homeName); err != nil {
		return nil, nil, err
	}
	orm.SetDefaultBunDBName(homeName)

	// Open explicitly configured named databases.
	extraNames := make([]string, 0, len(config.Get().PostgresqlDb.Databases))
	for name := range config.Get().PostgresqlDb.Databases {
		extraNames = append(extraNames, name)
	}
	sort.Strings(extraNames)
	for _, name := range extraNames {
		if strings.TrimSpace(name) == "" || name == homeName {
			continue
		}
		cfg := config.Get().PostgresqlDb.Databases[name]
		if strings.TrimSpace(cfg.Name) == "" {
			cfg.Name = name
		}
		if err := openConfiguredDatabase(instances, name, cfg, homeName); err != nil {
			cleanupConfiguredDatabases(instances)
			return nil, nil, err
		}
	}
	return instances, &configuredDatabaseCleanup{instances: instances}, nil
}

func openConfiguredDatabase(instances map[string]*orm.BunDB, name string, cfg config.PostgresqlConfig, homeName string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("database name is empty")
	}
	if _, exists := instances[name]; exists {
		return nil
	}
	if strings.TrimSpace(cfg.Name) == "" || name == homeName {
		cfg.Name = name
	}
	db, err := orm.NewBunDB(cfg)
	if err != nil {
		log.Errorf("init database %q: %v", name, err)
		return fmt.Errorf("init database %q: %w", name, err)
	}
	instances[name] = db
	orm.SetNamedBunDB(name, db)
	return nil
}

func cleanupConfiguredDatabases(instances map[string]*orm.BunDB) {
	names := make([]string, 0, len(instances))
	for name := range instances {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		orm.SetNamedBunDB(name, nil)
		_ = instances[name].Close()
	}
}

func (c *configuredDatabaseCleanup) Close() {
	if c == nil {
		return
	}
	cleanupConfiguredDatabases(c.instances)
}

// migrateConfiguredDatabases migrates every configured database and reports
// which logical databases were detected as fresh installs before migration.
// It uses the kernel-only migrate primitive (not the optional database
// module's hook) so core bootstrap never depends on that module being
// compiled in.
func migrateConfiguredDatabases(ctx context.Context, instances map[string]*orm.BunDB) (map[string]bool, error) {
	names := make([]string, 0, len(instances))
	freshInstalls := make(map[string]bool, len(instances))
	for name := range instances {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		db := instances[name]
		if db == nil || db.DB == nil {
			continue
		}
		freshInstall, err := migrateCoreDatabase(ctx, name, db.DB)
		if err != nil {
			return nil, err
		}
		if freshInstall {
			freshInstalls[name] = true
		}
	}
	return freshInstalls, nil
}
