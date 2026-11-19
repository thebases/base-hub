package engine

// File overview:
// - Responsibility: publishes best-effort live step-status events for a workflow run onto
//   the shared service bus so the canvas can update without waiting on its poll interval.
// - Pattern: publish failures are logged and swallowed — the persisted `step_runs[]` write via
//   markRun remains the sole source of truth; the event stream is a UI convenience only.

import (
	"context"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	coremodels "git.thebase.vn/base_hub/modules/base-bus/models"
)

// WorkflowRunStepEventType is the service-bus event type published on every
// step-status transition of a workflow run.
const WorkflowRunStepEventType = "workflow.run.step"

// workflowRunChannel scopes step-status events to the workflow's own topic so
// canvases only receive events for the workflow they have open.
func workflowRunChannel(topic string) string {
	return fmt.Sprintf("workflow:run:%s", strings.TrimSpace(topic))
}

// publishWorkflowRunStepEvent emits a lightweight {run_id, step_key, status}
// event. It is fire-and-forget: a publish error is logged but never returned,
// so it can never fail or roll back the run itself.
func publishWorkflowRunStepEvent(ctx context.Context, topic string, runID int64, stepKey, status string) {
	topic = strings.TrimSpace(topic)
	if topic == "" || status == "" {
		return
	}
	payload := map[string]any{
		"run_id":   runID,
		"step_key": stepKey,
		"status":   status,
	}
	_, err := orm.ExecuteRuntimeMethod(
		ctx,
		coremodels.ServiceBusModelName,
		"publish",
		nil,
		[]any{[]string{workflowRunChannel(topic)}, WorkflowRunStepEventType, payload},
		nil,
	)
	if err != nil {
		log.Errorf("workflow: publish run step event for run %d: %v", runID, err)
	}
}
