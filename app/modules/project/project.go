package project

// File overview:
// - Responsibility: provides bootstrap/aggregation glue for the `project` module — registers its
//   runtime models and MCP tool catalog during boot.
// - Key symbols: `init`, `module`.

import (
	"context"
	_ "embed"

	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/orm"
	projectmcp "git.thebase.vn/base_hub/modules/project/mcp"
	projectmodels "git.thebase.vn/base_hub/modules/project/models"
)

type module struct{}

//go:embed manifest.md
var manifestDescription string

// init registers this package runtime during bootstrap so its models, hooks, or commands are available before requests arrive.
func init() {
	// tbl_project_task.date_end was renamed date_done; declared here (not in the kernel) so a
	// pre-rename database keeps its data when the schema is reconciled.
	orm.RegisterLegacyColumnRename("tbl_project_task", "date_end", "date_done")
	// Register the capability here so later runtime code can discover and reuse it.
	kernelmodules.Register(module{})
}

// Manifest handles the manifest handled by this package.
func (module) Manifest() kernelmodules.Manifest {
	return kernelmodules.MustManifestFromMarkdown(manifestDescription)
}

// Bootstrap registers this module's MCP tool catalog and its runtime models.
func (module) Bootstrap(_ context.Context, boot *kernelmodules.BootstrapContext) error {
	projectmcp.Register()
	return boot.RegisterRuntime("project.runtime.models", projectmodels.InitRuntimeModels)
}
