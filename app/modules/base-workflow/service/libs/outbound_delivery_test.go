package libs

// File overview:
// - Responsibility: verifies workflow execution, retries, and module-level helper behavior for outbound delivery.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down execution behavior so retries, delivery helpers, and workflow-side utilities preserve the same operational semantics.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"git.thebase.vn/base_hub/internal/worker"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

type submitterStub struct {
	job     httpDeliveryJob
	timeout time.Duration
	calls   int
	err     error
}

func (s *submitterStub) SubmitWithTimeout(job httpDeliveryJob, timeout time.Duration) error {
	s.job = job
	s.timeout = timeout
	s.calls++
	return s.err
}

func TestQueueHTTPJSONCreatesQueuedRecordAndSubmitsJob(t *testing.T) {
	var created workflowmodels.OutboundDeliveryRecord
	var submitted httpDeliveryJob
	ctx := workflowmodels.ContextWithWorkflowRequestID(context.Background(), "d7b6b00f-9f97-4bd4-a8d8-1625f69a4698")
	ctx = workflowmodels.ContextWithWorkflowStepIdempotencyKey(ctx, "d7b6b00f-9f97-4bd4-a8d8-1625f69a4698:notify_partner")

	svc := &OutboundDeliveryService{
		createRecord: func(_ context.Context, record workflowmodels.OutboundDeliveryRecord) (*orm.InsertResult, error) {
			created = record
			return &orm.InsertResult{InsertedID: int64(41), Acknowledged: true}, nil
		},
		submitJob: func(_ context.Context, job httpDeliveryJob) error {
			submitted = job
			return nil
		},
		updateRecord: func(_ context.Context, _ orm.RecordMap, _ orm.RecordMap) (*orm.UpdateResult, error) {
			return &orm.UpdateResult{Acknowledged: true, ModifiedCount: 1}, nil
		},
		now:        func() time.Time { return time.Unix(1700000000, 0).UTC() },
		retryDelay: time.Minute,
	}

	headers := map[string]string{"Authorization": "Bearer token"}
	if err := svc.QueueHTTPJSON(ctx, "ipn.test", "https://example.com/hook", headers, "", map[string]interface{}{"ok": true}); err != nil {
		t.Fatalf("queue http json: %v", err)
	}

	if created.Status != workflowmodels.OutboundDeliveryStatusQueued {
		t.Fatalf("expected queued status, got %q", created.Status)
	}
	if created.DeliveryType != "http_json" {
		t.Fatalf("expected http_json delivery type, got %q", created.DeliveryType)
	}
	if created.Name != "d7b6b00f-9f97-4bd4-a8d8-1625f69a4698:notify_partner" {
		t.Fatalf("expected the step idempotency key as name, got %q", created.Name)
	}
	if created.NextAttemptAt != 1700000000+int64(outboundFastPathGrace/time.Second) {
		t.Fatalf("a fresh row must be left to the fast path for the grace period, next_attempt_at=%d", created.NextAttemptAt)
	}
	if created.Headers["Authorization"] != "Bearer token" || created.Headers["Content-Type"] != "application/json" {
		t.Fatalf("expected custom header merged over default, got %#v", created.Headers)
	}
	if submitted.DeliveryID != 41 || submitted.TargetRef != "https://example.com/hook" {
		t.Fatalf("unexpected submitted job %#v", submitted)
	}
	if submitted.Headers["Authorization"] != "Bearer token" || submitted.Headers["Content-Type"] != "application/json" {
		t.Fatalf("expected custom header merged into submitted job, got %#v", submitted.Headers)
	}
}

