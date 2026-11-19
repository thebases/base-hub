package routes

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/queries"
	internalroutes "git.thebase.vn/base_hub/internal/routes"
	"git.thebase.vn/base_hub/internal/services/log"
	"git.thebase.vn/base_hub/modules/website/models"
	"github.com/gofiber/fiber/v3"
)

// RegisterRoutes mounts public runtime endpoints that the root SPA uses to
// decide whether to render a published website or fall back to legacy content.
func RegisterRoutes(router fiber.Router) {
	if router == nil {
		return
	}
	router.Get("/website/runtime/resolve", handleWebsiteRuntimeResolve)
	router.Use(handleWebsiteRuntimeDocument)
}

// handleWebsiteRuntimeResolve is the admin Studio preview endpoint: it reports
// whether a route currently resolves to a published (deploy_path set) page,
// without serving the deployed file itself. The response carries only the
// published flag, never the filesystem path: the endpoint has no session check.
func handleWebsiteRuntimeResolve(c fiber.Ctx) error {
	siteRow, pageRow, enabled, err := resolveWebsitePageForRequest(c, strings.TrimSpace(c.Query("path")))
	if err != nil {
		status, message := websiteRuntimeResolveError(err)
		return fiber.NewError(status, message)
	}
	if !enabled {
		return c.JSON(fiber.Map{"enabled": false})
	}
	return c.JSON(websiteRuntimeResolvePayload(siteRow, pageRow))
}

func websiteRuntimeResolvePayload(siteRow, pageRow orm.RecordMap) fiber.Map {
	return fiber.Map{
		"enabled": true,
		"site": fiber.Map{
			"id":   siteRow["id"],
			"name": siteRow["name"],
			"slug": siteRow["slug"],
		},
		"page": fiber.Map{
			"id":         pageRow["id"],
			"name":       pageRow["name"],
			"route_path": pageRow["route_path"],
			"published":  runtimeString(pageRow["deploy_path"]) != "",
		},
	}
}

// handleWebsiteRuntimeDocument serves the active site's flat static scaffold
// straight from disk (see helpers_website_site_scaffold.go/helpers_owl_scaffold.go
// for the on-disk shape: root index.html/main.js/manifest.json/sitemap.xml/
// styles.css/sw.js/vendor/*, plus pages/<Name>/<Name>.{js,xml,css} per page).
// File locations are now deterministic relative paths, so asset requests
// resolve to <site.deploy_path>/<requestPath> directly -- no per-request page
// lookup needed. Any extensionless/navigable GET is served the single root
// index.html (SPA shell); client-side routing in main.js takes it from there.
func handleWebsiteRuntimeDocument(c fiber.Ctx) error {
	requestPath := strings.TrimSpace(c.Path())
	switch classifyWebsiteRuntimeRequest(c) {
	case websiteRuntimeRequestPageDocument:
		return serveWebsiteRuntimePageDocument(c)
	case websiteRuntimeRequestPageAsset, websiteRuntimeRequestVendorAsset:
		return serveWebsiteRuntimeStaticAsset(c, requestPath)
	default:
		return c.Next()
	}
}

// websiteRuntimeRequestKind is the pure (no I/O) classification of a request
// path/method/Accept header into which disk-serving branch handles it, kept
// separate from handleWebsiteRuntimeDocument so it's unit-testable without a
// database.
type websiteRuntimeRequestKind int

const (
	websiteRuntimeRequestNone websiteRuntimeRequestKind = iota
	websiteRuntimeRequestPageDocument
	websiteRuntimeRequestPageAsset
	websiteRuntimeRequestVendorAsset
)

