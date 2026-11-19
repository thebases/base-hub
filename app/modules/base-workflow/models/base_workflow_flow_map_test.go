package models

import (
	"context"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestWriteWorkflowFlowMapReadsOnlyScalarWorkflowFields(t *testing.T) {
	var readFields []string

	restoreFlow := orm.StubRuntimeRepository(FlowModelName, orm.RuntimeRepositoryStub{
		Read: func(_ context.Context, ids []string, fields []string) ([]orm.RecordMap, error) {
			readFields = append([]string(nil), fields...)
			if len(ids) != 1 || ids[0] != "11" {
				t.Fatalf("flow ids = %#v, want [11]", ids)
			}
			return []orm.RecordMap{{
				"id":      int64(11),
				"name":    "demo_workflow",
				"topic":   "wallet",
				"version": int64(1),
			}}, nil
		},
		Write: func(_ context.Context, ids []string, values orm.RecordMap) (bool, error) {
			if len(ids) != 1 || ids[0] != "11" {
				t.Fatalf("flow write ids = %#v, want [11]", ids)
			}
			if stringValue(values["flow_map"]) == "" {
				t.Fatalf("expected generated flow_map, got empty value")
			}
			return true, nil
		},
	})
	defer restoreFlow()

	restoreAction := orm.StubRuntimeRepository(WorkflowActionModelName, orm.RuntimeRepositoryStub{
		SearchRead: func(_ context.Context, filter orm.RecordMap, fields []string) ([]orm.RecordMap, error) {
			if int64Value(filter["workflow_id"]) != 11 {
				t.Fatalf("workflow_id filter = %#v, want 11", filter["workflow_id"])
			}
			_ = fields
			return []orm.RecordMap{{
				"id":             int64(21),
				"name":           "Parse Payload",
				"sequence":       int64(10),
				"action_code":    "parse_payload",
				"action_version": int64(1),
				"step_key":       "parse_payload",
				"is_active":      true,
			}}, nil
		},
	})
	defer restoreAction()

	if err := writeWorkflowFlowMap(context.Background(), 11); err != nil {
		t.Fatalf("writeWorkflowFlowMap() error = %v", err)
	}

	expected := []string{"id", "name", "topic", "version"}
	if len(readFields) != len(expected) {
		t.Fatalf("read fields = %#v, want %#v", readFields, expected)
	}
	for index := range expected {
		if readFields[index] != expected[index] {
			t.Fatalf("read fields = %#v, want %#v", readFields, expected)
		}
	}
}
