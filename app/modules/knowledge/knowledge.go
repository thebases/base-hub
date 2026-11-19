package knowledge

// File overview:
// - Responsibility: provides bootstrap or aggregation glue for the `knowledge` module, owning the embedded base documentation bundle used by the markdown viewer/editor routes.
// - Key symbols: `init`, `module`.
// - Pattern: Keeps module registration and transport ownership inside the module package so startup stays deterministic.

import (
	"context"
	"embed"
	"fmt"
	"io/fs"

	"git.thebase.vn/base_hub/internal/config"
	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	knowledgemcp "git.thebase.vn/base_hub/modules/knowledge/mcp"
	knowledgeroutes "git.thebase.vn/base_hub/modules/knowledge/routes"
	"github.com/gofiber/fiber/v3"
)

type module struct{}

//go:embed manifest.md
var manifestDescription string

// baseDocsBundle embeds the read-only documentation tree directly in this
// module so the knowledge feature no longer depends on the top-level
// main_embed.go embed graph. It is internal/control-plane content, so routes
// only surface it for the Home tenant. user_guide now lives nested under
// documents/base (documents/base/user_guide), so this single embed covers
// both; the user_guide sub-tree is still surfaced to every tenant (unlike the
// rest of documents/base) via fs.Sub below.
//
//go:embed all:documents/base
var baseDocsBundle embed.FS

// init registers this package runtime during bootstrap so its routes are available before requests arrive.
func init() {
	kernelmodules.Register(module{})
}

// Manifest returns the embedded module manifest.
func (module) Manifest() kernelmodules.Manifest {
	return kernelmodules.MustManifestFromMarkdown(manifestDescription)
}

// Bootstrap registers this module's knowledge.* MCP tool group with
// base-mcp's provider registry (see modules/knowledge/mcp) so it's reachable
// as soon as the process starts, independent of whether/when RegisterRoutes
// runs -- Bootstrap is the one lifecycle hook every module implements
// unconditionally (internal/modules.Module), unlike the optional
// HTTPRouteRegistrar interface RegisterRoutes belongs to.
func (module) Bootstrap(_ context.Context, _ *kernelmodules.BootstrapContext) error {
	knowledgemcp.Register()
	return nil
}

// SkipInstallGuard opts out of the kernel's blanket install guard so
// RegisterRoutes can mount /md/menu.json install-aware (empty tree instead of
// 404 — the Shell sidebar fetches it on every page load, installed or not).
// Every other route is still wrapped via kernelmodules.InstallGuardedRouter.
func (module) SkipInstallGuard() bool { return true }

// RegisterRoutes lets the module satisfy internal/modules.HTTPRouteRegistrar
// while keeping transport code inside the dedicated routes package.
func (m module) RegisterRoutes(router fiber.Router) {
	baseFS, err := fs.Sub(baseDocsBundle, "documents/base")
	if err != nil {
		panic(fmt.Errorf("prepare embedded knowledge base documents bundle: %w", err))
	}
	userGuideFS, err := fs.Sub(baseFS, "user_guide")
	if err != nil {
		panic(fmt.Errorf("prepare embedded knowledge user_guide documents bundle: %w", err))
	}

	options := knowledgeroutes.MarkdownRouteOptions{
		BaseFS:      baseFS,
		UserGuideFS: userGuideFS,
		HomeDBName:  config.HomeDBName(),
		PublicRootForDB: func(dbName string) string {
			return config.Get().KnowledgePublicDocsRoot(dbName)
		},
	}
	// Publish the same options for in-process, non-HTTP callers (the base-mcp
	// knowledge.* tool group) so they reuse the exact doc sources/roots
	// instead of re-deriving them.
	knowledgeroutes.SetActiveOptions(options)
	// Register this module's document service against the kernel hook so
	// other modules (base-mcp) can reach it without importing
	// modules/knowledge/routes directly -- keeps knowledge independently
	// installable/uninstallable.
	kernelmodules.RegisterKnowledgeDocumentProvider(knowledgeroutes.NewKernelDocumentProvider(options))
	// Menu goes on the raw router with its own install check; documents stay
	// behind the kernel guard (404 when the module isn't installed).
	name := m.Manifest().Name
	menuOptions := options
	menuOptions.Installed = func(ctx context.Context) (bool, error) {
		return kernelmodules.InstalledModule(ctx, name)
	}
	knowledgeroutes.RegisterMarkdownMenuRoute(router, menuOptions)
	knowledgeroutes.RegisterMarkdownDocumentRoutes(kernelmodules.InstallGuardedRouter(router, name), options)
}
