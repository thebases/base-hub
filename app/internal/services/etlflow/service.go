package etlflow

// File overview:
// - Responsibility: kernel entry point for executing a topic's workflow flow (Kafka consumer, /wf
//   routes). The workflow engine itself lives in modules/base-workflow and is reached through the
//   internal/modules workflow hook, so internal/ never imports the module.
// - Key symbols: `NewService`, `Service.ExecuteTopic`, `LogFlowExecutionResult`, the `Err*` sentinels.

import (
	"context"

	"git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/services/log"
)

var (
	ErrFlowNotFound             = modules.ErrWorkflowFlowNotFound
	ErrActionDefinitionNotFound = modules.ErrWorkflowActionDefinitionNotFound
	ErrActionNotSupported       = modules.ErrWorkflowActionNotSupported
	ErrRunRetryScheduled        = modules.ErrWorkflowRunRetryScheduled
)

// Service executes flows through the registered workflow module.
type Service struct{}

// NewService constructs and returns the service handled by this package.
func NewService() *Service {
	return &Service{}
}

// ExecuteTopic runs the active flow bound to topic.
func (s *Service) ExecuteTopic(ctx context.Context, topic string, input []byte) error {
	return modules.ExecuteWorkflowTopic(ctx, topic, input)
}

// LogFlowExecutionResult logs the flow execution result handled by this package.
func LogFlowExecutionResult(topic string, err error) {
	if err != nil {
		log.Errorf("workflow execution failed: topic=%s err=%v", topic, err)
		return
	}
	log.Infof("workflow execution succeeded: topic=%s", topic)
}