func classifyWebsiteRuntimeRequest(c fiber.Ctx) websiteRuntimeRequestKind {
	if c.Method() != fiber.MethodGet && c.Method() != fiber.MethodHead {
		return websiteRuntimeRequestNone
	}
	requestPath := strings.TrimSpace(c.Path())
	if requestPath == "" || isWebsiteRuntimeReservedPath(requestPath) {
		return websiteRuntimeRequestNone
	}
	// The site's own files (main.js, sw.js, vendor/*, pages/*, styles.css) are
	// served under websiteRuntimeAssetPrefix, never at the root: the core SPA
	// also loads /main.js, /sw.js and /vendor/* from the root, so claiming them
	// here made /web boot the website's router (white screen on "Sign in").
	if strings.HasPrefix(requestPath, websiteRuntimeAssetPrefix+"/") {
		return websiteRuntimeRequestPageAsset
	}
	switch requestPath {
	case "/sitemap.xml", "/manifest.json":
		// Crawler/PWA entry points with no core-SPA counterpart.
		return websiteRuntimeRequestPageAsset
	}
	switch strings.ToLower(filepath.Ext(requestPath)) {
	case "":
		accept := strings.ToLower(c.Get(fiber.HeaderAccept))
		if accept != "" && !strings.Contains(accept, fiber.MIMETextHTML) {
			return websiteRuntimeRequestNone
		}
		return websiteRuntimeRequestPageDocument
	default:
		return websiteRuntimeRequestNone
	}
}

// serveWebsiteRuntimePageDocument serves the active site's single root
// index.html for any navigable request -- the SPA shell that main.js then
// routes client-side. There is no more per-route DB page lookup at this
// layer: an unknown route still gets the shell (main.js falls back to the
// homepage), matching the export scaffold's own client-side fallback.
func serveWebsiteRuntimePageDocument(c fiber.Ctx) error {
	siteRow, enabled, err := resolveActiveWebsiteSiteForRequest(c)
	if err != nil || !enabled || siteRow == nil {
		return c.Next()
	}
	if handled, redirectErr := applyWebsiteRedirect(c, siteRow); handled || redirectErr != nil {
		return redirectErr
	}
	deployPath, ok := confinedSiteDeployPath(c, siteRow)
	if !ok {
		return c.Next()
	}
	document, readErr := os.ReadFile(filepath.Join(deployPath, "index.html"))
	if readErr != nil {
		return c.Next()
	}
	c.Type("html", "utf-8")
	return c.Send(injectWebsiteRuntimeBase(document))
}

// applyWebsiteRedirect applies the site's redirect rule for the request path
// before the shell is served. handled=true means a response was written.
// Lookup failures fall through to normal serving: a redirect table problem
// must never take the public site down.
func applyWebsiteRedirect(c fiber.Ctx, siteRow orm.RecordMap) (bool, error) {
	siteID, ok := runtimeInt64(siteRow["id"])
	if !ok || siteID <= 0 {
		return false, nil
	}
	dbName, err := resolveWebsiteRuntimeDBName(c)
	if err != nil {
		return false, nil
	}
	ctx := internalroutes.RPCContextForDB(c.Context(), dbName)
	path := normalizeRuntimePath(c.Path())
	rule, err := models.LookupWebsiteRedirect(ctx, siteID, path)
	if err != nil || rule == nil {
		return false, nil
	}
	switch runtimeString(rule["redirect_type"]) {
	case "404":
		return true, c.Status(fiber.StatusNotFound).SendString("Not Found")
	case "410":
		return true, c.Status(fiber.StatusGone).SendString("Gone")
	case "302":
		return true, c.Redirect().Status(fiber.StatusFound).To(runtimeString(rule["url_to"]))
	default:
		return true, c.Redirect().Status(fiber.StatusMovedPermanently).To(runtimeString(rule["url_to"]))
	}
}

// websiteRuntimeAssetPrefix is the public mount of the active site's deploy
// folder; see classifyWebsiteRuntimeRequest for why it is not the root.
const websiteRuntimeAssetPrefix = "/_site"

// websiteRuntimeBaseTag points the shell's relative "./main.js", "./vendor/*"
// and "./pages/*" URLs (and main.js's dynamic imports and sw.js registration,
// which resolve against it) at websiteRuntimeAssetPrefix. Absolute links such
// as "/web" or "/about" are unaffected.
const websiteRuntimeBaseTag = `<base href="` + websiteRuntimeAssetPrefix + `/">`

