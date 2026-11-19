package auth

// File overview:
// - Responsibility: provides bootstrap/aggregation glue for the `auth` module, mirroring
//   modules/base-mcp's module-registration pattern (this module also exposes HTTP transport).
// - Key symbols: `init`, `module`.

import (
	"context"
	_ "embed"

	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	authmcp "git.thebase.vn/base_hub/modules/base-auth/mcp"
	authmodels "git.thebase.vn/base_hub/modules/base-auth/models"
	authroutes "git.thebase.vn/base_hub/modules/base-auth/routes"
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

// Bootstrap registers this module's models (sys.oauth_client, sys.oauth_refresh_token).
func (module) Bootstrap(_ context.Context, boot *kernelmodules.BootstrapContext) error {
	authmcp.Register()
	return boot.RegisterRuntime("auth.runtime.models", authmodels.InitRuntimeModels)
}

// RegisterRoutes mounts the OAuth token, revoke, profile, and browser authorization endpoints.
func (module) RegisterRoutes(router fiber.Router) {
	authroutes.RegisterRoutes(router)
}
