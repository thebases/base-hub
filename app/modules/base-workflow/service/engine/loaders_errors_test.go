package engine

// File overview:
// - Responsibility: plan T51, loader sentinel errors stay matchable with errors.Is (the Kafka legacy
//   IPN fallback and the /wh no-route answer depend on it).

import (
	"context"
	"errors"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

func TestLoadRuntimeStepsWrapsActionDefinitionNotFound(t *testing.T) {
	svc := (&Service{
		loadFlowActions: func(context.Context, int64) ([]workflowmodels.WorkflowActionRecord, error) {
			return nil, nil
		},
	}).withDefaults()
	_, err := svc.loadRuntimeSteps(context.Background(), &workflowmodels.FlowRecord{BaseModel: orm.BaseModel{ID: 4}, Topic: "t", Version: 1})
	if !errors.Is(err, ErrActionDefinitionNotFound) {
		t.Fatalf("err = %v, want errors.Is ErrActionDefinitionNotFound", err)
	}
}
