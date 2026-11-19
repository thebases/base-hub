package modules

// File overview:
// - Responsibility: lets modules expose Fiber routes without pushing module-specific transport wiring into the global routes package.
// - Key symbols: `HTTPRouteRegistrar`, `RegisterHTTPRoutes`, `moduleInstallGuard`.
// - Pattern: Keeps route ownership close to each module while preserving deterministic registration order.

import (
	"strings"

	"github.com/gofiber/fiber/v3"
)

// HTTPRouteRegistrar is an optional module capability for attaching HTTP
// endpoints during server startup.
type HTTPRouteRegistrar interface {
	RegisterRoutes(router fiber.Router)
}

// SelfGuardedHTTPRoutes is an optional opt-out for modules whose
// RegisterRoutes mounts a prefix-less catch-all (router.Use with no path)
// rather than routes scoped under the module's own path. Such a catch-all
// must fall through to later-registered routes (the SPA/static handlers)
// when the module is not installed, but moduleInstallGuard 404s the request
// outright before the handler ever runs, so a catch-all module implementing
// this interface is mounted without the guard and is expected to perform its
// own InstalledModule check internally (see website's
// handleWebsiteRuntimeDocument). A module that needs only a few routes to
// degrade softly (e.g. knowledge's /md/menu.json returning an empty tree)
// opts out too and wraps the rest via InstallGuardedRouter.
type SelfGuardedHTTPRoutes interface {
	SkipInstallGuard() bool
}

// RegisterHTTPRoutes walks registered modules in manifest-name order and lets
// the ones that own transport routes attach them to the shared Fiber app.
// Routes are registered once at process boot from static Go module
// registration, independent of sys_module_module.state, so every module's
// routes are wrapped in moduleInstallGuard: uninstalling a module only
// deletes its seeded data (see UninstallModule/DeleteModuleData) and never
// unregisters routes, so without this guard an uninstalled module's HTTP
// endpoints would keep serving data until the process restarts. Modules that
// implement SelfGuardedHTTPRoutes (returning true) are exempted from that
// wrapping — see its doc comment for why.
func RegisterHTTPRoutes(router fiber.Router) {
	if router == nil {
		return
	}

	for _, module := range Registered() {
		registrar, ok := module.(HTTPRouteRegistrar)
		if !ok {
			continue
		}
		if selfGuarded, ok := module.(SelfGuardedHTTPRoutes); ok && selfGuarded.SkipInstallGuard() {
			registrar.RegisterRoutes(router)
			continue
		}
		registrar.RegisterRoutes(InstallGuardedRouter(router, module.Manifest().Name))
	}
}

// InstallGuardedRouter wraps router so every route registered through it runs
// moduleInstallGuard(name) first. RegisterHTTPRoutes uses it for every module;
// a SelfGuardedHTTPRoutes module uses it to keep most of its routes guarded
// while mounting a few install-aware ones on the raw router.
func InstallGuardedRouter(router fiber.Router, name string) fiber.Router {
	return &guardedRouter{Router: router, guard: moduleInstallGuard(strings.TrimSpace(name))}
}

// guardedRouter prepends a module's install-state guard to every route a
// registrar defines through it, instead of trying to scope the guard via
// router.Group("", guard). Fiber v3's Group with an empty prefix does not
// scope a middleware to routes added later on the returned sub-router - it
// registers the middleware as router.Use("/", guard) on the underlying app
// immediately, i.e. globally, matching every request site-wide the moment
// RegisterHTTPRoutes runs. That was harmless while every HTTPRouteRegistrar
// module happened to be a core/always-installed one, but as soon as an
// optional module (e.g. knowledge) compiles in via modulesgen and isn't
// installed, its globally-mounted guard 404s the entire app, including "/".
// Embedding fiber.Router means every interface method this type doesn't
// override (Domain/RouteChain/Route/Name/...) still delegates straight to
// the underlying router unguarded - fine today since no RegisterRoutes here
// uses them, but a future module route registered through one of those would
// need the same treatment added below.
type guardedRouter struct {
	fiber.Router
	guard fiber.Handler
}

