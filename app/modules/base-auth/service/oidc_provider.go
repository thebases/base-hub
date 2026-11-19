package service

// File overview:
// - Responsibility: implements `TokenIssuer` against an external OIDC-compliant provider
//   (Casdoor, Keycloak, ...), active only when `AuthService.Enable` is true (RBAC/OAuth backend
//   plan §4). `Validate` verifies signature/iss/aud/exp against the IdP's own JWKS; `IssuePassword`
//   and `IssueClientCredentials` proxy to the IdP's RFC-compliant token endpoint so callers never
//   need to know which issuer is behind `/oauth/token`.
// - Key symbols: `OIDCProvider`, `NewOIDCProvider`.
// - Security: JWKS/IdP outage fails closed — Validate never falls back to trusting an unverified
//   token (hardening checklist). `alg` is pinned to RS256, same as the builtin provider.

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/orm"
	"github.com/golang-jwt/jwt/v5"
)

// jwkToRSAPublicKey decodes a JWK's base64url-encoded modulus/exponent into
// an *rsa.PublicKey usable by jwt.ParseWithClaims.
func jwkToRSAPublicKey(key *jwksKey) (*rsa.PublicKey, error) {
	if key.kty != "RSA" {
		return nil, fmt.Errorf("oidc: unsupported key type %q", key.kty)
	}
	nBytes, err := base64.RawURLEncoding.DecodeString(key.n)
	if err != nil {
		return nil, fmt.Errorf("oidc: decode jwk modulus: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(key.e)
	if err != nil {
		return nil, fmt.Errorf("oidc: decode jwk exponent: %w", err)
	}
	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(nBytes),
		E: int(new(big.Int).SetBytes(eBytes).Int64()),
	}, nil
}

const jwksCacheTTL = 10 * time.Minute

// jwksMinRefetch bounds forced JWKS refetches: a token naming an unknown kid
// triggers at most one refetch per interval, so a stream of forged kids
// cannot turn every request into an IdP round trip under p.mu.
const jwksMinRefetch = time.Minute

// OIDCProvider implements TokenIssuer by delegating to an external IdP.
type OIDCProvider struct {
	httpClient *http.Client

	mu         sync.Mutex
	jwksCached time.Time
	jwksForced time.Time // last forced refetch attempt, successful or not
	keys       map[string]*jwksKey
	nowFunc    func() time.Time // test seam; nil = time.Now
}

type jwksKey struct {
	kty string
	n   string
	e   string
}

// NewOIDCProvider constructs a provider bound to the currently configured IdP.
func NewOIDCProvider() *OIDCProvider {
	return &OIDCProvider{httpClient: &http.Client{Timeout: 10 * time.Second}}
}

// IssuePassword proxies grant_type=password to the IdP's token endpoint.
func (p *OIDCProvider) IssuePassword(ctx context.Context, dbName, login, password string) (*TokenPair, error) {
	form := url.Values{
		"grant_type": {"password"},
		"username":   {login},
		"password":   {password},
		"scope":      {strings.TrimSpace(config.Get().AuthService.Scopes)},
	}
	// Keycloak/Casdoor reject Direct Access Grant requests without client_id
	// (and client_secret for confidential clients) — the pilot's registered
	// client identity, distinct from the caller's login/password.
	withClientCredentials(form)
	return p.proxyTokenRequest(ctx, form)
}

// IssueClientCredentials proxies grant_type=client_credentials to the IdP.
func (p *OIDCProvider) IssueClientCredentials(ctx context.Context, dbName, clientID, clientSecret string) (*TokenPair, error) {
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"scope":         {strings.TrimSpace(config.Get().AuthService.Scopes)},
	}
	return p.proxyTokenRequest(ctx, form)
}

// Refresh proxies grant_type=refresh_token to the IdP.
func (p *OIDCProvider) Refresh(ctx context.Context, dbName, refreshToken string) (*TokenPair, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}
	withClientCredentials(form)
	return p.proxyTokenRequest(ctx, form)
}

// withClientCredentials attaches the pilot's registered IdP client_id (and
// client_secret, when the client is confidential) to an outgoing token
// request. Without this, Keycloak/Casdoor reject password and refresh_token
// grants with invalid_client even though the credentials are correct.
func withClientCredentials(form url.Values) {
	clientID := strings.TrimSpace(config.Get().AuthService.ClientID)
	if clientID != "" {
		form.Set("client_id", clientID)
	}
	if clientSecret := config.Get().AuthService.ClientSecret; clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}
}

// Revoke is best-effort: external IdPs are not required to expose a
// standard revocation endpoint this provider can rely on, so no-op is the
// safe default rather than guessing at an unconfigured URL.
func (p *OIDCProvider) Revoke(ctx context.Context, dbName, refreshToken string) error {
	return nil
}