// The row is the outbox: a full worker queue leaves it queued for the sweeper and the step succeeds.
func TestQueueHTTPJSONLeavesRowQueuedWhenSubmitFails(t *testing.T) {
	var updates []orm.RecordMap

	svc := &OutboundDeliveryService{
		createRecord: func(_ context.Context, record workflowmodels.OutboundDeliveryRecord) (*orm.InsertResult, error) {
			return &orm.InsertResult{InsertedID: int64(52), Acknowledged: true}, nil
		},
		submitJob: func(_ context.Context, job httpDeliveryJob) error {
			log.Errorf("queue full")
			return fmt.Errorf("queue full")
		},
		updateRecord: func(_ context.Context, _ orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			updates = append(updates, values)
			return &orm.UpdateResult{Acknowledged: true, ModifiedCount: 1}, nil
		},
		now:        func() time.Time { return time.Unix(1700000000, 0).UTC() },
		retryDelay: time.Minute,
	}

	if err := svc.QueueHTTPJSON(context.Background(), "ipn.test", "https://example.com/hook", nil, "", nil); err != nil {
		t.Fatalf("a stored row is accepted even when the worker queue is full: %v", err)
	}
	if len(updates) != 0 {
		t.Fatalf("the row must stay queued for the sweeper, got updates %#v", updates)
	}
}

