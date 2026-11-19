package bus

// File overview:
// - Responsibility: provides bootstrap and route-registration glue for the `base-bus` module.
// - Key symbols: `init`, `module`.
// - Pattern: Mirrors base-automation/base-workflow's module descriptor shape so bootstrap ordering and route ownership stay predictable.

import (
	"context"
	_ "embed"

	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	busmcp "git.thebase.vn/base_hub/modules/base-bus/mcp"
	busmodels "git.thebase.vn/base_hub/modules/base-bus/models"
	busroutes "git.thebase.vn/base_hub/modules/base-bus/routes"
	"github.com/gofiber/fiber/v3"
)

type module struct{}

//go:embed manifest.md
var manifestDescription string

func init() {
	kernelmodules.Register(module{})
}

func (module) Manifest() kernelmodules.Manifest {
	return kernelmodules.MustManifestFromMarkdown(manifestDescription)
}

func (module) Bootstrap(_ context.Context, boot *kernelmodules.BootstrapContext) error {
	busmcp.Register()
	return boot.RegisterRuntime("base-bus.runtime.models", busmodels.InitRuntimeModels)
}

// RegisterRoutes attaches the service-bus transport endpoints to the shared Fiber app.
func (module) RegisterRoutes(router fiber.Router) {
	busroutes.RegisterRoutes(router)
}
