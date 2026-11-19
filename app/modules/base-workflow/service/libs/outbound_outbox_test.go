package libs

// Plan T53: notify is an outbox. One row per step (named after the step idempotency key) reused
// across attempts, a claimed send per attempt, exponential backoff up to max_attempts, and a
// sweeper that hands claimed rows to the worker with their Authorization resolved again.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

func fixedNow() time.Time { return time.Unix(1700000000, 0).UTC() }

func stepContext(key string) context.Context {
	ctx := workflowmodels.ContextWithWorkflowRequestID(context.Background(), "run-1")
	return workflowmodels.ContextWithWorkflowStepIdempotencyKey(ctx, key)
}

// A retried notify step reuses its row instead of failing on the unique name: the row is reset
// to queued with a fresh attempt budget and handed to the worker again.
func TestQueueHTTPJSONReusesTheStepRow(t *testing.T) {
	var updates []orm.RecordMap
	var submitted []httpDeliveryJob
	creates := 0
	svc := &OutboundDeliveryService{
		findByName: func(_ context.Context, name string) (orm.RecordMap, bool, error) {
			if name != "run-1:notify" {
				t.Fatalf("looked up %q, want the step key", name)
			}
			return orm.RecordMap{"id": int64(17), "status": workflowmodels.OutboundDeliveryStatusRetryableFailed}, true, nil
		},
		createRecord: func(context.Context, workflowmodels.OutboundDeliveryRecord) (*orm.InsertResult, error) {
			creates++
			return nil, errors.New("duplicate key value violates unique constraint")
		},
		updateRecord: func(_ context.Context, _ orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			updates = append(updates, values)
			return &orm.UpdateResult{Acknowledged: true, ModifiedCount: 1}, nil
		},
		submitJob: func(_ context.Context, job httpDeliveryJob) error { submitted = append(submitted, job); return nil },
		now:       fixedNow,
	}
	if err := svc.QueueHTTPJSON(stepContext("run-1:notify"), "t", "https://example.com/a", nil, "", map[string]any{"n": 2}); err != nil {
		t.Fatalf("QueueHTTPJSON: %v", err)
	}
	if creates != 0 {
		t.Fatalf("an existing step row must be reused, got %d creates", creates)
	}
	if len(updates) != 1 || updates[0]["status"] != workflowmodels.OutboundDeliveryStatusQueued || updates[0]["attempt_count"] != 0 || updates[0]["target_ref"] != "https://example.com/a" {
		t.Fatalf("requeue update = %#v", updates)
	}
	if len(submitted) != 1 || submitted[0].DeliveryID != 17 {
		t.Fatalf("submitted = %#v, want row 17", submitted)
	}
}

// A delivered (or in-flight) row is never sent again by a rerun of the step.
func TestQueueHTTPJSONDoesNotResendDeliveredRow(t *testing.T) {
	for _, status := range []string{workflowmodels.OutboundDeliveryStatusDelivered, workflowmodels.OutboundDeliveryStatusSending} {
		svc := &OutboundDeliveryService{
			findByName: func(context.Context, string) (orm.RecordMap, bool, error) {
				return orm.RecordMap{"id": float64(17), "status": status}, true, nil
			},
			updateRecord: func(context.Context, orm.RecordMap, orm.RecordMap) (*orm.UpdateResult, error) {
				t.Fatalf("%s row must not be rewritten", status)
				return nil, nil
			},
			submitJob: func(context.Context, httpDeliveryJob) error {
				t.Fatalf("%s row must not be sent again", status)
				return nil
			},
			now: fixedNow,
		}
		if err := svc.QueueHTTPJSON(stepContext("run-1:notify"), "t", "https://example.com/a", nil, "", nil); err != nil {
			t.Fatalf("%s: %v", status, err)
		}
	}
}

