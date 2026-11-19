package libs

// File overview:
// - Responsibility: plan T46 for outbound deliveries: the row is written in the caller's database,
//   the queued job carries that database, and the detached worker writes back into it (or does not
//   send at all when the database cannot be bound).

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

func tenantContext(dbName string) context.Context {
	return orm.ContextWithEnvironment(context.Background(), orm.NewEnvironmentWithDBName(nil, dbName, 7, nil, false))
}

func TestQueueHTTPJSONKeepsTenantDatabase(t *testing.T) {
	var createdIn string
	var submitted httpDeliveryJob
	svc := &OutboundDeliveryService{
		createRecord: func(ctx context.Context, _ workflowmodels.OutboundDeliveryRecord) (*orm.InsertResult, error) {
			createdIn = orm.EnvironmentFromContext(ctx).DBName()
			return &orm.InsertResult{InsertedID: int64(3), Acknowledged: true}, nil
		},
		submitJob: func(_ context.Context, job httpDeliveryJob) error {
			submitted = job
			return nil
		},
		now: func() time.Time { return time.Unix(1700000000, 0).UTC() },
	}
	if err := svc.QueueHTTPJSON(tenantContext("tenant_a"), "t", "https://example.com/hook", nil, "", nil); err != nil {
		t.Fatalf("QueueHTTPJSON: %v", err)
	}
	if createdIn != "tenant_a" || submitted.DBName != "tenant_a" {
		t.Fatalf("row created in %q, job db %q; want tenant_a for both", createdIn, submitted.DBName)
	}

	var retried httpDeliveryJob
	svc.submitJob = func(_ context.Context, job httpDeliveryJob) error { retried = job; return nil }
	svc.updateRecord = func(ctx context.Context, _ orm.RecordMap, _ orm.RecordMap) (*orm.UpdateResult, error) {
		if got := orm.EnvironmentFromContext(ctx).DBName(); got != "tenant_b" {
			t.Fatalf("manual retry wrote to %q, want tenant_b", got)
		}
		return &orm.UpdateResult{Acknowledged: true, ModifiedCount: 1}, nil
	}
	if err := svc.ManualRetry(tenantContext("tenant_b"), ManualRetryRequest{DeliveryID: 9, TargetRef: "https://example.com/r"}); err != nil {
		t.Fatalf("ManualRetry: %v", err)
	}
	if retried.DBName != "tenant_b" {
		t.Fatalf("retry job db = %q, want tenant_b", retried.DBName)
	}
}

func TestProcessHTTPDeliveryWritesIntoJobDatabase(t *testing.T) {
	previous := outboundDeliveryDBContext
	t.Cleanup(func() { outboundDeliveryDBContext = previous })
	outboundDeliveryDBContext = func(parent context.Context, dbName string) (context.Context, error) {
		if dbName == "gone" {
			return nil, errors.New("database is not available")
		}
		return tenantContext(dbName), nil
	}

	var writtenIn []string
	posted := 0
	svc := &OutboundDeliveryService{
		updateRecord: func(ctx context.Context, _ orm.RecordMap, _ orm.RecordMap) (*orm.UpdateResult, error) {
			writtenIn = append(writtenIn, orm.EnvironmentFromContext(ctx).DBName())
			return &orm.UpdateResult{Acknowledged: true, ModifiedCount: 1}, nil
		},
		postHTTP: func(context.Context, string, map[string]string, map[string]interface{}) (*http.Response, error) {
			posted++
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
		},
		now: func() time.Time { return time.Unix(1700000000, 0).UTC() },
	}

	if _, err := svc.processHTTPDelivery(context.Background(), httpDeliveryJob{DeliveryID: 1, TargetRef: "https://example.com", DBName: "tenant_a"}); err != nil {
		t.Fatalf("processHTTPDelivery: %v", err)
	}
	if len(writtenIn) != 2 || writtenIn[0] != "tenant_a" || writtenIn[1] != "tenant_a" {
		t.Fatalf("delivery rows written in %v, want sending + delivered in tenant_a", writtenIn)
	}

	writtenIn, posted = nil, 0
	if _, err := svc.processHTTPDelivery(context.Background(), httpDeliveryJob{DeliveryID: 2, TargetRef: "https://example.com", DBName: "gone"}); err == nil {
		t.Fatalf("expected an error when the delivery database cannot be bound")
	}
	if posted != 0 || len(writtenIn) != 0 {
		t.Fatalf("unbound database: posted=%d writes=%v, want nothing", posted, writtenIn)
	}
}
