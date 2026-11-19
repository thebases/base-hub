package service

// File overview:
// - Responsibility: implements the builtin OAuth 2.0 browser grants: Authorization Code
//   (RFC 6749 §4.1) and Implicit (RFC 6749 §4.2). Authorization Code issues a
//   short-lived, single-use code after a browser login at GET/POST /oauth/authorize and
//   exchanges it for a token pair at POST /oauth/token grant_type=authorization_code.
//   Implicit signs an access token directly after the same browser login and returns it in the
//   redirect URI fragment.
//   Builtin-issuer only, deliberately not part of the TokenIssuer interface: a hosted login page
//   is a builtin-specific feature, the external OIDC path's own Authorization Code + PKCE gap is
//   a separate proxying effort (RBAC/OAuth backend plan §13,
//   .agents/planning/2026-08-09-oauth-authorization-code-flow-plan.md).
// - Key symbols: `ValidateAuthorizeClient`, `IssueAuthorizationCode`, `IssueImplicitToken`,
//   `ExchangeAuthorizationCode`.

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	authmodels "git.thebase.vn/base_hub/modules/base-auth/models"
)

// authCodeTTL is short on purpose: an authorization code is a one-shot
// handoff value between the browser redirect and the immediate server-to-
// server token exchange, not a session credential (RFC 6749 §4.1.2 —
// recommended maximum lifetime is 10 minutes; this is well under that).
const authCodeTTL = 60 * time.Second

// AuthorizeClientPolicy is the per-client grant policy (sys.oauth_client allowed_grants and
// is_public) the authorize endpoint applies once the redirect_uri is trusted.
type AuthorizeClientPolicy interface {
	AllowsGrant(grant string) bool
	RequiresS256PKCE() bool
}

// ValidateAuthorizeClient confirms clientID exists and redirectURI is an
// exact match against its registered allow-list, and returns the client's
// grant policy. Callers must run this before rendering the login form or
// minting any code — an unregistered redirect_uri must never receive a
// redirect, the classic Authorization Code flow open-redirect vulnerability.
func ValidateAuthorizeClient(ctx context.Context, dbName, clientID, redirectURI string) (AuthorizeClientPolicy, error) {
	_, db, err := dbBoundContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	row, err := authmodels.LookupSysOauthClientByID(ctx, db, clientID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, fmt.Errorf("unknown client_id")
	}
	for _, uri := range row.RedirectURIs {
		if uri == redirectURI {
			return row, nil
		}
	}
	return nil, fmt.Errorf("redirect_uri is not registered for this client")
}

// validPKCEMethods are the two code_challenge_method values RFC 7636 defines.
// "plain" is accepted (matching Microsoft Entra's own leniency) even though
// S256 is the recommended default for any caller that can compute a SHA-256.
var validPKCEMethods = map[string]bool{"plain": true, "s256": true}

// ValidatePKCERequest checks the authorize-request-side PKCE parameters
// before any code is minted: code_challenge_method (if present) must be a
// known value and code_challenge must accompany it. code_challenge on its
// own, with no method, is valid — RFC 7636 defaults an omitted method to
// "plain". PKCE itself stays optional-if-present for this builtin flow (the
// existing ChatGPT Custom GPT Action client authenticates as a confidential
// client via client_secret and sends neither parameter).
func ValidatePKCERequest(codeChallenge, codeChallengeMethod string) error {
	method := strings.ToLower(strings.TrimSpace(codeChallengeMethod))
	if method == "" {
		return nil
	}
	if !validPKCEMethods[method] {
		return fmt.Errorf("code_challenge_method must be S256 or plain")
	}
	if strings.TrimSpace(codeChallenge) == "" {
		return fmt.Errorf("code_challenge is required when code_challenge_method is set")
	}
	return nil
}

// IssueAuthorizationCode verifies login/password against base_users (the
// same check IssuePassword uses) and, on success, stores a single-use code
// bound to clientID/redirectURI/the resolved user so the exchange step can
// trust it without re-authenticating. Returns the plaintext code — only its
// hash is persisted. Callers must call ValidateAuthorizeClient and
// ValidatePKCERequest first; this function does not re-check the
// redirect_uri allow-list or PKCE parameter shape.
func IssueAuthorizationCode(ctx context.Context, dbName, clientID, redirectURI, login, password, codeChallenge, codeChallengeMethod, scope string) (string, error) {
	authCtx, db, err := dbBoundContext(ctx, dbName)
	if err != nil {
		return "", err
	}
	identity, err := orm.AuthenticateRPCBasic(authCtx, login, password)
	if err != nil {
		return "", err
	}
	code, err := randomHex(32)
	if err != nil {
		return "", err
	}
	// RFC 7636 §4.3: a code_challenge with no explicit method defaults to "plain".
	method := strings.ToLower(strings.TrimSpace(codeChallengeMethod))
	if codeChallenge != "" && method == "" {
		method = "plain"
	}
	if err := authmodels.InsertSysOauthAuthCode(ctx, db, clientID, redirectURI, identity.UserID, dbName, code, time.Now().Add(authCodeTTL), codeChallenge, method, scope); err != nil {
		return "", err
	}
	return code, nil
}

