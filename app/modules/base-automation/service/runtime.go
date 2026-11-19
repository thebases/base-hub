package service

// File overview:
// - Responsibility: owns `base-automation` module runtime wiring — registers the sys.cron bridge
//   handler and gives the automation.action model's `run` RPC method a real executor to call.
// - Key symbols: `InitRuntime`.

import (
	"context"

	"git.thebase.vn/base_hub/internal/services/cron"
	"git.thebase.vn/base_hub/internal/services/log"
	automationmodels "git.thebase.vn/base_hub/modules/base-automation/models"
)

// InitRuntime wires the automation.action model's `run` method to the real
// ExecuteAction implementation and registers the sys.cron bridge handler that
// drives automation.cron.
func InitRuntime() func() {
	automationmodels.RegisterActionExecutor(func(ctx context.Context, actionID int64, payload map[string]any) (map[string]any, error) {
		return ExecuteAction(ctx, actionID, payload)
	})

	if err := cron.RegisterHandler(dispatchHandlerKey, dispatchDueAutomationCrons); err != nil {
		log.Fatalf("base-automation: register cron handler: %v", err)
	}

	return func() {
		automationmodels.RegisterActionExecutor(nil)
		cron.UnregisterHandler(dispatchHandlerKey)
	}
}
