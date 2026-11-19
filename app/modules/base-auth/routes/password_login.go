package routes

// File overview:
// - Responsibility: implements the JSON-body login contract POST /oauth/token accepts alongside
//   the RFC 6749 form-encoded grant_type flow (oauth_route.go), plus GET /oauth/me. Originally
//   POST /web/session/authenticate / GET /web/session/get_session_info, then POST /auth/token /
//   GET /auth/me, now consolidated onto the /oauth/* surface per user request.
// - Key symbols: `handleJSONPasswordLogin`, `handleGetSessionInfo`.
// - Pattern: Reuses internal/routes' RPC identity/DB-name resolution (exported for this purpose)
//   rather than duplicating it — that logic backs every /rpc endpoint too, unlike the small
//   DB-name-resolution/rate-limiter helpers this package already duplicates to avoid depending on
//   internal/routes at all.

import (
	"context"
	"strconv"
	"strings"
	"time"

	internalroutes "git.thebase.vn/base_hub/internal/routes"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	baseauth "git.thebase.vn/base_hub/modules/base-auth/service"
	"github.com/gofiber/fiber/v3"
	"github.com/uptrace/bun"
)

// PasswordLoginParamsDoc documents POST /oauth/token's JSON-body variant.
type PasswordLoginParamsDoc struct {
	DB       string         `json:"db" example:"bean_ipn"`
	Login    string         `json:"login" example:"admin"`
	Password string         `json:"password" example:"secret"`
	Context  map[string]any `json:"context"`
}

// PasswordLoginRequestDoc documents POST /oauth/token's JSON-body variant.
type PasswordLoginRequestDoc struct {
	Params PasswordLoginParamsDoc `json:"params"`
}

// PasswordLoginResultDoc documents the login result object. Shape follows RFC
// 6749 §5.1 plus this service's user_context/db_header extensions (RBAC/OAuth
// backend plan §12.1 Option A) — access_token replaces the removed api_key
// concept, and refresh_token/expires_in/token_type are required alongside it.
type PasswordLoginResultDoc struct {
	UID          int64          `json:"uid" example:"1"`
	DB           string         `json:"db" example:"bean_ipn"`
	AccessToken  string         `json:"access_token" example:"eyJhbGciOiJSUzI1NiJ9..."`
	RefreshToken string         `json:"refresh_token" example:"5f9d6c3b1a..."`
	ExpiresIn    int64          `json:"expires_in" example:"900"`
	TokenType    string         `json:"token_type" example:"Bearer"`
	UserContext  map[string]any `json:"user_context"`
	Username     string         `json:"username" example:"admin"`
	AuthType     string         `json:"auth_type" example:"oauth"`
	DBHeader     string         `json:"db_header" example:"X-Database-Name"`
}

// PasswordLoginResponseDoc documents the login response envelope.
type PasswordLoginResponseDoc struct {
	Result PasswordLoginResultDoc `json:"result"`
}

// SessionInfoResultDoc documents GET /oauth/me.
// Username/DBHeader/UserContext are populated whenever the request carries a
// valid identity, so an RFC 6749 grant-only login (which carries no profile
// data) can hydrate the same user_context the JSON login variant returns,
// with one authenticated round trip instead of a bespoke "me" endpoint.
type SessionInfoResultDoc struct {
	DB          string         `json:"db" example:"bean_ipn"`
	UID         int64          `json:"uid" example:"1"`
	IsWeb       bool           `json:"is_web" example:"true"`
	Valid       bool           `json:"valid" example:"true"`
	AuthMode    string         `json:"auth_mode" example:"none"`
	Username    string         `json:"username,omitempty" example:"admin"`
	DBHeader    string         `json:"db_header,omitempty" example:"X-Database-Name"`
	UserContext map[string]any `json:"user_context,omitempty"`
}

// SessionInfoResponseDoc documents the session-info response envelope.
type SessionInfoResponseDoc struct {
	Result SessionInfoResultDoc `json:"result"`
}

// isJSONPasswordLoginRequest tells the JSON-body login contract apart from
// the RFC 6749 form-encoded grant_type flow handleToken otherwise expects.
func isJSONPasswordLoginRequest(c fiber.Ctx) bool {
	contentType := strings.ToLower(strings.TrimSpace(string(c.Request().Header.ContentType())))
	return strings.Contains(contentType, fiber.MIMEApplicationJSON)
}

