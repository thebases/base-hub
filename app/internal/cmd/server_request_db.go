package cmd

// File overview:
// - Responsibility: per-request database selection via cookies and CSV helpers.
// - Key symbols: `setRequestDBCookies`, `clearRequestDBCookies`, `requestBoundDBName`, `bindRequestDatabase`.

import (
	"errors"
	"strings"

	"git.thebase.vn/base_hub/internal/config"
	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/routes"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/gofiber/fiber/v3"
)

func setRequestDBCookies(c fiber.Ctx, dbName string) {
	// SEC-07: Secure keeps the DB-binding cookies off plaintext transport in
	// production. HTTPOnly is intentionally NOT set: the SPA reads both cookies
	// via document.cookie to pin its RPC database, so HTTPOnly would silently break DB switching.
	secure := config.Get().AppEnv() == "production"
	c.Cookie(&fiber.Cookie{
		Name:     requestDBNameCookie,
		Value:    dbName,
		Path:     "/",
		Secure:   secure,
		SameSite: fiber.CookieSameSiteLaxMode,
	})
	c.Cookie(&fiber.Cookie{
		Name:     requestDBLockedCookie,
		Value:    "1",
		Path:     "/",
		Secure:   secure,
		SameSite: fiber.CookieSameSiteLaxMode,
	})
}

func clearRequestDBCookies(c fiber.Ctx) {
	c.Cookie(&fiber.Cookie{
		Name:     requestDBNameCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		SameSite: fiber.CookieSameSiteLaxMode,
	})
	c.Cookie(&fiber.Cookie{
		Name:     requestDBLockedCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		SameSite: fiber.CookieSameSiteLaxMode,
	})
}

// requestBoundDBName resolves the database name this request is pinned to:
// an explicit selector (?db= or X-Database-Name, checked first) or a locked
// cookie from a prior request. The allowlist check (home, never the template,
// registered+running tenants when the database module is present) happens in
// bindRequestDatabase via kernelmodules.CheckRequestDBName.
func requestBoundDBName(c fiber.Ctx) (string, bool) {
	if explicitDBName := routes.RequestDBNameRaw(c); explicitDBName != "" {
		dbName, _ := routes.ResolveRPCDBName(explicitDBName)
		return dbName, true
	}

	if strings.TrimSpace(c.Cookies(requestDBLockedCookie)) == "1" {
		cookieDBName := strings.TrimSpace(c.Cookies(requestDBNameCookie))
		if cookieDBName != "" {
			dbName, _ := routes.ResolveRPCDBName(cookieDBName)
			return dbName, true
		}
	}

	return "", false
}

func bindRequestDatabase(c fiber.Ctx) error {
	dbName, locked := requestBoundDBName(c)

	if !locked || dbName == "" {
		clearRequestDBCookies(c)
		return c.Next()
	}

	// SEC-T-8: dbName came straight from request input (header/query/cookie).
	// Reject the template and unregistered/stopped tenants before anything
	// opens a pool to them. Cookies are cleared so a stale binding to a
	// stopped tenant falls back to home on the next request.
	if err := kernelmodules.CheckRequestDBName(c.Context(), dbName); err != nil {
		log.Warnf("request rejected: database %q is not selectable: %v", dbName, err)
		clearRequestDBCookies(c)
		return requestDBRejection(err)
	}

	// NoCreate so a name that passed the check but has no Postgres database
	// (validator absent) can't spray-create one via admin credentials.
	registeredDB, err := orm.EnsureNamedBunDBNoCreate(dbName)
	if err != nil {
		log.Warnf("request rejected: database %q is unavailable: %v", dbName, err)
		clearRequestDBCookies(c)
		return fiber.NewError(fiber.StatusServiceUnavailable, kernelmodules.ErrRequestDBUnavailable.Error())
	}

	env := orm.NewEnvironmentWithDBName(registeredDB.DB, dbName, 0, map[string]any{
		orm.ContextKeyDatabaseName: dbName,
	}, false)
	c.SetContext(orm.ContextWithEnvironment(c.Context(), env))
	c.Locals(orm.ContextKeyDatabaseName, dbName)
	setRequestDBCookies(c, dbName)
	return c.Next()
}

// requestDBRejection maps a CheckRequestDBName error to a generic HTTP error:
// the internal reason (lookup failure text, registry state) stays in the log.
func requestDBRejection(err error) error {
	if errors.Is(err, kernelmodules.ErrRequestDBUnavailable) {
		return fiber.NewError(fiber.StatusServiceUnavailable, kernelmodules.ErrRequestDBUnavailable.Error())
	}
	return fiber.NewError(fiber.StatusBadRequest, kernelmodules.ErrRequestDBNotAllowed.Error())
}

// BindRequestDatabase exposes the request DB-binding middleware to the
// conformance suite (SRS CONF-03) so header/cookie database selection is
// exercised against the exact middleware production requests flow through,
// instead of a test-only re-implementation that could drift.
func BindRequestDatabase(c fiber.Ctx) error {
	return bindRequestDatabase(c)
}

func mergeCSVValues(base string, required ...string) []string {
	seen := make(map[string]struct{})
	values := make([]string, 0, len(required))

	// Walk each split(base, ",") entry in order so validation and side effects stay scoped to the current element.
	for _, value := range strings.Split(base, ",") {
		values = appendUniqueCSVValue(values, seen, value)
	}
	for _, value := range required {
		values = appendUniqueCSVValue(values, seen, value)
	}
	return values
}

// splitCSVValues turns a comma-separated config string into the []string
// shape fiber v3's cors.Config now expects (v2 accepted a raw CSV string).
func splitCSVValues(base string) []string {
	return mergeCSVValues(base)
}

func appendUniqueCSVValue(values []string, seen map[string]struct{}, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	key := strings.ToLower(value)
	if _, exists := seen[key]; exists {
		return values
	}
	seen[key] = struct{}{}
	return append(values, value)
}
