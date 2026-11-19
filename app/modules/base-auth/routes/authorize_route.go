package routes

// File overview:
// - Responsibility: implements GET/POST /oauth/authorize, the browser-redirect half of the
//   builtin OAuth 2.0 browser grants — Authorization Code (RFC 6749 §4.1) and Implicit
//   (RFC 6749 §4.2). It validates the requesting client/redirect_uri, renders a minimal
//   server-rendered login form, and on successful login redirects back to the caller's
//   redirect_uri with either a single-use code or an access token fragment. Exists so clients
//   that require a browser OAuth flow can work against Base HUB. See
//   .agents/planning/2026-08-09-oauth-authorization-code-flow-plan.md.
// - Key symbols: `handleAuthorizeGet`, `handleAuthorizePost`.
// - Security: redirect_uri is validated against the client's exact-match allow-list before any
//   form is rendered or code minted (open-redirect prevention); login reuses oauthLoginLimiter,
//   the same rate limiter every other login path on this endpoint shares.

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"html/template"
	"net/url"
	"strconv"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/config"
	internalroutes "git.thebase.vn/base_hub/internal/routes"
	authmodels "git.thebase.vn/base_hub/modules/base-auth/models"
	baseauth "git.thebase.vn/base_hub/modules/base-auth/service"
	"github.com/gofiber/fiber/v3"
)

// authorizeLoginTemplate renders the login form. html/template auto-escapes
// every {{.Field}} for its HTML/attribute context, so untrusted query
// params (client_id, redirect_uri, state, db, scope) are safe to interpolate
// directly — never switch this to text/template or string concatenation.
var authorizeLoginTemplate = template.Must(template.New("authorize").Parse(`<!doctype html>
<html>
<head><meta charset="utf-8"><title>Sign in</title></head>
<body>
<h1>Sign in</h1>
<p>Authorizing <strong>{{.ClientID}}</strong> to access your account.</p>
{{if .Error}}<p style="color:#b00020">{{.Error}}</p>{{end}}
<form method="POST" action="{{.ActionPath}}">
<input type="hidden" name="response_type" value="{{.ResponseType}}">
<input type="hidden" name="client_id" value="{{.ClientID}}">
<input type="hidden" name="redirect_uri" value="{{.RedirectURI}}">
<input type="hidden" name="state" value="{{.State}}">
<input type="hidden" name="db" value="{{.DB}}">
<input type="hidden" name="scope" value="{{.Scope}}">
<input type="hidden" name="code_challenge" value="{{.CodeChallenge}}">
<input type="hidden" name="code_challenge_method" value="{{.CodeChallengeMethod}}">
<input type="hidden" name="csrf_token" value="{{.CSRFToken}}">
<p><label>Username<br><input type="text" name="username" autofocus required></label></p>
<p><label>Password<br><input type="password" name="password" required></label></p>
<button type="submit">Sign in</button>
</form>
</body>
</html>`))

// authorizeParams carries the parsed RFC 6749 §4.1.1 request parameters
// (response_type, client_id, redirect_uri, state) plus Base HUB's own
// tenant-routing addition (db). scope is accepted and round-tripped through
// the form but not enforced — no scoped-permission model exists yet, so an
// issued token still carries the authenticated user's full RBAC role.
type authorizeParams struct {
	ResponseType        string
	ClientID            string
	RedirectURI         string
	State               string
	DB                  string
	Scope               string
	CodeChallenge       string
	CodeChallengeMethod string
	ActionPath          string
	Error               string
	CSRFToken           string
}

// parseAuthorizeParams reads request parameters via FormValue, which fasthttp
// resolves from the query string on GET and from the POST body on POST — so
// the same accessor covers the initial redirect and the login form's
// hidden-field round trip. DB falls back to the X-Database-Name header when
// the request carries no "db" param, mirroring the token endpoint's
// authorization_code leg (oauth_route.go) — the caller's fixed authorize-URL
// shape (e.g. ChatGPT's GPT Actions config) has no room for an extra query
// param, so an operator-run proxy in front of Base HUB can inject the header
// instead (see resolveOAuthDBName's own empty-string home-DB fallback, which
// still applies unchanged if neither is present).
func parseAuthorizeParams(c fiber.Ctx) authorizeParams {
	dbName := strings.TrimSpace(c.FormValue("db"))
	if dbName == "" {
		dbName = strings.TrimSpace(c.Get(internalroutes.RPCHeaderDatabaseName))
	}
	return authorizeParams{
		ResponseType:        strings.TrimSpace(c.FormValue("response_type")),
		ClientID:            strings.TrimSpace(c.FormValue("client_id")),
		RedirectURI:         strings.TrimSpace(c.FormValue("redirect_uri")),
		State:               c.FormValue("state"),
		DB:                  dbName,
		Scope:               c.FormValue("scope"),
		CodeChallenge:       strings.TrimSpace(c.FormValue("code_challenge")),
		CodeChallengeMethod: strings.TrimSpace(c.FormValue("code_challenge_method")),
	}
}

