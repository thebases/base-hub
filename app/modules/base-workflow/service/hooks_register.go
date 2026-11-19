package service

// File overview:
// - Responsibility: registers base-workflow's kernel hooks so internal/ never imports this module.

import (
	"context"
	"errors"

	"git.thebase.vn/base_hub/internal/modules"
)

func init() {
	modules.RegisterWorkflowTopicExecutor(executeWorkflowTopic)
}

// workflowSentinels maps engine errors to the kernel sentinels callers of
// internal/services/etlflow branch on.
var workflowSentinels = []struct{ engine, kernel error }{
	{ErrFlowNotFound, modules.ErrWorkflowFlowNotFound},
	{ErrActionDefinitionNotFound, modules.ErrWorkflowActionDefinitionNotFound},
	{ErrActionNotSupported, modules.ErrWorkflowActionNotSupported},
	{ErrRunRetryScheduled, modules.ErrWorkflowRunRetryScheduled},
}

func executeWorkflowTopic(ctx context.Context, topic string, input []byte) error {
	return kernelWorkflowError(NewService().ExecuteTopic(ctx, topic, input))
}

// kernelWorkflowError keeps the engine error (message and chain) and adds the
// matching kernel sentinel, so errors.Is matches both.
func kernelWorkflowError(err error) error {
	if err == nil {
		return nil
	}
	for _, s := range workflowSentinels {
		if errors.Is(err, s.engine) {
			return &kernelSentinelError{err: err, sentinel: s.kernel}
		}
	}
	return err
}

type kernelSentinelError struct {
	err      error
	sentinel error
}

func (e *kernelSentinelError) Error() string   { return e.err.Error() }
func (e *kernelSentinelError) Unwrap() []error { return []error{e.err, e.sentinel} }