// Two attempts racing on the unique name: the create that loses reuses the winner's row.
func TestQueueHTTPJSONReusesRowAfterCreateConflict(t *testing.T) {
	lookups := 0
	var submitted httpDeliveryJob
	svc := &OutboundDeliveryService{
		findByName: func(context.Context, string) (orm.RecordMap, bool, error) {
			lookups++
			if lookups == 1 {
				return nil, false, nil
			}
			return orm.RecordMap{"id": int64(23), "status": workflowmodels.OutboundDeliveryStatusQueued}, true, nil
		},
		createRecord: func(context.Context, workflowmodels.OutboundDeliveryRecord) (*orm.InsertResult, error) {
			return nil, errors.New("unique_violation")
		},
		updateRecord: func(context.Context, orm.RecordMap, orm.RecordMap) (*orm.UpdateResult, error) {
			return &orm.UpdateResult{Acknowledged: true, ModifiedCount: 1}, nil
		},
		submitJob: func(_ context.Context, job httpDeliveryJob) error { submitted = job; return nil },
		now:       fixedNow,
	}
	if err := svc.QueueHTTPJSON(stepContext("run-1:notify"), "t", "https://example.com/a", nil, "", nil); err != nil {
		t.Fatalf("QueueHTTPJSON: %v", err)
	}
	if submitted.DeliveryID != 23 {
		t.Fatalf("submitted %#v, want the winner's row 23", submitted)
	}
}

// Two notify steps of one run get distinct names; without a step key the name stays unique.
func TestOutboundDeliveryNamePerStep(t *testing.T) {
	if a, b := outboundDeliveryName(stepContext("run-1:a")), outboundDeliveryName(stepContext("run-1:b")); a == b {
		t.Fatalf("two steps share the name %q", a)
	}
	ctx := workflowmodels.ContextWithWorkflowRequestID(context.Background(), "run-1")
	first, second := outboundDeliveryName(ctx), outboundDeliveryName(ctx)
	if !strings.HasPrefix(first, "run-1:") || first == second {
		t.Fatalf("names without a step key = %q, %q; want unique and run-prefixed", first, second)
	}
}

func okResponse(status int, body string) func(context.Context, string, map[string]string, map[string]interface{}) (*http.Response, error) {
	return func(context.Context, string, map[string]string, map[string]interface{}) (*http.Response, error) {
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}, nil
	}
}

// The worker sends only after winning the claim; a row the sweeper owns is skipped.
func TestProcessHTTPDeliverySkipsRowClaimedElsewhere(t *testing.T) {
	posted := 0
	svc := &OutboundDeliveryService{
		claimOne: func(context.Context, int64, time.Time) (workflowmodels.ClaimedOutboundDelivery, bool, error) {
			return workflowmodels.ClaimedOutboundDelivery{}, false, nil
		},
		postHTTP: func(context.Context, string, map[string]string, map[string]interface{}) (*http.Response, error) {
			posted++
			return nil, errors.New("unreachable")
		},
		now: fixedNow,
	}
	if _, err := svc.processHTTPDelivery(context.Background(), httpDeliveryJob{DeliveryID: 4, TargetRef: "https://example.com"}); err != nil {
		t.Fatalf("processHTTPDelivery: %v", err)
	}
	if posted != 0 {
		t.Fatal("a row claimed by someone else must not be sent")
	}
}

// Failed attempts back off exponentially; the last allowed attempt ends permanent_failed.
func TestProcessHTTPDeliveryBackoffAndExhaustion(t *testing.T) {
	cases := []struct {
		attempt    int
		wantStatus string
		wantNext   int64
	}{
		{1, workflowmodels.OutboundDeliveryStatusRetryableFailed, 1700000000 + 15},
		{2, workflowmodels.OutboundDeliveryStatusRetryableFailed, 1700000000 + 30},
		{3, workflowmodels.OutboundDeliveryStatusPermanentFailed, 0},
	}
	for _, tc := range cases {
		var last orm.RecordMap
		svc := &OutboundDeliveryService{
			claimOne: func(context.Context, int64, time.Time) (workflowmodels.ClaimedOutboundDelivery, bool, error) {
				return workflowmodels.ClaimedOutboundDelivery{ID: 4, Attempt: tc.attempt, MaxAttempts: 3}, true, nil
			},
			updateRecord: func(_ context.Context, _ orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
				last = values
				return &orm.UpdateResult{Acknowledged: true, ModifiedCount: 1}, nil
			},
			postHTTP:   okResponse(http.StatusBadGateway, "bad gateway"),
			now:        fixedNow,
			retryDelay: 15 * time.Second,
		}
		if _, err := svc.processHTTPDelivery(context.Background(), httpDeliveryJob{DeliveryID: 4, TargetRef: "https://example.com"}); err == nil {
			t.Fatalf("attempt %d: expected a failure", tc.attempt)
		}
		if last["status"] != tc.wantStatus || last["next_attempt_at"] != tc.wantNext || last["attempt_count"] != tc.attempt {
			t.Fatalf("attempt %d: update %#v, want status %s next %d", tc.attempt, last, tc.wantStatus, tc.wantNext)
		}
	}
	if got := outboundRetryBackoff(15*time.Second, 20); got != maxOutboundRetryDelay {
		t.Fatalf("backoff must be capped, got %s", got)
	}
}

