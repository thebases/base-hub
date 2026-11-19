package service

// File overview:
// - Responsibility: test-only helpers that let DB-free unit tests obtain a bearer token
//   BuiltinProvider.Validate accepts, without a live Postgres connection. Production issuance
//   always goes through IssuePassword/IssueClientCredentials/Refresh, all of which persist a
//   refresh token row; these helpers skip that persistence on purpose (RBAC/OAuth backend plan
//   §1/§7.4 test-update requirement — every test that used to authenticate via the now-removed
//   X-API-Key/api_key/mcp_api_key/authtoken paths needs an OAuth-bearer equivalent, including the
//   route-layer unit tests that never touched a database in the first place).
// - Key symbols: `MintAccessTokenForTests`.

import (
	"context"
	"fmt"

	"git.thebase.vn/base_hub/internal/orm"
)

// MintAccessTokenForTests signs a builtin-shaped access token directly from
// the given identity claims, skipping refresh-token persistence so callers
// with no live database can still obtain a bearer token accepted by
// BuiltinProvider.Validate. Pair with SetSigningKeyForTests so signing itself
// never needs sys_config_param either.
func MintAccessTokenForTests(identity *orm.RBACIdentity, authMode string) (string, error) {
	if identity == nil {
		return "", fmt.Errorf("auth: identity is required")
	}
	key, err := LoadOrCreateSigningKey(context.Background())
	if err != nil {
		return "", err
	}
	token, _, err := signAccessToken(key, identity, authMode)
	return token, err
}