// handleJSONPasswordLogin serves POST /oauth/token's JSON body variant.
// Mirrors handlePasswordGrant's IssuePassword call but returns the fuller
// JSON-RPC-style envelope (uid/user_context/etc) legacy callers built around
// that contract still expect, instead of the bare RFC 6749 token pair.
func handleJSONPasswordLogin(c fiber.Ctx) error {
	var payload PasswordLoginRequestDoc
	if err := c.Bind().Body(&payload); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	login := strings.TrimSpace(payload.Params.Login)
	if login == "" {
		return fiber.NewError(fiber.StatusBadRequest, "login is required")
	}
	// SEC-03: throttle before any database work so brute-force attempts are
	// rejected cheaply. Shares oauthLoginLimiter with the form-encoded grant
	// (login_rate_limit.go) since both are the same endpoint.
	clientIP := c.IP()
	if retryAfter, allowed := oauthLoginLimiter.allow(login, clientIP); !allowed {
		c.Set(fiber.HeaderRetryAfter, strconv.FormatInt(int64((retryAfter+time.Second-1)/time.Second), 10))
		return fiber.NewError(fiber.StatusTooManyRequests, "too many failed login attempts, retry later")
	}
	dbName, err := resolveOAuthDBName(c.Context(), payload.Params.DB)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	provider := baseauth.CurrentProvider()
	pair, err := provider.IssuePassword(c.Context(), dbName, login, payload.Params.Password)
	if err != nil {
		// Only failed credential checks consume the lockout budget; payload and
		// DB-resolution errors above return without touching the limiter.
		oauthLoginLimiter.recordFailure(login, clientIP)
		return fiber.NewError(fiber.StatusUnauthorized, publicAuthError("password login", err, invalidLoginDescription))
	}
	// A verified login clears the pair's failure budget immediately.
	oauthLoginLimiter.recordSuccess(login, clientIP)

	// Decode the identity back out of the freshly minted access token instead
	// of re-querying base_users: Validate is a pure signature/claims parse (no
	// DB round trip beyond the already-cached signing key), so this avoids a
	// second password/user lookup just to read back profile fields.
	identity, err := provider.Validate(c.Context(), pair.AccessToken)
	if err != nil || identity == nil {
		return fiber.NewError(fiber.StatusInternalServerError, "failed to hydrate authenticated session")
	}
	// An external IdP mints tokens for its own tenant mapping (OIDC tenant
	// claim, or the home DB); a token for another DB than the one the login
	// asked for would be refused on every later /rpc call, so refuse it here.
	if err := internalroutes.EnsureIdentityDBMatch(identity.DBName, dbName); err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, invalidLoginDescription)
	}

	userContext := cloneAnyMap(payload.Params.Context)
	if len(identity.CompanyIDs) > 0 {
		// Mirror the authenticated company scope into the frontend context so
		// later /rpc calls inherit the same multi-company visibility by default.
		userContext[orm.ContextKeyAllowedCompanyIDs] = append([]int64(nil), identity.CompanyIDs...)
		if companies := sessionAllowedCompanies(c.Context(), identity.DBName, identity.UserID, identity.CompanyIDs); companies != nil {
			userContext[sessionAllowedCompaniesKey] = companies
		}
	}
	if identity.CompanyID > 0 {
		userContext[orm.ContextKeyCurrentCompanyID] = identity.CompanyID
	}
	// Always include lang so the frontend can activate the correct locale
	// without a separate context_get round-trip after login.
	userContext["lang"] = identity.Lang
	return c.JSON(fiber.Map{
		"result": fiber.Map{
			"uid":           identity.UserID,
			"db":            dbName,
			"access_token":  pair.AccessToken,
			"refresh_token": pair.RefreshToken,
			"expires_in":    pair.ExpiresIn,
			"token_type":    pair.TokenType,
			"user_context":  userContext,
			"username":      identity.Login,
			"auth_type":     "oauth",
			"db_header":     internalroutes.RPCHeaderDatabaseName,
		},
	})
}

