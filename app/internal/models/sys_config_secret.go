package models

// File overview:
// - Responsibility: keeps secret sys.config_param rows (plan T17b) away from RPC and MCP
//   callers that are not superadmin. The runtime config is synced into this table, so it holds
//   the database and broker passwords, the auth service client secret, the inbound mail webhook
//   secret and auth_service.rsa_private_key (the key that signs every builtin access token:
//   reading it lets base.group_system mint a superadmin token).
// - Key symbols: `isSecretConfigParamName`, `sysConfigSecretRecordRule`, `sysConfigSecretAccess`.
// - Pattern: a record rule hides secret rows from read, and secret plus auth_service.*/auth.* rows
//   (who the server trusts as identity provider) from write/unlink; an access handler refuses
//   create/rename onto those names and JSON-object values (a parent row such as "postgresql"
//   expands into nested keys at sync and could override the protected leaves). Both act only on
//   callers with an RPC identity: kernel services read the table with raw SQL as before.
//   Registering a model rule replaces the generic sys.rule provider for this model; no sys.rule
//   rows target sys.config_param.

import (
	"context"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
)

// secretConfigParamSuffixes match the last segment of a canonical (snake_case, dotted) key.
var secretConfigParamSuffixes = []string{
	"password", "passwd", "secret", "secret_key", "private_key", "signing_key", "server_key", "hmac_key",
	"access_key", "apikey", "api_key", "encrypt_key", "token", "credential", "credentials", "dsn",
}

// writeProtectedConfigParamPrefixes configure which identity provider and tokens the server
// trusts; changing them is as good as minting tokens, so writes need a superadmin.
var writeProtectedConfigParamPrefixes = []string{"auth_service", "auth"}

func init() {
	orm.RegisterRecordRule(SysConfigModelName, sysConfigSecretRecordRule)
	orm.RegisterAccessHandler(SysConfigModelName, sysConfigSecretAccess)
}

// isSecretConfigParamName reports whether a config key holds a credential or key material.
// Any spelling is accepted: camelCase segments are lowered and underscores ignored for the match.
func isSecretConfigParamName(name string) bool {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return false
	}
	last := trimmed
	if idx := strings.LastIndex(trimmed, "."); idx >= 0 {
		last = trimmed[idx+1:]
	}
	compact := strings.ReplaceAll(strings.ToLower(last), "_", "")
	for _, suffix := range secretConfigParamSuffixes {
		if strings.HasSuffix(compact, strings.ReplaceAll(suffix, "_", "")) {
			return true
		}
	}
	return false
}

// isWriteProtectedConfigParamName reports whether a non-superadmin may not create, change or
// delete the key: secrets plus the auth configuration subtree.
func isWriteProtectedConfigParamName(name string) bool {
	trimmed := strings.TrimSpace(name)
	if isSecretConfigParamName(trimmed) {
		return true
	}
	for _, prefix := range writeProtectedConfigParamPrefixes {
		if trimmed == prefix || strings.HasPrefix(trimmed, prefix+".") {
			return true
		}
	}
	return false
}

// sysConfigRestrictsCaller is true for non-sudo callers that came in over RPC/MCP.
func sysConfigRestrictsCaller(env *orm.Environment) bool {
	if env == nil || env.IsSudo() {
		return false
	}
	_, ok := env.ContextValue(orm.RBACContextAuthModeKey)
	return ok
}

// sysConfigDefaultAccess is a seam over the standard ACL check for tests.
var sysConfigDefaultAccess = orm.EnsureDefaultModelAccess

// sysConfigProtectedNames is a seam over the table scan for tests: the stored names that match.
var sysConfigProtectedNames = func(ctx context.Context, env *orm.Environment, match func(string) bool) ([]string, error) {
	db := env.DB()
	if db == nil {
		db = orm.GetBunDB()
	}
	var names []string
	if err := db.NewSelect().TableExpr("sys_config_param").Column("name").Scan(ctx, &names); err != nil {
		return nil, fmt.Errorf("sys.config_param: list names: %w", err)
	}
	secret := make([]string, 0)
	for _, name := range names {
		if match(name) {
			secret = append(secret, name)
		}
	}
	return secret, nil
}

func sysConfigSecretRecordRule(ctx context.Context, env *orm.Environment, _ *orm.ModelMetadata, operation string) (orm.DomainNode, error) {
	if !sysConfigRestrictsCaller(env) {
		return orm.TrueDomain(), nil
	}
	match := isSecretConfigParamName
	if operation != "read" {
		match = isWriteProtectedConfigParamName
	}
	names, err := sysConfigProtectedNames(ctx, env, match)
	if err != nil {
		return orm.DomainNode{}, err
	}
	if len(names) == 0 {
		return orm.TrueDomain(), nil
	}
	values := make([]any, 0, len(names))
	for _, name := range names {
		values = append(values, name)
	}
	return orm.Where("name", "not in", values), nil
}

func sysConfigSecretAccess(ctx context.Context, env *orm.Environment, model *orm.ModelMetadata, operation string, ids []string, values orm.RecordMap) error {
	if err := sysConfigDefaultAccess(ctx, env, model, operation, ids, values); err != nil {
		return err
	}
	if operation == "read" || !sysConfigRestrictsCaller(env) {
		return nil
	}
	if raw, ok := values["name"]; ok && isWriteProtectedConfigParamName(fmt.Sprint(raw)) {
		return orm.Forbiddenf("sys.config_param: only a superadmin may set %s", strings.TrimSpace(fmt.Sprint(raw)))
	}
	if raw, ok := values["value"]; ok {
		if _, isObject := raw.(map[string]any); isObject {
			return orm.Forbiddenf("sys.config_param: only a superadmin may store a JSON object value")
		}
	}
	return nil
}
