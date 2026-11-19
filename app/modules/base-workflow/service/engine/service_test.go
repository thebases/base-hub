package engine

// File overview:
// - Responsibility: verifies workflow execution, retries, and module-level helper behavior for service.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down execution behavior so retries, delivery helpers, and workflow-side utilities preserve the same operational semantics.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

func TestMain(m *testing.M) {
	// No database in unit tests: every lease is held unless a test injects its own hooks.
	defaultRenewRunLease = func(context.Context, int64, string) (bool, error) { return true, nil }
	defaultTakeRunLease = func(context.Context, int64, string) (bool, error) { return true, nil }
	// ...and every trigger's workflow can run (TestRegisterSkipsWorkflowThatCannotRun covers the gate).
	triggerWorkflowBlockedReason = func(context.Context, int64) (string, error) { return "", nil }
	shutdown := InitRuntime()
	code := m.Run()
	shutdown()
	os.Exit(code)
}

func TestExecuteTopicSelectsLatestActiveFlowVersion(t *testing.T) {
	var loadedTopic string
	var createdRun workflowmodels.WorkflowRunRecord
	var updates []orm.RecordMap

	svc := &Service{
		loadActiveFlow: func(_ context.Context, topic string) (*workflowmodels.FlowRecord, error) {
			loadedTopic = topic
			return &workflowmodels.FlowRecord{
				BaseModel: orm.BaseModel{ID: 11},
				Topic:     topic,
				Version:   7,
				IsActive:  true,
			}, nil
		},
		loadFlowActions: func(_ context.Context, workflowID int64) ([]workflowmodels.WorkflowActionRecord, error) {
			if workflowID != 11 {
				t.Fatalf("unexpected workflow id %d", workflowID)
			}
			return []workflowmodels.WorkflowActionRecord{{
				Sequence:      10,
				Name:          "Parse Payload",
				ActionCode:    "parse_payload",
				ActionVersion: 1,
			}}, nil
		},
		createRun: func(_ context.Context, record workflowmodels.WorkflowRunRecord) (*orm.InsertResult, error) {
			createdRun = record
			return &orm.InsertResult{InsertedID: int64(99), Acknowledged: true}, nil
		},
		updateRun: func(_ context.Context, _ orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			updates = append(updates, cloneRecordMap(values))
			return &orm.UpdateResult{Acknowledged: true, MatchedCount: 1, ModifiedCount: 1}, nil
		},
		lookupAction: resolveWorkflowModuleAction,
		now: func() time.Time {
			return time.Unix(1_700_000_000, 0).UTC()
		},
		sleep: func(context.Context, time.Duration) error { return nil },
	}

	if err := svc.ExecuteTopic(context.Background(), "topic.alpha", []byte(`{"hello":"world"}`)); err != nil {
		t.Fatalf("ExecuteTopic returned error: %v", err)
	}

	if loadedTopic != "topic.alpha" {
		t.Fatalf("expected topic lookup to use topic.alpha, got %q", loadedTopic)
	}
	if createdRun.Topic != "topic.alpha" {
		t.Fatalf("expected workflow run topic to be persisted, got %#v", createdRun)
	}
	if createdRun.WorkflowID == nil || *createdRun.WorkflowID != 11 {
		t.Fatalf("expected workflow run workflow_id 11, got %#v", createdRun.WorkflowID)
	}
	if createdRun.Name == "" {
		t.Fatalf("expected workflow request id to be persisted in workflow run name")
	}
	if createdRun.FlowVersion != 7 {
		t.Fatalf("expected workflow run version 7, got %d", createdRun.FlowVersion)
	}
	if createdRun.TriggerType != workflowmodels.WorkflowTriggerTypeWebhook {
		t.Fatalf("expected workflow run trigger_type webhook, got %q", createdRun.TriggerType)
	}
	if got := createdRun.InputPayload["hello"]; got != "world" {
		t.Fatalf("expected workflow run input_payload to keep original body, got %#v", createdRun.InputPayload)
	}
	if len(updates) == 0 {
		t.Fatalf("expected workflow run updates")
	}
	last := updates[len(updates)-1]
	if got, _ := last["status"].(string); got != workflowmodels.WorkflowRunStatusSucceeded {
		t.Fatalf("expected succeeded final status, got %#v", last["status"])
	}
}

