package cmd

import (
	"testing"

	"git.thebase.vn/base_hub/internal/config"
)

func TestStartupBootstrapEnabledModulesForHomeDB(t *testing.T) {
	previous := *config.Get()
	t.Cleanup(func() {
		config.Set(previous)
	})

	config.Update(func(c *config.AppConfig) { c.PostgresqlDb.HomeDBName = "home" })
	if got := startupBootstrapEnabledModules("home"); got != nil {
		t.Fatalf("startupBootstrapEnabledModules(home) = %#v, want nil (multi-tenant is always on)", got)
	}
}
