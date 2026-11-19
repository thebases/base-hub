package projecthelpdesk

// File overview:
// - Responsibility: provides bootstrap/aggregation glue for the
//   `project-helpdesk` bridge module — registers its manifest and its two
//   Fiber conversion endpoints. No runtime models of its own (it only ever
//   touches tbl.helpdesk_ticket/tbl.project_task by name, never a Go import
//   of either module).
// - Key symbols: `init`, `module`.

import (
	"context"
	_ "embed"

	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	projecthelpdeskhandlers "git.thebase.vn/base_hub/modules/project-helpdesk/handlers"
	"github.com/gofiber/fiber/v3"
)

type module struct{}

//go:embed manifest.md
var manifestDescription string

// init registers this package runtime during bootstrap so its manifest/routes are available before requests arrive.
func init() {
	kernelmodules.Register(module{})
}

// Manifest handles the manifest handled by this package.
func (module) Manifest() kernelmodules.Manifest {
	return kernelmodules.MustManifestFromMarkdown(manifestDescription)
}

// Bootstrap has nothing to register — this bridge owns no runtime models,
// only the two Fiber conversion endpoints below.
func (module) Bootstrap(_ context.Context, _ *kernelmodules.BootstrapContext) error {
	return nil
}

// RegisterRoutes satisfies kernelmodules.HTTPRouteRegistrar — wrapped in
// moduleInstallGuard by RegisterHTTPRoutes, so these endpoints 404 unless
// this bridge module is itself installed (which, per its auto_install
// manifest, only happens once both `project` and `helpdesk` are installed).
func (module) RegisterRoutes(router fiber.Router) {
	router.Post("/project-helpdesk/tickets/:id/convert-to-task", projecthelpdeskhandlers.ConvertTicketToTask)
	router.Post("/project-helpdesk/tasks/:id/convert-to-ticket", projecthelpdeskhandlers.ConvertTaskToTicket)
}