var websiteRuntimeBaseRE = regexp.MustCompile(`(?i)<base\s[^>]*>`)
var websiteRuntimeHeadRE = regexp.MustCompile(`(?i)<head[^>]*>`)

// injectWebsiteRuntimeBase rewrites a published index.html at serve time, so
// sites published before this prefix existed work without a republish and the
// on-disk scaffold stays a plain relative-path export.
func injectWebsiteRuntimeBase(document []byte) []byte {
	document = websiteRuntimeBaseRE.ReplaceAll(document, nil)
	if loc := websiteRuntimeHeadRE.FindIndex(document); loc != nil {
		out := make([]byte, 0, len(document)+len(websiteRuntimeBaseTag)+1)
		out = append(out, document[:loc[1]]...)
		out = append(out, '\n')
		out = append(out, websiteRuntimeBaseTag...)
		return append(out, document[loc[1]:]...)
	}
	return append([]byte(websiteRuntimeBaseTag+"\n"), document...)
}

// serveWebsiteRuntimeStaticAsset resolves any relative asset path (root files
// like "/main.js"/"/manifest.json"/"/sitemap.xml"/"/styles.css"/"/sw.js",
// "/vendor/<name>", or "/pages/<Name>/<Name>.{js,xml,css}") straight to
// <active site.deploy_path>/<requestPath>, since the flat scaffold's file
// locations are deterministic. Guards against path traversal since the path
// comes straight from the request URL.
func serveWebsiteRuntimeStaticAsset(c fiber.Ctx, requestPath string) error {
	siteRow, enabled, err := resolveActiveWebsiteSiteForRequest(c)
	if err != nil || !enabled || siteRow == nil {
		return c.Next()
	}
	return serveWebsiteSiteStaticAsset(c, siteRow, requestPath)
}

// serveWebsiteSiteStaticAsset serves requestPath from the already resolved site's deploy folder.
func serveWebsiteSiteStaticAsset(c fiber.Ctx, siteRow orm.RecordMap, requestPath string) error {
	if runtimeString(siteRow["deploy_path"]) == "" {
		return c.Next()
	}
	root, ok := confinedSiteDeployPath(c, siteRow)
	if !ok {
		// A deploy_path outside the tenant public root is never served (it would expose arbitrary
		// server folders to anonymous callers); answer 404 rather than fall through to other handlers.
		return c.SendStatus(fiber.StatusNotFound)
	}
	requestPath = strings.TrimPrefix(requestPath, websiteRuntimeAssetPrefix)
	assetPath := filepath.Clean(filepath.Join(root, requestPath))
	if assetPath != root && !strings.HasPrefix(assetPath, root+string(os.PathSeparator)) {
		return c.Next()
	}
	// The protected/ folder is never served as a file. A page template with a copy there belongs to a
	// logged-in-only page: hand it out only for a valid session, never from the public path.
	if rel, relErr := filepath.Rel(root, assetPath); relErr == nil {
		if rel == "protected" || strings.HasPrefix(rel, "protected"+string(os.PathSeparator)) {
			return c.Next()
		}
		if gated, gatedPath := websiteProtectedTemplatePath(root, rel); gated {
			identity, idErr := internalroutes.CurrentRPCIdentity(c)
			if idErr != nil || identity == nil {
				c.Set(fiber.HeaderCacheControl, "no-store")
				return c.SendStatus(fiber.StatusUnauthorized)
			}
			c.Set(fiber.HeaderCacheControl, "no-store")
			return c.SendFile(gatedPath)
		}
	}
	// Directories are never served: the SW precaches "./" (= /_site/), and SendFile on a folder
	// answers with a redirect carrying the internal host and filesystem path (mixed-content block).
	if info, statErr := os.Stat(assetPath); statErr != nil || info.IsDir() {
		return c.Next()
	}
	// Site files keep fixed URLs across publishes (main.js carries the route table, pages/<Name>/*
	// their content, sw.js the update check), and c.SendFile only sets Last-Modified, which leaves
	// room for heuristic browser/proxy caching to serve a stale copy after a publish -- e.g. a new
	// page missing from a cached route table. Force revalidation on every request; unchanged files
	// still answer 304.
	c.Set(fiber.HeaderCacheControl, "no-cache")
	return c.SendFile(assetPath)
}

