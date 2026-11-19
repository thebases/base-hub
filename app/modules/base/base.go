package base

// File overview:
// - Responsibility: provides bootstrap or aggregation glue for the `base` module in base.
// - Key symbols: `init`, `kernelmodules`, `module`.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"context"
	_ "embed"

	"git.thebase.vn/base_hub/internal/i18n"
	coremodels "git.thebase.vn/base_hub/internal/models"
	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	basemcp "git.thebase.vn/base_hub/modules/base/mcp"
	basemodels "git.thebase.vn/base_hub/modules/base/models"
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
	// Register this module's base.* MCP tool group (generic model/record
	// introspection and CRUD, plus the custom-model-definition wrapper tools)
	// with base-mcp's provider registry -- see modules/base/mcp. This is the
	// last of the mcp-module-scaffold plan's Phase 1-5 migrations: after this,
	// modules/base-mcp/routes registers no tools of its own.
	basemcp.Register()
	// Keep core metadata models and base business models in separate runtime
	// registries so the module owns the business-model move without disturbing
	// the remaining kernel package layout.
	if err := boot.RegisterRuntime("base.runtime.core", coremodels.InitRuntimeModels); err != nil {
		return err
	}
	// Load .po / .pot translation files from all module i18n/ directories into
	// the global registry so T(lang, msgid) is available to every request goroutine
	// before the first RPC is served. The embedded FS is set in main.go's init()
	// before BootstrapAll runs, so EmbeddedFS() is always non-nil here in production.
	if err := boot.RegisterRuntime("base.runtime.i18n", func() func() {
		if fsys := kernelmodules.ModuleTreeFS(); fsys != nil {
			_ = i18n.LoadFromFS(fsys)
		}
		return func() {}
	}); err != nil {
		return err
	}
	return boot.RegisterRuntime("base.runtime.models", basemodels.InitRuntimeModels)
}
