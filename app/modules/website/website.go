package website

import (
	"context"
	_ "embed"

	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	websitemcp "git.thebase.vn/base_hub/modules/website/mcp"
	websitemodels "git.thebase.vn/base_hub/modules/website/models"
	websiteroutes "git.thebase.vn/base_hub/modules/website/routes"
	"github.com/gofiber/fiber/v3"
)

type module struct{}

//go:embed manifest.md
var manifestDescription string

// init registers this package runtime during bootstrap so website models and
// routes are available before requests arrive.
func init() {
	kernelmodules.Register(module{})
}

// Manifest returns the embedded module manifest for catalog sync and bootstrap.
func (module) Manifest() kernelmodules.Manifest {
	return kernelmodules.MustManifestFromMarkdown(manifestDescription)
}

// Bootstrap wires the website runtime model registry into the shared startup
// sequence so model CRUD and publish actions exist before HTTP traffic
// arrives, and registers this module's website.* MCP tool group with
// base-mcp's provider registry (see modules/website/mcp).
func (module) Bootstrap(_ context.Context, boot *kernelmodules.BootstrapContext) error {
	websitemcp.Register()
	if err := boot.RegisterRuntime("website.runtime.models", websitemodels.InitRuntimeModels); err != nil {
		return err
	}
	return boot.RegisterRuntime("website.runtime.cron", websitemodels.InitPublishCron)
}

// RegisterRoutes attaches website-owned public/admin transport endpoints.
func (module) RegisterRoutes(router fiber.Router) {
	websiteroutes.RegisterRoutes(router)
}

// SkipInstallGuard opts out of kernelmodules.RegisterHTTPRoutes' blanket
// moduleInstallGuard: RegisterRoutes mounts a prefix-less catch-all
// (router.Use(handleWebsiteRuntimeDocument)) that must fall through to the
// SPA/static handlers registered afterward when website isn't installed, but
// the guard would 404 every request site-wide before that fallthrough could
// run. handleWebsiteRuntimeResolve and handleWebsiteRuntimeDocument each
// already check ModuleInstalled/queries.ModuleInstalled internally and
// c.Next() when disabled, so the guard is redundant here and actively
// harmful.
func (module) SkipInstallGuard() bool {
	return true
}