func TestResumeRunRehydratesWorkflowRequestIDContextFromRun(t *testing.T) {
	const workflowRequestID = "e527f89b-f694-4887-a8a8-93e17ca5c4f9"
	sawRequestID := ""

	svc := &Service{
		readRun: func(context.Context, int64) (*workflowmodels.WorkflowRunRecord, error) {
			return &workflowmodels.WorkflowRunRecord{
				BaseModel:   orm.BaseModel{ID: 33},
				Name:        workflowRequestID,
				Topic:       "topic.resume",
				FlowVersion: 3,
				Status:      workflowmodels.WorkflowRunStatusRunning,
				CurrentStep: "save-step",
			}, nil
		},
		loadFlowVersion: func(context.Context, string, int) (*workflowmodels.FlowRecord, error) {
			return &workflowmodels.FlowRecord{
				BaseModel: orm.BaseModel{ID: 12},
				Topic:     "topic.resume",
				Version:   3,
				IsActive:  true,
			}, nil
		},
		loadFlowActions: func(context.Context, int64) ([]workflowmodels.WorkflowActionRecord, error) {
			return []workflowmodels.WorkflowActionRecord{{
				Sequence:      10,
				Name:          "save-step",
				StepKey:       "save-step",
				ActionCode:    "capture",
				ActionVersion: 1,
			}}, nil
		},
		updateRun: func(_ context.Context, _ orm.RecordMap, _ orm.RecordMap) (*orm.UpdateResult, error) {
			return &orm.UpdateResult{Acknowledged: true, MatchedCount: 1, ModifiedCount: 1}, nil
		},
		lookupAction: func(name string, version int) (ActionHandler, bool) {
			if name != "capture" || version != 1 {
				return nil, false
			}
			return func(ctx context.Context, req ActionRequest) ([]byte, error) {
				sawRequestID = workflowmodels.WorkflowRequestIDFromContext(ctx)
				return req.Payload, nil
			}, true
		},
		now:   func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		sleep: func(context.Context, time.Duration) error { return nil },
	}

	if err := svc.ResumeRun(context.Background(), 33); err != nil {
		t.Fatalf("ResumeRun returned error: %v", err)
	}
	if sawRequestID != workflowRequestID {
		t.Fatalf("workflow request id = %q, want %q", sawRequestID, workflowRequestID)
	}
}

