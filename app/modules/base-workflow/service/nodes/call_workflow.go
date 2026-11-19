package nodes

// File overview:
// - Responsibility: the call_workflow@v1 node (plan T103) — runs another workflow's published
//   snapshot inline as a child run and continues with the child's final payload. The engine owns
//   the call itself (CallWorkflow hook: depth limit, recursion guard, parent_run_id, cancel and
//   deadline inheritance); this node only reads its params.
// - Key symbols: `CallWorkflowAction`, `targetWorkflowParam`.

import (
	"context"
	"fmt"
	"strings"
)

// targetWorkflowParam is the target picker shared by call_workflow and for_each. Its options are
// the published workflows, filled per request by the /wf/node-specs route (withDynamicActionParams).
var targetWorkflowParam = paramDef{
	Name:      "target_workflow_id",
	Label:     "Target Workflow",
	Type:      "select",
	Required:  true,
	Help:      "Published workflow to run as a sub-workflow. It runs its published version in this database under the same identity.",
	ExtraKey:  "target_workflow_id",
	ValueType: valueTypeInt,
}

func init() {
	registerNode(nodeDef{
		Code:        "call_workflow",
		Version:     1,
		Sequence:    120,
		Label:       "Call Workflow",
		Description: "Run another published workflow with the current payload as a child run and continue with its final payload. At most 5 nested calls; a workflow may not call itself through the chain.",
		Category:    "Control",
		Params: []paramDef{
			targetWorkflowParam,
			{Name: "wait", Label: "Wait", Type: "select", Default: "sync", ExtraKey: "wait",
				Help:    "sync = wait for the child run and use its output (the only mode for now).",
				Options: []paramOption{{Value: "sync", Label: "Wait For Result"}}},
			{Name: "payload_mapping", Label: "Payload Mapping", Type: "select", Default: "pass_through", ExtraKey: "payload_mapping",
				Help:    "pass_through = the child receives the current payload as is.",
				Options: []paramOption{{Value: "pass_through", Label: "Pass Through"}}},
		},
		// The child may run any node, so a dry run records the intent instead of calling it.
		SideEffect: effectScript,
	}, CallWorkflowAction)
}

// CallWorkflowAction runs the target workflow and returns its final payload.
func CallWorkflowAction(ctx context.Context, req WorkflowActionRequest) ([]byte, error) {
	targetID, err := subWorkflowParams(req.Step.Params)
	if err != nil {
		return nil, fmt.Errorf("call_workflow: %w", err)
	}
	if workflowActionRuntimeHooks.CallWorkflow == nil {
		return nil, fmt.Errorf("call_workflow: call hook is not initialized")
	}
	output, err := workflowActionRuntimeHooks.CallWorkflow(ctx, targetID, req.Payload, "call")
	if err != nil {
		return nil, fmt.Errorf("call_workflow: %w", err)
	}
	return output, nil
}

// subWorkflowParams reads the target id and refuses modes this version does not implement.
func subWorkflowParams(params map[string]any) (int64, error) {
	targetID := int64(intParamDefault(params, "target_workflow_id", 0))
	if targetID <= 0 {
		return 0, fmt.Errorf("select a target workflow (target_workflow_id)")
	}
	if wait := strings.TrimSpace(firstStringParam(params, "wait")); wait != "" && wait != "sync" {
		return 0, fmt.Errorf("unsupported wait mode %q (only sync)", wait)
	}
	if mapping := strings.TrimSpace(firstStringParam(params, "payload_mapping")); mapping != "" && mapping != "pass_through" {
		return 0, fmt.Errorf("unsupported payload_mapping %q (only pass_through)", mapping)
	}
	return targetID, nil
}
