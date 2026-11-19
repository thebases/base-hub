package models

import (
	"context"
	"testing"
)

func TestAuthorizeWorkflowRunChannel(t *testing.T) {
	prev := workflowTopicReadable
	t.Cleanup(func() { workflowTopicReadable = prev })
	workflowTopicReadable = func(_ context.Context, topic string) (bool, error) { return topic == "orders", nil }

	ctx := context.Background()
	if ok, err := authorizeWorkflowRunChannel(ctx, 5, "workflow:run:orders"); err != nil || !ok {
		t.Fatalf("readable workflow: ok=%v err=%v, want allowed", ok, err)
	}
	if ok, _ := authorizeWorkflowRunChannel(ctx, 5, "workflow:run:payroll"); ok {
		t.Fatal("unreadable workflow must be denied")
	}
	if ok, _ := authorizeWorkflowRunChannel(ctx, 0, "workflow:run:orders"); ok {
		t.Fatal("anonymous caller must be denied")
	}
	if ok, _ := authorizeWorkflowRunChannel(ctx, 5, "workflow:run:"); ok {
		t.Fatal("empty topic must be denied")
	}
}
