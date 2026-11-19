package cmd

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/config"
	testutil "git.thebase.vn/base_hub/internal/test"
	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

func TestMigrateConfiguredDatabasesBootstrapsEmptyExistingDatabase(t *testing.T) {
	testutil.RequireDBIntegrationTests(t)
	cfg := testutil.EnvPostgresConfig()
	adminConn, err := testutil.OpenAdminConn(t.Context(), cfg)
	if err != nil {
		t.Skipf("open postgres admin connection: %v", err)
	}
	defer adminConn.Close(t.Context())

	tempDBName := "bootstrap_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminConn.Exec(t.Context(), "CREATE DATABASE "+tempDBName); err != nil {
		t.Skipf("create temporary database %q: %v", tempDBName, err)
	}
	t.Cleanup(func() {
		_, _ = adminConn.Exec(
			context.Background(),
			"DROP DATABASE IF EXISTS "+tempDBName+" WITH (FORCE)",
		)
	})

	config.LoadConfig(filepath.Join("..", "config", "config_dev.yaml"))
	testutil.RestoreConfig(t, func(appCfg *config.AppConfig) {
		appCfg.PostgresqlDb = cfg
		appCfg.PostgresqlDb.Name = tempDBName
		appCfg.PostgresqlDb.Databases = nil
	})

	instances, cleanupDBs, err := initConfiguredDatabases(context.Background())
	if err != nil {
		t.Fatalf("init configured databases: %v", err)
	}
	defer cleanupDBs.Close()

	shutdownModels := initRuntimeModels()
	defer shutdownModels()

	db := instances[tempDBName]
	if db == nil || db.DB == nil {
		t.Fatalf("database %q was not opened", tempDBName)
	}

	var beforeCount int
	if err := db.DB.NewRaw(`
		SELECT COUNT(*)
		FROM information_schema.tables
		WHERE table_schema = current_schema()
	`).Scan(t.Context(), &beforeCount); err != nil {
		t.Fatalf("count tables before migration: %v", err)
	}
	if beforeCount != 0 {
		t.Fatalf("expected empty existing database to start with 0 tables, got %d", beforeCount)
	}

	freshInstalls, err := migrateConfiguredDatabases(t.Context(), instances)
	if err != nil {
		t.Fatalf("migrate configured databases: %v", err)
	}
	if !freshInstalls[tempDBName] {
		t.Fatalf("expected %q to be reported as a fresh install", tempDBName)
	}

	for _, tableName := range []string{
		"sys_model",
		"sys_config_param",
		"sys_module_module",
		"base_users",
	} {
		assertBootstrapTableExists(t, db.DB, tableName)
	}
}

func TestBootstrapStartupTargetDatabaseSkipsExistingUnseededDatabaseWhenInitImportDisabled(t *testing.T) {
	testutil.RequireDBIntegrationTests(t)
	cfg := testutil.EnvPostgresConfig()
	adminConn, err := testutil.OpenAdminConn(t.Context(), cfg)
	if err != nil {
		t.Skipf("open postgres admin connection: %v", err)
	}
	defer adminConn.Close(t.Context())

	tempDBName := "bootstrap_seed_repair_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminConn.Exec(t.Context(), "CREATE DATABASE "+tempDBName); err != nil {
		t.Skipf("create temporary database %q: %v", tempDBName, err)
	}
	t.Cleanup(func() {
		_, _ = adminConn.Exec(context.Background(), "DROP DATABASE IF EXISTS "+tempDBName+" WITH (FORCE)")
	})

	config.LoadConfig(filepath.Join("..", "config", "config_dev.yaml"))
	testutil.RestoreConfig(t, func(appCfg *config.AppConfig) {
		appCfg.PostgresqlDb = cfg
		appCfg.PostgresqlDb.HomeDBName = tempDBName
		appCfg.PostgresqlDb.Name = tempDBName
		appCfg.PostgresqlDb.Databases = nil
		appCfg.App.InitImport = false
	})

	instances, cleanupDBs, err := initConfiguredDatabases(context.Background())
	if err != nil {
		t.Fatalf("init configured databases: %v", err)
	}
	defer cleanupDBs.Close()

	shutdownModels := initRuntimeModels()
	defer shutdownModels()

	if _, err := migrateConfiguredDatabases(t.Context(), instances); err != nil {
		t.Fatalf("migrate configured databases: %v", err)
	}

	db := instances[tempDBName]
	if db == nil || db.DB == nil {
		t.Fatalf("database %q was not opened", tempDBName)
	}

	var usersBefore int
	if err := db.DB.NewRaw(`SELECT COUNT(*) FROM base_users`).Scan(t.Context(), &usersBefore); err != nil {
		t.Fatalf("count base_users before bootstrap: %v", err)
	}
	if usersBefore != 0 {
		t.Fatalf("expected base_users to start empty before bootstrap, got %d", usersBefore)
	}

	previousAssets := embeddedAssets
	t.Cleanup(func() {
		embeddedAssets = previousAssets
	})
	moduleFS := os.DirFS(filepath.Clean(filepath.Join("..", "..", "modules")))
	if _, err := fs.Stat(moduleFS, "base/data"); err != nil {
		t.Fatalf("stat embedded test module fs: %v", err)
	}
	SetEmbeddedAssets(EmbeddedAssets{Modules: moduleFS})

	if err := bootstrapStartupTargetDatabase(t.Context(), tempDBName, false, false); err != nil {
		t.Fatalf("bootstrap startup target database: %v", err)
	}

	var usersAfter int
	if err := db.DB.NewRaw(`SELECT COUNT(*) FROM base_users`).Scan(t.Context(), &usersAfter); err != nil {
		t.Fatalf("count base_users after bootstrap: %v", err)
	}
	if usersAfter != 0 {
		t.Fatalf("expected existing unseeded database to stay unseeded when initImport is false, got %d users", usersAfter)
	}
}