// resolveAndValidateClient resolves/normalizes the tenant db name and
// confirms client_id is known + redirect_uri is registered for it — before
// any form is rendered or code minted, so an unregistered redirect_uri or
// unknown client never reaches a point where the caller's browser gets
// redirected anywhere (open-redirect prevention, RFC 6749 §4.1.2.1 / §10.6).
// Mutates params.DB to the resolved/canonical db name. Failures here are
// always reported inline (400, no redirect) — redirect_uri isn't trusted yet.
func resolveAndValidateClient(c fiber.Ctx, params *authorizeParams) (baseauth.AuthorizeClientPolicy, error) {
	if params.ClientID == "" || params.RedirectURI == "" {
		return nil, fiber.NewError(fiber.StatusBadRequest, "client_id and redirect_uri are required")
	}
	dbName, err := resolveOAuthDBName(c.Context(), params.DB)
	if err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	params.DB = dbName
	policy, err := baseauth.ValidateAuthorizeClient(c.Context(), dbName, params.ClientID, params.RedirectURI)
	if err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, publicAuthError("authorize client check", err, "unknown client_id or unregistered redirect_uri"))
	}
	return policy, nil
}

// validateClientGrantPolicy applies the client's allowed_grants and public-client rule
// (plan T36). Reported by redirect like the other protocol errors: redirect_uri is trusted here.
func validateClientGrantPolicy(policy baseauth.AuthorizeClientPolicy, params authorizeParams) *authorizeProtocolError {
	grant := authmodels.OAuthGrantAuthorizationCode
	if params.ResponseType == "token" {
		grant = authmodels.OAuthGrantImplicit
	}
	if !policy.AllowsGrant(grant) {
		return &authorizeProtocolError{Code: "unauthorized_client", Description: "this client is not allowed to use response_type=" + params.ResponseType}
	}
	if grant == authmodels.OAuthGrantAuthorizationCode && policy.RequiresS256PKCE() &&
		(params.CodeChallenge == "" || !strings.EqualFold(params.CodeChallengeMethod, "S256")) {
		return &authorizeProtocolError{Code: "invalid_request", Description: "this client must send code_challenge with code_challenge_method=S256"}
	}
	return nil
}

// authorizeProtocolError is a spec-defined error code/description pair for
// RFC 6749 §4.1.2.1 — once redirect_uri is trusted (resolveAndValidateClient
// passed), any further request-shape problem must be reported by redirecting
// to redirect_uri with error/error_description, never by rendering an inline
// page, so the caller (e.g. ChatGPT) can react programmatically.
type authorizeProtocolError struct {
	Code        string
	Description string
}

// validateProtocolParams checks request-shape parameters that RFC 6749
// requires to be reported via redirect once the client/redirect_uri are
// already trusted: response_type and the PKCE code_challenge_method. PKCE is
// meaningful only for authorization-code requests, so implicit-flow requests
// reject it instead of silently pretending to bind a challenge.
func validateProtocolParams(params authorizeParams) *authorizeProtocolError {
	switch params.ResponseType {
	case "code":
		if err := baseauth.ValidatePKCERequest(params.CodeChallenge, params.CodeChallengeMethod); err != nil {
			return &authorizeProtocolError{Code: "invalid_request", Description: err.Error()}
		}
	case "token":
		if params.CodeChallenge != "" || params.CodeChallengeMethod != "" {
			return &authorizeProtocolError{Code: "invalid_request", Description: "PKCE parameters are only supported for response_type=code"}
		}
	default:
		return &authorizeProtocolError{Code: "unsupported_response_type", Description: "response_type must be code or token"}
	}
	return nil
}

