package engine

// File overview:
// - Responsibility: claims due workflow runs, reaps stale running runs and computes retry times.
// - Key symbols: `claimDueWorkflowRuns`, `reapStaleWorkflowRuns`.

import (
	"context"
	"fmt"

	"git.thebase.vn/base_hub/internal/modelruntime"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

func (s *Service) nextRetryAt(step workflowmodels.Step) int64 {
	backoff := step.ResolvedBackoff()
	if backoff <= 0 {
		return 0
	}
	return s.now().Add(backoff).Unix()
}

// claimDueWorkflowRuns claims the due workflow runs handled by this package.
func claimDueWorkflowRuns(ctx context.Context, limit int64, beforeTs int64) ([]int64, error) {
	result, err := modelruntime.NewService().Execute(ctx, modelruntime.Request{
		Model:  workflowmodels.WorkflowRunModelName,
		Method: "claim_due_retries",
		Kwargs: map[string]any{
			"limit":     limit,
			"before_ts": beforeTs,
			// Claimed runs are leased to this batch's token (plan T49).
			"lease_owner":   createLeaseOwner(ctx),
			"lease_seconds": runLeaseSeconds,
		},
	})
	if err != nil {
		log.Errorf("workflow: claim due workflow runs: %v", err)
		return nil, fmt.Errorf("workflow: claim due workflow runs: %w", err)
	}

	rows, ok := result.([]orm.RecordMap)
	if !ok {
		log.Errorf("workflow: claim due workflow runs returned %T", result)
		return nil, fmt.Errorf("workflow: claim due workflow runs returned %T", result)
	}

	return runRowIDs(rows), nil
}

// reapStaleWorkflowRuns reaps the stale workflow runs handled by this package.
func reapStaleWorkflowRuns(ctx context.Context, limit int64, staleAfterSeconds int64) ([]int64, error) {
	result, err := modelruntime.NewService().Execute(ctx, modelruntime.Request{
		Model:  workflowmodels.WorkflowRunModelName,
		Method: "reap_stale_running",
		Kwargs: map[string]any{
			"limit":               limit,
			"stale_after_seconds": staleAfterSeconds,
		},
	})
	if err != nil {
		log.Errorf("workflow: reap stale workflow runs: %v", err)
		return nil, fmt.Errorf("workflow: reap stale workflow runs: %w", err)
	}

	rows, ok := result.([]orm.RecordMap)
	if !ok {
		log.Errorf("workflow: reap stale workflow runs returned %T", result)
		return nil, fmt.Errorf("workflow: reap stale workflow runs returned %T", result)
	}

	return runRowIDs(rows), nil
}

// runRowIDs extracts the run ids from claimed rows, skipping ids of an unsupported type or form.
func runRowIDs(rows []orm.RecordMap) []int64 {
	runIDs := make([]int64, 0, len(rows))
	for _, row := range rows {
		switch typed := row["id"].(type) {
		case int64:
			runIDs = append(runIDs, typed)
		case int:
			runIDs = append(runIDs, int64(typed))
		case float64:
			runIDs = append(runIDs, int64(typed))
		case string:
			if parsed, ok := int64FromString(typed); ok {
				runIDs = append(runIDs, parsed)
			}
		}
	}
	return runIDs
}