func TestBootstrapStartupTargetDatabaseImportsExistingUnseededDatabaseWhenInitImportEnabled(t *testing.T) {
	testutil.RequireDBIntegrationTests(t)
	cfg := testutil.EnvPostgresConfig()
	adminConn, err := testutil.OpenAdminConn(t.Context(), cfg)
	if err != nil {
		t.Skipf("open postgres admin connection: %v", err)
	}
	defer adminConn.Close(t.Context())

	tempDBName := "bootstrap_seed_enabled_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminConn.Exec(t.Context(), "CREATE DATABASE "+tempDBName); err != nil {
		t.Skipf("create temporary database %q: %v", tempDBName, err)
	}
	t.Cleanup(func() {
		_, _ = adminConn.Exec(context.Background(), "DROP DATABASE IF EXISTS "+tempDBName+" WITH (FORCE)")
	})

	config.LoadConfig(filepath.Join("..", "config", "config_dev.yaml"))
	testutil.RestoreConfig(t, func(appCfg *config.AppConfig) {
		appCfg.PostgresqlDb = cfg
		appCfg.PostgresqlDb.HomeDBName = tempDBName
		appCfg.PostgresqlDb.Name = tempDBName
		appCfg.PostgresqlDb.Databases = nil
		appCfg.App.InitImport = false
	})

	instances, cleanupDBs, err := initConfiguredDatabases(context.Background())
	if err != nil {
		t.Fatalf("init configured databases: %v", err)
	}
	defer cleanupDBs.Close()

	shutdownModels := initRuntimeModels()
	defer shutdownModels()

	if _, err := migrateConfiguredDatabases(t.Context(), instances); err != nil {
		t.Fatalf("migrate configured databases: %v", err)
	}

	db := instances[tempDBName]
	if db == nil || db.DB == nil {
		t.Fatalf("database %q was not opened", tempDBName)
	}

	if err := config.SetInitialMasterDataImportEnabled(t.Context(), db.DB, true); err != nil {
		t.Fatalf("set init_import=true: %v", err)
	}

	previousAssets := embeddedAssets
	t.Cleanup(func() {
		embeddedAssets = previousAssets
	})
	moduleFS := os.DirFS(filepath.Clean(filepath.Join("..", "..", "modules")))
	if _, err := fs.Stat(moduleFS, "base/data"); err != nil {
		t.Fatalf("stat embedded test module fs: %v", err)
	}
	SetEmbeddedAssets(EmbeddedAssets{Modules: moduleFS})

	if err := bootstrapStartupTargetDatabase(t.Context(), tempDBName, false, false); err != nil {
		t.Fatalf("bootstrap startup target database: %v", err)
	}

	var usersAfter int
	if err := db.DB.NewRaw(`SELECT COUNT(*) FROM base_users`).Scan(t.Context(), &usersAfter); err != nil {
		t.Fatalf("count base_users after bootstrap: %v", err)
	}
	if usersAfter == 0 {
		t.Fatalf("expected initImport=true to seed base_users for an existing unseeded database")
	}
}

