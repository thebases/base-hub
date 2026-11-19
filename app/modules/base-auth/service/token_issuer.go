package service

// File overview:
// - Responsibility: defines the `TokenIssuer` contract both the builtin OAuth 2.0 provider and
//   the external OIDC provider implement, plus the JWT claim shape self-issued access tokens
//   carry (RBAC/OAuth backend plan, §3-§5).
// - Key symbols: `TokenIssuer`, `TokenPair`, `AccessTokenClaims`.

import (
	"context"

	"git.thebase.vn/base_hub/internal/orm"
	"github.com/golang-jwt/jwt/v5"
)

// TokenPair is the RFC 6749 §5.1 access-token response shape.
type TokenPair struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int64 // seconds
	TokenType    string
	// Scope is only populated by grants that round-trip a requested scope
	// (currently authorization_code); empty for the others, which omit it
	// from the response rather than fabricate a value.
	Scope string
}

// TokenIssuer is implemented by both the builtin OAuth 2.0 provider and the
// external-OIDC provider so /oauth/token and bearer validation never need to
// know which one is active (RBAC/OAuth backend plan §5, CurrentProvider).
//
// dbName scopes issuance to one tenant database: the minted access token
// carries it as the "db" claim, and a token is only ever valid for the DB it
// was issued against (plan §12.5).
type TokenIssuer interface {
	IssuePassword(ctx context.Context, dbName, login, password string) (*TokenPair, error)
	IssueClientCredentials(ctx context.Context, dbName, clientID, clientSecret string) (*TokenPair, error)
	Refresh(ctx context.Context, dbName, refreshToken string) (*TokenPair, error)
	Validate(ctx context.Context, accessToken string) (*orm.RBACIdentity, error)
	Revoke(ctx context.Context, dbName, refreshToken string) error
}

// TokenValidator narrows TokenIssuer down to bearer-token validation only, for
// callers that check an incoming Authorization header and never issue,
// refresh, or revoke tokens themselves (service-bus WS auth, /rpc identity,
// MCP bearer auth). Both BuiltinProvider and OIDCProvider already satisfy
// this through their existing Validate method — no new implementation needed.
type TokenValidator interface {
	Validate(ctx context.Context, accessToken string) (*orm.RBACIdentity, error)
}

// AccessTokenClaims is the JWT claim set minted for a builtin access token.
type AccessTokenClaims struct {
	jwt.RegisteredClaims
	Login string `json:"login"`
	Role  string `json:"role"`
	// RoleID is base_users.group_id (the caller's primary group), distinct
	// from GroupIDs (the full expanded set including implied groups). Record
	// rules (internal/models/sys_rule.go, via orm.CallerGroupBIDs) match the
	// primary id plus GroupIDs; dropping either here makes an OAuth-issued
	// identity match no group rule, which now denies access (fail closed)
	// while ACL checks keep working, so the break is easy to miss. Same
	// class of bug as the Lang field previously being dropped from this
	// struct (see backend-context.md) — any new RBACIdentity field needs a
	// matching claims-struct update, not just an EnvironmentForIdentity one.
	RoleID       int64   `json:"role_id"`
	GroupIDs     []int64 `json:"group_ids"`
	CompanyID    int64   `json:"company_id"`
	CompanyIDs   []int64 `json:"company_ids"`
	DB           string  `json:"db"`
	IsSuperadmin bool    `json:"is_superadmin"`
	// Lang is the user's preferred locale (base_users.lang), carried through so
	// a session-info lookup after an OAuth login doesn't need a second DB read
	// to hydrate the frontend's user_context.
	Lang string `json:"lang"`
	// AuthMode records how this token was minted ("oauth_password",
	// "oauth_client_credentials") so downstream RBAC/audit code can tell an
	// OAuth-issued identity apart from the legacy auth paths.
	AuthMode string `json:"auth_mode"`
}
