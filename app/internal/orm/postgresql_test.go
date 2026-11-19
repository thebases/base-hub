package orm

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"git.thebase.vn/base_hub/internal/config"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestResolveNamedDatabaseConfigUsesExplicitNamedDatabaseConfig(t *testing.T) {
	previousConfig := *config.Get()
	t.Cleanup(func() {
		config.Set(previousConfig)
	})

	config.Update(func(c *config.AppConfig) {
		c.PostgresqlDb = config.PostgresqlConfig{
			Host:       "runtime-host",
			Port:       5432,
			User:       "runtime-user",
			Password:   "runtime-pass",
			HomeDBName: "home",
			Databases: map[string]config.PostgresqlConfig{
				"tenant-a": {
					Host:     "tenant-host",
					Port:     5433,
					User:     "tenant-user",
					Password: "tenant-pass",
				},
			},
		}
	})

	got := resolveNamedDatabaseConfig("tenant-a")
	if got.Host != "tenant-host" || got.Port != 5433 || got.User != "tenant-user" {
		t.Fatalf("resolveNamedDatabaseConfig(tenant-a) = %#v, want explicit named config", got)
	}
	if got.Name != "tenant-a" {
		t.Fatalf("resolveNamedDatabaseConfig(tenant-a).Name = %q, want %q", got.Name, "tenant-a")
	}
}

func TestResolveNamedDatabaseConfigUsesBootstrapConfigForHomeReconnect(t *testing.T) {
	previousConfig := *config.Get()
	t.Cleanup(func() {
		config.Set(previousConfig)
	})

	loadBootstrapConfigForTest(t, `
postgresql:
  db_host: bootstrap-host
  db_port: 5434
  db_user: bootstrap-user
  db_password: bootstrap-pass
  home_db_name: home
  db_sslmode: disable
`)
	config.Update(func(c *config.AppConfig) {
		c.PostgresqlDb = config.PostgresqlConfig{
			Host:       "runtime-host",
			Port:       5432,
			User:       "runtime-user",
			Password:   "runtime-pass",
			HomeDBName: "home",
		}
	})
	got := resolveNamedDatabaseConfig("home")
	if got.Host != "bootstrap-host" || got.Port != 5434 || got.User != "bootstrap-user" {
		t.Fatalf("resolveNamedDatabaseConfig(home) = %#v, want bootstrap config", got)
	}
	if got.Name != "home" {
		t.Fatalf("resolveNamedDatabaseConfig(home).Name = %q, want %q", got.Name, "home")
	}
}

func TestShouldCreateNamedDatabaseOnReconnect(t *testing.T) {
	dbMissingErr := &pgconn.PgError{Code: "3D000", Message: "database does not exist"}
	if !shouldCreateNamedDatabaseOnReconnect("tenant-a", dbMissingErr) {
		t.Fatalf("shouldCreateNamedDatabaseOnReconnect() = false, want true for pg error 3D000")
	}
	if !shouldCreateNamedDatabaseOnReconnect("tenant-a", fmt.Errorf("FATAL: database \"tenant-a\" does not exist")) {
		t.Fatalf("shouldCreateNamedDatabaseOnReconnect() = false, want true for plain-text missing-db error")
	}
	if shouldCreateNamedDatabaseOnReconnect("", dbMissingErr) {
		t.Fatalf("shouldCreateNamedDatabaseOnReconnect() = true, want false for empty logical name")
	}
	if shouldCreateNamedDatabaseOnReconnect("tenant-a", fmt.Errorf("permission denied")) {
		t.Fatalf("shouldCreateNamedDatabaseOnReconnect() = true, want false for unrelated error")
	}
}

func TestResolveRegisteredDatabaseDoesNotFallBackToDefault(t *testing.T) {
	bunDBRegistry.mu.Lock()
	previousDefaultName := bunDBRegistry.defaultName
	previousInstances := make(map[string]*BunDB, len(bunDBRegistry.instances))
	for name, db := range bunDBRegistry.instances {
		previousInstances[name] = db
	}
	bunDBRegistry.instances = map[string]*BunDB{"home": &BunDB{}}
	bunDBRegistry.defaultName = "home"
	bunDBRegistry.mu.Unlock()
	t.Cleanup(func() {
		bunDBRegistry.mu.Lock()
		bunDBRegistry.defaultName = previousDefaultName
		bunDBRegistry.instances = previousInstances
		bunDBRegistry.mu.Unlock()
	})

	if got := ResolveRegisteredDatabase("tenant-a"); got != nil {
		t.Fatalf("ResolveRegisteredDatabase(tenant-a) = %#v, want nil when tenant is not registered", got)
	}
	if got := ResolveDatabase("tenant-a"); got == nil {
		t.Fatalf("ResolveDatabase(tenant-a) = nil, want default fallback for non-lifecycle callers")
	}
}

func loadBootstrapConfigForTest(t *testing.T, yamlBody string) {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte(yamlBody), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	config.LoadConfig(configPath)
}