// The sweeper sends what it claimed, with the Authorization resolved in the row's workflow and
// company scope, and gives the attempt back when the worker queue is full.
func TestSweepDueDeliveries(t *testing.T) {
	previous := credentialRefLookup
	t.Cleanup(func() { credentialRefLookup = previous })
	var lookedUp workflowmodels.WorkflowScope
	credentialRefLookup = func(ctx context.Context, param, ref string) (string, error) {
		if ref == "@cred:gone" {
			return "", errors.New("credential not found")
		}
		lookedUp, _ = workflowmodels.WorkflowScopeFromContext(ctx)
		return "Bearer s3cret", nil
	}
	workflowID, companyID := int64(9), int64(2)
	var submitted []httpDeliveryJob
	var updates []orm.RecordMap
	svc := &OutboundDeliveryService{
		claimDue: func(_ context.Context, opts workflowmodels.OutboundSweepOptions) ([]workflowmodels.ClaimedOutboundDelivery, error) {
			if opts.Limit != 20 {
				t.Fatalf("limit = %d", opts.Limit)
			}
			return []workflowmodels.ClaimedOutboundDelivery{
				{ID: 1, Attempt: 2, MaxAttempts: 3, TargetRef: "https://a", Headers: map[string]any{"X-A": "1"}, AuthorizationRef: "@cred:partner", WorkflowID: &workflowID, CompanyID: &companyID},
				{ID: 2, Attempt: 1, MaxAttempts: 3, TargetRef: "https://b", AuthorizationRef: "@cred:gone"},
				{ID: 3, Attempt: 1, MaxAttempts: 3, TargetRef: "https://c"},
			}, nil
		},
		submitJob: func(_ context.Context, job httpDeliveryJob) error {
			if job.DeliveryID == 3 {
				return errors.New("queue full")
			}
			submitted = append(submitted, job)
			return nil
		},
		updateRecord: func(_ context.Context, filter orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			values["id"] = filter["id"]
			updates = append(updates, values)
			return &orm.UpdateResult{Acknowledged: true, ModifiedCount: 1}, nil
		},
		now:        fixedNow,
		retryDelay: 15 * time.Second,
	}
	sent, err := svc.SweepDueDeliveries(context.Background(), 20)
	if err != nil || sent != 1 {
		t.Fatalf("sent=%d err=%v, want 1 row handed over", sent, err)
	}
	job := submitted[0]
	if job.DeliveryID != 1 || job.Attempt != 2 || job.Headers["Authorization"] != "Bearer s3cret" || job.Headers["X-A"] != "1" || job.Headers["Content-Type"] != "application/json" {
		t.Fatalf("job = %#v", job)
	}
	if lookedUp.WorkflowID != 9 || lookedUp.CompanyID == nil || *lookedUp.CompanyID != 2 {
		t.Fatalf("credential scope = %#v, want workflow 9 company 2", lookedUp)
	}
	if len(updates) != 2 {
		t.Fatalf("updates = %#v, want a failure for row 2 and a release for row 3", updates)
	}
	if updates[0]["id"] != int64(2) || updates[0]["status"] != workflowmodels.OutboundDeliveryStatusRetryableFailed {
		t.Fatalf("unresolvable credential update = %#v", updates[0])
	}
	if updates[1]["id"] != int64(3) || updates[1]["attempt_count"] != 0 || updates[1]["status"] != workflowmodels.OutboundDeliveryStatusRetryableFailed {
		t.Fatalf("release update = %#v, want the attempt given back", updates[1])
	}
}
