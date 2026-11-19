package models

import (
	"context"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func templateFixture(t *testing.T) *workflowFixture {
	t.Helper()
	if _, err := orm.RegisterModelType[FlowRecord](FlowModelSpec); err != nil {
		t.Fatalf("register base.workflow: %v", err)
	}
	fx := newWorkflowFixture(t, 0)
	fx.flows.rows[11]["is_template"] = true
	fx.flows.rows[11]["is_active"] = false
	fx.flows.rows[11]["description"] = "Import orders"
	fx.triggers.rows[51] = orm.RecordMap{"id": int64(51), "workflow_id": int64(11), "trigger_type": "kafka", "topic": "orders", "is_active": true}
	return fx
}

func TestInstantiateTemplateCreatesInactiveNonTemplateCopy(t *testing.T) {
	fx := templateFixture(t)

	result, err := instantiateWorkflowTemplate(context.Background(), nil, []string{"11"}, nil, map[string]any{"name": "My Orders"})
	if err != nil {
		t.Fatalf("instantiate_template: %v", err)
	}
	newID := int64Value(result.(orm.RecordMap)["id"])
	copied := fx.flows.rows[newID]
	if newID == 11 || boolValue(copied["is_template"]) || boolValue(copied["is_active"]) || copied["name"] != "My Orders" {
		t.Fatalf("copy = %#v, want an inactive non-template named My Orders", copied)
	}
	if got := len(fx.actions.where("workflow_id", newID)); got != 3 {
		t.Fatalf("copied steps = %d, want 3", got)
	}
	for _, trigger := range fx.triggers.where("workflow_id", newID) {
		if boolValue(trigger["is_active"]) {
			t.Fatalf("copied trigger %v is active", trigger)
		}
	}
	if !boolValue(fx.flows.rows[11]["is_template"]) {
		t.Fatal("the template itself changed")
	}
}

func TestInstantiateTemplateRefusesNonTemplate(t *testing.T) {
	fx := templateFixture(t)
	fx.flows.rows[11]["is_template"] = false
	if _, err := instantiateWorkflowTemplate(context.Background(), nil, []string{"11"}, nil, nil); errorCode(err) != orm.ErrInvalidParams {
		t.Fatalf("err = %v, want InvalidParams", err)
	}
	if len(fx.flows.rows) != 1 {
		t.Fatalf("a workflow was created: %d rows", len(fx.flows.rows))
	}
}

func TestListTemplates(t *testing.T) {
	fx := templateFixture(t)
	fx.flows.rows[12] = orm.RecordMap{"id": int64(12), "name": "Live", "topic": "live", "is_template": false}

	result, err := listWorkflowTemplates(context.Background(), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("list_templates: %v", err)
	}
	got := result.([]WorkflowTemplateSummary)
	if len(got) != 1 {
		t.Fatalf("templates = %#v, want only the template", got)
	}
	want := WorkflowTemplateSummary{ID: 11, Name: "Order Import", Description: "Import orders", TriggerTypes: []string{"kafka"}, StepCount: 3}
	if got[0].ID != want.ID || got[0].Name != want.Name || got[0].Description != want.Description || got[0].StepCount != want.StepCount || len(got[0].TriggerTypes) != 1 || got[0].TriggerTypes[0] != "kafka" {
		t.Fatalf("summary = %#v, want %#v", got[0], want)
	}
}

func TestTemplateMethodsRegistered(t *testing.T) {
	methods := map[string]orm.RuntimeMethod{}
	for _, method := range FlowModelSpec.RuntimeMethods {
		methods[method.Name] = method
	}
	if list, ok := methods["list_templates"]; !ok || !list.ReadOnly || !list.ModelOnly {
		t.Fatalf("list_templates = %#v", list)
	}
	if inst, ok := methods["instantiate_template"]; !ok || inst.ReadOnly || !inst.UsesPathID {
		t.Fatalf("instantiate_template = %#v", inst)
	}
}
