package routes

// File overview:
// - Responsibility: implements POST /oauth/token (RFC 6749 §4, grant_type in
//   {password, refresh_token, client_credentials, authorization_code}) and POST /oauth/revoke, both
//   delegating to baseauth.CurrentProvider() so the same endpoint works whether the builtin
//   issuer or an external OIDC provider is active (RBAC/OAuth backend plan §3/§5). The grant_type
//   flow accepts either a form-encoded body or a flat JSON body using the exact same field names
//   (grant_type/username/password/db/refresh_token/...) — see jsonTokenRequest/dispatchGrant — so a
//   caller can switch Content-Type without renaming a single key. A JSON body can also use the
//   legacy {"params":{"login":...}} login envelope — see password_login.go — and GET /oauth/me
//   reports the caller's session/profile. `/auth/*` aliases mount the same handlers for
//   already-configured OAuth clients.
// - Key symbols: `RegisterRoutes`, `handleToken`, `dispatchGrant`, `handleRevoke`.
// - Pattern: Keeps transport concerns at the HTTP boundary: form/JSON parsing and validation
//   happens here, the actual grant logic lives in modules/base-auth/service.

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/config"
	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	internalroutes "git.thebase.vn/base_hub/internal/routes"
	"git.thebase.vn/base_hub/internal/services/log"
	baseauth "git.thebase.vn/base_hub/modules/base-auth/service"
	"github.com/gofiber/fiber/v3"
)

const (
	grantTypePassword          = "password"
	grantTypeRefreshToken      = "refresh_token"
	grantTypeClientCredentials = "client_credentials"
	grantTypeAuthorizationCode = "authorization_code"
)

// RegisterRoutes mounts the OAuth 2.0 token endpoint surface.
func RegisterRoutes(router fiber.Router) {
	if router == nil {
		return
	}
	router.Post("/oauth/token", handleToken)
	router.Post("/oauth/revoke", handleRevoke)
	router.Get("/oauth/me", handleGetSessionInfo)
	router.Get("/oauth/authorize", handleAuthorizeGet)
	router.Post("/oauth/authorize", handleAuthorizePost)
	// Keep the previous /auth/* URL surface alive for deployed clients whose
	// OAuth provider settings already point at /auth/authorize or /auth/token.
	// Without explicit aliases those browser requests fall through to the PWA
	// fallback and render a blank app shell instead of the authorize form.
	router.Post("/auth/token", handleToken)
	router.Post("/auth/revoke", handleRevoke)
	router.Get("/auth/me", handleGetSessionInfo)
	router.Get("/auth/authorize", handleAuthorizeGet)
	router.Post("/auth/authorize", handleAuthorizePost)
}

// tokenResponse is the RFC 6749 §5.1 access-token response shape.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int64  `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope,omitempty"`
}

// oauthErrorBody is the RFC 6749 §5.2 token-endpoint error response shape —
// distinct from the plain {"error": "<message>"} Fiber's default error
// handler would otherwise emit, since callers doing real OAuth client
// implementations key off the stable `error` code, not free-text messages.
type oauthErrorBody struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}

func writeOAuthTokenError(c fiber.Ctx, status int, code, description string) error {
	return c.Status(status).JSON(oauthErrorBody{Error: code, ErrorDescription: description})
}

