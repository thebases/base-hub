package models

import (
	"context"
	"errors"
	"testing"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/modules/base-credential/secretbox"
)

func withWorkflowTestEncryptKey(t *testing.T) {
	t.Helper()
	previous := config.Get().App.EncryptKey
	t.Cleanup(func() { config.Update(func(cfg *config.AppConfig) { cfg.App.EncryptKey = previous }) })
	config.Update(func(cfg *config.AppConfig) { cfg.App.EncryptKey = "workflow-credential-test-key-0123456789" })
}

func TestWorkflowCredentialRefusesClientCiphertext(t *testing.T) {
	withWorkflowTestEncryptKey(t)
	for _, creating := range []bool{true, false} {
		_, err := normalizeWorkflowCredentialValues(context.Background(), orm.RecordMap{
			"name":            "k",
			"encrypted_value": "attacker-chosen",
		}, nil, creating)
		var typed *orm.Error
		if !errors.As(err, &typed) || typed.Code != orm.ErrInvalidParams {
			t.Fatalf("creating=%v: err = %v, want invalid params", creating, err)
		}
	}
}

func TestWorkflowCredentialValueIsSealedUnderAppKey(t *testing.T) {
	withWorkflowTestEncryptKey(t)
	normalized, err := normalizeWorkflowCredentialValues(context.Background(), orm.RecordMap{
		"name":  "hmac",
		"value": "plain-secret",
	}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := normalized["value"]; ok {
		t.Fatal("plaintext value must not be persisted")
	}
	stored, _ := normalized["encrypted_value"].(string)
	if !secretbox.IsCurrent(stored) {
		t.Fatalf("encrypted_value %q is not a v1 ciphertext", stored)
	}
	plain, err := WorkflowCredentialDecryptHook(stored)
	if err != nil || plain != "plain-secret" {
		t.Fatalf("decrypt = %q, %v", plain, err)
	}
}
