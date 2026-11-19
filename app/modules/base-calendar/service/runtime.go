package service

// File overview:
// - Responsibility: owns `base-calendar` module runtime wiring — registers the orphaned-recurrence
//   cleanup job as a base-automation go_handler, mirroring modules/base-credential/service/runtime.go.
// - Key symbols: `InitRuntime`.

import (
	"git.thebase.vn/base_hub/internal/services/log"
	automationservice "git.thebase.vn/base_hub/modules/base-automation/service"
)

// InitRuntime registers the orphaned-recurrence cleanup job as a
// base-automation go_handler, dispatched by base-automation's own sys.cron
// bridge via the seeded automation.action/automation.cron rows
// (data/01_automation_action.json, data/02_automation_cron.json) instead of a
// base-calendar-owned direct sys.cron registration.
func InitRuntime() func() {
	if err := automationservice.RegisterGoHandler(cleanupOrphanedRecurrencesHandlerKey, cleanupOrphanedRecurrences); err != nil {
		log.Fatalf("base-calendar: register go handler: %v", err)
	}

	return func() {
		automationservice.UnregisterGoHandler(cleanupOrphanedRecurrencesHandlerKey)
	}
}
