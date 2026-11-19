package cmd

// File overview:
// - Responsibility: static asset and SPA index serving (user FE, web SPA, module static resources).
// - Key symbols: `shouldServeUserFEIndex`, `shouldServeWebSPAIndex`, `serveModuleStaticResource`, `applyNoCacheMarkdownHeaders`.

import (
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"

	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/static"
)

// shouldServeUserFEIndex handles the end-user SPA fallback served at "/". It
// excludes /web, whose deep links resolve through serveWebSPAIndex instead.
func shouldServeUserFEIndex(c fiber.Ctx) bool {
	// Only GET/HEAD browser navigations should resolve to the SPA entrypoint.
	if c.Method() != fiber.MethodGet && c.Method() != fiber.MethodHead {
		return false
	}

	path := c.Path()
	if strings.HasPrefix(path, "/apidocs") || strings.HasPrefix(path, "/web") {
		return false
	}

	accept := strings.ToLower(c.Get(fiber.HeaderAccept))
	if accept != "" && !strings.Contains(accept, fiber.MIMETextHTML) {
		return false
	}

	// Leave direct asset requests to the static middleware and API handlers.
	if ext := filepath.Ext(path); ext != "" {
		return false
	}
	return true
}

// shouldServeWebSPAIndex gates the /web deep-link SPA fallback, which serves
// app/frontend's index.html (resolvedUserFEStaticAssets) — the same
// document and asset root already mounted at "/", so its relative asset URLs
// resolve correctly regardless of whether the browser is on "/" or "/web/*".
func shouldServeWebSPAIndex(c fiber.Ctx) bool {
	// Only browser navigations should fall back to the SPA entrypoint.
	if c.Method() != fiber.MethodGet && c.Method() != fiber.MethodHead {
		return false
	}

	p := c.Path()

	if !strings.HasPrefix(p, "/web") {
		return false
	}

	if strings.HasPrefix(p, "/api") ||
		strings.HasPrefix(p, "/apidocs") ||
		strings.HasPrefix(p, "/swagger") ||
		strings.HasPrefix(p, "/md") {
		return false
	}

	// Keep real backend endpoints on the server side. Metadata model routes
	// legitimately contain dots (for example `/web/tbl.country.state`), so a
	// blanket "segment has dot" check breaks browser refresh on list/kanban pages.
	for _, prefix := range []string{
		"/web/image",
		"/web/content",
		"/web/assets",
		// Report rendering endpoints (SRS-F-020) must reach the backend, not
		// the SPA fallback.
		"/report",
	} {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return false
		}
	}

	// Keep obvious frontend bundle assets on the static handler while still
	// allowing dotted model names such as `base.users` or `tbl.country.state`.
	switch strings.ToLower(path.Ext(path.Base(p))) {
	case ".js", ".css", ".map", ".ico", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".woff", ".woff2", ".ttf", ".eot", ".txt", ".xml":
		return false
	}

	accept := strings.ToLower(c.Get(fiber.HeaderAccept))
	return accept == "" || strings.Contains(accept, fiber.MIMETextHTML)
}

// moduleInstalledForStatic is the install-state check behind /<module>/static/*; a variable so
// tests can flip a module between installed and uninstalled without a database.
var moduleInstalledForStatic = kernelmodules.InstalledModule

// serveModuleStaticResource serves <module>/static/** of an INSTALLED module. The install state is
// read from the request's database on every request, so installing a module makes its static
// folder reachable immediately and uninstalling it makes every file 404 again (no cache to purge;
// assets are served no-cache with an ETag, so browsers revalidate and get the 404).
func serveModuleStaticResource(c fiber.Ctx) error {
	moduleName := strings.TrimSpace(c.Params("module"))
	resourcePath := strings.TrimSpace(c.Params("*"))
	if moduleName == "" || resourcePath == "" {
		return fiber.ErrNotFound
	}
	for _, part := range strings.Split(filepath.ToSlash(resourcePath), "/") {
		if part == "" || part == "." || part == ".." {
			return fiber.ErrNotFound
		}
	}
	enabled, err := moduleInstalledForStatic(c.Context(), moduleName)
	if err != nil {
		return fiber.ErrInternalServerError
	}
	if !enabled {
		return fiber.ErrNotFound
	}
	// Module static assets ship with the Go module tree itself (disk during
	// development, the `modules` go:embed in production).
	raw, err := kernelmodules.ReadModuleResource(filepath.ToSlash(filepath.Join(moduleName, "static", resourcePath)))
	if err != nil {
		return fiber.ErrNotFound
	}
	if ext := filepath.Ext(resourcePath); ext != "" {
		c.Type(ext)
	}
	return sendRevalidatedAsset(c, raw)
}

