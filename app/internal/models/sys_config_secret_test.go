package models

import (
	"context"
	"errors"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestIsSecretConfigParamName(t *testing.T) {
	secret := []string{
		"postgresql.db_password", "mqtt.password", "auth_service.client_secret",
		"auth_service.rsa_private_key", "auth.oauth.rsa_private_key", "app.discuss_inbound_webhook_secret",
		"app.support_apikey", "app.encrypt_key", "authService.clientSecret", "postgresql.dbPassword",
		"minio.secret_key", "s3.access_key", "jwt.signing_key", "fcm.server_key", "smtp.passwd", "gcp.credentials", "sentry.dsn",
	}
	for _, name := range secret {
		if !isSecretConfigParamName(name) {
			t.Errorf("%s: want secret", name)
		}
	}
	plain := []string{
		"auth_service.rsa_key_id", "server.access_token_ttl_minutes", "mqtt.tls_key_file",
		"mqtt.user_name", "attachment.max_size_mb", "pwa.name", "",
	}
	for _, name := range plain {
		if isSecretConfigParamName(name) {
			t.Errorf("%s: want not secret", name)
		}
	}
}

func stubSysConfigSecret(t *testing.T, names ...string) {
	t.Helper()
	prevNames, prevDefault := sysConfigProtectedNames, sysConfigDefaultAccess
	sysConfigProtectedNames = func(_ context.Context, _ *orm.Environment, match func(string) bool) ([]string, error) {
		out := []string{}
		for _, name := range names {
			if match(name) {
				out = append(out, name)
			}
		}
		return out, nil
	}
	sysConfigDefaultAccess = func(context.Context, *orm.Environment, *orm.ModelMetadata, string, []string, orm.RecordMap) error {
		return nil
	}
	t.Cleanup(func() { sysConfigProtectedNames, sysConfigDefaultAccess = prevNames, prevDefault })
}

func sysConfigRPCEnv() *orm.Environment {
	return orm.NewEnvironment(nil, 7, map[string]any{orm.RBACContextAuthModeKey: "bearer"}, false)
}

func TestSysConfigSecretRecordRuleHidesSecretsFromRPC(t *testing.T) {
	stubSysConfigSecret(t, "auth_service.rsa_private_key", "postgresql.db_password", "auth_service.jwks_url", "pwa.name")
	ctx := context.Background()

	got, err := sysConfigSecretRecordRule(ctx, sysConfigRPCEnv(), nil, "read")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != orm.DomainKindCondition || got.Field != "name" || got.Operator != "not in" {
		t.Fatalf("RPC caller: want name not in secrets, got %+v", got)
	}
	if values, _ := got.Value.([]any); len(values) != 2 {
		t.Fatalf("RPC read: want the 2 secret names excluded, got %+v", got.Value)
	}
	// Writes also exclude the auth configuration (jwks_url decides which IdP is trusted).
	got, err = sysConfigSecretRecordRule(ctx, sysConfigRPCEnv(), nil, "write")
	if err != nil {
		t.Fatal(err)
	}
	if values, _ := got.Value.([]any); len(values) != 3 {
		t.Fatalf("RPC write: want 3 excluded names, got %+v", got.Value)
	}

	for label, env := range map[string]*orm.Environment{
		"sudo":     orm.NewEnvironment(nil, 1, map[string]any{orm.RBACContextAuthModeKey: "bearer"}, true),
		"internal": orm.NewEnvironment(nil, 0, nil, false),
	} {
		got, err := sysConfigSecretRecordRule(ctx, env, nil, "read")
		if err != nil || got.Kind != orm.DomainKindTrue {
			t.Fatalf("%s: want unrestricted, got %+v err %v", label, got, err)
		}
	}
}

func TestSysConfigSecretAccessRefusesSecretNames(t *testing.T) {
	stubSysConfigSecret(t)
	ctx := context.Background()
	meta := &orm.ModelMetadata{Name: SysConfigModelName}

	for _, op := range []string{"create", "write"} {
		err := sysConfigSecretAccess(ctx, sysConfigRPCEnv(), meta, op, nil, orm.RecordMap{"name": "auth_service.rsa_private_key", "value": "x"})
		var ormErr *orm.Error
		if !errors.As(err, &ormErr) || ormErr.Code != orm.ErrForbidden {
			t.Fatalf("%s secret name: err = %v, want forbidden", op, err)
		}
	}
	for label, values := range map[string]orm.RecordMap{
		"auth subtree": {"name": "auth_service.jwks_url", "value": "https://evil.example"},
		"auth parent":  {"name": "auth_service", "value": "x"},
		"object value": {"name": "postgresql", "value": map[string]any{"host": "evil.example"}},
	} {
		err := sysConfigSecretAccess(ctx, sysConfigRPCEnv(), meta, "create", nil, values)
		var ormErr *orm.Error
		if !errors.As(err, &ormErr) || ormErr.Code != orm.ErrForbidden {
			t.Fatalf("%s: err = %v, want forbidden", label, err)
		}
	}
	if err := sysConfigSecretAccess(ctx, sysConfigRPCEnv(), meta, "create", nil, orm.RecordMap{"name": "pwa.name", "value": "x"}); err != nil {
		t.Fatalf("plain key: %v", err)
	}
	if err := sysConfigSecretAccess(ctx, orm.NewEnvironment(nil, 1, nil, true), meta, "create", nil, orm.RecordMap{"name": "mqtt.password"}); err != nil {
		t.Fatalf("sudo: %v", err)
	}
}
