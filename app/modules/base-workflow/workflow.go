package workflow

// File overview:
// - Responsibility: provides bootstrap or aggregation glue for the `workflow` module in workflow.
// - Key symbols: `init`, `kernelmodules`, `module`.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.
// - Pattern: Follows a state-machine style flow where persisted workflow state determines whether execution starts fresh, resumes, retries, or hands work to another step.

import (
	"context"
	_ "embed"

	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	workflowmcp "git.thebase.vn/base_hub/modules/base-workflow/mcp"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
	workflowroutes "git.thebase.vn/base_hub/modules/base-workflow/routes"
	workflowservice "git.thebase.vn/base_hub/modules/base-workflow/service"
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
	// Register this module's workflow.* MCP tool group with base-mcp's
	// provider registry (see modules/base-workflow/mcp) before any runtime
	// registration below, matching the other migrated modules' convention.
	workflowmcp.Register()
	if err := boot.RegisterRuntime("workflow.runtime.models", workflowmodels.InitRuntimeModels); err != nil {
		return err
	}
	if err := boot.RegisterRuntime("workflow.runtime.retention", workflowmodels.InitRetentionRuntime); err != nil {
		return err
	}
	return boot.RegisterRuntime("workflow.runtime.service", workflowservice.InitRuntime)
}

// RegisterRoutes attaches workflow-owned HTTP endpoints to the shared Fiber app.
func (module) RegisterRoutes(router fiber.Router) {
	workflowroutes.RegisterRoutes(router)
}
