package calendar

// File overview:
// - Responsibility: provides bootstrap/aggregation glue for the `base-calendar` module — registers
//   its runtime models and its base-automation go_handler wiring during boot.
// - Key symbols: `init`, `module`.

import (
	"context"
	_ "embed"

	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	calendarmcp "git.thebase.vn/base_hub/modules/base-calendar/mcp"
	calendarmodels "git.thebase.vn/base_hub/modules/base-calendar/models"
	calendarservice "git.thebase.vn/base_hub/modules/base-calendar/service"
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

// Bootstrap registers this module's runtime models and its base-automation
// go_handler registration (cleanup_cron.go), mirroring base-credential's
// dependency-on-base-automation bootstrap shape.
func (module) Bootstrap(_ context.Context, boot *kernelmodules.BootstrapContext) error {
	calendarmcp.Register()
	if err := boot.RegisterRuntime("base-calendar.runtime.models", calendarmodels.InitRuntimeModels); err != nil {
		return err
	}
	return boot.RegisterRuntime("base-calendar.runtime.service", calendarservice.InitRuntime)
}