func TestManualRetryRequeuesExistingDeliveryAndSubmitsJob(t *testing.T) {
	var updates []orm.RecordMap
	var submitted httpDeliveryJob

	svc := &OutboundDeliveryService{
		submitJob: func(_ context.Context, job httpDeliveryJob) error {
			submitted = job
			return nil
		},
		updateRecord: func(_ context.Context, _ orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			updates = append(updates, values)
			return &orm.UpdateResult{Acknowledged: true, ModifiedCount: 1}, nil
		},
		now:        func() time.Time { return time.Unix(1700000000, 0).UTC() },
		retryDelay: time.Minute,
	}

	err := svc.ManualRetry(context.Background(), ManualRetryRequest{
		DeliveryID:  88,
		Topic:       "topic.outbound",
		TargetRef:   "https://example.com/retry",
		Headers:     map[string]string{"Authorization": "Bearer token"},
		RequestBody: map[string]interface{}{"ok": true},
		MaxAttempts: 5,
	})
	if err != nil {
		t.Fatalf("manual retry: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("update count = %d, want 1", len(updates))
	}
	if got := updates[0]["status"]; got != workflowmodels.OutboundDeliveryStatusQueued {
		t.Fatalf("queued update status = %#v, want queued", got)
	}
	if got := updates[0]["next_attempt_at"]; got != int64(1700000000) {
		t.Fatalf("next_attempt_at = %#v, want 1700000000", got)
	}
	if submitted.DeliveryID != 88 || submitted.TargetRef != "https://example.com/retry" {
		t.Fatalf("submitted job = %#v, want delivery id 88 and matching target", submitted)
	}
	if submitted.MaxAttempts != 5 {
		t.Fatalf("submitted max attempts = %d, want 5", submitted.MaxAttempts)
	}
	if submitted.Headers["Authorization"] != "Bearer token" || submitted.Headers["Content-Type"] != "application/json" {
		t.Fatalf("submitted headers = %#v, want merged authorization + content-type", submitted.Headers)
	}
}

func TestManualRetryMarksRetryableFailureWhenSubmitFails(t *testing.T) {
	var updates []orm.RecordMap

	svc := &OutboundDeliveryService{
		submitJob: func(_ context.Context, _ httpDeliveryJob) error {
			return fmt.Errorf("queue full")
		},
		updateRecord: func(_ context.Context, _ orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			updates = append(updates, values)
			return &orm.UpdateResult{Acknowledged: true, ModifiedCount: 1}, nil
		},
		now:        func() time.Time { return time.Unix(1700000000, 0).UTC() },
		retryDelay: time.Minute,
	}

	err := svc.ManualRetry(context.Background(), ManualRetryRequest{
		DeliveryID:  89,
		TargetRef:   "https://example.com/retry",
		RequestBody: map[string]interface{}{"ok": true},
	})
	if err == nil {
		t.Fatalf("expected manual retry submit failure")
	}
	// The row stays queued (due now) so the sweeper sends it; the attempt budget restarts.
	if len(updates) != 1 {
		t.Fatalf("update count = %d, want 1", len(updates))
	}
	if got := updates[0]["status"]; got != workflowmodels.OutboundDeliveryStatusQueued {
		t.Fatalf("update status = %#v, want queued", got)
	}
	if got := updates[0]["attempt_count"]; got != 0 {
		t.Fatalf("attempt_count = %#v, want reset to 0", got)
	}
}

func TestIsPermanentHTTPStatus(t *testing.T) {
	if !isPermanentHTTPStatus(http.StatusBadRequest) {
		t.Fatalf("expected 400 to be permanent")
	}
	if isPermanentHTTPStatus(http.StatusTooManyRequests) {
		t.Fatalf("expected 429 to stay retryable")
	}
	if isPermanentHTTPStatus(http.StatusBadGateway) {
		t.Fatalf("expected 502 to stay retryable")
	}
}

func TestSubmitHTTPDeliveryJobCapsWaitAtDefaultTimeout(t *testing.T) {
	stub := &submitterStub{}

	if err := submitHTTPDeliveryJob(context.Background(), stub, httpDeliveryJob{DeliveryID: 71}); err != nil {
		t.Fatalf("submit http delivery job: %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("SubmitWithTimeout() calls = %d, want 1", stub.calls)
	}
	if stub.timeout != worker.DefaultSubmitTimeout {
		t.Fatalf("timeout = %s, want %s", stub.timeout, worker.DefaultSubmitTimeout)
	}
}

func TestSubmitHTTPDeliveryJobUsesEarlierCallerDeadline(t *testing.T) {
	stub := &submitterStub{}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	if err := submitHTTPDeliveryJob(ctx, stub, httpDeliveryJob{DeliveryID: 72}); err != nil {
		t.Fatalf("submit http delivery job: %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("SubmitWithTimeout() calls = %d, want 1", stub.calls)
	}
	if stub.timeout <= 0 || stub.timeout > 1500*time.Millisecond {
		t.Fatalf("timeout = %s, want <= 1.5s and > 0", stub.timeout)
	}
}

func TestProcessHTTPDeliveryStoresResponseBodyOnSuccess(t *testing.T) {
	var updates []orm.RecordMap
	svc := &OutboundDeliveryService{
		updateRecord: func(_ context.Context, _ orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			updates = append(updates, values)
			return &orm.UpdateResult{Acknowledged: true, ModifiedCount: 1}, nil
		},
		postHTTP: func(_ context.Context, _ string, _ map[string]string, _ map[string]interface{}) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			}, nil
		},
		now:        func() time.Time { return time.Unix(1700000000, 0).UTC() },
		retryDelay: time.Minute,
	}

	if _, err := svc.processHTTPDelivery(context.Background(), httpDeliveryJob{
		DeliveryID:  91,
		TargetRef:   "https://example.com/hook",
		Headers:     map[string]string{"Content-Type": "application/json"},
		RequestBody: map[string]interface{}{"ok": true},
		MaxAttempts: 3,
	}); err != nil {
		t.Fatalf("processHTTPDelivery() error = %v", err)
	}
	if len(updates) != 2 {
		t.Fatalf("update count = %d, want 2", len(updates))
	}
	if got := updates[1]["response_body"]; got != `{"ok":true}` {
		t.Fatalf("response_body = %#v, want success response body", got)
	}
}

func TestProcessHTTPDeliveryStoresResponseBodyOnRetryableFailure(t *testing.T) {
	var updates []orm.RecordMap
	svc := &OutboundDeliveryService{
		updateRecord: func(_ context.Context, _ orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			updates = append(updates, values)
			return &orm.UpdateResult{Acknowledged: true, ModifiedCount: 1}, nil
		},
		postHTTP: func(_ context.Context, _ string, _ map[string]string, _ map[string]interface{}) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Body:       io.NopCloser(strings.NewReader("bad gateway")),
			}, nil
		},
		now:        func() time.Time { return time.Unix(1700000000, 0).UTC() },
		retryDelay: time.Minute,
	}

	if _, err := svc.processHTTPDelivery(context.Background(), httpDeliveryJob{
		DeliveryID:  92,
		TargetRef:   "https://example.com/hook",
		Headers:     map[string]string{"Content-Type": "application/json"},
		RequestBody: map[string]interface{}{"ok": true},
		MaxAttempts: 3,
	}); err == nil {
		t.Fatalf("expected retryable failure")
	}
	if len(updates) != 2 {
		t.Fatalf("update count = %d, want 2", len(updates))
	}
	if got := updates[1]["response_body"]; got != "bad gateway" {
		t.Fatalf("response_body = %#v, want retryable failure response body", got)
	}
}
