package models

// Service-bus channel authorization for workflow run events (plan T16).
//
// The canvas subscribes to "workflow:run:<topic>" for live step status. base-bus does not know
// workflows, so this module vouches for the channel: the caller may receive it when they can read
// the workflow with that topic (model ACL plus record rules, under the caller's environment).

import (
	"context"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	busmodels "git.thebase.vn/base_hub/modules/base-bus/models"
)

const workflowRunChannelPrefix = "workflow:run:"

// workflowTopicReadable is a test seam over the ACL-checked count query.
var workflowTopicReadable = canReadWorkflowTopic

func init() {
	busmodels.RegisterServiceBusChannelAuthorizer(workflowRunChannelPrefix, authorizeWorkflowRunChannel)
}

func authorizeWorkflowRunChannel(ctx context.Context, userID int64, channel string) (bool, error) {
	topic := strings.TrimSpace(strings.TrimPrefix(channel, workflowRunChannelPrefix))
	if userID <= 0 || topic == "" {
		return false, nil
	}
	return workflowTopicReadable(ctx, topic)
}

// canReadWorkflowTopic counts the workflows with this topic the caller can see. The model ACL is
// checked explicitly because orm.ExecuteRuntimeMethod (server-side dispatch) does not run it;
// search_count applies the record rules for the caller's environment.
func canReadWorkflowTopic(ctx context.Context, topic string) (bool, error) {
	meta, ok := orm.LookupModel(FlowModelName)
	if !ok {
		return false, nil
	}
	method, ok := meta.LookupRuntimeMethod("search_count")
	if !ok {
		return false, nil
	}
	if err := orm.EnsureRuntimeModelAccess(ctx, meta, method, nil); err != nil {
		return false, nil
	}
	result, err := orm.ExecuteRuntimeMethod(ctx, FlowModelName, "search_count", nil,
		[]any{[]any{[]any{"topic", "=", topic}}}, nil)
	if err != nil {
		return false, err
	}
	switch count := result.(type) {
	case int:
		return count > 0, nil
	case int64:
		return count > 0, nil
	default:
		return false, nil
	}
}