// IssueImplicitToken verifies login/password and signs a short-lived access
// token without issuing a refresh token. The implicit flow returns credentials
// through the browser redirect fragment, so refresh tokens must not be minted
// here: a long-lived secret in front-channel browser history would be much
// harder to contain if leaked.
func IssueImplicitToken(ctx context.Context, dbName, login, password, scope string) (*TokenPair, error) {
	authCtx, _, err := dbBoundContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	identity, err := orm.AuthenticateRPCBasic(authCtx, login, password)
	if err != nil {
		return nil, err
	}
	identity.DBName = dbName
	key, err := LoadOrCreateSigningKey(ctx)
	if err != nil {
		return nil, err
	}
	accessToken, accessTTL, err := signAccessToken(key, identity, "oauth_implicit")
	if err != nil {
		return nil, err
	}
	return &TokenPair{
		AccessToken: accessToken,
		ExpiresIn:   int64(accessTTL.Seconds()),
		TokenType:   "Bearer",
		Scope:       scope,
	}, nil
}

// verifyPKCE checks a presented code_verifier against the code_challenge
// bound at issuance (RFC 7636 §4.6). An empty challenge means the authorize
// request didn't use PKCE, so the exchange doesn't require a verifier either
// — this flow treats PKCE as optional-if-present, not mandatory.
func verifyPKCE(challenge, method, verifier string) error {
	if challenge == "" {
		return nil
	}
	if verifier == "" {
		return fmt.Errorf("code_verifier is required for this authorization code")
	}
	switch strings.ToLower(method) {
	case "s256":
		sum := sha256.Sum256([]byte(verifier))
		computed := base64.RawURLEncoding.EncodeToString(sum[:])
		if subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) != 1 {
			return fmt.Errorf("code_verifier does not match code_challenge")
		}
	default: // "plain", or unset (normalized to "plain" at issuance)
		if subtle.ConstantTimeCompare([]byte(verifier), []byte(challenge)) != 1 {
			return fmt.Errorf("code_verifier does not match code_challenge")
		}
	}
	return nil
}

// ExchangeAuthorizationCode redeems a single-use code minted by
// IssueAuthorizationCode for a fresh access+refresh token pair. Verifies the
// client secret, that redirect_uri matches what was bound at issuance
// (RFC 6749 §4.1.3 — prevents a stolen code being replayed against a
// different redirect target), and — if the authorize request used PKCE —
// that codeVerifier hashes/matches the bound code_challenge (RFC 7636 §4.6).
// dbName is resolved by the caller (oauth_route.go) from the
// X-Database-Name header, since ChatGPT's exchange request body has no "db"
// field the way the other grants get one — an operator-run proxy in front of
// Base HUB is expected to inject that header on the token-exchange call.
func ExchangeAuthorizationCode(ctx context.Context, dbName, clientID, clientSecret, code, redirectURI, codeVerifier string) (*TokenPair, error) {
	authCtx, db, err := dbBoundContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	// Consume first: a code must be single-use even if the client-secret check
	// below fails, otherwise an attacker could brute-force the secret while
	// replaying the same still-valid code across attempts.
	row, err := authmodels.ConsumeSysOauthAuthCode(ctx, db, code)
	if err != nil {
		return nil, err
	}
	clientRow, err := authmodels.LookupSysOauthClientByID(ctx, db, clientID)
	if err != nil {
		return nil, err
	}
	if clientRow == nil || !authmodels.VerifyClientSecret(clientRow.ClientSecretHash, clientSecret) {
		return nil, fmt.Errorf("invalid client credentials")
	}
	if row.ClientID != clientID || row.RedirectURI != redirectURI {
		return nil, fmt.Errorf("redirect_uri does not match the authorization request")
	}
	// Re-checked here, not only at /oauth/authorize: the policy may have changed in between,
	// and a public client's code must carry an S256 challenge (plan T36).
	if !clientRow.AllowsGrant(authmodels.OAuthGrantAuthorizationCode) {
		return nil, fmt.Errorf("client is not allowed to use authorization_code")
	}
	if clientRow.RequiresS256PKCE() && !strings.EqualFold(row.CodeChallengeMethod, "s256") {
		return nil, fmt.Errorf("this client must use S256 PKCE")
	}
	if err := verifyPKCE(row.CodeChallenge, row.CodeChallengeMethod, codeVerifier); err != nil {
		return nil, err
	}
	identity, err := identityForRefresh(authCtx, row.UserID, dbName)
	if err != nil {
		return nil, err
	}
	pair, err := NewBuiltinProvider().mintTokenPair(ctx, db, identity, "oauth_authorization_code")
	if err != nil {
		return nil, err
	}
	pair.Scope = row.Scope
	return pair, nil
}
