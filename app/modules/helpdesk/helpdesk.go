package helpdesk

// File overview:
// - Responsibility: provides bootstrap/aggregation glue for the `helpdesk`
//   module — registers its runtime models during boot, mirroring
//   base-calendar/base-credential's module-root bootstrap shape.
// - Key symbols: `init`, `module`.

import (
	"context"
	_ "embed"

	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	helpdeskmodels "git.thebase.vn/base_hub/modules/helpdesk/models"
)

type module struct{}

//go:embed manifest.md
var manifestDescription string

// init registers this package runtime during bootstrap so its models, hooks, or commands are available before requests arrive.
func init() {
	// Register the capability here so later runtime code can discover and reuse it.
	kernelmodules.Register(module{})
}

// Manifest handles the manifest handled by this package.
func (module) Manifest() kernelmodules.Manifest {
	return kernelmodules.MustManifestFromMarkdown(manifestDescription)
}

// Bootstrap registers this module's runtime models (tickets, teams, stages,
// tags, ticket types/categories/channels, SLA, ratings, activity links) plus
// its periodic jobs (SLA sweep, auto-close, close-inactive), registered as
// base-automation go_handlers rather than a direct kernel sys.cron binding
// (see models/cron.go). No MCP tools, no custom Fiber routes/handlers —
// generic model-registry CRUD/RPC plus a handful of RuntimeMethods
// (post_ticket_message, perform_action, complete_source_activity) cover this
// module's surface.
func (module) Bootstrap(_ context.Context, boot *kernelmodules.BootstrapContext) error {
	if err := boot.RegisterRuntime("helpdesk.runtime.models", helpdeskmodels.InitRuntimeModels); err != nil {
		return err
	}
	return boot.RegisterRuntime("helpdesk.runtime.cron", helpdeskmodels.InitCron)
}
