package service

// File overview:
// - Responsibility: owns `credential` module runtime wiring — registers the token-refresh job as a
//   base-automation go_handler and gives sys.credential's `login` runtime action a real implementation
//   to call, mirroring modules/base-automation/service/runtime.go.
// - Key symbols: `InitRuntime`.

import (
	"context"

	"git.thebase.vn/base_hub/internal/services/log"
	automationservice "git.thebase.vn/base_hub/modules/base-automation/service"
	credentialmodels "git.thebase.vn/base_hub/modules/base-credential/models"
)

// InitRuntime wires sys.credential's `login` method to the real
// LoginCredential implementation and registers the refresh-before-expiry job
// as a base-automation go_handler, dispatched by base-automation's own
// sys.cron bridge via the seeded automation.action/automation.cron rows
// (data/01_automation_refresh_action.json, data/02_automation_refresh_cron.json)
// instead of a credential-owned direct sys.cron registration.
func InitRuntime() func() {
	credentialmodels.LoginHook = func(ctx context.Context, credentialID int64) error {
		return LoginCredential(ctx, credentialID)
	}

	if err := automationservice.RegisterGoHandler(refreshDueCredentialsHandlerKey, refreshDueCredentials); err != nil {
		log.Fatalf("credential: register go handler: %v", err)
	}

	return func() {
		credentialmodels.LoginHook = nil
		automationservice.UnregisterGoHandler(refreshDueCredentialsHandlerKey)
	}
}
