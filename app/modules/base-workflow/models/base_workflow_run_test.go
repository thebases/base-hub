package models

// File overview:
// - Responsibility: verifies workflow model state, metadata, and persistence behavior for base workflow run.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down workflow model semantics so request IDs, run state, and action definitions remain replay-safe and observable.

import (
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestNormalizeWorkflowRunValuesNormalizesWorkflowID(t *testing.T) {
	values, err := normalizeWorkflowRunValues(orm.RecordMap{
		"workflow_id": int64(42),
		"status":      "running",
	}, nil, true)
	if err != nil {
		t.Fatalf("normalizeWorkflowRunValues() error = %v", err)
	}

	workflowID, ok := values["workflow_id"].(*int64)
	if !ok || workflowID == nil || *workflowID != 42 {
		t.Fatalf("workflow_id = %#v, want *int64(42)", values["workflow_id"])
	}
}

func TestNormalizeWorkflowRunValuesRejectsInvalidWorkflowID(t *testing.T) {
	_, err := normalizeWorkflowRunValues(orm.RecordMap{
		"workflow_id": false,
		"status":      "running",
	}, nil, true)
	if err == nil {
		t.Fatalf("expected invalid workflow_id to fail")
	}
}

func TestNormalizeWorkflowRunValuesRequiresNextRetryAtForWaitingRetry(t *testing.T) {
	_, err := normalizeWorkflowRunValues(orm.RecordMap{
		"status": "waiting_retry",
	}, nil, true)
	if err == nil {
		t.Fatalf("expected waiting_retry without next_retry_at to fail")
	}
}

func TestNormalizeWorkflowRunValuesNormalizesStepRuns(t *testing.T) {
	values, err := normalizeWorkflowRunValues(orm.RecordMap{
		"status": "running",
		"step_runs": []any{
			map[string]any{
				"step_name": "Parse Payload",
				"action":    "parse_payload",
				"status":    "running",
				"attempt":   1,
			},
		},
	}, nil, true)
	if err != nil {
		t.Fatalf("normalizeWorkflowRunValues() error = %v", err)
	}

	stepRuns, ok := values["step_runs"].([]WorkflowStepRun)
	if !ok || len(stepRuns) != 1 {
		t.Fatalf("step_runs = %#v, want one normalized step run", values["step_runs"])
	}
	if got := stepRuns[0].StepKey; got != "Parse Payload" {
		t.Fatalf("step_key = %#v, want Parse Payload", got)
	}
	if got := stepRuns[0].StepType; got != WorkflowStepTypeAction {
		t.Fatalf("step_type = %#v, want %s", got, WorkflowStepTypeAction)
	}
}

func TestNormalizeWorkflowRunValuesAcceptsSkippedStepRuns(t *testing.T) {
	values, err := normalizeWorkflowRunValues(orm.RecordMap{
		"status": "running",
		"step_runs": []any{
			map[string]any{
				"step_name": "Create Owner Partner",
				"action":    "crud_model",
				"status":    WorkflowRunStatusSkipped,
				"attempt":   0,
			},
		},
	}, nil, true)
	if err != nil {
		t.Fatalf("normalizeWorkflowRunValues() error = %v", err)
	}

	stepRuns, ok := values["step_runs"].([]WorkflowStepRun)
	if !ok || len(stepRuns) != 1 {
		t.Fatalf("step_runs = %#v, want one normalized step run", values["step_runs"])
	}
	if stepRuns[0].Status != WorkflowRunStatusSkipped {
		t.Fatalf("status = %#v, want %s", stepRuns[0].Status, WorkflowRunStatusSkipped)
	}
}