func TestBootstrapStartupTargetDatabaseSeedsFreshDatabaseWhenInitImportDisabled(t *testing.T) {
	testutil.RequireDBIntegrationTests(t)
	cfg := testutil.EnvPostgresConfig()
	adminConn, err := testutil.OpenAdminConn(t.Context(), cfg)
	if err != nil {
		t.Skipf("open postgres admin connection: %v", err)
	}
	defer adminConn.Close(t.Context())

	tempDBName := "bootstrap_fresh_seed_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminConn.Exec(t.Context(), "CREATE DATABASE "+tempDBName); err != nil {
		t.Skipf("create temporary database %q: %v", tempDBName, err)
	}
	t.Cleanup(func() {
		_, _ = adminConn.Exec(context.Background(), "DROP DATABASE IF EXISTS "+tempDBName+" WITH (FORCE)")
	})

	config.LoadConfig(filepath.Join("..", "config", "config_dev.yaml"))
	testutil.RestoreConfig(t, func(appCfg *config.AppConfig) {
		appCfg.PostgresqlDb = cfg
		appCfg.PostgresqlDb.HomeDBName = tempDBName
		appCfg.PostgresqlDb.Name = tempDBName
		appCfg.PostgresqlDb.Databases = nil
		appCfg.App.InitImport = false
	})

	instances, cleanupDBs, err := initConfiguredDatabases(context.Background())
	if err != nil {
		t.Fatalf("init configured databases: %v", err)
	}
	defer cleanupDBs.Close()

	shutdownModels := initRuntimeModels()
	defer shutdownModels()

	freshInstalls, err := migrateConfiguredDatabases(t.Context(), instances)
	if err != nil {
		t.Fatalf("migrate configured databases: %v", err)
	}
	if !freshInstalls[tempDBName] {
		t.Fatalf("expected %q to be marked fresh after migrating an empty database", tempDBName)
	}

	db := instances[tempDBName]
	if db == nil || db.DB == nil {
		t.Fatalf("database %q was not opened", tempDBName)
	}

	var usersBefore int
	if err := db.DB.NewRaw(`SELECT COUNT(*) FROM base_users`).Scan(t.Context(), &usersBefore); err != nil {
		t.Fatalf("count base_users before bootstrap: %v", err)
	}
	if usersBefore != 0 {
		t.Fatalf("expected base_users to be empty before fresh bootstrap, got %d", usersBefore)
	}

	previousAssets := embeddedAssets
	t.Cleanup(func() {
		embeddedAssets = previousAssets
	})
	moduleFS := os.DirFS(filepath.Clean(filepath.Join("..", "..", "modules")))
	if _, err := fs.Stat(moduleFS, "base/data"); err != nil {
		t.Fatalf("stat embedded test module fs: %v", err)
	}
	SetEmbeddedAssets(EmbeddedAssets{Modules: moduleFS})

	if err := bootstrapStartupTargetDatabase(t.Context(), tempDBName, false, true); err != nil {
		t.Fatalf("bootstrap startup target database for fresh db: %v", err)
	}

	var usersAfter int
	if err := db.DB.NewRaw(`SELECT COUNT(*) FROM base_users`).Scan(t.Context(), &usersAfter); err != nil {
		t.Fatalf("count base_users after bootstrap: %v", err)
	}
	if usersAfter == 0 {
		t.Fatalf("expected bootstrap to seed base_users for a fresh database even when initImport is false")
	}
}

func assertBootstrapTableExists(t *testing.T, db *bun.DB, tableName string) {
	t.Helper()

	var exists bool
	if err := db.NewRaw(`
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.tables
			WHERE table_schema = current_schema()
			  AND table_name = ?
		)
	`, tableName).Scan(t.Context(), &exists); err != nil {
		t.Fatalf("check table %s: %v", tableName, err)
	}
	if !exists {
		t.Fatalf("expected bootstrap to create table %q", tableName)
	}
}
