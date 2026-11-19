package kafkaconsumer

import (
	"errors"
	"fmt"
	"testing"

	"git.thebase.vn/base_hub/internal/services/etlflow"
)

// Plan T48: a scheduled workflow retry must not be retried again by the consumer.
func TestFlowHandlerResultTreatsScheduledRetryAsHandled(t *testing.T) {
	if err := flowHandlerResult(fmt.Errorf("%w: boom", etlflow.ErrRunRetryScheduled)); err != nil {
		t.Fatalf("scheduled retry returned %v, want nil", err)
	}
	failure := errors.New("dead letter")
	if err := flowHandlerResult(failure); !errors.Is(err, failure) {
		t.Fatalf("real failure returned %v", err)
	}
	if err := flowHandlerResult(nil); err != nil {
		t.Fatalf("success returned %v", err)
	}
}
