package libs

// Plan T40: outbound rows store the credential reference, not the resolved Authorization header,
// and a manual retry resolves the reference again.

import (
	"context"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

func TestQueueHTTPJSONStoresAuthorizationRefNotHeader(t *testing.T) {
	var created workflowmodels.OutboundDeliveryRecord
	var submitted httpDeliveryJob
	svc := &OutboundDeliveryService{
		createRecord: func(_ context.Context, record workflowmodels.OutboundDeliveryRecord) (*orm.InsertResult, error) {
			created = record
			return &orm.InsertResult{InsertedID: int64(7), Acknowledged: true}, nil
		},
		submitJob: func(_ context.Context, job httpDeliveryJob) error {
			submitted = job
			return nil
		},
		now:        func() time.Time { return time.Unix(1700000000, 0).UTC() },
		retryDelay: time.Minute,
	}

	headers := map[string]string{"Authorization": "Bearer s3cret", "X-Trace": "1"}
	if err := svc.QueueHTTPJSON(context.Background(), "t", "https://example.com/hook", headers, "@syscred:partner", nil); err != nil {
		t.Fatalf("queue: %v", err)
	}
	if _, ok := created.Headers["Authorization"]; ok {
		t.Fatalf("stored headers keep the resolved Authorization: %#v", created.Headers)
	}
	if created.Headers["X-Trace"] != "1" {
		t.Fatalf("stored headers lost other headers: %#v", created.Headers)
	}
	if created.AuthorizationRef != "@syscred:partner" {
		t.Fatalf("authorization_ref = %q, want @syscred:partner", created.AuthorizationRef)
	}
	if submitted.Headers["Authorization"] != "Bearer s3cret" {
		t.Fatalf("queued job must still send the header, got %#v", submitted.Headers)
	}
}

func TestManualRetryResolvesAuthorizationRef(t *testing.T) {
	prev := credentialRefLookup
	var gotParam, gotRef string
	credentialRefLookup = func(_ context.Context, param, ref string) (string, error) {
		gotParam, gotRef = param, ref
		return "Bearer fresh", nil
	}
	t.Cleanup(func() { credentialRefLookup = prev })

	var submitted httpDeliveryJob
	svc := &OutboundDeliveryService{
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
	err := svc.ManualRetry(context.Background(), ManualRetryRequest{
		DeliveryID:       9,
		TargetRef:        "https://example.com/hook",
		Headers:          map[string]string{"X-Trace": "1"},
		AuthorizationRef: "@syscred:partner",
	})
	if err != nil {
		t.Fatalf("manual retry: %v", err)
	}
	if gotParam != "authorization" || gotRef != "@syscred:partner" {
		t.Fatalf("lookup(%q, %q), want (authorization, @syscred:partner)", gotParam, gotRef)
	}
	if submitted.Headers["Authorization"] != "Bearer fresh" || submitted.Headers["X-Trace"] != "1" {
		t.Fatalf("retry headers = %#v", submitted.Headers)
	}
}

func TestAuthoredCredentialRefs(t *testing.T) {
	refs := AuthoredCredentialRefs(map[string]any{
		"authorization": " @syscred:partner ",
		"key":           "@cred:k",
		"url":           "https://example.com",
		"headers":       map[string]any{"X": "@cred:ignored"},
	})
	if len(refs) != 2 || refs["authorization"] != "@syscred:partner" || refs["key"] != "@cred:k" {
		t.Fatalf("refs = %#v", refs)
	}
	if AuthoredCredentialRefs(map[string]any{"url": "x"}) != nil {
		t.Fatalf("expected nil without refs")
	}
}
