package models

import (
	"testing"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/modules/base-credential/secretbox"
)

func TestSysCredentialSecretsSealedUnderAppKey(t *testing.T) {
	previous := config.Get().App.EncryptKey
	t.Cleanup(func() { config.Update(func(cfg *config.AppConfig) { cfg.App.EncryptKey = previous }) })
	config.Update(func(cfg *config.AppConfig) { cfg.App.EncryptKey = "sys-credential-test-key-0123456789abcd" })

	normalized, err := normalizeSysCredentialValues(orm.RecordMap{
		"name": "erp", "credential_type": CredentialTypeAPIKey, "access_token": "api-key-123",
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := normalized["access_token"].(string)
	if !secretbox.IsCurrent(stored) {
		t.Fatalf("access_token %q is not a v1 ciphertext", stored)
	}
	if plain, err := SysCredentialDecryptHook(stored); err != nil || plain != "api-key-123" {
		t.Fatalf("decrypt = %q, %v", plain, err)
	}

	config.Update(func(cfg *config.AppConfig) { cfg.App.EncryptKey = "" })
	if _, err := normalizeSysCredentialValues(orm.RecordMap{"password": "x"}, false); err == nil {
		t.Fatal("encrypting without app.encryptKey must fail")
	}
}

// The model spec must carry the redacting overrides, or reads would return ciphertext again.
func TestSysCredentialSpecRedactsReadMethods(t *testing.T) {
	declared := map[string]bool{}
	for _, method := range SysCredentialModelSpec.RuntimeMethods {
		declared[method.Name] = true
	}
	for _, name := range []string{"read", "search_read", "search_fetch", "read_group", "copy_data", "copy", "find_or_create"} {
		if !declared[name] {
			t.Fatalf("sys.credential does not override %s", name)
		}
	}
}