// handleGetSessionInfo serves GET /oauth/me, reusing internal/routes' RPC
// identity/DB-name resolution (the same logic every /rpc endpoint uses) so a
// bearer token minted by either login flow above reports consistent
// db/uid/user_context here.
func handleGetSessionInfo(c fiber.Ctx) error {
	// Per-identity response at a fixed URL: any intermediary or browser HTTP
	// cache that ignores the Authorization header (most do, absent a Vary
	// declaration) could otherwise replay one user's session info as another
	// user's after a fresh login. Belt-and-suspenders alongside the frontend
	// service worker's explicit bypass of this path in sw.js.
	c.Set(fiber.HeaderCacheControl, "no-store, no-cache, must-revalidate")
	c.Set(fiber.HeaderPragma, "no-cache")
	if _, err := internalroutes.RequestRPCDBName(c); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	identity, _ := internalroutes.CurrentRPCIdentity(c)
	// An unauthenticated caller gets 401, never a session body: the old response carried
	// uid 1 (the admin id) with valid=false, which a client that skipped the flag read as admin.
	if identity == nil {
		c.Set(fiber.HeaderWWWAuthenticate, "Bearer")
		return fiber.NewError(fiber.StatusUnauthorized, "authentication required")
	}
	return c.JSON(fiber.Map{"result": fiber.Map{
		"db":           identity.DBName,
		"uid":          identity.UserID,
		"is_web":       true,
		"valid":        true,
		"auth_mode":    identity.AuthMode,
		"username":     identity.Username,
		"db_header":    internalroutes.RPCHeaderDatabaseName,
		"user_context": sessionUserContext(c.Context(), identity),
	}})
}

// sessionUserContext mirrors the user_context shape handleJSONPasswordLogin
// returns, so a caller that authenticated via the RFC 6749 form-encoded grant
// (which carries only the token pair) can hydrate the same company/lang
// scoping with one GET /oauth/me call.
func sessionUserContext(ctx context.Context, identity *internalroutes.RPCRequestIdentity) map[string]any {
	userContext := map[string]any{"lang": identity.Lang}
	if len(identity.CompanyIDs) > 0 {
		userContext[orm.ContextKeyAllowedCompanyIDs] = append([]int64(nil), identity.CompanyIDs...)
		if companies := sessionAllowedCompanies(ctx, identity.DBName, identity.UserID, identity.CompanyIDs); companies != nil {
			userContext[sessionAllowedCompaniesKey] = companies
		}
	}
	if identity.CompanyID > 0 {
		userContext[orm.ContextKeyCurrentCompanyID] = identity.CompanyID
	}
	return userContext
}

// sessionAllowedCompaniesKey carries the company switcher options in user_context.
const sessionAllowedCompaniesKey = "allowed_companies"

// sessionAllowedCompanies returns [{id, name}] for the caller's authorized companies, in
// identity order, for the SPA company switcher. companyIDs (the identity's) is already the
// RBAC-collapsed authorized set (only the primary company without base.group_multi_company),
// so reading names here as the server widens nothing: tbl.company itself stays readable only
// by base.group_system over RPC. A lookup failure returns nil; the switcher then hides.
func sessionAllowedCompanies(ctx context.Context, dbName string, userID int64, companyIDs []int64) []map[string]any {
	db := orm.ResolveBunDB(dbName)
	if db == nil || len(companyIDs) == 0 {
		return nil
	}
	var rows []struct {
		ID   int64  `bun:"id"`
		Name string `bun:"name"`
	}
	err := db.NewSelect().
		TableExpr("tbl_company").
		ColumnExpr("id, name").
		Where("id IN (?)", bun.In(companyIDs)).
		Scan(ctx, &rows)
	if err != nil {
		log.Warnf("oauth: load allowed companies for user %d: %v", userID, err)
		return nil
	}
	names := make(map[int64]string, len(rows))
	for _, row := range rows {
		names[row.ID] = row.Name
	}
	out := make([]map[string]any, 0, len(companyIDs))
	for _, id := range companyIDs {
		if name, ok := names[id]; ok {
			out = append(out, map[string]any{"id": id, "name": name})
		}
	}
	return out
}

// cloneAnyMap mirrors internal/routes' helper of the same name — kept as its
// own copy for the same reason resolveOAuthDBName is (avoids depending on
// internal/routes for a two-line helper). Always returns a non-nil map so
// callers can write into it unconditionally.
func cloneAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

// fiber:context-methods migrated
