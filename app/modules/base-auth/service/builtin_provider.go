package service

// File overview:
// - Responsibility: implements `TokenIssuer` against this service's own built-in OAuth 2.0
//   authorization server — self-issued RS256 JWT access tokens plus rotating, hashed-at-rest
//   opaque refresh tokens (RBAC/OAuth backend plan, §3). Reuses the existing password-check and
//   RBAC-row-enrichment logic in internal/orm/runtime_rbac.go rather than duplicating it.
// - Key symbols: `BuiltinProvider`, `NewBuiltinProvider`.
// - Security: `alg` is pinned to RS256 on both signing and verification (rejects `alg: none`,
//   SEC hardening checklist item); refresh tokens rotate on every use and reuse of an already
//   revoked token revokes the whole token family (breach-detection pattern).

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	authmodels "git.thebase.vn/base_hub/modules/base-auth/models"
	"github.com/golang-jwt/jwt/v5"
	"github.com/uptrace/bun"
)

const (
	defaultAccessTokenTTL  = 15 * time.Minute
	defaultRefreshTokenTTL = 30 * 24 * time.Hour
	defaultIssuer          = "base-hub"

	authModeClientCredentials = "oauth_client_credentials"
	clientSubjectPrefix       = "client:"
)

// lookupRBACIdentityByUserID is a test seam over the base_users lookup that
// Refresh re-reads the caller's current RBAC scope from.
var lookupRBACIdentityByUserID = orm.LookupRBACIdentityByUserID

// BuiltinProvider implements TokenIssuer using this process's own signing key.
type BuiltinProvider struct{}

// NewBuiltinProvider constructs the default, always-available provider.
func NewBuiltinProvider() *BuiltinProvider {
	return &BuiltinProvider{}
}

func accessTokenTTL() time.Duration {
	minutes := config.Get().Server.AccessTokenTTLMinutes
	if minutes <= 0 {
		return defaultAccessTokenTTL
	}
	return time.Duration(minutes) * time.Minute
}

func refreshTokenTTL() time.Duration {
	days := config.Get().Server.RefreshTokenTTLDays
	if days <= 0 {
		return defaultRefreshTokenTTL
	}
	return time.Duration(days) * 24 * time.Hour
}

func tokenIssuer() string {
	issuer := strings.TrimSpace(config.Get().Server.Issuer)
	if issuer == "" {
		return defaultIssuer
	}
	return issuer
}

// IssuePassword authenticates login/password against base_users (reusing
// orm.AuthenticateRPCBasic's bcrypt/legacy-hash check) and mints a token pair.
func (p *BuiltinProvider) IssuePassword(ctx context.Context, dbName, login, password string) (*TokenPair, error) {
	authCtx, db, err := dbBoundContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	identity, err := orm.AuthenticateRPCBasic(authCtx, login, password)
	if err != nil {
		return nil, err
	}
	identity.DBName = dbName
	return p.mintTokenPair(ctx, db, identity, "oauth_password")
}

// IssueClientCredentials authenticates a registered sys.oauth_client and
// mints an access token bound to that client's configured identity — the
// direct successor to "one shared static X-API-Key = superadmin" (plan §3).
// No refresh token is issued (RFC 6749 §4.4.3): the client re-authenticates
// with its secret instead, and a refresh row would carry a sys_oauth_client
// id that Refresh would otherwise resolve as a base_users id (SEC-T-3).
func (p *BuiltinProvider) IssueClientCredentials(ctx context.Context, dbName, clientID, clientSecret string) (*TokenPair, error) {
	_, db, err := dbBoundContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	row, err := authmodels.LookupSysOauthClientByID(ctx, db, clientID)
	if err != nil {
		return nil, err
	}
	if row == nil || !authmodels.VerifyClientSecret(row.ClientSecretHash, clientSecret) {
		return nil, fmt.Errorf("invalid client credentials")
	}
	if !row.AllowsGrant(authmodels.OAuthGrantClientCredentials) {
		return nil, fmt.Errorf("client is not allowed to use client_credentials")
	}
	identity := &orm.RBACIdentity{
		UserID:       row.ID,
		DBName:       dbName,
		Login:        "client:" + strings.TrimSpace(clientID),
		RoleID:       row.RoleID,
		IsSuperadmin: row.IsSuperadmin,
	}
	return mintAccessTokenOnly(ctx, identity, authModeClientCredentials)
}

