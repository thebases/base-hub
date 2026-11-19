package nodes

// File overview:
// - Responsibility: the condition@v1 node — evaluates a boolean expression against the
//   payload via the runtime EvaluateCondition hook and writes the result so downstream
//   branch_label/condition_step_key steps can decide whether to run.
// - Key symbols: `ConditionAction`.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/services/log"
)

// init registers this node's single definition (plan T93): canvas spec, catalog entry and
// param validation all derive from it.
func init() {
	registerNode(nodeDef{
		Code:        "condition",
		Version:     1,
		Sequence:    100,
		Label:       "Condition",
		Description: "Route execution to one of two paths based on a boolean expression evaluated against the current payload.",
		Category:    "Control",
		Params: []paramDef{
			{
				Name:     "expression",
				Label:    "Condition",
				Type:     "json",
				Required: true,
				Help:     `JSON condition object evaluated against the current payload. Leaf operators: exists, empty, equals, not_equals, in, not_in, gt, gte, lt, lte (numbers only), contains (substring or array element), regex (RE2, max 512 chars); combine with all/any/not. Example: {"field":"amount","gte":100}`,
				ExtraKey: "expression",
			},
			{Name: "true_label", Label: "Yes Label", Type: "string", Default: "Yes", ExtraKey: "true_label"},
			{Name: "false_label", Label: "No Label", Type: "string", Default: "No", ExtraKey: "false_label"},
		},
		// The catalog default is an empty object; a canvas default would prefill the required field.
		ExtraDefaults: map[string]any{"expression": map[string]any{}},
	}, ConditionAction)
}

// ConditionAction evaluates a boolean expression against the current payload
// and writes _condition_result into the output. Downstream steps with a
// branch_label of "yes" or "no" use this value to decide whether to run.
func ConditionAction(_ context.Context, req WorkflowActionRequest) ([]byte, error) {
	input, err := decodeJSONObject(req.Payload)
	if err != nil {
		log.Errorf("condition: %v", err)
		return nil, fmt.Errorf("condition: %w", err)
	}

	expression, hasExpr, exprErr := actionObjectParam(req.Step.Params, "expression", "expr")
	if exprErr != nil {
		log.Errorf("condition: expression must be a JSON object: %v", exprErr)
		return nil, fmt.Errorf("condition: expression must be a JSON object: %w", exprErr)
	}

	var result bool
	if !hasExpr || len(expression) == 0 {
		result = true
	} else {
		if workflowActionRuntimeHooks.EvaluateCondition == nil {
			log.Errorf("condition: evaluate hook is not initialized")
			return nil, fmt.Errorf("condition: evaluate hook is not initialized")
		}
		result, err = workflowActionRuntimeHooks.EvaluateCondition(expression, input)
		if err != nil {
			log.Errorf("condition: %v", err)
			return nil, fmt.Errorf("condition: %w", err)
		}
	}

	output := cloneJSONObject(input)
	output["_condition_result"] = result
	// Write a step-scoped result key so multiple condition nodes in the same workflow
	// do not overwrite each other's results. Downstream steps with condition_step_key
	// set will use _condition_result_<key> instead of the global _condition_result.
	if key := strings.TrimSpace(req.Step.Key); key != "" {
		output["_condition_result_"+key] = result
	}
	return json.Marshal(output)
}
