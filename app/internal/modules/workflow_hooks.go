package modules

// File overview:
// - Responsibility: inbound hook letting the workflow module execute a topic's flow for kernel
//   callers (Kafka consumer, internal/services/etlflow) without internal/ importing
//   modules/base-workflow.
// - Key symbols: `WorkflowTopicExecutor`, `RegisterWorkflowTopicExecutor`, `ExecuteWorkflowTopic`,
//   the `ErrWorkflow*` sentinels.

import (
	"context"
	"errors"
	"sync/atomic"
)

// The workflow module's executor wraps its own errors so errors.Is matches
// these sentinels (kernel callers branch on them) as well as its own.
var (
	ErrWorkflowFlowNotFound             = errors.New("flow not found")
	ErrWorkflowActionDefinitionNotFound = errors.New("action definition not found")
	ErrWorkflowActionNotSupported       = errors.New("action not supported")
	ErrWorkflowRunRetryScheduled        = errors.New("workflow run retry scheduled")
	// ErrWorkflowUnavailable: no workflow module registered an executor (module
	// not compiled in); it also matches ErrWorkflowFlowNotFound, since without
	// the module no topic has a flow.
	ErrWorkflowUnavailable = errors.Join(errors.New("workflow module is not available"), ErrWorkflowFlowNotFound)
)

// WorkflowTopicExecutor runs the active flow bound to topic with input.
type WorkflowTopicExecutor func(ctx context.Context, topic string, input []byte) error

var workflowTopicExecutor atomic.Pointer[WorkflowTopicExecutor]

// RegisterWorkflowTopicExecutor installs the executor (base-workflow calls it
// from init()). nil clears it.
func RegisterWorkflowTopicExecutor(fn WorkflowTopicExecutor) {
	if fn == nil {
		workflowTopicExecutor.Store(nil)
		return
	}
	workflowTopicExecutor.Store(&fn)
}

// ExecuteWorkflowTopic runs the registered executor, or returns
// ErrWorkflowUnavailable when none is registered.
func ExecuteWorkflowTopic(ctx context.Context, topic string, input []byte) error {
	fn := workflowTopicExecutor.Load()
	if fn == nil {
		return ErrWorkflowUnavailable
	}
	return (*fn)(ctx, topic, input)
}
