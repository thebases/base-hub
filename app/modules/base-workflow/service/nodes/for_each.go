package nodes

// File overview:
// - Responsibility: the for_each@v1 node (plan T103) — runs a sub-workflow once per element of a
//   payload array (through the same CallWorkflow hook as call_workflow@v1) with bounded
//   concurrency, and collects the child outputs into an array in input order.
// - Invariants: at most maxConcurrency children run at once (errgroup SetLimit); every child
//   inherits the step ctx, so cancel_run, max_run_seconds and the step timeout stop the remaining
//   items; with fail_fast the first failure cancels the siblings and fails the step.
// - Key symbols: `ForEachAction`.

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
	"golang.org/x/sync/errgroup"
)

const (
	forEachMaxConcurrency = 8
	forEachDefaultItems   = 100
	// forEachItemsCeiling bounds max_items itself: every item is a child run row.
	forEachItemsCeiling = 1000
)

func init() {
	registerNode(nodeDef{
		Code:        "for_each",
		Version:     1,
		Sequence:    130,
		Label:       "For Each",
		Description: "Run a published sub-workflow once per element of a payload array (bounded concurrency) and collect the outputs into an array in input order.",
		Category:    "Control",
		Params: []paramDef{
			{Name: "items_path", Label: "Items Path", Type: "string", Required: true, ExtraKey: "items_path",
				Help: "Dotted payload path of the array to iterate (for example order.lines). An object element is the child payload as is; any other element is sent as {\"item\": value}."},
			targetWorkflowParam,
			{Name: "max_concurrency", Label: "Max Concurrency", Type: "number", Default: 1, ExtraKey: "max_concurrency",
				Help: "Children running at once (1 to 8)."},
			{Name: "max_items", Label: "Max Items", Type: "number", Default: forEachDefaultItems, ExtraKey: "max_items",
				Help: "The step fails without retries when the array is longer (at most 1000)."},
			{Name: "fail_fast", Label: "Fail Fast", Type: "boolean", Default: true, ExtraKey: "fail_fast",
				Help: "On: the first failed item stops the others and fails the step. Off: every item runs and failures are collected as {\"_error\": ...} entries."},
			{Name: "output_field", Label: "Output Field", Type: "string", Default: "results", ExtraKey: "output_field",
				Help: "Payload key that receives the array of child outputs."},
		},
		Validate: validateForEachParams,
		// Children may run any node, so a dry run records the intent instead of calling them.
		SideEffect: effectScript,
	}, ForEachAction)
}

func validateForEachParams(values map[string]any) []workflowmodels.ParamIssue {
	var issues []workflowmodels.ParamIssue
	if n := intParamDefault(values, "max_concurrency", 1); n < 1 || n > forEachMaxConcurrency {
		issues = append(issues, workflowmodels.ParamIssue{Param: "max_concurrency", Message: "Max Concurrency must be between 1 and 8"})
	}
	if n := intParamDefault(values, "max_items", forEachDefaultItems); n < 1 || n > forEachItemsCeiling {
		issues = append(issues, workflowmodels.ParamIssue{Param: "max_items", Message: "Max Items must be between 1 and 1000"})
	}
	return issues
}

// forEachConfig is the resolved params of one for_each step.
type forEachConfig struct {
	targetID    int64
	items       []any
	concurrency int
	failFast    bool
	outputField string
}

// ForEachAction runs the target workflow per item and writes the outputs (input order) into the
// payload under output_field, plus `_for_each: {total, failed}`.
func ForEachAction(ctx context.Context, req WorkflowActionRequest) ([]byte, error) {
	input, err := decodeJSONObject(req.Payload)
	if err != nil {
		return nil, fmt.Errorf("for_each: %w", err)
	}
	cfg, err := forEachParams(req.Step.Params, input)
	if err != nil {
		return nil, fmt.Errorf("for_each: %w", err)
	}
	if workflowActionRuntimeHooks.CallWorkflow == nil {
		return nil, fmt.Errorf("for_each: call hook is not initialized")
	}
	results, failed, err := runForEachItems(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("for_each: %w", err)
	}
	output := cloneJSONObject(input)
	output[cfg.outputField] = results
	output["_for_each"] = map[string]any{"total": len(cfg.items), "failed": failed}
	return json.Marshal(output)
}

func forEachParams(params map[string]any, input map[string]any) (forEachConfig, error) {
	targetID, err := subWorkflowParams(params)
	if err != nil {
		return forEachConfig{}, err
	}
	path := strings.TrimSpace(firstStringParam(params, "items_path"))
	raw, found := lookupNestedBinding(input, path)
	items, isArray := raw.([]any)
	if path == "" || !found || !isArray {
		return forEachConfig{}, fmt.Errorf("items_path %q is not an array in the payload", path)
	}
	maxItems := min(max(intParamDefault(params, "max_items", forEachDefaultItems), 1), forEachItemsCeiling)
	if len(items) > maxItems {
		// The same payload always fails the same way: do not retry.
		return forEachConfig{}, fmt.Errorf("%d items exceed max_items %d: %w", len(items), maxItems, workflowmodels.ErrWorkflowStepNonRetryable)
	}
	outputField := strings.TrimSpace(firstStringParam(params, "output_field"))
	if outputField == "" {
		outputField = "results"
	}
	return forEachConfig{
		targetID:    targetID,
		items:       items,
		concurrency: min(max(intParamDefault(params, "max_concurrency", 1), 1), forEachMaxConcurrency),
		failFast:    boolParamDefault(params, "fail_fast", true),
		outputField: outputField,
	}, nil
}

// runForEachItems runs one child per item, at most cfg.concurrency at once. results[i] is item i's
// output, or {"_error": {...}} when fail_fast is off and it failed. A stopped step (cancel,
// deadline) returns the stop error: the remaining items never start.
func runForEachItems(ctx context.Context, cfg forEachConfig) ([]any, int, error) {
	results := make([]any, len(cfg.items))
	failures := make([]error, len(cfg.items))
	group, groupCtx := errgroup.WithContext(ctx)
	if !cfg.failFast {
		// Collect mode: one failure must not cancel its siblings; only the step ctx stops them.
		group, groupCtx = &errgroup.Group{}, ctx
	}
	group.SetLimit(cfg.concurrency)
	for i, item := range cfg.items {
		if groupCtx.Err() != nil {
			break
		}
		group.Go(func() error {
			// Go may have waited for a free slot: never start a child after the stop.
			if groupCtx.Err() != nil {
				return nil
			}
			output, err := callForEachItem(groupCtx, cfg.targetID, i, item)
			if err != nil {
				failures[i] = err
				if cfg.failFast {
					return fmt.Errorf("item %d: %w", i, err)
				}
				return nil
			}
			results[i] = output
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, 0, err
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, context.Cause(ctx)
	}
	failed := 0
	for i, err := range failures {
		if err != nil {
			failed++
			results[i] = map[string]any{"_error": map[string]any{"index": i, "message": err.Error()}}
		}
	}
	return results, failed, nil
}

func callForEachItem(ctx context.Context, targetID int64, index int, item any) (any, error) {
	payload, ok := item.(map[string]any)
	if !ok {
		payload = map[string]any{"item": item}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	output, err := workflowActionRuntimeHooks.CallWorkflow(ctx, targetID, encoded, "item"+strconv.Itoa(index))
	if err != nil {
		return nil, err
	}
	var decoded any
	if err := json.Unmarshal(output, &decoded); err != nil {
		return string(output), nil
	}
	return decoded, nil
}