func applyNoCacheMarkdownHeaders(c fiber.Ctx) error {
	c.Set(fiber.HeaderCacheControl, "no-store, no-cache, must-revalidate")
	c.Set(fiber.HeaderPragma, "no-cache")
	c.Set(fiber.HeaderExpires, "0")
	return c.Next()
}

func configureStaticAssetRoutes(app *fiber.App) error {
	userFEAssets, err := resolveUserFEStaticAssets()
	if err != nil {
		log.Errorf("resolve user-fe static assets: %v", err)
		return fmt.Errorf("resolve user-fe static assets: %w", err)
	}
	resolvedUserFEStaticAssets = userFEAssets
	log.Infof("serving root static assets from %s", userFEAssets.Source)

	// Lists the frontend/modules/<name>/register.js files this build ships so the SPA can load
	// module-owned form widgets and routes without the base bundle naming any module.
	app.Get(frontendModulesPath, serveFrontendModules)

	// "/" serves app/frontend directly, or a tenant's published
	// website when the website module resolves one for the request (see
	// website/routes/runtime_route.go, mounted ahead of this static fallback
	// by kernelmodules.RegisterHTTPRoutes).
	app.Use("/", serveWithoutXMLComments(userFEAssets.FS, ""))
	app.Use("/", serveIndexAtRoot(userFEAssets.FS))
	app.Use("/", static.New("", static.Config{
		FS:         userFEAssets.FS,
		Browse:     false,
		IndexNames: []string{"index.html"},
		MaxAge:     3600,
		Next:       shouldSkipRootStaticMiddleware,
	}))
	app.Use(serveUserFEIndex)
	// /web deep links (login, SDUI model views, ...) resolve client-side
	// inside the same app/frontend bundle mounted at "/", so its
	// index.html backs /web too.
	app.Use("/web", serveWebSPAIndex)
	return nil
}

// frontendModulesPath is the SPA's module-registration manifest (see serveFrontendModules).
const frontendModulesPath = "/frontend-modules.json"

// serveFrontendModules answers the names of every module folder that ships a register.js, found
// by globbing the served frontend tree, so an edition adds a module's widgets/routes by adding its
// folder alone. No auth: it only reveals which static files are already publicly served.
func serveFrontendModules(c fiber.Ctx) error {
	names := []string{}
	if resolvedUserFEStaticAssets.FS != nil {
		matches, err := fs.Glob(resolvedUserFEStaticAssets.FS, "modules/*/register.js")
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		for _, match := range matches {
			names = append(names, strings.Split(match, "/")[1])
		}
	}
	c.Set(fiber.HeaderCacheControl, "no-cache")
	return c.JSON(fiber.Map{"modules": names})
}

func shouldSkipRootStaticMiddleware(c fiber.Ctx) bool {
	path := c.Path()
	return strings.HasPrefix(path, "/apidocs") || shouldServeWebSPAIndex(c)
}

func serveUserFEIndex(c fiber.Ctx) error {
	if !shouldServeUserFEIndex(c) {
		return c.Next()
	}
	return sendStaticAsset(c, resolvedUserFEStaticAssets.FS, "index.html", fiber.StatusOK)
}

func serveWebSPAIndex(c fiber.Ctx) error {
	if !shouldServeWebSPAIndex(c) {
		return c.Next()
	}
	return sendStaticAsset(c, resolvedUserFEStaticAssets.FS, "index.html", fiber.StatusOK)
}