func renderAuthorizeForm(c fiber.Ctx, params authorizeParams, errMsg string) error {
	params.Error = errMsg
	token, err := issueAuthorizeCSRFToken(c)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to render the sign-in form")
	}
	params.CSRFToken = token
	// The page collects credentials: never framed (clickjacking), never cached.
	c.Set("Content-Security-Policy", "frame-ancestors 'none'")
	c.Set(fiber.HeaderXFrameOptions, "DENY")
	c.Set(fiber.HeaderCacheControl, "no-store")
	// Post back to the same mounted authorize path that rendered the form.
	// This keeps /oauth/authorize and /auth/authorize aliases behaviorally
	// identical instead of forcing alias users back through one canonical URL.
	if params.ActionPath == "" {
		params.ActionPath = c.Path()
	}
	c.Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)
	return authorizeLoginTemplate.Execute(c.Response().BodyWriter(), params)
}

// handleAuthorizeGet renders the login form after validating the request.
func handleAuthorizeGet(c fiber.Ctx) error {
	params := parseAuthorizeParams(c)
	policy, err := resolveAndValidateClient(c, &params)
	if err != nil {
		return err
	}
	if perr := validateProtocolParams(params); perr != nil {
		return redirectAuthorizeError(c, params, perr)
	}
	if perr := validateClientGrantPolicy(policy, params); perr != nil {
		return redirectAuthorizeError(c, params, perr)
	}
	return renderAuthorizeForm(c, params, "")
}

// handleAuthorizePost verifies the submitted credentials and, on success,
// redirects to redirect_uri with a single-use code + the original state
// (passed through unchanged, per ChatGPT/RFC 6749 requirements). On failure
// it re-renders the same form with an error rather than redirecting, so a
// bad password never leaks a code and never bounces the browser away.
func handleAuthorizePost(c fiber.Ctx) error {
	params := parseAuthorizeParams(c)
	policy, err := resolveAndValidateClient(c, &params)
	if err != nil {
		return err
	}
	if perr := validateProtocolParams(params); perr != nil {
		return redirectAuthorizeError(c, params, perr)
	}
	if perr := validateClientGrantPolicy(policy, params); perr != nil {
		return redirectAuthorizeError(c, params, perr)
	}
	// Login CSRF: a cross-site form must not sign the victim's browser in as the attacker and
	// hand the attacker's code to the client. The POST must carry the token the GET set.
	if !validAuthorizeCSRFToken(c) {
		c.Status(fiber.StatusForbidden)
		return renderAuthorizeForm(c, params, "the sign-in form expired, please sign in again")
	}
	login := strings.TrimSpace(c.FormValue("username"))
	password := c.FormValue("password")
	if login == "" {
		return renderAuthorizeForm(c, params, "username is required")
	}
	// SEC-03: same shared limiter every login path on this endpoint uses
	// (login_rate_limit.go) — a browser-based login attempt is just another
	// login attempt for rate-limiting purposes.
	clientIP := c.IP()
	if retryAfter, allowed := oauthLoginLimiter.allow(login, clientIP); !allowed {
		c.Set(fiber.HeaderRetryAfter, strconv.FormatInt(int64((retryAfter+time.Second-1)/time.Second), 10))
		return renderAuthorizeForm(c, params, "too many failed login attempts, retry later")
	}
	redirectURL, err := issueAuthorizeRedirect(c, params, login, password)
	if err != nil {
		oauthLoginLimiter.recordFailure(login, clientIP)
		return renderAuthorizeForm(c, params, "invalid login or password")
	}
	oauthLoginLimiter.recordSuccess(login, clientIP)
	return c.Redirect().Status(fiber.StatusFound).To(redirectURL)
}

// issueAuthorizeRedirect owns the post-login branch between browser grants so
// handleAuthorizePost can keep one rate-limiting/error-rendering path for both
// authorization code and implicit flow.
func issueAuthorizeRedirect(c fiber.Ctx, params authorizeParams, login, password string) (string, error) {
	if params.ResponseType == "token" {
		pair, err := baseauth.IssueImplicitToken(c.Context(), params.DB, login, password, params.Scope)
		if err != nil {
			return "", err
		}
		return buildImplicitRedirect(params.RedirectURI, pair, params.State)
	}
	code, err := baseauth.IssueAuthorizationCode(c.Context(), params.DB, params.ClientID, params.RedirectURI, login, password, params.CodeChallenge, params.CodeChallengeMethod, params.Scope)
	if err != nil {
		return "", err
	}
	return buildAuthorizeRedirect(params.RedirectURI, code, params.State)
}

