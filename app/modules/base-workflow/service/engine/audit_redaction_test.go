package engine

// File overview:
// - Responsibility: plan T45, what workflow runs store about their payloads: built-in and per-workflow
//   key redaction, hash/none modes, the resume checkpoint kept until a final status, and the
//   generic aes_decrypt error.

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

func TestAuditPolicyFullRedactsSensitiveKeys(t *testing.T) {
	policy := newAuditPolicy(workflowmodels.WorkflowAuditPayloadFull, "card_ref\n customer.Phone ; ")
	payload := map[string]any{
		"amount":        float64(10),
		"Authorization": "Bearer abc",
		"X-Api-Key":     "k1",
		"pan":           "4111111111111111",
		"_aes_key":      "aes",
		"card_ref":      "custom",
		"empty_token":   nil,
		"customer":      map[string]any{"phone": "0909", "name": "An"},
		"phone":         "root phone is not the customer.phone path",
		"items":         []any{map[string]any{"cvv": "123", "sku": "A"}},
		"nested":        map[string]any{"client_secret": "s", "pin_block": "p", "ok": true},
	}
	original, _ := json.Marshal(payload)

	got := policy.apply(payload)

	want := map[string]any{
		"amount":        float64(10),
		"Authorization": auditRedactedValue,
		"X-Api-Key":     auditRedactedValue,
		"pan":           auditRedactedValue,
		"_aes_key":      auditRedactedValue,
		"card_ref":      auditRedactedValue,
		"empty_token":   nil,
		"customer":      map[string]any{"phone": auditRedactedValue, "name": "An"},
		"phone":         "root phone is not the customer.phone path",
		"items":         []any{map[string]any{"cvv": auditRedactedValue, "sku": "A"}},
		"nested":        map[string]any{"client_secret": auditRedactedValue, "pin_block": auditRedactedValue, "ok": true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("apply() =\n%#v\nwant\n%#v", got, want)
	}
	if after, _ := json.Marshal(payload); string(after) != string(original) {
		t.Fatalf("apply() mutated its input: %s", after)
	}
}

func withAuditDigestKey(t *testing.T, key []byte) {
	t.Helper()
	previous := auditDigestKey
	auditDigestKey = func() []byte { return key }
	t.Cleanup(func() { auditDigestKey = previous })
}

func TestAuditPolicyHashAndNoneAreIdempotent(t *testing.T) {
	withAuditDigestKey(t, []byte("test-key"))
	payload := map[string]any{"payload_hex": "0200F23A", "b": 1}

	hash := newAuditPolicy(workflowmodels.WorkflowAuditPayloadHash, "")
	first := hash.apply(payload)
	digest, _ := first[auditDigestField].(string)
	if first[auditMarkerKey] != workflowmodels.WorkflowAuditPayloadHash || len(digest) != 64 || first["payload_hex"] != nil {
		t.Fatalf("hash apply() = %#v", first)
	}
	if again := hash.apply(map[string]any{"b": 1, "payload_hex": "0200F23A"}); !reflect.DeepEqual(again, first) {
		t.Fatalf("hash is not deterministic: %#v vs %#v", again, first)
	}
	// markRun re-sends stored snapshots: an already hashed payload must not be hashed again.
	if twice := hash.apply(first); !reflect.DeepEqual(twice, first) {
		t.Fatalf("hash apply() twice = %#v, want %#v", twice, first)
	}
	// After a jsonb read the size is a float64; still recognized.
	roundTrip := map[string]any{auditMarkerKey: "hash", "bytes": float64(30), auditDigestField: digest}
	if again := hash.apply(roundTrip); !reflect.DeepEqual(again, roundTrip) {
		t.Fatalf("stored hash re-digested: %#v", again)
	}
	// The digest is keyed: another server key gives another digest.
	withAuditDigestKey(t, []byte("other-key"))
	if other := hash.apply(payload); other[auditDigestField] == digest {
		t.Fatalf("digest does not depend on the server key")
	}
	// Without a key only the size is kept.
	withAuditDigestKey(t, nil)
	if keyless := hash.apply(payload); len(keyless) != 2 || keyless[auditDigestField] != nil {
		t.Fatalf("keyless hash = %#v, want marker + bytes only", keyless)
	}

	none := newAuditPolicy(workflowmodels.WorkflowAuditPayloadNone, "")
	if got := none.apply(payload); !reflect.DeepEqual(got, map[string]any{auditMarkerKey: "none"}) {
		t.Fatalf("none apply() = %#v", got)
	}
	if got := none.apply(map[string]any{}); len(got) != 0 {
		t.Fatalf("empty payload must stay empty, got %#v", got)
	}
	if got := newAuditPolicy("bogus", "").mode; got != workflowmodels.WorkflowAuditPayloadFull {
		t.Fatalf("unknown mode = %q, want full", got)
	}
}

// auditTestService runs one step that fails once (waiting_retry) and then succeeds.
func TestAuditPolicySpoofedMarkerIsStillRedacted(t *testing.T) {
	full := newAuditPolicy(workflowmodels.WorkflowAuditPayloadFull, "")
	spoofs := []map[string]any{
		{auditMarkerKey: "none", "pan": "4111111111111111"},
		{auditMarkerKey: "hash", "bytes": 1, "pan": "4111111111111111"},
		{auditMarkerKey: "hash", "pan": "4111111111111111"},
		{auditMarkerKey: "hash", "bytes": 1, auditDigestField: "4111111111111111"},
	}
	for _, spoof := range spoofs {
		got := full.apply(spoof)
		if pan, ok := got["pan"]; ok && pan != auditRedactedValue {
			t.Fatalf("spoofed marker %#v stored the PAN: %#v", spoof, got)
		}
		if reflect.DeepEqual(got, spoof) && spoof["pan"] != nil {
			t.Fatalf("spoofed marker %#v skipped redaction", spoof)
		}
	}
	// A path entry with empty segments still matches.
	trailing := newAuditPolicy(workflowmodels.WorkflowAuditPayloadFull, "customer..phone., ")
	got := trailing.apply(map[string]any{"customer": map[string]any{"phone": "0909"}})
	if got["customer"].(map[string]any)["phone"] != auditRedactedValue {
		t.Fatalf("path with empty segments did not match: %#v", got)
	}
}

func auditTestService(t *testing.T, seen *[]map[string]any, created *workflowmodels.WorkflowRunRecord, updates *[]orm.RecordMap) *Service {
	t.Helper()
	calls := 0
	return &Service{
		createRun: func(_ context.Context, record workflowmodels.WorkflowRunRecord) (*orm.InsertResult, error) {
			*created = record
			return &orm.InsertResult{InsertedID: int64(9), Acknowledged: true}, nil
		},
		updateRun: func(_ context.Context, _ orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			*updates = append(*updates, cloneRecordMap(values))
			return &orm.UpdateResult{Acknowledged: true, MatchedCount: 1, ModifiedCount: 1}, nil
		},
		lookupAction: func(string, int) (ActionHandler, bool) {
			return func(_ context.Context, req ActionRequest) ([]byte, error) {
				*seen = append(*seen, payloadForAudit(req.Payload))
				calls++
				if calls == 1 {
					return nil, errTestFlaky
				}
				return req.Payload, nil
			}, true
		},
		now:   func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		sleep: func(context.Context, time.Duration) error { return nil },
	}
}

var errTestFlaky = &flakyError{}

type flakyError struct{}

func (*flakyError) Error() string { return "flaky" }

func auditTestRuntime(redactKeys string) flowRuntime {
	return flowRuntime{
		flow: workflowmodels.FlowRecord{BaseModel: orm.BaseModel{ID: 3}, Topic: "pay", Version: 1, IsActive: true, AuditRedactKeys: redactKeys},
		steps: []workflowmodels.Step{{
			Key: "charge", Name: "charge", Action: "charge", ActionVersion: 1,
			RetryPolicy: workflowmodels.RetryPolicy{MaxAttempts: 2},
		}},
	}
}

func TestExecuteFlowFullModeRedactsHistoryButKeepsResumeCheckpoint(t *testing.T) {
	var seen []map[string]any
	var created workflowmodels.WorkflowRunRecord
	var updates []orm.RecordMap
	svc := auditTestService(t, &seen, &created, &updates)

	ctx := workflowmodels.ContextWithWorkflowTriggerType(context.Background(), workflowmodels.WorkflowTriggerTypeWebhook)
	input := []byte(`{"amount":10,"pan":"4111111111111111","customer":{"phone":"0909"}}`)
	// First run: the step fails once and the run waits for its retry (T48). Second run: the
	// handler now succeeds and the run finishes.
	if err := svc.executeFlow(ctx, auditTestRuntime("customer.phone"), input); !errors.Is(err, ErrRunRetryScheduled) {
		t.Fatalf("executeFlow: %v, want ErrRunRetryScheduled", err)
	}
	if err := svc.executeFlow(ctx, auditTestRuntime("customer.phone"), input); err != nil {
		t.Fatalf("executeFlow: %v", err)
	}

	// The handler always works on the real payload.
	for _, payload := range seen {
		if payload["pan"] != "4111111111111111" {
			t.Fatalf("handler saw a redacted payload: %#v", payload)
		}
	}
	if created.AuditMode != workflowmodels.WorkflowAuditPayloadFull {
		t.Fatalf("audit_mode = %q, want full", created.AuditMode)
	}
	if created.InputPayload["pan"] != auditRedactedValue || created.OutputPayload["pan"] != auditRedactedValue {
		t.Fatalf("pending run stored card data: %#v / %#v", created.InputPayload, created.OutputPayload)
	}
	if created.InputPayload["amount"] != float64(10) {
		t.Fatalf("non-sensitive key lost: %#v", created.InputPayload)
	}

	sawRetryCheckpoint := false
	for _, update := range updates {
		if input, ok := update["input_payload"].(map[string]any); ok && input["pan"] != auditRedactedValue {
			t.Fatalf("input_payload stored card data: %#v", input)
		}
		stepRuns, _ := update["step_runs"].([]workflowmodels.WorkflowStepRun)
		for _, stepRun := range stepRuns {
			for _, snapshot := range []map[string]any{stepRun.InputSnapshot, stepRun.OutputSnapshot} {
				if snapshot["pan"] != auditRedactedValue {
					t.Fatalf("step snapshot stored card data: %#v", snapshot)
				}
				if customer, _ := snapshot["customer"].(map[string]any); customer["phone"] != auditRedactedValue {
					t.Fatalf("workflow redact path not applied: %#v", snapshot)
				}
			}
		}
		output, _ := update["output_payload"].(map[string]any)
		switch update["status"] {
		case workflowmodels.WorkflowRunStatusRetryWaiting:
			sawRetryCheckpoint = true
			if output["pan"] != "4111111111111111" {
				t.Fatalf("waiting_retry checkpoint must stay complete for resume, got %#v", output)
			}
		case workflowmodels.WorkflowRunStatusSucceeded:
			if output["pan"] != auditRedactedValue {
				t.Fatalf("final output_payload stored card data: %#v", output)
			}
		}
	}
	if !sawRetryCheckpoint {
		t.Fatalf("expected a waiting_retry update, got %d updates", len(updates))
	}
	if last := updates[len(updates)-1]; last["status"] != workflowmodels.WorkflowRunStatusSucceeded {
		t.Fatalf("last status = %#v", last["status"])
	}
}

func TestExecuteFlowTCPDefaultsToHash(t *testing.T) {
	var seen []map[string]any
	var created workflowmodels.WorkflowRunRecord
	var updates []orm.RecordMap
	svc := auditTestService(t, &seen, &created, &updates)

	ctx := workflowmodels.ContextWithWorkflowTriggerType(context.Background(), workflowmodels.WorkflowTriggerTypeTCP)
	_ = svc.executeFlow(ctx, auditTestRuntime(""), []byte(`{"payload_hex":"0200F23A"}`)) // fails once (retry scheduled)
	if err := svc.executeFlow(ctx, auditTestRuntime(""), []byte(`{"payload_hex":"0200F23A"}`)); err != nil {
		t.Fatalf("executeFlow: %v", err)
	}
	if created.AuditMode != workflowmodels.WorkflowAuditPayloadHash || created.InputPayload[auditMarkerKey] != "hash" {
		t.Fatalf("tcp run = mode %q input %#v, want hash", created.AuditMode, created.InputPayload)
	}
	last := updates[len(updates)-1]
	if output, _ := last["output_payload"].(map[string]any); output[auditMarkerKey] != "hash" {
		t.Fatalf("final output_payload = %#v, want hash", output)
	}
	for _, stepRun := range last["step_runs"].([]workflowmodels.WorkflowStepRun) {
		if stepRun.InputSnapshot[auditMarkerKey] != "hash" || stepRun.OutputSnapshot[auditMarkerKey] != "hash" {
			t.Fatalf("step snapshot not hashed: %#v", stepRun)
		}
	}
	if seen[0]["payload_hex"] != "0200F23A" {
		t.Fatalf("handler saw %#v", seen[0])
	}

	// A trigger-chosen mode wins over the transport default.
	updates, created = nil, workflowmodels.WorkflowRunRecord{}
	ctx = workflowmodels.ContextWithWorkflowAuditMode(ctx, workflowmodels.WorkflowAuditPayloadNone)
	svc = auditTestService(t, &seen, &created, &updates)
	if err := svc.executeFlow(ctx, auditTestRuntime(""), []byte(`{"payload_hex":"0200F23A"}`)); !errors.Is(err, ErrRunRetryScheduled) {
		t.Fatalf("executeFlow: %v, want ErrRunRetryScheduled", err)
	}
	if created.AuditMode != workflowmodels.WorkflowAuditPayloadNone {
		t.Fatalf("audit_mode = %q, want none", created.AuditMode)
	}
}

func TestResumeRunKeepsStoredAuditMode(t *testing.T) {
	var updates []orm.RecordMap
	svc := &Service{
		readRun: func(context.Context, int64) (*workflowmodels.WorkflowRunRecord, error) {
			return &workflowmodels.WorkflowRunRecord{
				BaseModel: orm.BaseModel{ID: 5}, Name: "req-5", Topic: "pay", FlowVersion: 1,
				TriggerType: workflowmodels.WorkflowTriggerTypeWebhook, AuditMode: workflowmodels.WorkflowAuditPayloadNone,
				Status: workflowmodels.WorkflowRunStatusRetryWaiting, CurrentStep: "charge",
				OutputPayload: map[string]any{"pan": "4111111111111111"},
			}, nil
		},
		loadFlowVersion: func(_ context.Context, topic string, version int) (*workflowmodels.FlowRecord, error) {
			return &workflowmodels.FlowRecord{BaseModel: orm.BaseModel{ID: 3}, Topic: topic, Version: version, IsActive: true}, nil
		},
		loadFlowActions: func(context.Context, int64) ([]workflowmodels.WorkflowActionRecord, error) {
			return []workflowmodels.WorkflowActionRecord{{Sequence: 10, StepKey: "charge", Name: "charge", ActionCode: "charge", ActionVersion: 1}}, nil
		},
		updateRun: func(_ context.Context, _ orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			updates = append(updates, cloneRecordMap(values))
			return &orm.UpdateResult{Acknowledged: true, MatchedCount: 1, ModifiedCount: 1}, nil
		},
		lookupAction: func(string, int) (ActionHandler, bool) {
			return func(_ context.Context, req ActionRequest) ([]byte, error) {
				if payloadForAudit(req.Payload)["pan"] != "4111111111111111" {
					t.Fatalf("resumed step did not get the checkpoint payload: %s", req.Payload)
				}
				return req.Payload, nil
			}, true
		},
		now:   func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		sleep: func(context.Context, time.Duration) error { return nil },
	}
	if err := svc.ResumeRun(context.Background(), 5); err != nil {
		t.Fatalf("ResumeRun: %v", err)
	}
	last := updates[len(updates)-1]
	if output, _ := last["output_payload"].(map[string]any); output[auditMarkerKey] != "none" {
		t.Fatalf("final output_payload = %#v, want none marker", output)
	}
}

func TestAESDecryptErrorIsGeneric(t *testing.T) {
	aesHandler, _ := resolveWorkflowModuleAction("aes_action", 1)
	decrypt, _ := resolveWorkflowModuleAction("aes_decrypt_action", 1)
	encrypt := func() map[string]any {
		out, err := aesHandler(context.Background(), ActionRequest{
			Payload: []byte(`{"a":1}`),
			Step:    workflowmodels.Step{Params: map[string]any{"output_field": "ciphertext", "include_key": true}},
		})
		if err != nil {
			t.Fatalf("aes_action: %v", err)
		}
		return payloadForAudit(out)
	}
	first, second := encrypt(), encrypt()

	cases := map[string]map[string]any{
		"wrong key":        {"ciphertext": first["ciphertext"], "_aes_key": second["_aes_key"]},
		"bad ciphertext":   {"ciphertext": "bm90LWNpcGhlcnRleHQ=", "_aes_key": first["_aes_key"]},
		"not base64 input": {"ciphertext": "%%%", "_aes_key": first["_aes_key"]},
	}
	for name, payload := range cases {
		raw, _ := json.Marshal(payload)
		_, err := decrypt(context.Background(), ActionRequest{
			Payload: raw,
			Step:    workflowmodels.Step{Params: map[string]any{"input_field": "ciphertext"}},
		})
		if err == nil || err.Error() != "aes_decrypt_action: decrypt failed" {
			t.Fatalf("%s: err = %v, want the generic decrypt error", name, err)
		}
	}
}
