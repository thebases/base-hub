package models

// File overview:
// - Responsibility: carries a workflow step's idempotency key on the handler ctx (plan T56), so
//   nodes (post_output_object, notify, lua_script) can read it without the engine type.
// - Key symbols: `ContextWithWorkflowStepIdempotencyKey`, `WorkflowStepIdempotencyKeyFromContext`.

import (
	"context"
	"strings"
)

type workflowStepIdempotencyKeyContextKey struct{}

// ContextWithWorkflowStepIdempotencyKey binds key (`<run name>:<step key>`, the same for every
// attempt of the step in one run). An empty key leaves ctx unchanged.
func ContextWithWorkflowStepIdempotencyKey(ctx context.Context, key string) context.Context {
	key = strings.TrimSpace(key)
	if key == "" {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, workflowStepIdempotencyKeyContextKey{}, key)
}

// WorkflowStepIdempotencyKeyFromContext returns the key bound by the engine for the running step,
// or "" outside a step (or for a run without a name).
func WorkflowStepIdempotencyKeyFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	key, _ := ctx.Value(workflowStepIdempotencyKeyContextKey{}).(string)
	return key
}
