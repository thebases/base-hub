package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRedactedForLogHidesEverySecret(t *testing.T) {
	var cfg AppConfig
	cfg.App.EncryptKey = "s-encrypt"
	cfg.App.DiscussInboundWebhookSecret = "s-webhook"
	cfg.MqttClient.Password = "s-mqtt"
	cfg.AuthService.ClientSecret = "s-client"
	cfg.PostgresqlDb.Password = "s-pg"
	cfg.PostgresqlDb.Databases = map[string]PostgresqlConfig{"t1": {Password: "s-tenant"}}

	dump := fmt.Sprintf("%+v", redactedForLog(cfg))
	for _, secret := range []string{"s-encrypt", "s-webhook", "s-mqtt", "s-client", "s-pg", "s-tenant"} {
		if strings.Contains(dump, secret) {
			t.Errorf("config dump leaks %q", secret)
		}
	}
	if cfg.PostgresqlDb.Databases["t1"].Password != "s-tenant" {
		t.Fatal("redactedForLog mutated the caller's Databases map")
	}
}

func TestWriteConfigFileIsOwnerOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := writeConfigFile(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Fatalf("mode = %o, want 600", mode)
	}
}