// confinedSiteDeployPath returns the site's deploy folder only when it lies under the request
// tenant's <shared>/<db>/public root (see models.ConfinedWebsiteDeployPath).
func confinedSiteDeployPath(c fiber.Ctx, siteRow orm.RecordMap) (string, bool) {
	dbName, err := resolveWebsiteRuntimeDBName(c)
	if err != nil {
		return "", false
	}
	return models.ConfinedWebsiteDeployPath(dbName, runtimeString(siteRow["deploy_path"]))
}

// websiteProtectedTemplatePath maps pages/<Name>/<Name>.xml to <root>/protected/<Name>.xml and
// reports whether that protected copy exists (i.e. the page is logged-in only).
func websiteProtectedTemplatePath(root, rel string) (bool, string) {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 3 || parts[0] != "pages" || parts[2] != parts[1]+".xml" {
		return false, ""
	}
	// The page's own component module must still be deployed: an unpublished page leaves its protected
	// template behind, and that must not be served.
	if _, err := os.Stat(filepath.Join(root, "pages", parts[1], parts[1]+".js")); err != nil {
		return false, ""
	}
	candidate := filepath.Join(root, "protected", parts[2])
	if _, err := os.Stat(candidate); err != nil {
		return false, ""
	}
	return true, candidate
}

// extraReservedRuntimePrefixes holds path prefixes other modules own on the public root (e.g.
// website-blog's "/blog"). Modules register during Bootstrap, before any request is served, so no
// lock is needed.
var extraReservedRuntimePrefixes []string

// RegisterReservedRuntimePrefix lets a module that depends on website claim a bare-root prefix
// ("/blog") so the catch-all runtime never serves the site's SPA shell for it. Routes are mounted
// in module-name order and "website" sorts first, so without this the shell would shadow the
// dependent module's own handlers.
func RegisterReservedRuntimePrefix(prefix string) {
	prefix = "/" + strings.Trim(strings.TrimSpace(prefix), "/")
	if prefix == "/" {
		return
	}
	for _, existing := range extraReservedRuntimePrefixes {
		if existing == prefix {
			return
		}
	}
	extraReservedRuntimePrefixes = append(extraReservedRuntimePrefixes, prefix)
}

// ResolveSiteForRequest returns the public site (domain-bound, else the first active one) for
// the request, plus the tenant context the lookup ran in. enabled=false means the website module
// is not installed on this tenant or no site resolves.
func ResolveSiteForRequest(c fiber.Ctx) (siteRow orm.RecordMap, ctx context.Context, enabled bool, err error) {
	dbName, err := resolveWebsiteRuntimeDBName(c)
	if err != nil {
		return nil, nil, false, err
	}
	ctx = internalroutes.RPCContextForDB(c.Context(), dbName)
	siteRow, enabled, err = resolveActiveWebsiteSiteForRequest(c)
	return siteRow, ctx, enabled, err
}

func isWebsiteRuntimeReservedPath(requestPath string) bool {
	// Keep mounted backend/admin surfaces ahead of the public website fallback.
	// The website runtime is a catch-all — serveWebsiteRuntimePageDocument
	// serves the active site's index.html for ANY extensionless GET not
	// reserved here — so a missing prefix lets a published site steal that
	// app's deep links and assets. "/login" and "/view-md" are
	// app/frontend's own bare-root routes (router.js's ROUTES; root
	// "/" itself is deliberately left unreserved so a tenant's published
	// site can still own it). "/frontend" was removed from this list once
	// that mount was dropped in the 2026-09-02 root cutover (see
	// frontend-context.md).
	for _, prefix := range append([]string{
		"/api",
		"/apidocs",
		"/health",
		"/jsonrpc",
		"/login",
		"/md",
		"/metrics",
		"/mqtt",
		"/report",
		"/rpc",
		"/swagger",
		"/view-md",
		"/web",
		"/website/runtime",
	}, extraReservedRuntimePrefixes...) {
		if requestPath == prefix || strings.HasPrefix(requestPath, prefix+"/") {
			return true
		}
	}
	return false
}

