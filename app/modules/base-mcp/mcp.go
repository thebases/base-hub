package mcp

// File overview:
// - Responsibility: provides bootstrap or aggregation glue for the `mcp` module in mcp.
// - Key symbols: `init`, `module`.
// - Pattern: Keeps module registration and transport ownership inside the module package so startup stays deterministic.

import (
	"context"
	_ "embed"

	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	mcproutes "git.thebase.vn/base_hub/modules/base-mcp/routes"
	"github.com/gofiber/fiber/v3"
)

type module struct{}

//go:embed manifest.md
var manifestDescription string

// init registers this package runtime during bootstrap so its routes are available before requests arrive.
func init() {
	kernelmodules.Register(module{})
	// Lets install/uninstall/reinit (internal/modules/lifecycle.go) proactively
	// refresh the module catalog without importing modules/base-mcp directly.
	kernelmodules.RegisterMCPCacheInvalidator(mcproutes.InvalidateMCPCache)
}

// Manifest returns the embedded module manifest.
func (module) Manifest() kernelmodules.Manifest {
	return kernelmodules.MustManifestFromMarkdown(manifestDescription)
}

// Bootstrap keeps the module bootstrap contract satisfied even though the
// current MCP module only exposes HTTP transport.
func (module) Bootstrap(_ context.Context, _ *kernelmodules.BootstrapContext) error {
	return nil
}

// RegisterRoutes lets the module satisfy internal/modules.HTTPRouteRegistrar
// while keeping transport code inside the dedicated routes package.
func (module) RegisterRoutes(router fiber.Router) {
	mcproutes.RegisterRoutes(router)
}