func (g *guardedRouter) Get(path string, handler any, handlers ...any) fiber.Router {
	g.Router.Get(path, g.guard, append([]any{handler}, handlers...)...)
	return g
}

func (g *guardedRouter) Head(path string, handler any, handlers ...any) fiber.Router {
	g.Router.Head(path, g.guard, append([]any{handler}, handlers...)...)
	return g
}

func (g *guardedRouter) Post(path string, handler any, handlers ...any) fiber.Router {
	g.Router.Post(path, g.guard, append([]any{handler}, handlers...)...)
	return g
}

func (g *guardedRouter) Put(path string, handler any, handlers ...any) fiber.Router {
	g.Router.Put(path, g.guard, append([]any{handler}, handlers...)...)
	return g
}

func (g *guardedRouter) Delete(path string, handler any, handlers ...any) fiber.Router {
	g.Router.Delete(path, g.guard, append([]any{handler}, handlers...)...)
	return g
}

func (g *guardedRouter) Connect(path string, handler any, handlers ...any) fiber.Router {
	g.Router.Connect(path, g.guard, append([]any{handler}, handlers...)...)
	return g
}

func (g *guardedRouter) Options(path string, handler any, handlers ...any) fiber.Router {
	g.Router.Options(path, g.guard, append([]any{handler}, handlers...)...)
	return g
}

func (g *guardedRouter) Trace(path string, handler any, handlers ...any) fiber.Router {
	g.Router.Trace(path, g.guard, append([]any{handler}, handlers...)...)
	return g
}

func (g *guardedRouter) Patch(path string, handler any, handlers ...any) fiber.Router {
	g.Router.Patch(path, g.guard, append([]any{handler}, handlers...)...)
	return g
}

func (g *guardedRouter) Query(path string, handler any, handlers ...any) fiber.Router {
	g.Router.Query(path, g.guard, append([]any{handler}, handlers...)...)
	return g
}

func (g *guardedRouter) Add(methods []string, path string, handler any, handlers ...any) fiber.Router {
	g.Router.Add(methods, path, g.guard, append([]any{handler}, handlers...)...)
	return g
}

func (g *guardedRouter) All(path string, handler any, handlers ...any) fiber.Router {
	g.Router.All(path, g.guard, append([]any{handler}, handlers...)...)
	return g
}

// Use inserts the guard immediately after any leading path prefix argument(s)
// (string or []string) and before the actual middleware/handlers, so a
// guarded prefix-scoped Use (e.g. base-bus's router.Use("/bus/ws", ...))
// still 404s correctly when its module isn't installed.
func (g *guardedRouter) Use(args ...any) fiber.Router {
	i := 0
	for i < len(args) {
		switch args[i].(type) {
		case string, []string:
			i++
		default:
			// Stop at the first handler so the guard lands before it — setting
			// i = len(args) here would append the guard after every handler,
			// letting an uninstalled module's middleware run anyway.
			goto insert
		}
	}
insert:
	guarded := make([]any, 0, len(args)+1)
	guarded = append(guarded, args[:i]...)
	guarded = append(guarded, g.guard)
	guarded = append(guarded, args[i:]...)
	g.Router.Use(guarded...)
	return g
}

func (g *guardedRouter) Group(prefix string, handlers ...any) fiber.Router {
	return &guardedRouter{Router: g.Router.Group(prefix, handlers...), guard: g.guard}
}

// moduleInstallGuard 404s any request under a module's routes once
// sys_module_module reports it as not installed, matching the same
// InstalledModule check serveModuleStaticResource already applies to
// per-module static assets.
func moduleInstallGuard(name string) fiber.Handler {
	return func(c fiber.Ctx) error {
		installed, err := InstalledModule(c.Context(), name)
		if err != nil {
			return fiber.ErrInternalServerError
		}
		if !installed {
			return fiber.ErrNotFound
		}
		return c.Next()
	}
}
