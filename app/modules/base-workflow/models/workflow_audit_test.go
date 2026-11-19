package models

// File overview:
// - Responsibility: plan T45 trigger config `audit_payload`: kept on every transport, validated,
//   and resolved to the transport default when unset.

import (
	"context"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestNormalizeWorkflowTriggerValuesKeepsAuditPayload(t *testing.T) {
	for _, triggerType := range []string{WorkflowTriggerTypeWebhook, WorkflowTriggerTypeKafka, WorkflowTriggerTypeMQTT} {
		values, err := normalizeWorkflowTriggerValues(nil, orm.RecordMap{
			"workflow_id":  int64(7),
			"trigger_type": triggerType,
			"topic":        "orders.created",
			"config":       map[string]any{TriggerConfigAuditPayload: " HASH "},
		}, nil, true)
		if err != nil {
			t.Fatalf("%s: normalize error = %v", triggerType, err)
		}
		config := values["config"].(map[string]any)
		if config[TriggerConfigAuditPayload] != WorkflowAuditPayloadHash {
			t.Fatalf("%s: config = %#v, want audit_payload hash", triggerType, config)
		}
	}

	values, err := normalizeWorkflowTriggerValues(nil, orm.RecordMap{
		"workflow_id": int64(7), "trigger_type": WorkflowTriggerTypeKafka, "topic": "t",
		"config": map[string]any{TriggerConfigAuditPayload: "  "},
	}, nil, true)
	if err != nil {
		t.Fatalf("blank audit_payload: %v", err)
	}
	if _, ok := values["config"].(map[string]any)[TriggerConfigAuditPayload]; ok {
		t.Fatalf("blank audit_payload must be dropped, got %#v", values["config"])
	}

	_, err = normalizeWorkflowTriggerValues(nil, orm.RecordMap{
		"workflow_id": int64(7), "trigger_type": WorkflowTriggerTypeWebhook, "topic": "t",
		"config": map[string]any{TriggerConfigAuditPayload: "everything"},
	}, nil, true)
	if err == nil {
		t.Fatalf("invalid audit_payload accepted")
	}
}

func TestWorkflowAuditModeDefaults(t *testing.T) {
	if got := WorkflowAuditModeFromTriggerConfig(WorkflowTriggerTypeTCP, nil); got != WorkflowAuditPayloadHash {
		t.Fatalf("tcp default = %q, want hash", got)
	}
	if got := WorkflowAuditModeFromTriggerConfig(WorkflowTriggerTypeMQTT, map[string]any{}); got != WorkflowAuditPayloadFull {
		t.Fatalf("mqtt default = %q, want full", got)
	}
	if got := WorkflowAuditModeFromTriggerConfig(WorkflowTriggerTypeTCP, map[string]any{TriggerConfigAuditPayload: "full"}); got != WorkflowAuditPayloadFull {
		t.Fatalf("explicit tcp mode = %q, want full", got)
	}
	ctx := ContextWithWorkflowAuditMode(context.Background(), "bogus")
	if got := WorkflowAuditModeFromContext(ctx); got != "" {
		t.Fatalf("invalid mode bound to ctx: %q", got)
	}
}
