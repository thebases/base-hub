package nodes

// File overview:
// - Responsibility: the wait_approval@v1 node definition (plan T103). The linear engine never calls
//   this handler for a real run: it parks the run at the step (status waiting_approval, hashed
//   resume token, optional expiry) and continues it on base.workflow.run approve/reject. The
//   handler only runs where parking is impossible: a dry run (passes through, recording that the
//   wait was skipped) and a DAG run (refused: validate/publish already reject it).
//   The optional approver_group_id is copied onto the run when it parks (approval_group_id), so a
//   decision reads it from the run, never from a definition that may have been republished.
// - Key symbols: `WaitApprovalAction`, `MaxApprovalWaitSeconds`, `ApprovalGroupID`.

import (
	"context"
	"encoding/json"
	"fmt"

	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

// ApproverGroupParam is the optional tbl.groups id whose operators decide without the token. Its
// select options are filled per request by the /wf/node-specs route (withDynamicActionParams).
const ApproverGroupParam = workflowmodels.WaitApprovalGroupParam

// MaxApprovalWaitSeconds caps expires_in_seconds (30 days).
const MaxApprovalWaitSeconds = 30 * 24 * 3600

func init() {
	registerNode(nodeDef{
		Code:        workflowmodels.WaitApprovalActionCode,
		Version:     1,
		Sequence:    140,
		Label:       "Wait For Approval",
		Description: "Pause the run until an operator approves or rejects it (base.workflow.run approve/reject with the resume token). Approval continues with _approval {approved, by, comment} in the payload; rejection fails the run or follows on_error_step_key. Linear workflows only.",
		Category:    "Control",
		Params: []paramDef{
			{Name: "expires_in_seconds", Label: "Expires In Seconds", Type: "number", Default: 0, ExtraKey: "expires_in_seconds",
				Help: "The wait fails the run after this many seconds without a decision (0 = never; at most 30 days)."},
			{Name: ApproverGroupParam, Label: "Approver Group", Type: "select", ExtraKey: ApproverGroupParam, ValueType: valueTypeInt,
				Help: "Optional. Members of this group who hold the workflow operator role may approve or reject the waiting run without its resume token. The user who started the run still cannot approve it."},
		},
		Validate: validateWaitApprovalParams,
	}, WaitApprovalAction)
}

func validateWaitApprovalParams(values map[string]any) []workflowmodels.ParamIssue {
	if n := intParamDefault(values, "expires_in_seconds", 0); n < 0 || n > MaxApprovalWaitSeconds {
		return []workflowmodels.ParamIssue{{Param: "expires_in_seconds", Message: "Expires In Seconds must be between 0 and 2592000"}}
	}
	return nil
}

// ApprovalWaitSeconds reads a step's expires_in_seconds, clamped to 0..MaxApprovalWaitSeconds.
func ApprovalWaitSeconds(params map[string]any) int64 {
	return int64(min(max(intParamDefault(params, "expires_in_seconds", 0), 0), MaxApprovalWaitSeconds))
}

// ApprovalGroupID reads a step's approver_group_id; 0 when unset or not a positive id.
func ApprovalGroupID(params map[string]any) int64 {
	return int64(max(intParamDefault(params, ApproverGroupParam, 0), 0))
}

// WaitApprovalAction runs only in a dry run (pass through, marked) or a DAG run (refused).
func WaitApprovalAction(ctx context.Context, req WorkflowActionRequest) ([]byte, error) {
	if !workflowmodels.WorkflowDryRunFromContext(ctx) {
		return nil, fmt.Errorf("wait_approval: only linear workflows can wait for approval: %w", workflowmodels.ErrWorkflowStepNonRetryable)
	}
	input, err := decodeJSONObject(req.Payload)
	if err != nil {
		return nil, fmt.Errorf("wait_approval: %w", err)
	}
	output := cloneJSONObject(input)
	output["_approval"] = map[string]any{"approved": true, "by": 0, "comment": "dry run: approval wait skipped"}
	return json.Marshal(output)
}
