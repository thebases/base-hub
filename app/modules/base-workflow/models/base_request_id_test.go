package models

// File overview:
// - Responsibility: verifies workflow model state, metadata, and persistence behavior for base request id.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down workflow model semantics so request IDs, run state, and action definitions remain replay-safe and observable.

import (
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
	"github.com/google/uuid"
)

func TestNormalizeOutboundDeliveryValuesGeneratesRequestIDWhenMissing(t *testing.T) {
	values, err := normalizeOutboundDeliveryValues(orm.RecordMap{
		"delivery_type": "http_json",
		"target_ref":    "https://example.com/callback",
	}, nil, true)
	if err != nil {
		t.Fatalf("normalizeOutboundDeliveryValues() error = %v", err)
	}

	requestID, _ := values["name"].(string)
	if _, err := uuid.Parse(requestID); err != nil {
		t.Fatalf("name = %#v, want uuid4: %v", values["name"], err)
	}
}

func TestNormalizeFlowValuesGeneratesNameWhenMissing(t *testing.T) {
	values, err := normalizeFlowValues(orm.RecordMap{
		"topic": "topic.alpha",
	}, nil, true)
	if err != nil {
		t.Fatalf("normalizeFlowValues() error = %v", err)
	}

	name, _ := values["name"].(string)
	if !strings.HasPrefix(name, "workflow-") {
		t.Fatalf("name = %#v, want workflow-*", values["name"])
	}
}

func TestNormalizeWorkflowRunValuesGeneratesRequestIDWhenMissing(t *testing.T) {
	values, err := normalizeWorkflowRunValues(orm.RecordMap{
		"status": "running",
	}, nil, true)
	if err != nil {
		t.Fatalf("normalizeWorkflowRunValues() error = %v", err)
	}

	requestID, _ := values["name"].(string)
	if _, err := uuid.Parse(requestID); err != nil {
		t.Fatalf("name = %#v, want uuid4: %v", values["name"], err)
	}
}

func TestNormalizeWorkflowRunValuesUsesExplicitRequestIDOnCreate(t *testing.T) {
	const explicit = "workflow-run-1762142400000-00000000-0000-4000-8000-000000000001"
	values, err := normalizeWorkflowRunValues(orm.RecordMap{
		"request_id": explicit,
		"status":     "running",
	}, nil, true)
	if err != nil {
		t.Fatalf("normalizeWorkflowRunValues() error = %v", err)
	}
	got, _ := values["name"].(string)
	if got != explicit {
		t.Fatalf("name = %#v, want explicit request id", got)
	}
}

func TestNormalizeWorkflowRunValuesUsesWorkflowRequestIDWhenPresent(t *testing.T) {
	const workflowRequestID = "1fd56a49-08fa-4e77-8a7f-eb41544f8bc2"
	values, err := normalizeWorkflowRunValues(orm.RecordMap{
		"workflow_request_id": workflowRequestID,
		"status":              "running",
	}, nil, true)
	if err != nil {
		t.Fatalf("normalizeWorkflowRunValues() error = %v", err)
	}
	if got, _ := values["name"].(string); got != workflowRequestID {
		t.Fatalf("name = %#v, want workflow request id", got)
	}
}

func TestNormalizeOutboundDeliveryGeneratesRequestIDEvenWithWorkflowRunRequestID(t *testing.T) {
	const wfRequestID = "workflow-run-1762142400000-00000000-0000-4000-8000-000000000001"
	values, err := normalizeOutboundDeliveryValues(orm.RecordMap{
		"delivery_type":           "http_json",
		"target_ref":              "https://example.com/callback",
		"workflow_run_request_id": wfRequestID,
	}, nil, true)
	if err != nil {
		t.Fatalf("normalizeOutboundDeliveryValues() error = %v", err)
	}

	requestID, _ := values["name"].(string)
	if _, err := uuid.Parse(requestID); err != nil {
		t.Fatalf("name = %#v, want uuid4: %v", values["name"], err)
	}
	if requestID == wfRequestID {
		t.Fatalf("name = %#v, want generated value distinct from workflow run request_id", requestID)
	}
}

func TestNormalizeOutboundDeliveryUsesWorkflowRequestIDWhenPresent(t *testing.T) {
	const workflowRequestID = "17e7257d-7800-4f2c-8202-f8a3b25fe4e8"
	values, err := normalizeOutboundDeliveryValues(orm.RecordMap{
		"delivery_type":       "http_json",
		"target_ref":          "https://example.com/callback",
		"workflow_request_id": workflowRequestID,
	}, nil, true)
	if err != nil {
		t.Fatalf("normalizeOutboundDeliveryValues() error = %v", err)
	}
	if got, _ := values["name"].(string); got != workflowRequestID {
		t.Fatalf("name = %#v, want workflow request id", got)
	}
}

func TestNormalizeOutboundDeliveryUsesExplicitNameOnCreate(t *testing.T) {
	const explicit = "outbound-delivery-explicit"
	values, err := normalizeOutboundDeliveryValues(orm.RecordMap{
		"delivery_type":           "http_json",
		"target_ref":              "https://example.com/callback",
		"name":                    explicit,
		"workflow_run_request_id": "workflow-run-should-be-ignored",
	}, nil, true)
	if err != nil {
		t.Fatalf("normalizeOutboundDeliveryValues() error = %v", err)
	}
	got, _ := values["name"].(string)
	if got != explicit {
		t.Fatalf("name = %#v, want explicit request id", got)
	}
}