// redirectAuthorizeError sends the browser back to redirect_uri with
// error/error_description/state (RFC 6749 §4.1.2.1) — only reachable once
// resolveAndValidateClient has already trusted redirect_uri.
func redirectAuthorizeError(c fiber.Ctx, params authorizeParams, perr *authorizeProtocolError) error {
	parsed, err := url.Parse(params.RedirectURI)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to build redirect")
	}
	query := parsed.Query()
	query.Set("error", perr.Code)
	query.Set("error_description", perr.Description)
	if params.State != "" {
		query.Set("state", params.State)
	}
	parsed.RawQuery = query.Encode()
	return c.Redirect().Status(fiber.StatusFound).To(parsed.String())
}

// buildAuthorizeRedirect appends code (and state, if present) as query
// params onto redirectURI, merging with any query string it already carries
// rather than assuming it has none — ChatGPT's documented callback shape
// (https://chat.openai.com/aip/{g-ID}/oauth/callback) has none today, but
// nothing here should break if a future redirect_uri does.
func buildAuthorizeRedirect(redirectURI, code, state string) (string, error) {
	parsed, err := url.Parse(redirectURI)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("code", code)
	if state != "" {
		query.Set("state", state)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

// buildImplicitRedirect appends OAuth implicit-flow response values to the URL
// fragment, not the query string. RFC 6749 §4.2.2 uses the fragment so the
// browser-delivered token is not sent back to the server as part of the
// redirect request path.
func buildImplicitRedirect(redirectURI string, pair *baseauth.TokenPair, state string) (string, error) {
	parsed, err := url.Parse(redirectURI)
	if err != nil {
		return "", err
	}
	fragment := url.Values{}
	fragment.Set("access_token", pair.AccessToken)
	fragment.Set("token_type", pair.TokenType)
	fragment.Set("expires_in", strconv.FormatInt(pair.ExpiresIn, 10))
	if state != "" {
		fragment.Set("state", state)
	}
	if pair.Scope != "" {
		fragment.Set("scope", pair.Scope)
	}
	parsed.Fragment = fragment.Encode()
	return parsed.String(), nil
}

// authorizeCSRFCookie holds the double-submit token of the /oauth/authorize sign-in form over
// plain HTTP (development). Over HTTPS the `__Host-` name is used: the browser then accepts the
// cookie only when Secure, Path=/ and host-only, so a sibling subdomain cannot plant a token.
const (
	authorizeCSRFCookie       = "base_oauth_authorize_csrf"
	authorizeCSRFCookieSecure = "__Host-base_oauth_authorize_csrf"
)

// authorizeCSRFSecure reports whether the CSRF cookie must be Secure: the request came over TLS
// (directly or via a trusted proxy's X-Forwarded-Proto) or the app runs in production.
func authorizeCSRFSecure(c fiber.Ctx) bool {
	return c.Scheme() == "https" || config.Get().AppEnv() == "production"
}

func authorizeCSRFCookieName(c fiber.Ctx) string {
	if authorizeCSRFSecure(c) {
		return authorizeCSRFCookieSecure
	}
	return authorizeCSRFCookie
}

// issueAuthorizeCSRFToken returns the token for the form being rendered: the one already in the
// browser's cookie when it is well-formed (a re-render after a wrong password keeps working in
// other tabs), otherwise a new random one set as an HttpOnly cookie for 30 minutes.
func issueAuthorizeCSRFToken(c fiber.Ctx) (string, error) {
	name := authorizeCSRFCookieName(c)
	if existing := c.Cookies(name); len(existing) == 43 {
		return existing, nil
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	c.Cookie(&fiber.Cookie{
		Name:     name,
		Value:    token,
		Path:     "/",
		MaxAge:   1800,
		HTTPOnly: true,
		Secure:   authorizeCSRFSecure(c),
		SameSite: fiber.CookieSameSiteLaxMode,
	})
	return token, nil
}

// validAuthorizeCSRFToken compares the form's csrf_token with the cookie in constant time. A
// cross-site page can post the form but can neither read nor set this cookie.
func validAuthorizeCSRFToken(c fiber.Ctx) bool {
	cookie := c.Cookies(authorizeCSRFCookieName(c))
	form := c.FormValue("csrf_token")
	if len(cookie) != 43 || len(form) != len(cookie) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie), []byte(form)) == 1
}

// fiber:context-methods migrated