// Validate verifies an IdP-issued JWT: signature via JWKS, iss, aud, exp with
// configured clock-skew tolerance, then maps claims to an RBACIdentity via
// RoleClaim/LoginClaim. AutoProvision stays false by default: an unknown
// login is rejected rather than silently creating a backend account.
func (p *OIDCProvider) Validate(ctx context.Context, accessToken string) (*orm.RBACIdentity, error) {
	cfg := config.Get().AuthService
	keys, err := p.jwks(ctx)
	if err != nil {
		// Fail closed: an unreachable/broken JWKS endpoint must never be
		// treated as "trust the token anyway" (hardening checklist).
		return nil, fmt.Errorf("oidc: jwks unavailable: %w", err)
	}

	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(accessToken, claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		key, ok := keys[kid]
		if !ok {
			// The IdP may have rotated its signing key since our last cached
			// fetch (up to jwksCacheTTL stale); force one refresh before
			// rejecting so rotation doesn't cause a window of false denials.
			refreshed, refreshErr := p.forceRefreshJWKS(ctx)
			if refreshErr != nil {
				return nil, fmt.Errorf("oidc: unknown key id %q: refresh failed: %w", kid, refreshErr)
			}
			key, ok = refreshed[kid]
			if !ok {
				return nil, fmt.Errorf("oidc: unknown key id %q", kid)
			}
		}
		return jwkToRSAPublicKey(key)
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(cfg.Issuer),
		jwt.WithAudience(cfg.Audience),
		jwt.WithLeeway(time.Duration(cfg.ClockSkewSecs)*time.Second))
	if err != nil || !token.Valid {
		return nil, fmt.Errorf("oidc: invalid token: %w", err)
	}

	loginClaim := strings.TrimSpace(cfg.LoginClaim)
	if loginClaim == "" {
		loginClaim = "preferred_username"
	}
	login, _ := claims[loginClaim].(string)
	login = strings.TrimSpace(login)
	if login == "" {
		return nil, fmt.Errorf("oidc: token is missing claim %q", loginClaim)
	}

	// SEC-T-11: bind the token to one tenant DB and resolve the login there,
	// never in whatever DB the request context happens to carry. The caller
	// (EnsureIdentityDBMatch) then refuses the token on any other tenant.
	dbName, err := oidcTokenDBName(claims, cfg.TenantClaim, config.HomeDBName())
	if err != nil {
		return nil, err
	}
	dbCtx, _, err := dbBoundContext(ctx, dbName)
	if err != nil {
		return nil, err
	}
	identity, err := orm.LookupRBACIdentityByLogin(dbCtx, login)
	if err != nil {
		return nil, err
	}
	if identity == nil {
		if !cfg.AutoProvision {
			return nil, fmt.Errorf("oidc: no local account for login %q", login)
		}
		return nil, fmt.Errorf("oidc: auto-provisioning is not implemented")
	}
	identity.AuthMode = "oidc"
	identity.DBName = dbName
	return identity, nil
}

// oidcTokenDBName returns the tenant DB an IdP token is valid for: the string
// value of tenantClaim when one is configured (required, fail closed when
// missing), otherwise the home DB.
func oidcTokenDBName(claims jwt.MapClaims, tenantClaim, homeDBName string) (string, error) {
	tenantClaim = strings.TrimSpace(tenantClaim)
	if tenantClaim == "" {
		homeDBName = strings.TrimSpace(homeDBName)
		if homeDBName == "" {
			return "", fmt.Errorf("oidc: home database is not configured")
		}
		return homeDBName, nil
	}
	dbName, _ := claims[tenantClaim].(string)
	dbName = strings.TrimSpace(dbName)
	if dbName == "" {
		return "", fmt.Errorf("oidc: token is missing claim %q", tenantClaim)
	}
	return dbName, nil
}

func (p *OIDCProvider) proxyTokenRequest(ctx context.Context, form url.Values) (*TokenPair, error) {
	endpoint := strings.TrimSpace(config.Get().AuthService.Endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("oidc: AuthService.Endpoint is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("oidc: token request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("oidc: token endpoint returned %d: %s", resp.StatusCode, string(body))
	}
	var payload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		TokenType    string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("oidc: decode token response: %w", err)
	}
	return &TokenPair{
		AccessToken:  payload.AccessToken,
		RefreshToken: payload.RefreshToken,
		ExpiresIn:    payload.ExpiresIn,
		TokenType:    payload.TokenType,
	}, nil
}

// jwks returns the cached JWKS key set, refetching every jwksCacheTTL.
func (p *OIDCProvider) jwks(ctx context.Context) (map[string]*jwksKey, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.keys != nil && p.now().Sub(p.jwksCached) < jwksCacheTTL {
		return p.keys, nil
	}
	return p.fetchJWKS(ctx)
}

// forceRefreshJWKS bypasses the TTL and refetches, used when a token
// references a kid the current cache doesn't have (likely IdP key rotation)
// so we don't reject valid tokens for up to jwksCacheTTL. At most one forced
// refetch per jwksMinRefetch (failed attempts count too); inside that window
// the cached set is returned and the unknown kid is rejected.
func (p *OIDCProvider) forceRefreshJWKS(ctx context.Context) (map[string]*jwksKey, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	if !p.jwksForced.IsZero() && now.Sub(p.jwksForced) < jwksMinRefetch {
		return p.keys, nil
	}
	p.jwksForced = now
	return p.fetchJWKS(ctx)
}

func (p *OIDCProvider) now() time.Time {
	if p.nowFunc != nil {
		return p.nowFunc()
	}
	return time.Now()
}

// fetchJWKS performs the actual JWKS HTTP fetch. Callers must hold p.mu.
func (p *OIDCProvider) fetchJWKS(ctx context.Context) (map[string]*jwksKey, error) {
	jwksURL := strings.TrimSpace(config.Get().AuthService.JWKSURL)
	if jwksURL == "" {
		return nil, fmt.Errorf("oidc: AuthService.JWKSURL is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("jwks endpoint returned %d", resp.StatusCode)
	}
	var payload struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	keys := make(map[string]*jwksKey, len(payload.Keys))
	for _, k := range payload.Keys {
		keys[k.Kid] = &jwksKey{kty: k.Kty, n: k.N, e: k.E}
	}
	p.keys = keys
	p.jwksCached = p.now()
	return keys, nil
}
