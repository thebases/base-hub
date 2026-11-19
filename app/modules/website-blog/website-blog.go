package websiteblog

// File overview:
// - Responsibility: descriptor for the `website-blog` module — registers its runtime models and
//   the public /blog routes on top of the `website` module.
// - Key symbols: `module`, `init`.

import (
	"context"
	_ "embed"

	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	blogmcp "git.thebase.vn/base_hub/modules/website-blog/mcp"
	blogmodels "git.thebase.vn/base_hub/modules/website-blog/models"
	blogroutes "git.thebase.vn/base_hub/modules/website-blog/routes"
	"github.com/gofiber/fiber/v3"
)

type module struct{}

//go:embed manifest.md
var manifestDescription string

func init() {
	kernelmodules.Register(module{})
}

// Manifest returns the embedded module manifest.
func (module) Manifest() kernelmodules.Manifest {
	return kernelmodules.MustManifestFromMarkdown(manifestDescription)
}

// Bootstrap registers the blog MCP tool group and runtime models.
func (module) Bootstrap(_ context.Context, boot *kernelmodules.BootstrapContext) error {
	blogmcp.Register()
	if err := blogroutes.RegisterDataSources(); err != nil {
		return err
	}
	return boot.RegisterRuntime("website-blog.runtime.models", blogmodels.InitRuntimeModels)
}

// RegisterRoutes mounts the public /blog pages (install-guarded by the kernel, so they 404 until
// the module is installed on the tenant).
func (module) RegisterRoutes(router fiber.Router) {
	blogroutes.RegisterRoutes(router)
}
