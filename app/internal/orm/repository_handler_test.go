package orm

// File overview:
// - Responsibility: verifies ORM runtime semantics, repository behavior, and registry coordination for repository handler.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down runtime invariants so refactors preserve transaction, RBAC, registry, and storage behavior.

// These tests show how handlers compose registration, repository access, and
// runtime execution without exposing storage-specific details to callers.

import (
	"testing"
	"time"
)

type handlerBankDocument struct {
	BaseModel
	ProxyValue string `bun:"proxy_value" json:"proxy_value"`
}

type handlerEvent struct {
	EventPath string                 `json:"event_path"`
	EventLog  map[string]interface{} `json:"event_log"`
}

type handlerEventDocument struct {
	BaseModel
	EventPath string                 `bun:"event_path" json:"event_path"`
	EventLog  map[string]interface{} `bun:"event_log,type:jsonb,nullzero" json:"event_log"`
	EventTime int64                  `bun:"event_time" json:"event_time"`
}

func prepareHandlerEventTask(job map[string]any) (map[string]any, error) {
	if job == nil {
		return nil, nil
	}

	switch payload := job["payload"].(type) {
	case handlerEvent:
		job["payload"] = handlerEventDocument{
			EventPath: payload.EventPath,
			EventLog:  payload.EventLog,
			EventTime: time.Now().Unix(),
		}
	case *handlerEvent:
		if payload != nil {
			job["payload"] = handlerEventDocument{
				EventPath: payload.EventPath,
				EventLog:  payload.EventLog,
				EventTime: time.Now().Unix(),
			}
		}
	}

	return job, nil
}

func TestNewHandlerWithModelAppliesOptions(t *testing.T) {
	prepared := false
	handler := NewHandlerWithModel[handlerBankDocument](
		ModelSpec{
			Name:  "bank.account",
			Label: "Bank Account",
		},
		WithTaskPreparer[handlerBankDocument](func(job map[string]any) (map[string]any, error) {
			prepared = true
			job["prepared"] = true
			return job, nil
		}),
	)

	if handler.modelSpec.Name != "bank.account" {
		t.Fatalf("expected bank.account spec, got %q", handler.modelSpec.Name)
	}

	job, err := handler.prepareTask(map[string]any{})
	if err != nil {
		t.Fatalf("prepare task: %v", err)
	}
	if !prepared {
		t.Fatalf("expected task preparer to run")
	}
	if got, _ := job["prepared"].(bool); !got {
		t.Fatalf("expected prepared flag to be set")
	}
}

func TestPrepareEventTaskConvertsPayloadToDocument(t *testing.T) {
	job, err := prepareHandlerEventTask(map[string]any{
		"action": ActionCreate,
		"payload": handlerEvent{
			EventPath: "/events/payment",
			EventLog: map[string]any{
				"amount": 1000,
			},
		},
	})
	if err != nil {
		t.Fatalf("prepare event task: %v", err)
	}

	payload, ok := job["payload"].(handlerEventDocument)
	if !ok {
		t.Fatalf("expected payload to be converted to handlerEventDocument, got %T", job["payload"])
	}
	if payload.EventPath != "/events/payment" {
		t.Fatalf("expected event path to be preserved, got %q", payload.EventPath)
	}
	if payload.EventLog["amount"] != 1000 {
		t.Fatalf("expected event log payload to be preserved")
	}
	if payload.EventTime == 0 {
		t.Fatalf("expected event time to be set")
	}
}
