package models

// File overview:
// - Responsibility: defines the `workflow` module model contract for request context, including field metadata, runtime methods, and module-specific invariants.
// - Key symbols: `ContextWithWorkflowRequestID`, `WorkflowRequestIDFromContext`, `workflowRequestIDContextKey`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"context"
	"strings"
	"sync"
)

type workflowRequestIDContextKey struct{}
type workflowTriggerTypeContextKey struct{}
type workflowOutputSinkContextKey struct{}

const (
	WorkflowTriggerTypeWebhook    = "webhook"
	WorkflowTriggerTypeKafka      = "kafka"
	WorkflowTriggerTypeMQTT       = "mqtt"
	WorkflowTriggerTypeModel      = "model"
	WorkflowTriggerTypeTCP        = "tcp"
	WorkflowTriggerTypeManualTest = "manual_test"
)

// Model-trigger operations: mirror the orm.RecordEvent* constants (kept as
// separate string literals here — models package must not import orm's
// internal event names to stay decoupled — but the values must match exactly
// since they are compared 1:1 against the event name orm.fireRecordEvent
// dispatches).
const (
	WorkflowModelTriggerBeforeCreate = "before_create"
	WorkflowModelTriggerAfterCreate  = "after_create"
	WorkflowModelTriggerBeforeWrite  = "before_write"
	WorkflowModelTriggerAfterWrite   = "after_write"
	WorkflowModelTriggerBeforeUnlink = "before_unlink"
	WorkflowModelTriggerAfterUnlink  = "after_unlink"
)

// ContextWithWorkflowRequestID binds one workflow-scoped request ID so every
// model write in the same lifecycle can reuse the same durable correlation key.
func ContextWithWorkflowRequestID(ctx context.Context, requestID string) context.Context {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, workflowRequestIDContextKey{}, requestID)
}

func WorkflowRequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	requestID, _ := ctx.Value(workflowRequestIDContextKey{}).(string)
	return strings.TrimSpace(requestID)
}

// EnsureWorkflowRequestID ensures the workflow request id handled by this package.
func EnsureWorkflowRequestID(ctx context.Context) (context.Context, string) {
	if requestID := WorkflowRequestIDFromContext(ctx); requestID != "" {
		return ctx, requestID
	}
	requestID := uuid4String()
	return ContextWithWorkflowRequestID(ctx, requestID), requestID
}

// ContextWithWorkflowTriggerType binds the trigger source to the workflow context
// so audit rows can persist which transport started the run.
func ContextWithWorkflowTriggerType(ctx context.Context, triggerType string) context.Context {
	triggerType = strings.TrimSpace(triggerType)
	if triggerType == "" {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, workflowTriggerTypeContextKey{}, triggerType)
}

// WorkflowTriggerTypeFromContext returns the trigger source carried by the workflow context.
func WorkflowTriggerTypeFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	triggerType, _ := ctx.Value(workflowTriggerTypeContextKey{}).(string)
	return strings.TrimSpace(triggerType)
}

// EnsureWorkflowTriggerType binds defaultTriggerType only when the context does
// not already carry one, so callers that need a non-default trigger source
// (e.g. a manual test run) can set it before handing the context to a code
// path that would otherwise stamp its own default.
func EnsureWorkflowTriggerType(ctx context.Context, defaultTriggerType string) context.Context {
	if triggerType := WorkflowTriggerTypeFromContext(ctx); triggerType != "" {
		return ctx
	}
	return ContextWithWorkflowTriggerType(ctx, defaultTriggerType)
}

// WorkflowOutput receives the final payload of a successful run for callers that need a synchronous
// answer (the tcp trigger writes it back as the reply). Triggers that do not bind a sink are unaffected.
type WorkflowOutput struct {
	mu      sync.Mutex
	payload []byte
	set     bool
}

// Set stores a copy of payload as the run's final output.
func (o *WorkflowOutput) Set(payload []byte) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.payload, o.set = append([]byte(nil), payload...), true
	o.mu.Unlock()
}

// Get returns the stored output and whether a run completed successfully into this sink.
func (o *WorkflowOutput) Get() ([]byte, bool) {
	if o == nil {
		return nil, false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]byte(nil), o.payload...), o.set
}

// ContextWithWorkflowOutputSink binds sink so the engine copies the run's final payload into it.
func ContextWithWorkflowOutputSink(ctx context.Context, sink *WorkflowOutput) context.Context {
	if sink == nil {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, workflowOutputSinkContextKey{}, sink)
}

// ContextWithoutWorkflowOutputSink hides any bound sink: a sub-workflow run (plan T103) must never
// answer its caller's synchronous (tcp) reply with its own output.
func ContextWithoutWorkflowOutputSink(ctx context.Context) context.Context {
	if WorkflowOutputSinkFromContext(ctx) == nil {
		return ctx
	}
	return context.WithValue(ctx, workflowOutputSinkContextKey{}, (*WorkflowOutput)(nil))
}

// WorkflowOutputSinkFromContext returns the bound sink, or nil.
func WorkflowOutputSinkFromContext(ctx context.Context) *WorkflowOutput {
	if ctx == nil {
		return nil
	}
	sink, _ := ctx.Value(workflowOutputSinkContextKey{}).(*WorkflowOutput)
	return sink
}