// mintAccessTokenOnly signs an access token with no refresh token, for grants
// that must not be renewable without re-presenting their credentials.
func mintAccessTokenOnly(ctx context.Context, identity *orm.RBACIdentity, authMode string) (*TokenPair, error) {
	key, err := LoadOrCreateSigningKey(ctx)
	if err != nil {
		return nil, err
	}
	accessToken, accessTTL, err := signAccessToken(key, identity, authMode)
	if err != nil {
		return nil, err
	}
	return &TokenPair{AccessToken: accessToken, ExpiresIn: int64(accessTTL.Seconds()), TokenType: "Bearer"}, nil
}

// Refresh rotates a refresh token: the presented token is revoked and a fresh
// pair is issued in the same family, so a stolen-and-replayed refresh token
// is detectable the next time either party uses it (RBAC/OAuth plan §3).
func (p *BuiltinProvider) Refresh(ctx context.Context, dbName, refreshToken string) (*TokenPair, error) {
	authCtx, db, err := dbBoundContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	// Consume and mint commit together. A concurrent refresh with the same
	// token blocks on the consumed row until this transaction commits, then
	// sees it revoked (reuse) and its family revoke below also kills the token
	// minted here. A failed mint rolls the consume back instead of burning the
	// session.
	var pair *TokenPair
	var revokeFamily string
	var refusal error
	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		row, reused, err := authmodels.ConsumeSysOauthRefreshToken(ctx, tx, refreshToken)
		if err != nil {
			return err
		}
		if reused {
			// Breach-detection: a revoked token was presented again. Invalidate
			// the whole chain rather than just failing this one request.
			revokeFamily = row.FamilyID
			refusal = fmt.Errorf("refresh token has been revoked")
			return nil
		}
		// Only rows stamped as user rows may be refreshed: user_id is fed to
		// the base_users lookup below, and legacy/client rows hold a
		// sys_oauth_client id there, so client row N would become user N
		// (often the admin). Their whole family is revoked so a client chain
		// can never be resumed.
		if row.SubjectKind != authmodels.RefreshTokenSubjectUser {
			log.Warnf("auth: refused refresh token not issued to a user (family revoked, kind=%q)", row.SubjectKind)
			revokeFamily = row.FamilyID
			refusal = orm.Forbiddenf("refresh token was not issued to a user, sign in again")
			return nil
		}
		identity, err := identityForRefresh(authCtx, row.UserID, row.DBName)
		if err != nil {
			return err
		}
		pair, err = p.mintTokenPairInFamily(ctx, tx, identity, "oauth_refresh", row.FamilyID)
		return err
	})
	if err != nil {
		return nil, err
	}
	if revokeFamily != "" {
		_ = authmodels.RevokeSysOauthRefreshTokenFamily(ctx, db, revokeFamily)
		return nil, refusal
	}
	return pair, nil
}