// resolveWebsitePageForRequest looks up the request's site (via
// resolveWebsiteSiteForRequest: domain-bound, or the single active site) and
// the page matching requestPath by route_path (or the homepage for "/").
// Returns enabled=false when the module is off, no site resolves, or no page
// matches the route.
func resolveWebsitePageForRequest(c fiber.Ctx, requestPath string) (orm.RecordMap, orm.RecordMap, bool, error) {
	dbName, err := resolveWebsiteRuntimeDBName(c)
	if err != nil {
		return nil, nil, false, err
	}
	ctx := internalroutes.RPCContextForDB(c.Context(), dbName)

	enabled, err := queries.ModuleInstalled(ctx, orm.ResolvePGXPool(dbName), "website")
	if err != nil {
		return nil, nil, false, err
	}
	if !enabled {
		return nil, nil, false, nil
	}

	siteRow, err := resolveWebsiteSiteForRequest(ctx, c)
	if err != nil {
		return nil, nil, false, err
	}
	if siteRow == nil {
		return nil, nil, false, nil
	}
	siteID, ok := runtimeInt64(siteRow["id"])
	if !ok || siteID <= 0 {
		return nil, nil, false, nil
	}

	normalizedPath := normalizeRuntimePath(requestPath)
	pageRow, err := resolveWebsitePageByRoute(ctx, siteID, normalizedPath)
	if err != nil {
		return nil, nil, false, err
	}
	if pageRow == nil {
		return nil, nil, false, nil
	}
	return siteRow, pageRow, true, nil
}

func resolveWebsitePageByRoute(ctx context.Context, siteID int64, routePath string) (orm.RecordMap, error) {
	filters := orm.RecordMap{
		"site_id": siteID,
		"__limit": 1,
	}
	if routePath == "/" {
		filters["is_homepage"] = true
	} else {
		filters["route_path"] = routePath
	}
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(models.WebsitePageModelName, nil), filters, nil)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// resolveActiveWebsiteSiteForRequest resolves the tenant's tbl.website_site row
// for the current request (see resolveWebsiteSiteForRequest for the
// domain-vs-single-active-site precedence), without resolving any page. Used
// by the document/static-asset handlers, which serve straight from the
// site's deploy_path using deterministic relative paths rather than a
// per-request page lookup.
func resolveActiveWebsiteSiteForRequest(c fiber.Ctx) (orm.RecordMap, bool, error) {
	dbName, err := resolveWebsiteRuntimeDBName(c)
	if err != nil {
		return nil, false, err
	}
	ctx := internalroutes.RPCContextForDB(c.Context(), dbName)

	enabled, err := queries.ModuleInstalled(ctx, orm.ResolvePGXPool(dbName), "website")
	if err != nil {
		return nil, false, err
	}
	if !enabled {
		return nil, false, nil
	}

	siteRow, err := resolveWebsiteSiteForRequest(ctx, c)
	if err != nil {
		return nil, false, err
	}
	if siteRow == nil {
		return nil, false, nil
	}
	return siteRow, true, nil
}

