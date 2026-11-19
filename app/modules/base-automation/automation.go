package automation

// File overview:
// - Responsibility: provides bootstrap or aggregation glue for the `base-automation` module.
// - Key symbols: `init`, `module`.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"context"
	_ "embed"

	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	automationmcp "git.thebase.vn/base_hub/modules/base-automation/mcp"
	automationmodels "git.thebase.vn/base_hub/modules/base-automation/models"
	automationservice "git.thebase.vn/base_hub/modules/base-automation/service"
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
	automationmcp.Register()
	if err := boot.RegisterRuntime("base-automation.runtime.models", automationmodels.InitRuntimeModels); err != nil {
		return err
	}
	return boot.RegisterRuntime("base-automation.runtime.service", automationservice.InitRuntime)
}
