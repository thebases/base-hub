package models

// Plan T53: which outbound rows the sweeper claims (the Go mirror of the claim's WHERE clause), and
// workflow_id staying engine-owned.

import (
	"context"
	"errors"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestOutboundDeliveryClaimable(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	opts := OutboundSweepOptions{Now: now, StaleSending: 5 * time.Minute, Horizon: 24 * time.Hour}
	base := func(status string) OutboundDeliveryRecord {
		return OutboundDeliveryRecord{DeliveryType: OutboundDeliveryTypeHTTPJSON, Status: status, AttemptCount: 1, MaxAttempts: 3, NextAttemptAt: now.Unix() - 1, LastAttemptAt: now.Unix() - 600}
	}
	cases := []struct {
		name string
		row  func() OutboundDeliveryRecord
		want bool
	}{
		{"due queued", func() OutboundDeliveryRecord { return base(OutboundDeliveryStatusQueued) }, true},
		{"due retryable", func() OutboundDeliveryRecord { return base(OutboundDeliveryStatusRetryableFailed) }, true},
		{"retryable not due", func() OutboundDeliveryRecord {
			r := base(OutboundDeliveryStatusRetryableFailed)
			r.NextAttemptAt = now.Unix() + 10
			return r
		}, false},
		{"queued in fast-path grace", func() OutboundDeliveryRecord {
			r := base(OutboundDeliveryStatusQueued)
			r.NextAttemptAt = now.Unix() + 30
			return r
		}, false},
		{"due beyond horizon", func() OutboundDeliveryRecord {
			r := base(OutboundDeliveryStatusRetryableFailed)
			r.NextAttemptAt = now.Add(-25 * time.Hour).Unix()
			return r
		}, false},
		{"stale sending", func() OutboundDeliveryRecord { return base(OutboundDeliveryStatusSending) }, true},
		{"fresh sending", func() OutboundDeliveryRecord {
			r := base(OutboundDeliveryStatusSending)
			r.LastAttemptAt = now.Unix() - 10
			return r
		}, false},
		{"no attempt left", func() OutboundDeliveryRecord {
			r := base(OutboundDeliveryStatusRetryableFailed)
			r.AttemptCount = 3
			return r
		}, false},
		{"post_output_object row", func() OutboundDeliveryRecord {
			r := base(OutboundDeliveryStatusRetryableFailed)
			r.DeliveryType = OutboundDeliveryTypeHTTPJSONSync
			return r
		}, false},
		{"delivered", func() OutboundDeliveryRecord { return base(OutboundDeliveryStatusDelivered) }, false},
		{"permanent", func() OutboundDeliveryRecord { return base(OutboundDeliveryStatusPermanentFailed) }, false},
	}
	for _, tc := range cases {
		if got := OutboundDeliveryClaimable(tc.row(), opts); got != tc.want {
			t.Errorf("%s: claimable = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestOutboundWorkflowIDIsEngineOwned(t *testing.T) {
	rpc := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironment(nil, 7, map[string]any{orm.RBACContextAuthModeKey: "bearer"}, false))
	if err := ensureOutboundWorkflowIDAllowed(rpc, nil, orm.RecordMap{"workflow_id": int64(3)}); err == nil {
		t.Fatal("an RPC caller must not set workflow_id on create")
	}
	if err := ensureOutboundWorkflowIDAllowed(rpc, nil, orm.RecordMap{"workflow_id": nil}); err != nil {
		t.Fatalf("an empty workflow_id is fine: %v", err)
	}
	rows := []orm.RecordMap{{"workflow_id": int64(3)}}
	if err := ensureOutboundWorkflowIDAllowed(rpc, rows, orm.RecordMap{"workflow_id": int64(4)}); err == nil {
		t.Fatal("an RPC caller must not move a row to another workflow")
	}
	if err := ensureOutboundWorkflowIDAllowed(rpc, rows, orm.RecordMap{"workflow_id": int64(3)}); err != nil {
		t.Fatalf("an unchanged workflow_id is fine: %v", err)
	}
	if err := ensureOutboundWorkflowIDAllowed(OutboundEngineContext(rpc), nil, orm.RecordMap{"workflow_id": int64(3)}); err != nil {
		t.Fatalf("the engine sets it: %v", err)
	}
}

func TestWorkflowStepIdempotencyKey(t *testing.T) {
	if got := WorkflowStepIdempotencyKey(" run ", " step "); got != "run:step" {
		t.Fatalf("key = %q", got)
	}
	if WorkflowStepIdempotencyKey("", "step") != "" || WorkflowStepIdempotencyKey("run", "") != "" {
		t.Fatal("a missing part gives no key")
	}
}

// post_output_object sends both its step-key name and the request id: the name must win, or every
// delivery of a run would take the request id and the second one would hit the unique index.
func TestNormalizeOutboundDeliveryPrefersNameOverRequestID(t *testing.T) {
	values, err := normalizeOutboundDeliveryValues(orm.RecordMap{
		"delivery_type":       OutboundDeliveryTypeHTTPJSONSync,
		"target_ref":          "https://example.com/callback",
		"workflow_request_id": "run-1",
		"name":                "run-1:post_partner",
	}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if values["name"] != "run-1:post_partner" {
		t.Fatalf("name = %#v, want the explicit step-key name", values["name"])
	}
}

// TestOutboundGenericWriteNeedsAuthor: manual_retry is checked as "write", which the operator role
// passes, so the generic write itself must refuse operators (they may only manual_retry).
func TestOutboundGenericWriteNeedsAuthor(t *testing.T) {
	writes := 0
	restore := orm.StubRuntimeRepository(OutboundDeliveryModelName, orm.RuntimeRepositoryStub{
		Read: func(context.Context, []string, []string) ([]orm.RecordMap, error) {
			return []orm.RecordMap{{"id": int64(5), "status": OutboundDeliveryStatusDelivered}}, nil
		},
		Write: func(context.Context, []string, orm.RecordMap) (bool, error) {
			writes++
			return true, nil
		},
	})
	defer restore()
	meta := &orm.ModelMetadata{Name: OutboundDeliveryModelName}
	kwargs := map[string]any{"values": map[string]any{"status": OutboundDeliveryStatusQueued}}
	rpc := orm.ContextWithEnvironment(context.Background(), workflowRPCEnv(7))

	stubWorkflowAccess(t, workflowTestOperator...)
	_, err := writeOutboundDelivery(rpc, meta, []string{"5"}, nil, kwargs)
	var ormErr *orm.Error
	if !errors.As(err, &ormErr) || ormErr.Code != orm.ErrForbidden || writes != 0 {
		t.Fatalf("operator generic write: err = %v writes = %d, want forbidden and no write", err, writes)
	}

	stubWorkflowAccess(t, workflowTestSystem...)
	if _, err := writeOutboundDelivery(rpc, meta, []string{"5"}, nil, kwargs); err != nil || writes != 1 {
		t.Fatalf("group_system write: err = %v writes = %d", err, writes)
	}
	stubWorkflowAccess(t, workflowTestOperator...)
	if _, err := writeOutboundDelivery(OutboundEngineContext(rpc), meta, []string{"5"}, nil, kwargs); err != nil || writes != 2 {
		t.Fatalf("engine write: err = %v writes = %d", err, writes)
	}
}