// resolveWebsiteSiteForRequest picks the tenant's tbl.website_site row for the
// current request: a domain binding (tbl.website_domain, keyed by Host) takes
// priority when one matches, so a tenant with multiple published sites can
// route each to its own hostname; falling back to the single
// is_active=true site keeps single-site tenants working with zero
// tbl.website_domain rows configured.
func resolveWebsiteSiteForRequest(ctx context.Context, c fiber.Ctx) (orm.RecordMap, error) {
	if siteRow, err := resolveWebsiteSiteForHost(ctx, c.Hostname()); err != nil {
		return nil, err
	} else if siteRow != nil {
		return siteRow, nil
	}

	siteRows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(models.WebsiteSiteModelName, nil), orm.RecordMap{
		"is_active": true,
		"__order":   "id asc",
		"__limit":   1,
	}, nil)
	if err != nil {
		return nil, err
	}
	if len(siteRows) == 0 {
		return nil, nil
	}
	return siteRows[0], nil
}

// resolveWebsiteSiteForHost looks up an active tbl.website_domain binding for
// host and, if found, the tbl.website_site row it points at. Returns a nil row
// (not an error) when no binding matches, so callers can fall back to the
// legacy single-active-site lookup.
func resolveWebsiteSiteForHost(ctx context.Context, host string) (orm.RecordMap, error) {
	host = normalizeRuntimeHost(host)
	if host == "" {
		return nil, nil
	}

	domainRows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(models.WebsiteDomainModelName, nil), orm.RecordMap{
		"host":      host,
		"is_active": true,
		"__limit":   1,
	}, nil)
	if err != nil {
		return nil, err
	}
	if len(domainRows) == 0 {
		return nil, nil
	}
	siteID, ok := runtimeInt64(domainRows[0]["site_id"])
	if !ok || siteID <= 0 {
		return nil, nil
	}

	siteRows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(models.WebsiteSiteModelName, nil), orm.RecordMap{
		"id":      siteID,
		"__limit": 1,
	}, nil)
	if err != nil {
		return nil, err
	}
	if len(siteRows) == 0 {
		return nil, nil
	}
	return siteRows[0], nil
}

// websiteRuntimeResolveError maps a resolve failure by type, not by message text: a typed orm
// error keeps its status and message; anything else is a 500 whose detail stays in the log (the
// endpoint is anonymous, so raw database errors must not reach the caller).
func websiteRuntimeResolveError(err error) (int, string) {
	var typed *orm.Error
	if errors.As(err, &typed) {
		return typed.Status, typed.Message
	}
	log.Errorf("website runtime resolve: %v", err)
	return fiber.StatusInternalServerError, "website runtime resolve failed"
}

func resolveWebsiteRuntimeDBName(c fiber.Ctx) (string, error) {
	// Root/public traffic can be database-bound by the shared middleware from
	// either X-Database-Name/?db= or the locked DB cookie. Prefer that
	// environment so published sites resolve from the same tenant the
	// browser is already using.
	if env := orm.EnvironmentFromContext(c.Context()); env != nil {
		if dbName := strings.TrimSpace(env.DBName()); dbName != "" {
			return dbName, nil
		}
	}
	return internalroutes.ResolveRPCDBName(internalroutes.RequestDBNameRaw(c))
}

// normalizeRuntimeHost strips any port and lowercases the Host header value
// so tbl.website_domain lookups match regardless of case or an explicit port
// (e.g. local dev hitting "Tenant.Example.com:8080").
func normalizeRuntimeHost(host string) string {
	return strings.ToLower(strings.TrimSpace(strings.SplitN(strings.TrimSpace(host), ":", 2)[0]))
}

func normalizeRuntimePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	for strings.Contains(path, "//") {
		path = strings.ReplaceAll(path, "//", "/")
	}
	path = strings.TrimRight(path, "/")
	if path == "" {
		return "/"
	}
	return path
}

func runtimeString(raw any) string {
	switch typed := raw.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
	}
}

func runtimeInt64(raw any) (int64, bool) {
	switch typed := raw.(type) {
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	case float64:
		return int64(typed), true
	case string:
		value, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return value, err == nil
	case []any:
		// Runtime repository reads serialize many2one fields as [id, label].
		if len(typed) == 0 {
			return 0, false
		}
		return runtimeInt64(typed[0])
	case map[string]any:
		return runtimeInt64(typed["id"])
	default:
		return 0, false
	}
}

// fiber:context-methods migrated
