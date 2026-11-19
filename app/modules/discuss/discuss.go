package discuss

// File overview:
// - Responsibility: provides bootstrap/aggregation glue for the `discuss`
//   module — registers its runtime models during boot, mirroring
//   helpdesk/base-calendar/base-credential's module-root bootstrap shape.
// - Key symbols: `init`, `module`.

import (
	"context"
	_ "embed"

	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	discussmodels "git.thebase.vn/base_hub/modules/discuss/models"
	discussroutes "git.thebase.vn/base_hub/modules/discuss/routes"
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

// Bootstrap registers this module's runtime models (tbl.discuss_message,
// tbl.discuss_message_subtype, tbl.discuss_followers, tbl.discuss_activity_type,
// tbl.discuss_activity, tbl.discuss_canned_response, tbl.discuss_alias,
// tbl.discuss_notification, tbl.discuss_channel, tbl.discuss_channel_member,
// tbl.discuss_mail_server, tbl.discuss_mail). Generic model-registry CRUD/RPC covers
// every model, and PostMessage/Subscribe/Unsubscribe (models/service.go),
// the channel helpers (models/channel_service.go), EnqueueMail/
// SendQueuedMail (models/mail_sender.go), and RouteInboundEmail (models/
// inbound_router.go) cover the business logic that isn't plain CRUD. The
// second RegisterRuntime call also registers SendQueuedMail as a sys.cron
// handler (models/mail_sender.go's InitMailCron) so outbound email gets
// swept periodically once a real scheduler dispatches a matching sys.cron
// row. RegisterRoutes below mounts the one custom Fiber endpoint this module
// needs: the inbound-email webhook (an external mail relay posts to it, so
// it can't go through the generic model-registry RPC surface).
func (module) Bootstrap(_ context.Context, boot *kernelmodules.BootstrapContext) error {
	if err := boot.RegisterRuntime("discuss.runtime.models", discussmodels.InitRuntimeModels); err != nil {
		return err
	}
	return boot.RegisterRuntime("discuss.runtime.mail_cron", discussmodels.InitMailCron)
}

// RegisterRoutes attaches the inbound-email webhook to the shared Fiber app.
func (module) RegisterRoutes(router fiber.Router) {
	discussroutes.RegisterRoutes(router)
}