// Validate parses and verifies a self-issued JWT: signature, exp, and iss are
// checked against this process's own key, no network call or DB round trip
// needed. alg is pinned to RS256 so `alg: none` is always rejected.
func (p *BuiltinProvider) Validate(ctx context.Context, accessToken string) (*orm.RBACIdentity, error) {
	key, err := LoadOrCreateSigningKey(ctx)
	if err != nil {
		return nil, err
	}
	claims := &AccessTokenClaims{}
	token, err := jwt.ParseWithClaims(accessToken, claims, func(t *jwt.Token) (any, error) {
		return key.PublicKey, nil
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(tokenIssuer()))
	if err != nil || !token.Valid {
		return nil, fmt.Errorf("invalid access token")
	}
	return &orm.RBACIdentity{
		UserID:       userIDFromSubject(claims.Subject),
		DBName:       claims.DB,
		Login:        claims.Login,
		RoleID:       claims.RoleID,
		RoleName:     claims.Role,
		GroupIDs:     claims.GroupIDs,
		CompanyID:    claims.CompanyID,
		CompanyIDs:   claims.CompanyIDs,
		AuthMode:     claims.AuthMode,
		IsSuperadmin: claims.IsSuperadmin,
		Lang:         claims.Lang,
	}, nil
}

// Revoke invalidates one refresh token (best-effort logout hook, plan §12.3).
func (p *BuiltinProvider) Revoke(ctx context.Context, dbName, refreshToken string) error {
	_, db, err := dbBoundContext(ctx, dbName)
	if err != nil {
		return err
	}
	return authmodels.RevokeSysOauthRefreshTokenByHash(ctx, db, refreshToken)
}

func (p *BuiltinProvider) mintTokenPair(ctx context.Context, db bun.IDB, identity *orm.RBACIdentity, authMode string) (*TokenPair, error) {
	familyID, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	return p.mintTokenPairInFamily(ctx, db, identity, authMode, familyID)
}

func (p *BuiltinProvider) mintTokenPairInFamily(ctx context.Context, db bun.IDB, identity *orm.RBACIdentity, authMode, familyID string) (*TokenPair, error) {
	key, err := LoadOrCreateSigningKey(ctx)
	if err != nil {
		return nil, err
	}
	accessToken, accessTTL, err := signAccessToken(key, identity, authMode)
	if err != nil {
		return nil, err
	}

	refreshToken, err := randomHex(32)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	refreshTTL := refreshTokenTTL()
	if err := authmodels.InsertSysOauthRefreshToken(ctx, db, identity.UserID, identity.DBName, familyID, refreshToken, now, now.Add(refreshTTL)); err != nil {
		return nil, err
	}

	return &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    int64(accessTTL.Seconds()),
		TokenType:    "Bearer",
	}, nil
}

// signAccessToken builds and signs one builtin-shaped access token from the
// given identity/authMode, returning the signed string and the TTL used to
// stamp its expiry. Extracted from mintTokenPairInFamily so
// MintAccessTokenForTests (testsupport.go) can mint a bearer token without
// going through refresh-token persistence, which needs a live DB.
func signAccessToken(key *SigningKey, identity *orm.RBACIdentity, authMode string) (string, time.Duration, error) {
	now := time.Now()
	accessTTL := accessTokenTTL()
	claims := &AccessTokenClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   accessTokenSubject(identity, authMode),
			Issuer:    tokenIssuer(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(accessTTL)),
		},
		Login:        identity.Login,
		Role:         identity.RoleName,
		RoleID:       identity.RoleID,
		GroupIDs:     identity.GroupIDs,
		CompanyID:    identity.CompanyID,
		CompanyIDs:   identity.CompanyIDs,
		DB:           identity.DBName,
		IsSuperadmin: identity.IsSuperadmin,
		AuthMode:     authMode,
		Lang:         identity.Lang,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = key.KeyID
	signed, err := token.SignedString(key.PrivateKey)
	if err != nil {
		return "", 0, fmt.Errorf("auth: sign access token: %w", err)
	}
	return signed, accessTTL, nil
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("auth: generate random token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// accessTokenSubject renders the JWT "sub": "client:<sys_oauth_client id>"
// for client_credentials so a client token is never mistaken for a user id,
// otherwise the base_users id.
func accessTokenSubject(identity *orm.RBACIdentity, authMode string) string {
	if authMode == authModeClientCredentials {
		return fmt.Sprintf("%s%d", clientSubjectPrefix, identity.UserID)
	}
	return fmt.Sprintf("%d", identity.UserID)
}

// userIDFromSubject maps "sub" back to the environment uid. A client subject
// still yields its sys_oauth_client id, as before this marker existed: the
// kernel RBAC treats uid <= 0 as anonymous (rbacCallerGroupIDs), so uid 0
// would lock out every role-scoped client. The marker plus auth_mode keep the
// two kinds distinguishable for any consumer that needs to tell them apart.
func userIDFromSubject(subject string) int64 {
	var id int64
	_, _ = fmt.Sscanf(strings.TrimPrefix(subject, clientSubjectPrefix), "%d", &id)
	return id
}

// identityForRefresh rebuilds a minimal identity for token re-issuance,
// re-reading the user's current role/company scope so a refreshed access
// token reflects any RBAC changes made since the last login rather than
// replaying stale claims from the prior token.
func identityForRefresh(ctx context.Context, userID int64, dbName string) (*orm.RBACIdentity, error) {
	identity, err := lookupRBACIdentityByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if identity == nil {
		return nil, fmt.Errorf("user no longer exists")
	}
	identity.DBName = dbName
	return identity, nil
}
