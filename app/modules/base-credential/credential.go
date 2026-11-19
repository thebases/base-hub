package credential

// File overview:
// - Responsibility: provides bootstrap or aggregation glue for the `credential` module.
// - Key symbols: `init`, `module`.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"context"
	_ "embed"

	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	credentialmcp "git.thebase.vn/base_hub/modules/base-credential/mcp"
	credentialmodels "git.thebase.vn/base_hub/modules/base-credential/models"
	credentialservice "git.thebase.vn/base_hub/modules/base-credential/service"
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
	credentialmcp.Register()
	if err := boot.RegisterRuntime("credential.runtime.models", credentialmodels.InitRuntimeModels); err != nil {
		return err
	}
	return boot.RegisterRuntime("credential.runtime.service", credentialservice.InitRuntime)
}
