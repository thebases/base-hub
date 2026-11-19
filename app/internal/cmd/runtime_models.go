package cmd

// File overview:
// - Responsibility: implements command-line workflow for runtime models so operational tasks reuse the same config and bootstrap rules as the main service.
// - Key symbols: `initRuntimeModels`.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.
// - Pattern: Concentrates startup ordering here so dependency initialization, embedded assets, and shutdown hooks stay predictable during deploys and local runs.

import (
	"context"

	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	_ "git.thebase.vn/base_hub/modules/all"
)

// initRuntimeModels registers every active runtime-backed model and returns one
// cleanup callback that shuts them down in reverse order.
func initRuntimeModels() func() {
	shutdown, _, err := kernelmodules.BootstrapAll(context.Background())
	if err != nil {
		panic(err)
	}
	return shutdown
}
