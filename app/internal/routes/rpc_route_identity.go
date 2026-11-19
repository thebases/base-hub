package routes

// File overview:
// - Responsibility: authenticated RPC identity resolution and per-request database name/environment resolution for the rpc route surface.
// - Key symbols: `currentRPCIdentity`, `requireRPCIdentity`, `requestRPCDBName`, `resolveRPCDBName`, `rpcContextForDB`.

import (
	"context"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/base"
	"git.thebase.vn/base_hub/internal/config"
	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/gofiber/fiber/v3"
)

// currentRPCIdentity resolves the authenticated RPC caller. OAuth 2.0 is the
// sole inbound credential now (RBAC/OAuth backend plan §1/§7.4): the static
// X-API-Key service key (now back only as the opt-in app.supportApikey path below, per-user via
// base_users.api_key), the opaque authtoken bearer lookup, and a bare
// Authorization: Basic header were all removed in this cutover. A service/
// automation caller that used to rely on X-API-Key now registers a
// sys.oauth_client row and authenticates via grant_type=client_credentials
// (plan §3/§7.3) instead.
func currentRPCIdentity(c fiber.Ctx) (*rpcRequestIdentity, error) {
	dbName, err := requestRPCDBName(c)
	if err != nil {
		return nil, err
	}
	// Deliberately no eager DB-connectivity check here: JWT bearer validation
	// is self-contained (signature/exp/iss against this process's own key,
	// no network call — plan §3), so an unreachable target DB must not block
	// authentication itself. A genuinely unreachable DB still surfaces its
	// own error once the authenticated request actually dispatches against it.

	authHeader := strings.TrimSpace(c.Get(fiber.HeaderAuthorization))
	// X-API-Key is an opt-in second credential (app.supportApikey, default off). An Authorization
	// header, when present, always wins so an OAuth caller is never silently downgraded.
	if authHeader == "" && config.Get().App.SupportApikey {
		if apiKey := strings.TrimSpace(c.Get(rpcHeaderAPIKey)); apiKey != "" {
			return apiKeyRPCIdentity(c, dbName, apiKey)
		}
	}
	if authHeader == "" {
		return nil, nil
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 {
		log.Errorf("invalid Authorization header format")
		return nil, fmt.Errorf("invalid Authorization header format")
	}
	switch strings.ToLower(parts[0]) {
	case "bearer":
		identity, err := kernelmodules.ValidateBearerToken(c.Context(), parts[1])
		if err != nil || identity == nil {
			log.Errorf("invalid bearer token")
			return nil, fmt.Errorf("invalid bearer token")
		}
		if err := EnsureIdentityDBMatch(identity.DBName, dbName); err != nil {
			log.Errorf("oauth token is not valid for database %q", dbName)
			return nil, err
		}
		identity.DBName = dbName
		return &rpcRequestIdentity{
			UserID:     identity.UserID,
			Username:   identity.Login,
			RoleID:     identity.RoleID,
			RoleName:   identity.RoleName,
			GroupIDs:   append([]int64(nil), identity.GroupIDs...),
			DBName:     dbName,
			CompanyID:  identity.CompanyID,
			CompanyIDs: append([]int64(nil), identity.CompanyIDs...),
			AuthMode:   "oauth",
			Sudo:       identity.IsSuperadmin,
			Lang:       identity.Lang,
		}, nil
	default:
		// Basic auth and any other scheme are not accepted on /rpc: Basic
		// survives only as a token-endpoint input (POST /oauth/token,
		// grant_type=password), not as a per-call header (plan §6).
		log.Errorf("invalid Authorization header format")
		return nil, fmt.Errorf("invalid Authorization header format")
	}
}

// rpcHeaderAPIKey is the service-to-service credential header honored on /rpc when
// app.supportApikey is true.
const rpcHeaderAPIKey = "X-API-Key"

// apiKeyRPCIdentity resolves base_users.api_key into the same identity shape the OAuth bearer path
// builds (role, groups, company scope from the user's row), so both methods authorize identically.
func apiKeyRPCIdentity(c fiber.Ctx, dbName, apiKey string) (*rpcRequestIdentity, error) {
	ctx, err := rpcContextForDB(c.Context(), dbName)
	if err != nil {
		return nil, err
	}
	identity, err := orm.LookupRBACIdentityByAPIKey(ctx, apiKey)
	if err != nil || identity == nil {
		log.Errorf("invalid api key")
		return nil, fmt.Errorf("invalid api key")
	}
	identity.DBName = dbName
	return &rpcRequestIdentity{
		UserID:     identity.UserID,
		Username:   identity.Login,
		RoleID:     identity.RoleID,
		RoleName:   identity.RoleName,
		GroupIDs:   append([]int64(nil), identity.GroupIDs...),
		DBName:     dbName,
		CompanyID:  identity.CompanyID,
		CompanyIDs: append([]int64(nil), identity.CompanyIDs...),
		AuthMode:   "apikey",
		Sudo:       identity.IsSuperadmin,
		Lang:       identity.Lang,
	}, nil
}

func CurrentRPCIdentity(c fiber.Ctx) (*RPCRequestIdentity, error) {
	return currentRPCIdentity(c)
}

func requestRPCDBName(c fiber.Ctx) (string, error) {
	if raw := RequestDBNameRaw(c); raw != "" {
		return resolveRPCDBName(raw)
	}
	if env := orm.EnvironmentFromContext(c.Context()); env != nil {
		if dbName := strings.TrimSpace(env.DBName()); dbName != "" {
			return dbName, nil
		}
	}
	return currentRPCDBName(), nil
}

// RequestDBNameRaw returns the unresolved tenant database name for this
// request, in precedence order: the ?db= query param first (explicit,
// caller-visible override), then the X-Database-Name header (for callers
// that can't set query params, e.g. some OAuth-shape requests), and finally
// falling through empty so resolveRPCDBName/currentRPCDBName can apply the
// Config > postgresql > homeDbName fallback. Callers must still run the
// result through ResolveRPCDBName/resolveRPCDBName to validate it against
// the registered DB names.
func RequestDBNameRaw(c fiber.Ctx) string {
	if v := strings.TrimSpace(c.Query("db")); v != "" {
		return v
	}
	return strings.TrimSpace(c.Get(rpcHeaderDatabaseName))
}

// RequestRPCDBName resolves the DB name bound to this request (header,
// request-bound environment, or the current default in that order) — exported
// so other packages needing the same resolution (e.g. modules/base-auth/routes'
// GET /oauth/me) don't have to duplicate it.
func RequestRPCDBName(c fiber.Ctx) (string, error) {
	return requestRPCDBName(c)
}

// EnsureIdentityDBMatch is the single tenant-isolation check: a validated
// identity carrying a non-empty DBName (i.e. a token minted for a specific
// tenant) must match the DB the current request resolved to, or the token is
// rejected — this is what stops a token minted for tenant A from being
// replayed against tenant B by swapping the DB selector. An identity with no
// DBName (not yet bound) is not checked. Shared by every bearer-token
// validation path (rpc, MCP, service-bus WS) so the rule only lives in one
// place.
func EnsureIdentityDBMatch(identityDBName, requestDBName string) error {
	if identityDBName != "" && identityDBName != requestDBName {
		return fmt.Errorf("oauth token is not valid for the requested database")
	}
	return nil
}

// resolveRPCDBName resolves the DB name a request selector should target: an
// explicit raw value is returned as-is (the allowlist gate is the
// bindRequestDatabase middleware via kernelmodules.CheckRequestDBName, which
// runs before every route; EnsureNamedBunDBNoCreate then opens it), and an empty value
// falls back to the home control-plane DB, which is also the default for any
// request that names no database at all (see currentRPCDBName).
func resolveRPCDBName(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw != "" {
		return raw, nil
	}
	return currentRPCDBName(), nil
}

func ResolveRPCDBName(raw string) (string, error) {
	return resolveRPCDBName(raw)
}

// currentRPCDBName is the fallback DB for any request that names none
// explicitly: always the configured home control-plane DB, never whichever
// data-plane DB happens to be registered.
func currentRPCDBName() string {
	return strings.TrimSpace(config.HomeDBName())
}

// requireRPCIdentity requires the rpc identity handled by this package.
func requireRPCIdentity(c fiber.Ctx, requireSuperadmin bool) (*rpcRequestIdentity, error) {
	identity, err := currentRPCIdentity(c)
	if err != nil {
		return nil, fiber.NewError(fiber.StatusUnauthorized, err.Error())
	}
	if identity == nil {
		return nil, fiber.NewError(fiber.StatusUnauthorized, "rpc authentication is required")
	}
	if requireSuperadmin && !identity.Sudo {
		return nil, fiber.NewError(fiber.StatusForbidden, "superadmin access is required")
	}
	return identity, nil
}

func RequireRPCIdentity(c fiber.Ctx, requireSuperadmin bool) (*RPCRequestIdentity, error) {
	return requireRPCIdentity(c, requireSuperadmin)
}

func rpcContextForDB(ctx context.Context, dbName string) (context.Context, error) {
	// dbName is request-sourced in every current caller (website/workflow
	// public routes) — NoCreate so it can't spray-create a database.
	registeredDB, err := orm.EnsureNamedBunDBNoCreate(dbName)
	if err != nil {
		return nil, err
	}
	env := orm.NewEnvironmentWithDBName(registeredDB.DB, dbName, 0, map[string]any{
		orm.ContextKeyDatabaseName: dbName,
	}, false)
	return orm.ContextWithEnvironment(ctx, env), nil
}

func RPCContextForDB(ctx context.Context, dbName string) context.Context {
	next, err := rpcContextForDB(ctx, dbName)
	if err != nil {
		log.Warnf("RPCContextForDB %q: %v", dbName, err)
		return ctx
	}
	return next
}

func rpcAccessDeniedError(err error) error {
	return fiber.NewError(fiber.StatusForbidden, fmt.Sprintf("%s: %s", base.GetCodeMeaning(base.PermissionDenied), err.Error()))
}

// fiber:context-methods migrated