func TestExecuteStepsRetriesAndDeadLettersAfterExhaustion(t *testing.T) {
	var updates []orm.RecordMap
	attempts := 0

	svc := &Service{
		createRun: func(_ context.Context, record workflowmodels.WorkflowRunRecord) (*orm.InsertResult, error) {
			return &orm.InsertResult{InsertedID: int64(7), Acknowledged: true}, nil
		},
		updateRun: func(_ context.Context, _ orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			updates = append(updates, cloneRecordMap(values))
			return &orm.UpdateResult{Acknowledged: true, MatchedCount: 1, ModifiedCount: 1}, nil
		},
		lookupAction: func(name string, version int) (ActionHandler, bool) {
			if name != "flaky" || version != 1 {
				return nil, false
			}
			return func(_ context.Context, _ ActionRequest) ([]byte, error) {
				attempts++
				return nil, errors.New("boom")
			}, true
		},
		now:   func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		sleep: func(context.Context, time.Duration) error { return nil },
	}

	steps := []workflowmodels.Step{{
		Name:        "flaky-step",
		Action:      "flaky",
		RetryPolicy: workflowmodels.RetryPolicy{MaxAttempts: 2},
	}}
	// ExecuteSteps runs have no flow version and cannot be resumed, so they retry in place.
	err := svc.ExecuteSteps(context.Background(), "topic.retry", steps, []byte(`{"hello":"world"}`))
	if err == nil || errors.Is(err, ErrRunRetryScheduled) {
		t.Fatalf("expected retry exhaustion error, got %v", err)
	}
	for _, update := range updates {
		if update["status"] == workflowmodels.WorkflowRunStatusRetryWaiting {
			t.Fatalf("an unresumable run was parked as waiting_retry")
		}
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
	last := updates[len(updates)-1]
	if got, _ := last["status"].(string); got != workflowmodels.WorkflowRunStatusDeadLetter {
		t.Fatalf("expected dead_letter final status, got %#v", last["status"])
	}
}

func TestExecuteFlowPersistsTriggerScopedInputPayload(t *testing.T) {
	var createdRun workflowmodels.WorkflowRunRecord
	var updates []orm.RecordMap

	svc := &Service{
		createRun: func(_ context.Context, record workflowmodels.WorkflowRunRecord) (*orm.InsertResult, error) {
			createdRun = record
			return &orm.InsertResult{InsertedID: int64(55), Acknowledged: true}, nil
		},
		updateRun: func(_ context.Context, _ orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			updates = append(updates, cloneRecordMap(values))
			return &orm.UpdateResult{Acknowledged: true, MatchedCount: 1, ModifiedCount: 1}, nil
		},
		now:   func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		sleep: func(context.Context, time.Duration) error { return nil },
	}

	ctx := workflowmodels.ContextWithWorkflowTriggerType(context.Background(), workflowmodels.WorkflowTriggerTypeKafka)
	err := svc.executeFlow(ctx, flowRuntime{
		flow: workflowmodels.FlowRecord{
			BaseModel: orm.BaseModel{ID: 13},
			Topic:     "topic.kafka",
			Version:   2,
			IsActive:  true,
		},
		steps: []workflowmodels.Step{},
	}, []byte(`{"source":"kafka","hello":"world"}`))
	if err != nil {
		t.Fatalf("executeFlow returned error: %v", err)
	}
	if createdRun.TriggerType != workflowmodels.WorkflowTriggerTypeKafka {
		t.Fatalf("expected trigger_type kafka, got %q", createdRun.TriggerType)
	}
	if got := createdRun.InputPayload["source"]; got != "kafka" {
		t.Fatalf("expected input_payload to preserve trigger body, got %#v", createdRun.InputPayload)
	}
	if len(updates) == 0 {
		t.Fatalf("expected workflow run updates")
	}
	first := updates[0]
	inputPayload, ok := first["input_payload"].(map[string]any)
	if !ok || inputPayload["hello"] != "world" {
		t.Fatalf("expected first update to keep input_payload, got %#v", first["input_payload"])
	}
}

func TestExecuteStepsContinuesWhenStepAllowsContinueOnError(t *testing.T) {
	var updates []orm.RecordMap
	attempts := 0

	svc := &Service{
		createRun: func(_ context.Context, record workflowmodels.WorkflowRunRecord) (*orm.InsertResult, error) {
			return &orm.InsertResult{InsertedID: int64(8), Acknowledged: true}, nil
		},
		updateRun: func(_ context.Context, _ orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			updates = append(updates, cloneRecordMap(values))
			return &orm.UpdateResult{Acknowledged: true, MatchedCount: 1, ModifiedCount: 1}, nil
		},
		lookupAction: func(name string, version int) (ActionHandler, bool) {
			switch name {
			case "flaky":
				return func(_ context.Context, _ ActionRequest) ([]byte, error) {
					attempts++
					return nil, errors.New("boom")
				}, true
			case "save_to_db":
				return func(_ context.Context, req ActionRequest) ([]byte, error) { return req.Payload, nil }, true
			default:
				return nil, false
			}
		},
		now:   func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		sleep: func(context.Context, time.Duration) error { return nil },
	}

	steps := []workflowmodels.Step{
		{Key: "flaky-step", Action: "flaky", RetryPolicy: workflowmodels.RetryPolicy{MaxAttempts: 2}, ContinueOnError: true},
		{Key: "save-step", Action: "save_to_db"},
	}
	err := svc.ExecuteSteps(context.Background(), "topic.continue", steps, []byte(`{"hello":"world"}`))
	if err != nil {
		t.Fatalf("expected continue_on_error flow to succeed, got %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
	last := updates[len(updates)-1]
	if got, _ := last["status"].(string); got != workflowmodels.WorkflowRunStatusSucceeded {
		t.Fatalf("expected succeeded final status, got %#v", last["status"])
	}
	rawStepRuns, ok := last["step_runs"].([]workflowmodels.WorkflowStepRun)
	if !ok || len(rawStepRuns) != 3 {
		t.Fatalf("expected 3 step runs (2 failed retries + 1 success), got %#v", last["step_runs"])
	}
	if !rawStepRuns[1].ContinueOnError {
		t.Fatalf("expected failed retry step run to preserve continue_on_error")
	}
}

func TestExecuteStepsSkipsConditionedStepWhenConditionDoesNotMatch(t *testing.T) {
	var updates []orm.RecordMap
	skippedCalls := 0
	finalCalls := 0

	svc := &Service{
		createRun: func(_ context.Context, record workflowmodels.WorkflowRunRecord) (*orm.InsertResult, error) {
			return &orm.InsertResult{InsertedID: int64(18), Acknowledged: true}, nil
		},
		updateRun: func(_ context.Context, _ orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			updates = append(updates, cloneRecordMap(values))
			return &orm.UpdateResult{Acknowledged: true, MatchedCount: 1, ModifiedCount: 1}, nil
		},
		lookupAction: func(name string, version int) (ActionHandler, bool) {
			switch name {
			case "skip-me":
				return func(_ context.Context, _ ActionRequest) ([]byte, error) {
					skippedCalls++
					return []byte(`{"unexpected":true}`), nil
				}, true
			case "final-step":
				return func(_ context.Context, req ActionRequest) ([]byte, error) {
					finalCalls++
					return req.Payload, nil
				}, true
			default:
				return nil, false
			}
		},
		now:   func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		sleep: func(context.Context, time.Duration) error { return nil },
	}

	err := svc.ExecuteSteps(context.Background(), "topic.condition", []workflowmodels.Step{
		{
			Key:    "skip-step",
			Action: "skip-me",
			Params: map[string]any{
				"condition": map[string]any{
					"field":  "owner_partner_id",
					"exists": false,
				},
			},
		},
		{
			Key:    "final-step",
			Action: "final-step",
		},
	}, []byte(`{"owner_partner_id":42}`))
	if err != nil {
		t.Fatalf("expected conditioned flow to succeed, got %v", err)
	}
	if skippedCalls != 0 {
		t.Fatalf("expected conditioned step handler not to run, got %d calls", skippedCalls)
	}
	if finalCalls != 1 {
		t.Fatalf("expected downstream step to run once, got %d", finalCalls)
	}

	last := updates[len(updates)-1]
	rawStepRuns, ok := last["step_runs"].([]workflowmodels.WorkflowStepRun)
	if !ok || len(rawStepRuns) != 2 {
		t.Fatalf("expected 2 step runs (1 skipped + 1 success), got %#v", last["step_runs"])
	}
	if rawStepRuns[0].Status != workflowmodels.WorkflowRunStatusSkipped {
		t.Fatalf("expected first step run to be skipped, got %#v", rawStepRuns[0])
	}
	if rawStepRuns[1].Status != workflowmodels.WorkflowRunStatusSucceeded {
		t.Fatalf("expected second step run to succeed, got %#v", rawStepRuns[1])
	}
}

func TestExecuteStepsUsesActionVersionAndStoresStepRuns(t *testing.T) {
	var updates []orm.RecordMap

	svc := &Service{
		createRun: func(_ context.Context, record workflowmodels.WorkflowRunRecord) (*orm.InsertResult, error) {
			return &orm.InsertResult{InsertedID: int64(5), Acknowledged: true}, nil
		},
		updateRun: func(_ context.Context, _ orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			updates = append(updates, cloneRecordMap(values))
			return &orm.UpdateResult{Acknowledged: true, MatchedCount: 1, ModifiedCount: 1}, nil
		},
		lookupAction: func(name string, version int) (ActionHandler, bool) {
			if name != "versioned" || version != 2 {
				return nil, false
			}
			return func(_ context.Context, _ ActionRequest) ([]byte, error) {
				return []byte(`{"ok":true}`), nil
			}, true
		},
		now:   func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		sleep: func(context.Context, time.Duration) error { return nil },
	}

	if err := svc.ExecuteSteps(context.Background(), "topic.versioned", []workflowmodels.Step{{
		Name:          "versioned-step",
		Action:        "versioned",
		ActionVersion: 2,
	}}, []byte(`{"hello":"world"}`)); err != nil {
		t.Fatalf("ExecuteSteps returned error: %v", err)
	}

	last := updates[len(updates)-1]
	rawStepRuns, ok := last["step_runs"].([]workflowmodels.WorkflowStepRun)
	if !ok || len(rawStepRuns) != 1 {
		t.Fatalf("expected 1 step run, got %#v", last["step_runs"])
	}
	if rawStepRuns[0].ImplementationKey != "versioned" || rawStepRuns[0].ImplementationVersion != 2 {
		t.Fatalf("expected implementation versioned@v2, got %#v", rawStepRuns[0])
	}
}

func TestExecuteStepsResolvesBuiltinImplementationBeforeLookup(t *testing.T) {
	var lookedUpKey string
	var lookedUpVersion int
	var receivedRequest ActionRequest

	svc := &Service{
		createRun: func(_ context.Context, record workflowmodels.WorkflowRunRecord) (*orm.InsertResult, error) {
			return &orm.InsertResult{InsertedID: int64(15), Acknowledged: true}, nil
		},
		updateRun: func(_ context.Context, _ orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			return &orm.UpdateResult{Acknowledged: true, MatchedCount: 1, ModifiedCount: 1}, nil
		},
		lookupAction: func(name string, version int) (ActionHandler, bool) {
			lookedUpKey = name
			lookedUpVersion = version
			return func(_ context.Context, req ActionRequest) ([]byte, error) {
				receivedRequest = req
				return req.Payload, nil
			}, true
		},
		now:   func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		sleep: func(context.Context, time.Duration) error { return nil },
	}

	err := svc.ExecuteSteps(context.Background(), "topic.persisted", []workflowmodels.Step{{
		Name:          "persisted-step",
		Action:        "save_to_db",
		ActionVersion: 1,
		Params:        map[string]any{"model": "base.workflow.outbound", "tenant": "vip"},
	}}, []byte(`{"hello":"world"}`))
	if err != nil {
		t.Fatalf("ExecuteSteps returned error: %v", err)
	}
	if lookedUpKey != "save_to_db" || lookedUpVersion != 1 {
		t.Fatalf("lookupAction called with %s@v%d, want save_to_db@v1", lookedUpKey, lookedUpVersion)
	}
	if got := receivedRequest.Step.Params["tenant"]; got != "vip" {
		t.Fatalf("expected explicit param override, got %#v", got)
	}
}

func TestPayloadForAuditFallsBackToRaw(t *testing.T) {
	auditPayload := payloadForAudit([]byte("not-json"))
	if got, _ := auditPayload["raw"].(string); got != "not-json" {
		t.Fatalf("expected raw payload fallback, got %#v", auditPayload)
	}
}

func TestResolveWorkflowModuleActionRejectsNonObjectPayloads(t *testing.T) {
	handler, ok := resolveWorkflowModuleAction("flatten_json", 1)
	if !ok {
		t.Fatalf("expected flatten_json handler")
	}
	if _, err := handler(context.Background(), ActionRequest{Payload: []byte(`["not","an","object"]`)}); err == nil {
		t.Fatalf("expected array payload to fail")
	}
}

func TestResolveWorkflowModuleActionAddsParsedFlag(t *testing.T) {
	handler, ok := resolveWorkflowModuleAction("parse_payload", 1)
	if !ok {
		t.Fatalf("expected parse_payload handler")
	}
	output, err := handler(context.Background(), ActionRequest{Payload: []byte(`{"x":1}`)})
	if err != nil {
		t.Fatalf("workflow module parse handler returned error: %v", err)
	}

	var decoded map[string]interface{}
	if err := json.Unmarshal(output, &decoded); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	if got, _ := decoded["parsed"].(bool); !got {
		t.Fatalf("expected parsed flag, got %#v", decoded)
	}
}

func TestResumeDueRunsResumesClaimedWaitingRetryRun(t *testing.T) {
	var updates []orm.RecordMap
	attempts := 0

	svc := &Service{
		claimDueRuns: func(_ context.Context, limit int64, beforeTs int64) ([]int64, error) {
			if limit != 25 || beforeTs != 1_700_000_000 {
				t.Fatalf("unexpected claim params: limit=%d before=%d", limit, beforeTs)
			}
			return []int64{21}, nil
		},
		readRun: func(_ context.Context, runID int64) (*workflowmodels.WorkflowRunRecord, error) {
			if runID != 21 {
				t.Fatalf("unexpected run id %d", runID)
			}
			return &workflowmodels.WorkflowRunRecord{
				BaseModel:   orm.BaseModel{ID: 21},
				Topic:       "topic.retry",
				FlowVersion: 3,
				Status:      workflowmodels.WorkflowRunStatusRunning,
				CurrentStep: "flaky-step",
				OutputPayload: map[string]any{
					"hello": "world",
				},
				StepRuns: []workflowmodels.WorkflowStepRun{{
					StepName: "flaky-step", StepKey: "flaky-step", Action: "flaky", ActionVersion: 1, Attempt: 1, Status: workflowmodels.WorkflowRunStatusFailed,
				}},
				AttemptCount: 1,
			}, nil
		},
		loadFlowVersion: func(_ context.Context, topic string, version int) (*workflowmodels.FlowRecord, error) {
			return &workflowmodels.FlowRecord{BaseModel: orm.BaseModel{ID: 44}, Topic: topic, Version: version, IsActive: true}, nil
		},
		loadFlowActions: func(_ context.Context, workflowID int64) ([]workflowmodels.WorkflowActionRecord, error) {
			return []workflowmodels.WorkflowActionRecord{{Sequence: 10, StepKey: "flaky-step", Name: "flaky-step", ActionCode: "flaky", ActionVersion: 1, RetryPolicy: workflowmodels.RetryPolicy{MaxAttempts: 2}}}, nil
		},
		updateRun: func(_ context.Context, _ orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			updates = append(updates, cloneRecordMap(values))
			return &orm.UpdateResult{Acknowledged: true, MatchedCount: 1, ModifiedCount: 1}, nil
		},
		lookupAction: func(name string, version int) (ActionHandler, bool) {
			return func(_ context.Context, req ActionRequest) ([]byte, error) {
				attempts++
				return req.Payload, nil
			}, true
		},
		now:   func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		sleep: func(context.Context, time.Duration) error { return nil },
	}

	resumed, err := svc.ResumeDueRuns(context.Background(), 25)
	if err != nil {
		t.Fatalf("ResumeDueRuns returned error: %v", err)
	}
	if resumed != 1 || attempts != 1 {
		t.Fatalf("unexpected resume result: resumed=%d attempts=%d", resumed, attempts)
	}
	last := updates[len(updates)-1]
	if got, _ := last["status"].(string); got != workflowmodels.WorkflowRunStatusSucceeded {
		t.Fatalf("expected succeeded final status, got %#v", last["status"])
	}
}

func TestResolveWorkflowModuleActionFlattensNestedObjects(t *testing.T) {
	handler, ok := resolveWorkflowModuleAction("flatten_json", 1)
	if !ok {
		t.Fatalf("expected flatten_json handler")
	}
	output, err := handler(context.Background(), ActionRequest{
		Payload: []byte(`{"payment":{"transaction_id":"TXN-1","amount":42},"state":{"status":"SUCCESS"}}`),
	})
	if err != nil {
		t.Fatalf("workflow module flatten handler returned error: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(output, &decoded); err != nil {
		t.Fatalf("unmarshal flattened output: %v", err)
	}
	if got := decoded["transaction_id"]; got != "TXN-1" {
		t.Fatalf("transaction_id = %#v, want TXN-1", got)
	}
}

func TestResolveWorkflowModuleActionRendersTemplateFromFlattenedInput(t *testing.T) {
	handler, ok := resolveWorkflowModuleAction("transform_data", 1)
	if !ok {
		t.Fatalf("expected transform_data handler")
	}
	output, err := handler(context.Background(), ActionRequest{
		Payload: []byte(`{"payment":{"transaction_id":"TXN-2","amount":99},"state":{"status":"SUCCESS"}}`),
		Step: workflowmodels.Step{
			Params: map[string]any{
				"template": map[string]any{"txn_id": "#transaction_id", "amount": "#amount", "message": "status=#status"},
			},
		},
	})
	if err != nil {
		t.Fatalf("workflow module transform handler returned error: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(output, &decoded); err != nil {
		t.Fatalf("unmarshal transformed output: %v", err)
	}
	if got := decoded["txn_id"]; got != "TXN-2" {
		t.Fatalf("txn_id = %#v, want TXN-2", got)
	}
}

func TestResolveWorkflowModuleCryptoActionsWrapPayloads(t *testing.T) {
	hmacHandler, ok := resolveWorkflowModuleAction("hmac_action", 1)
	if !ok {
		t.Fatalf("expected hmac_action handler")
	}
	signed, err := hmacHandler(context.Background(), ActionRequest{
		Payload: []byte(`{"transaction_id":"TXN-3","amount":123,"status":"SUCCESS"}`),
		Step: workflowmodels.Step{
			Params: map[string]any{"key": "demo-key", "key_list": []any{"transaction_id", "amount", "status"}, "output_field": "signature"},
		},
	})
	if err != nil {
		t.Fatalf("workflow module hmac handler returned error: %v", err)
	}

	aesHandler, ok := resolveWorkflowModuleAction("aes_action", 1)
	if !ok {
		t.Fatalf("expected aes_action handler")
	}
	encrypted, err := aesHandler(context.Background(), ActionRequest{
		Payload: signed,
		Step: workflowmodels.Step{
			Params: map[string]any{"key": "0123456789abcdef0123456789abcdef", "output_field": "ciphertext"},
		},
	})
	if err != nil {
		t.Fatalf("workflow module aes handler returned error: %v", err)
	}

	var encryptedDecoded map[string]any
	if err := json.Unmarshal(encrypted, &encryptedDecoded); err != nil {
		t.Fatalf("unmarshal encrypted payload: %v", err)
	}
	if ciphertext, _ := encryptedDecoded["ciphertext"].(string); ciphertext == "" {
		t.Fatalf("expected ciphertext field, got %#v", encryptedDecoded)
	}
}

func TestResolveWorkflowModuleCryptoActionsGenerateKeysAndDecrypt(t *testing.T) {
	hmacHandler, ok := resolveWorkflowModuleAction("hmac_action", 1)
	if !ok {
		t.Fatalf("expected hmac_action handler")
	}
	signed, err := hmacHandler(context.Background(), ActionRequest{
		Payload: []byte(`{"merchant_id":"M-1","order_id":"O-9","amount":123,"currency":"VND"}`),
		Step: workflowmodels.Step{
			Params: map[string]any{
				"key_list":    []any{"merchant_id", "order_id", "amount", "currency"},
				"include_key": true,
				"include_raw": true,
			},
		},
	})
	if err != nil {
		t.Fatalf("workflow module hmac handler returned error: %v", err)
	}

	aesHandler, ok := resolveWorkflowModuleAction("aes_action", 1)
	if !ok {
		t.Fatalf("expected aes_action handler")
	}
	encrypted, err := aesHandler(context.Background(), ActionRequest{
		Payload: signed,
		Step: workflowmodels.Step{
			Params: map[string]any{
				"output_field": "ciphertext",
				"include_key":  true,
				"include_raw":  true,
			},
		},
	})
	if err != nil {
		t.Fatalf("workflow module aes handler returned error: %v", err)
	}

	decryptHandler, ok := resolveWorkflowModuleAction("aes_decrypt_action", 1)
	if !ok {
		t.Fatalf("expected aes_decrypt_action handler")
	}
	decrypted, err := decryptHandler(context.Background(), ActionRequest{
		Payload: encrypted,
		Step: workflowmodels.Step{
			Params: map[string]any{
				"input_field": "ciphertext",
				"key_field":   "_aes_key",
				"include_key": true,
				"include_raw": true,
			},
		},
	})
	if err != nil {
		t.Fatalf("workflow module aes decrypt handler returned error: %v", err)
	}

	var signedDecoded map[string]any
	if err := json.Unmarshal(signed, &signedDecoded); err != nil {
		t.Fatalf("unmarshal signed payload: %v", err)
	}
	if signedDecoded["_hmac_key"] == "" {
		t.Fatalf("expected generated hmac key, got %#v", signedDecoded)
	}
	if signedDecoded["signature"] == "" {
		t.Fatalf("expected signature, got %#v", signedDecoded)
	}

	var encryptedDecoded map[string]any
	if err := json.Unmarshal(encrypted, &encryptedDecoded); err != nil {
		t.Fatalf("unmarshal encrypted payload: %v", err)
	}
	if encryptedDecoded["_aes_key"] == "" {
		t.Fatalf("expected generated aes key, got %#v", encryptedDecoded)
	}
	if encryptedDecoded["ciphertext"] == "" {
		t.Fatalf("expected ciphertext, got %#v", encryptedDecoded)
	}

	var decryptedDecoded map[string]any
	if err := json.Unmarshal(decrypted, &decryptedDecoded); err != nil {
		t.Fatalf("unmarshal decrypted payload: %v", err)
	}
	if got := decryptedDecoded["merchant_id"]; got != "M-1" {
		t.Fatalf("merchant_id = %#v, want M-1", got)
	}
	if got := decryptedDecoded["order_id"]; got != "O-9" {
		t.Fatalf("order_id = %#v, want O-9", got)
	}
	if decryptedDecoded["signature"] == "" {
		t.Fatalf("expected decrypted payload to keep signature, got %#v", decryptedDecoded)
	}
	if decryptedDecoded["_aes_key"] == "" {
		t.Fatalf("expected decrypt output to include aes key, got %#v", decryptedDecoded)
	}
}

func cloneRecordMap(in orm.RecordMap) orm.RecordMap {
	if in == nil {
		return nil
	}

	out := make(orm.RecordMap, len(in))
	for key, value := range in {
		switch typed := value.(type) {
		case []workflowmodels.WorkflowStepRun:
			out[key] = append([]workflowmodels.WorkflowStepRun(nil), typed...)
		default:
			out[key] = value
		}
	}
	return out
}

func TestNewStepRunShape(t *testing.T) {
	start := time.Unix(100, 0)
	run := newStepRun(stepRunSpec{
		name: "n", key: "k", step: workflowmodels.Step{Action: " a ", ContinueOnError: true},
		implementationKey: "impl", implementationVersion: 2, attempt: 3,
		status: workflowmodels.WorkflowRunStatusFailed, startedAt: start, finishedAt: start.Add(1500 * time.Millisecond),
		input: []byte(`{"a":1}`), output: []byte(`{"b":2}`), nextRetryAt: 9, err: errors.New("boom"),
	})
	if run.Action != "a" || run.Attempt != 3 || run.DurationMs != 1500 || run.NextRetryAt != 9 || run.Error != "boom" || !run.ContinueOnError {
		t.Fatalf("unexpected run: %+v", run)
	}
	if ok := newStepRun(stepRunSpec{startedAt: start, finishedAt: start}); ok.Error != "" {
		t.Fatalf("nil err must leave Error empty: %q", ok.Error)
	}
}

// TestRunStepSendsPayloadCredentialRefsLiterally locks WF-S-1: a payload value shaped like a
// credential ref, interpolated into an authored "{{...}}" param, must reach the handler as literal
// text. No credential with these names exists, so any resolver call would fail the step.
func TestRunStepSendsPayloadCredentialRefsLiterally(t *testing.T) {
	var got map[string]any
	handler := func(_ context.Context, req ActionRequest) ([]byte, error) {
		got = req.Step.Params
		return []byte(`{}`), nil
	}
	authored := map[string]any{
		"authorization": "{{token}}",
		"url":           "{{callback_url}}",
		"body":          "Bearer {{other}}",
	}
	step := workflowmodels.Step{Name: "send", Action: "notify", Params: authored}
	payload := []byte(`{"token":"@syscred:t14-missing","callback_url":"https://attacker.test","other":"@cred:t14-missing"}`)

	if _, err := (&Service{}).runStep(context.Background(), handler, workflowmodels.FlowRecord{Topic: "topic.t14"}, "send", step, payload); err != nil {
		t.Fatalf("runStep: %v", err)
	}
	want := map[string]any{
		"authorization": "@syscred:t14-missing",
		"url":           "https://attacker.test",
		"body":          "Bearer @cred:t14-missing",
	}
	for k, w := range want {
		if got[k] != w {
			t.Fatalf("%s = %#v, want literal %#v", k, got[k], w)
		}
	}
	if authored["authorization"] != "{{token}}" {
		t.Fatalf("stored step params were mutated: %#v", authored)
	}
}
