package engine

import (
	"context"
	"errors"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

// Every trigger delivery loads its flow through loadActiveFlowByID, so a template (plan T101) is
// never started by a trigger, even when someone has activated it.
func TestTriggerDeliveryRefusesTemplate(t *testing.T) {
	row := orm.RecordMap{"id": int64(5), "name": "demo", "topic": "demo", "version": int64(1), "is_active": true, "is_template": true}
	previous := readTriggerFlowRows
	readTriggerFlowRows = func(context.Context, int64) ([]orm.RecordMap, error) {
		return []orm.RecordMap{cloneRecordMap(row)}, nil
	}
	t.Cleanup(func() { readTriggerFlowRows = previous })

	if _, err := loadActiveFlowByID(context.Background(), 5); !errors.Is(err, ErrFlowNotFound) {
		t.Fatalf("template flow: err = %v, want ErrFlowNotFound", err)
	}
	row["is_template"] = false
	if flow, err := loadActiveFlowByID(context.Background(), 5); err != nil || flow.ID != 5 {
		t.Fatalf("normal flow: flow=%v err=%v", flow, err)
	}
}