// jsonTokenRequest is the flat JSON-body mirror of the form-encoded grant_type
// flow dispatchGrant implements: identical key names to the form fields below
// (grant_type/username/password/db/refresh_token/client_id/client_secret/
// code/redirect_uri/code_verifier) so a caller can POST the same field set as
// either application/x-www-form-urlencoded or application/json. Distinct from
// the legacy {"params":{"login":...}} login envelope (password_login.go),
// which has no "grant_type" key — that's how handleToken tells them apart.
type jsonTokenRequest struct {
	GrantType    string `json:"grant_type"`
	Username     string `json:"username"`
	Password     string `json:"password"`
	DB           string `json:"db"`
	RefreshToken string `json:"refresh_token"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	Code         string `json:"code"`
	RedirectURI  string `json:"redirect_uri"`
	CodeVerifier string `json:"code_verifier"`
}

// tokenGrantFields carries the grant_type flow's non-db fields regardless of
// which wire format (form or flat JSON) they arrived in, so dispatchGrant only
// has one code path to maintain.
type tokenGrantFields struct {
	Username     string
	Password     string
	RefreshToken string
	ClientID     string
	ClientSecret string
	Code         string
	RedirectURI  string
	CodeVerifier string
}

func handleToken(c fiber.Ctx) error {
	// A JSON body carries one of two distinct contracts, told apart by
	// peeking for "grant_type": the RFC 6749 flow below (same field names as
	// the form-encoded variant) or the legacy {"params":{"login":...}} login
	// envelope (originally POST /web/session/authenticate, then POST
	// /auth/token, now consolidated onto this route — see password_login.go).
	// Dispatch on Content-Type before touching FormValue, which JSON bodies
	// don't populate.
	if isJSONPasswordLoginRequest(c) {
		var probe struct {
			GrantType string `json:"grant_type"`
		}
		body := c.Body()
		if err := json.Unmarshal(body, &probe); err != nil || strings.TrimSpace(probe.GrantType) == "" {
			return handleJSONPasswordLogin(c)
		}
		var req jsonTokenRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return writeOAuthTokenError(c, fiber.StatusBadRequest, "invalid_request", err.Error())
		}
		return dispatchGrant(c, strings.TrimSpace(req.GrantType), req.DB, tokenGrantFields{
			Username:     strings.TrimSpace(req.Username),
			Password:     req.Password,
			RefreshToken: strings.TrimSpace(req.RefreshToken),
			ClientID:     strings.TrimSpace(req.ClientID),
			ClientSecret: req.ClientSecret,
			Code:         strings.TrimSpace(req.Code),
			RedirectURI:  strings.TrimSpace(req.RedirectURI),
			CodeVerifier: strings.TrimSpace(req.CodeVerifier),
		})
	}
	return dispatchGrant(c, strings.TrimSpace(c.FormValue("grant_type")), c.FormValue("db"), tokenGrantFields{
		Username:     strings.TrimSpace(c.FormValue("username")),
		Password:     c.FormValue("password"),
		RefreshToken: strings.TrimSpace(c.FormValue("refresh_token")),
		ClientID:     strings.TrimSpace(c.FormValue("client_id")),
		ClientSecret: c.FormValue("client_secret"),
		Code:         strings.TrimSpace(c.FormValue("code")),
		RedirectURI:  strings.TrimSpace(c.FormValue("redirect_uri")),
		CodeVerifier: strings.TrimSpace(c.FormValue("code_verifier")),
	})
}

// dispatchGrant implements the RFC 6749 §4 grant_type switch shared by both
// wire formats handleToken accepts — the raw field extraction differs (form
// vs JSON) but the grant logic, status codes, and error bodies must not.
func dispatchGrant(c fiber.Ctx, grantType, rawDBName string, fields tokenGrantFields) error {
	dbName, err := resolveOAuthDBName(c.Context(), rawDBName)
	if err != nil {
		return writeOAuthTokenError(c, fiber.StatusBadRequest, "invalid_request", err.Error())
	}

	provider := baseauth.CurrentProvider()
	switch grantType {
	case grantTypePassword:
		return handlePasswordGrant(c, provider, dbName, fields.Username, fields.Password)
	case grantTypeRefreshToken:
		if throttled, err := rejectThrottledGrant(c, ""); throttled {
			return err
		}
		pair, err := provider.Refresh(c.Context(), dbName, fields.RefreshToken)
		if err != nil {
			return writeOAuthTokenError(c, fiber.StatusUnauthorized, "invalid_grant", publicAuthError("refresh_token grant", err, "invalid or expired refresh token"))
		}
		return c.JSON(tokenFromPair(pair))
	case grantTypeClientCredentials:
		clientKey := grantClientKey(fields.ClientID)
		if throttled, err := rejectThrottledGrant(c, clientKey); throttled {
			return err
		}
		pair, err := provider.IssueClientCredentials(c.Context(), dbName, fields.ClientID, fields.ClientSecret)
		if err != nil {
			oauthGrantLimiter.recordFailure(clientKey, c.IP())
			return writeOAuthTokenError(c, fiber.StatusUnauthorized, "invalid_client", publicAuthError("client_credentials grant", err, "invalid client credentials"))
		}
		oauthGrantLimiter.recordSuccess(clientKey, c.IP())
		return c.JSON(tokenFromPair(pair))
	case grantTypeAuthorizationCode:
		// Unlike the other grants, dbName above (resolved from the "db"
		// field) is unused here — ChatGPT's fixed exchange-request shape has no
		// room for one. Resolve from X-Database-Name instead, which an
		// operator-run proxy in front of Base HUB can inject on this call;
		// resolveOAuthDBName's own empty-string fallback to config.HomeDBName()
		// (postgresql.homeDbName) applies unchanged if the header is absent too.
		// Builtin-issuer only, same as GET/POST /oauth/authorize
		// (authorize_route.go) — not part of the TokenIssuer interface
		// baseauth.CurrentProvider() would otherwise dispatch through.
		codeDBName, err := resolveOAuthDBName(c.Context(), c.Get(internalroutes.RPCHeaderDatabaseName))
		if err != nil {
			return writeOAuthTokenError(c, fiber.StatusBadRequest, "invalid_request", err.Error())
		}
		clientKey := grantClientKey(fields.ClientID)
		if throttled, err := rejectThrottledGrant(c, clientKey); throttled {
			return err
		}
		pair, err := baseauth.ExchangeAuthorizationCode(c.Context(), codeDBName,
			fields.ClientID, fields.ClientSecret, fields.Code, fields.RedirectURI, fields.CodeVerifier)
		if err != nil {
			oauthGrantLimiter.recordFailure(clientKey, c.IP())
			return writeOAuthTokenError(c, fiber.StatusUnauthorized, "invalid_grant", publicAuthError("authorization_code grant", err, "invalid authorization code, client, redirect_uri or code_verifier"))
		}
		oauthGrantLimiter.recordSuccess(clientKey, c.IP())
		return c.JSON(tokenFromPair(pair))
	default:
		return writeOAuthTokenError(c, fiber.StatusBadRequest, "unsupported_grant_type", "grant_type must be one of password, refresh_token, client_credentials, authorization_code")
	}
}

// invalidLoginDescription is the only text a failed password login returns.
const invalidLoginDescription = "invalid login or password"

// publicAuthError logs the real cause and returns description for the client.
// Grant errors can carry pgx or other internal text and these endpoints are
// unauthenticated (plan T31), so the detail stays in the server log.
func publicAuthError(op string, err error, description string) string {
	log.Warnf("auth %s failed: %v", op, err)
	return description
}

// grantClientKey is the oauthGrantLimiter failure key for a client grant.
func grantClientKey(clientID string) string {
	return "client:" + clientID
}

// rejectThrottledGrant answers 429 when oauthGrantLimiter refuses the call.
// key "" applies only the per-IP window.
func rejectThrottledGrant(c fiber.Ctx, key string) (bool, error) {
	retryAfter, allowed := oauthGrantLimiter.allow(key, c.IP())
	if allowed {
		return false, nil
	}
	c.Set(fiber.HeaderRetryAfter, strconv.FormatInt(int64((retryAfter+time.Second-1)/time.Second), 10))
	return true, writeOAuthTokenError(c, fiber.StatusTooManyRequests, "invalid_request", "too many requests, retry later")
}

func handlePasswordGrant(c fiber.Ctx, provider baseauth.TokenIssuer, dbName, login, password string) error {
	if login == "" {
		return writeOAuthTokenError(c, fiber.StatusBadRequest, "invalid_request", "username is required")
	}
	// SEC-03: throttle before any database work, same limiter and policy the
	// JSON-body login variant uses (password_login.go) — one shared budget
	// per login+IP since both are the same endpoint.
	clientIP := c.IP()
	if retryAfter, allowed := oauthLoginLimiter.allow(login, clientIP); !allowed {
		c.Set(fiber.HeaderRetryAfter, strconv.FormatInt(int64((retryAfter+time.Second-1)/time.Second), 10))
		return writeOAuthTokenError(c, fiber.StatusTooManyRequests, "invalid_request", "too many failed login attempts, retry later")
	}
	pair, err := provider.IssuePassword(c.Context(), dbName, login, password)
	if err != nil {
		oauthLoginLimiter.recordFailure(login, clientIP)
		return writeOAuthTokenError(c, fiber.StatusUnauthorized, "invalid_grant", publicAuthError("password grant", err, invalidLoginDescription))
	}
	oauthLoginLimiter.recordSuccess(login, clientIP)
	return c.JSON(tokenFromPair(pair))
}

func handleRevoke(c fiber.Ctx) error {
	dbName, err := resolveOAuthDBName(c.Context(), c.FormValue("db"))
	if err != nil {
		return writeOAuthTokenError(c, fiber.StatusBadRequest, "invalid_request", err.Error())
	}
	refreshToken := strings.TrimSpace(c.FormValue("refresh_token"))
	if refreshToken == "" {
		return writeOAuthTokenError(c, fiber.StatusBadRequest, "invalid_request", "refresh_token is required")
	}
	// Best-effort per RFC 7009 §2.2: revocation of an invalid/unknown token
	// still returns 200 so a caller can't probe token validity via this endpoint.
	_ = baseauth.CurrentProvider().Revoke(c.Context(), dbName, refreshToken)
	return c.SendStatus(fiber.StatusOK)
}

func tokenFromPair(pair *baseauth.TokenPair) tokenResponse {
	return tokenResponse{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		ExpiresIn:    pair.ExpiresIn,
		TokenType:    pair.TokenType,
		Scope:        pair.Scope,
	}
}

// resolveOAuthDBName mirrors internal/routes/rpc_route.go's resolveRPCDBName
// (kept as its own copy to avoid an internal/routes <-> modules/base-auth
// import cycle): empty defaults to the home control-plane DB. An explicit name
// (form field, JSON body or header) must pass the same request-DB allowlist the
// global middleware applies to ?db=/header/cookie (plan T12): home is always
// allowed, the template never, tenants only while registered and running. The
// error is deliberately generic so callers can return it as a 400 verbatim.
func resolveOAuthDBName(ctx context.Context, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return strings.TrimSpace(config.HomeDBName()), nil
	}
	if err := kernelmodules.CheckRequestDBName(ctx, raw); err != nil {
		return "", kernelmodules.ErrRequestDBNotAllowed
	}
	return raw, nil
}

// fiber:context-methods migrated
