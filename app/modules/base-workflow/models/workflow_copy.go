package models

// File overview:
// - Responsibility: cross-model orchestration for `base.workflow` that reaches into
//   `base.workflow.action`/`base.workflow.trigger` on the workflow's behalf — duplicating a
//   workflow with its children (`copy`/`copy_data`) and replacing a workflow's whole action
//   sequence in one call (`replace_action_sequence`, used by the canvas save path). Split out
//   of `base_workflow.go` so the `FlowRecord` model file stays limited to persistence/CRUD for
//   its own table.
// - Key symbols: `copyFlow`, `copyDataFlow`, `replaceActionSequenceFlow`, `replaceWorkflowEdgesFlow`.
// - Pattern: every mutation runs in one transaction locked on the workflow row (workflow_tx.go,
//   plan T59): copy locks the source, the replace methods lock the target, check the optional
//   expected_revision and bump revision. Copy creates the workflow, its actions, its DAG edges and
//   only then its triggers; non-webhook triggers are copied inactive (plan T61).

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

type flowCopyPlan struct {
	SourceWorkflowID int64
	CreateValues     orm.RecordMap
}

// copyDataFlow prepares duplicate-safe create payloads so the copied workflow
// parent can be created first and then receive recreated child rows.
func copyDataFlow(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	plans, err := buildFlowCopyPlans(ctx, model, ids, args, kwargs)
	if err != nil {
		return nil, err
	}
	out := make([]orm.RecordMap, 0, len(plans))
	for _, plan := range plans {
		out = append(out, cloneRecordMap(plan.CreateValues))
	}
	if len(out) == 1 {
		return out[0], nil
	}
	return out, nil
}

// copyFlow creates the duplicated workflow first, then copies actions, and
// only then copies triggers so a failed action copy never leaves a live trigger
// pointing at a half-built workflow graph.
func copyFlow(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	plans, err := buildFlowCopyPlans(ctx, model, ids, args, kwargs)
	if err != nil {
		return nil, err
	}
	created := make([]orm.RecordMap, 0, len(plans))
	for _, plan := range plans {
		// One transaction per copy, locked on the source so a concurrent canvas save cannot
		// change the steps half way through the copy; a failure leaves no partial workflow.
		var record orm.RecordMap
		err := runInWorkflowTx(ctx, plan.SourceWorkflowID, func(txCtx context.Context, _ int64) error {
			var err error
			record, err = createCopiedFlow(txCtx, model, plan)
			return err
		})
		if err != nil {
			return nil, err
		}
		created = append(created, record)
	}
	if len(created) == 1 {
		return created[0], nil
	}
	return created, nil
}

func buildFlowCopyPlans(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) ([]flowCopyPlan, error) {
	if len(ids) == 0 {
		log.Errorf("base.workflow.copy_data requires ids")
		return nil, orm.InvalidParamsf("base.workflow.copy_data requires ids")
	}
	defaults, err := recordMapFromArgs(args, kwargs, "default", "defaults")
	if err != nil {
		return nil, err
	}
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(FlowModelName, model), ids, nil)
	if err != nil {
		return nil, err
	}
	plans := make([]flowCopyPlan, 0, len(rows))
	for _, row := range rows {
		values := cloneRecordMap(row)
		delete(values, "id")
		delete(values, "created_at")
		delete(values, "updated_at")
		delete(values, "flow_map")
		delete(values, "action_ids")
		delete(values, "run_ids")
		delete(values, "revision")
		for key, value := range defaults {
			values[key] = value
		}
		// Workflow name/topic are unique, so the duplicate must get fresh values
		// unless the caller explicitly overrides them.
		if _, exists := defaults["name"]; !exists {
			values["name"] = workflowCopyUniqueText(row["name"], "workflow")
		}
		if _, exists := defaults["topic"]; !exists {
			values["topic"] = workflowCopyUniqueText(row["topic"], "workflow")
		}
		plans = append(plans, flowCopyPlan{
			SourceWorkflowID: int64Value(row["id"]),
			CreateValues:     values,
		})
	}
	return plans, nil
}

func createCopiedFlow(ctx context.Context, model *orm.ModelMetadata, plan flowCopyPlan) (orm.RecordMap, error) {
	createdAny, err := createFlow(ctx, model, nil, []any{cloneRecordMap(plan.CreateValues)}, nil)
	if err != nil {
		return nil, err
	}
	created, ok := createdAny.(orm.RecordMap)
	if !ok {
		return nil, fmt.Errorf("base.workflow.copy expected one created record, got %T", createdAny)
	}
	targetWorkflowID := int64Value(created["id"])
	if err := copyWorkflowActionsToNewFlow(ctx, plan.SourceWorkflowID, targetWorkflowID); err != nil {
		return nil, err
	}
	if err := copyWorkflowEdgesToNewFlow(ctx, plan.SourceWorkflowID, targetWorkflowID); err != nil {
		return nil, err
	}
	if err := copyWorkflowTriggersToNewFlow(ctx, plan.SourceWorkflowID, targetWorkflowID, stringValue(created["topic"])); err != nil {
		return nil, err
	}
	return created, nil
}

func copyWorkflowActionsToNewFlow(ctx context.Context, sourceWorkflowID int64, targetWorkflowID int64) error {
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WorkflowActionModelName, nil), orm.RecordMap{
		"workflow_id": sourceWorkflowID,
		"__order":     "sequence asc, id asc",
	}, nil)
	if err != nil {
		return err
	}
	for _, row := range rows {
		values := cloneWorkflowActionCopyValues(row, targetWorkflowID)
		if _, err := createWorkflowAction(ctx, nil, nil, []any{values}, nil); err != nil {
			return err
		}
	}
	return nil
}

func cloneWorkflowActionCopyValues(row orm.RecordMap, workflowID int64) orm.RecordMap {
	values := cloneRecordMap(row)
	delete(values, "id")
	delete(values, "created_at")
	delete(values, "updated_at")
	values["workflow_id"] = workflowID
	return values
}

// copyWorkflowEdgesToNewFlow recreates the source's DAG edges on the copy (plan T61); without them
// a DAG workflow copy ran with no dependencies.
func copyWorkflowEdgesToNewFlow(ctx context.Context, sourceWorkflowID int64, targetWorkflowID int64) error {
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WorkflowEdgeModelName, nil), orm.RecordMap{
		"workflow_id": sourceWorkflowID,
		"__order":     "id asc",
	}, nil)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := createWorkflowEdge(ctx, nil, nil, []any{workflowEdgeCopyValues(row, targetWorkflowID)}, nil); err != nil {
			return err
		}
	}
	return nil
}

// workflowEdgeCopyValues keeps only the portable edge fields (company follows the new workflow).
func workflowEdgeCopyValues(row orm.RecordMap, workflowID int64) orm.RecordMap {
	return orm.RecordMap{
		"workflow_id":    workflowID,
		"from_step_key":  strings.TrimSpace(stringValue(row["from_step_key"])),
		"to_step_key":    strings.TrimSpace(stringValue(row["to_step_key"])),
		"edge_type":      strings.TrimSpace(stringValue(row["edge_type"])),
		"condition_expr": stringValue(row["condition_expr"]),
	}
}

func copyWorkflowTriggersToNewFlow(ctx context.Context, sourceWorkflowID int64, targetWorkflowID int64, targetTopic string) error {
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WorkflowTriggerModelName, nil), orm.RecordMap{
		"workflow_id": sourceWorkflowID,
		"__order":     "id asc",
	}, nil)
	if err != nil {
		return err
	}
	for _, row := range rows {
		values := cloneWorkflowTriggerCopyValues(row, targetWorkflowID, targetTopic)
		if _, err := createWorkflowTrigger(ctx, nil, nil, []any{values}, nil); err != nil {
			return err
		}
	}
	return nil
}

func cloneWorkflowTriggerCopyValues(row orm.RecordMap, workflowID int64, targetTopic string) orm.RecordMap {
	values := cloneRecordMap(row)
	delete(values, "id")
	delete(values, "created_at")
	delete(values, "updated_at")
	values["workflow_id"] = workflowID
	// Webhook routing follows the workflow topic, so the duplicated webhook
	// trigger must point at the new workflow topic instead of the source one.
	if strings.EqualFold(stringValue(values["trigger_type"]), WorkflowTriggerTypeWebhook) && strings.TrimSpace(targetTopic) != "" {
		values["topic"] = strings.TrimSpace(targetTopic)
	}
	// Every non-webhook trigger is created inactive (plan T61): a copied MQTT/Kafka trigger would
	// consume the same topic/group as its source (each message processed twice), a model trigger
	// would fire both workflows and a tcp trigger would collide on its port. The operator reviews
	// the copy and enables it. A webhook follows the new, unique topic, so it stays as it was.
	delete(values, "company_id")
	if !strings.EqualFold(stringValue(values["trigger_type"]), WorkflowTriggerTypeWebhook) {
		values["is_active"] = false
	}
	return values
}

func workflowCopyUniqueText(raw any, fallback string) string {
	base := strings.TrimSpace(stringValue(raw))
	if base == "" {
		base = strings.TrimSpace(fallback)
	}
	if base == "" {
		base = "workflow"
	}
	suffix := strings.ReplaceAll(uuid4String(), "-", "")
	if len(suffix) > 8 {
		suffix = suffix[:8]
	}
	return fmt.Sprintf("%s-copy-%s", base, suffix)
}

// replaceActionSequenceFlow replaces a workflow's whole ordered action sequence (canvas save, MCP,
// generate-from-markdown). In one transaction locked on the workflow (plan T59): optional
// expected_revision check, unlink + recreate the actions, drop the edges of steps that are gone
// (plan T62), write the optional layout, regenerate flow_map, bump revision. Returns
// {"ok": true, "revision": <new revision>}.
func replaceActionSequenceFlow(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	const method = "base.workflow.replace_action_sequence"
	workflowID, ok := workflowIDFromMethodCall(ids, args, kwargs)
	if !ok || workflowID <= 0 {
		log.Errorf("%s requires a valid workflow id", method)
		return nil, orm.InvalidParamsf("%s requires a valid workflow id", method)
	}
	expected, err := expectedRevisionFromKwargs(method, kwargs)
	if err != nil {
		return nil, err
	}
	layout, hasLayout, err := workflowLayoutFromKwargs(method, kwargs)
	if err != nil {
		return nil, err
	}

	items, err := workflowActionValuesListFromArgs(args, kwargs)
	if err != nil {
		return nil, err
	}

	normalizedItems := make([]orm.RecordMap, 0, len(items))
	explicitKeys := make([]bool, 0, len(items))
	for index, item := range items {
		values := cloneRecordMap(item)
		values["workflow_id"] = workflowID
		// Array order is the execution order when an item carries no sequence of its own.
		if _, exists := values["sequence"]; !exists {
			values["sequence"] = int64(index+1) * 10
		}
		explicitKeys = append(explicitKeys, explicitStepKey(values))
		normalized, err := normalizeWorkflowActionValues(ctx, values, nil, true)
		if err != nil {
			return nil, err
		}
		normalizedItems = append(normalizedItems, normalized)
	}
	if err := assignUniqueStepKeys(method, normalizedItems, explicitKeys); err != nil {
		return nil, err
	}

	// model here is the dispatcher-resolved metadata for base.workflow (the
	// calling model), not base.workflow.action — passing it into RuntimeModelRef
	// would make it win over WorkflowActionModelName and route these calls at
	// the wrong table (flow_record instead of the action table). Pass nil so
	// RuntimeModelRef falls back to a registry lookup keyed by name.
	actionModel := orm.RuntimeModelRef(WorkflowActionModelName, nil)

	revision, err := runWorkflowMutation(ctx, method, workflowID, expected, func(txCtx context.Context) error {
		existingRows, err := orm.RuntimeRepositorySearchRead(txCtx, actionModel, orm.RecordMap{
			"workflow_id": workflowID,
			"__order":     "sequence asc, id asc",
		}, []string{"id"})
		if err != nil {
			return err
		}
		existingIDs := make([]string, 0, len(existingRows))
		for _, row := range existingRows {
			if id := int64Value(row["id"]); id > 0 {
				existingIDs = append(existingIDs, strconv.FormatInt(id, 10))
			}
		}
		if len(existingIDs) > 0 {
			ok, err := orm.RuntimeRepositoryUnlink(txCtx, actionModel, existingIDs)
			if err != nil {
				return err
			}
			if !ok {
				log.Errorf("%s failed to remove existing actions", method)
				return fmt.Errorf("%s failed to remove existing actions", method)
			}
		}

		keep := make(map[string]struct{}, len(normalizedItems))
		for _, item := range normalizedItems {
			// Recreated actions take the workflow's company; they were stored with none (plan T41).
			if err := inheritWorkflowChildCompanyID(txCtx, nil, item); err != nil {
				return err
			}
			if _, err := orm.RuntimeRepositoryCreate(txCtx, actionModel, item); err != nil {
				return err
			}
			keep[stringValue(item["step_key"])] = struct{}{}
		}
		if err := deleteWorkflowEdgesNotIn(txCtx, workflowID, keep); err != nil {
			return err
		}
		if hasLayout {
			if _, err := orm.RuntimeRepositoryWrite(txCtx, orm.RuntimeModelRef(FlowModelName, nil), []string{strconv.FormatInt(workflowID, 10)}, orm.RecordMap{"layout": layout}); err != nil {
				return err
			}
		}
		return writeWorkflowFlowMap(txCtx, workflowID)
	})
	if err != nil {
		return nil, err
	}
	return orm.RecordMap{"ok": true, "revision": revision}, nil
}

// workflowLayoutFromKwargs reads the optional canvas layout saved together with the sequence.
func workflowLayoutFromKwargs(method string, kwargs map[string]any) (map[string]any, bool, error) {
	raw, exists := kwargs["layout"]
	if !exists || raw == nil {
		return nil, false, nil
	}
	layout, err := normalizeObjectMap(raw)
	if err != nil {
		log.Errorf("%s layout must be an object", method)
		return nil, false, orm.InvalidParamsf("%s layout must be an object", method)
	}
	return layout, true, nil
}

func workflowIDFromMethodCall(ids []string, args []any, kwargs map[string]any) (int64, bool) {
	if len(ids) > 0 {
		return int64FromAny(ids[0])
	}
	switch typed := argValue(args, 0).(type) {
	case []any:
		if len(typed) == 0 {
			return 0, false
		}
		return int64FromAny(typed[0])
	case []string:
		if len(typed) == 0 {
			return 0, false
		}
		return int64FromAny(typed[0])
	case []int64:
		if len(typed) == 0 {
			return 0, false
		}
		return typed[0], true
	case []int:
		if len(typed) == 0 {
			return 0, false
		}
		return int64(typed[0]), true
	}
	for _, key := range []string{"id", "workflow_id", "workflowId"} {
		if id, ok := int64FromAny(mapValue(kwargs, key)); ok {
			return id, true
		}
	}
	return 0, false
}

// replaceWorkflowEdgesFlow is the DAG-mode counterpart of replaceActionSequenceFlow: unlink then
// recreate the whole edge set of one workflow. Kahn's cycle validation runs over the full submitted
// graph before any DB write; inside the locked transaction (plan T59) every edge must name existing
// steps of the workflow (plan T62), and the revision is checked and bumped. A rejected save leaves
// the persisted edges untouched. Execution-time re-validation (service/engine's executeRunDAG)
// still runs its own pass; this one gives operators inline feedback. Returns
// {"ok": true, "revision": <new revision>}.
func replaceWorkflowEdgesFlow(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	const method = "base.workflow.replace_workflow_edges"
	workflowID, ok := workflowIDFromMethodCall(ids, args, kwargs)
	if !ok || workflowID <= 0 {
		log.Errorf("%s requires a valid workflow id", method)
		return nil, orm.InvalidParamsf("%s requires a valid workflow id", method)
	}
	expected, err := expectedRevisionFromKwargs(method, kwargs)
	if err != nil {
		return nil, err
	}

	items, err := workflowEdgeValuesListFromArgs(args, kwargs)
	if err != nil {
		return nil, err
	}

	normalizedItems := make([]orm.RecordMap, 0, len(items))
	graphEdges := make([]WorkflowGraphEdge, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		values := cloneRecordMap(item)
		values["workflow_id"] = workflowID
		normalized, err := normalizeWorkflowEdgeValues(values, nil, true)
		if err != nil {
			return nil, err
		}
		dedupeKey := strings.Join([]string{
			stringValue(normalized["from_step_key"]),
			stringValue(normalized["to_step_key"]),
			stringValue(normalized["edge_type"]),
		}, "\x00")
		if _, exists := seen[dedupeKey]; exists {
			log.Errorf("%s duplicate edge %s->%s (%s) submitted twice", method, normalized["from_step_key"], normalized["to_step_key"], normalized["edge_type"])
			return nil, orm.InvalidParamsf("%s duplicate edge %s->%s (%s) submitted twice", method, normalized["from_step_key"], normalized["to_step_key"], normalized["edge_type"])
		}
		seen[dedupeKey] = struct{}{}
		normalizedItems = append(normalizedItems, normalized)
		graphEdges = append(graphEdges, WorkflowGraphEdge{
			FromStepKey: stringValue(normalized["from_step_key"]),
			ToStepKey:   stringValue(normalized["to_step_key"]),
		})
	}

	if _, err := ValidateWorkflowGraph(graphEdges); err != nil {
		var cycleErr *WorkflowGraphCycleError
		if errors.As(err, &cycleErr) {
			log.Errorf("%s rejected: cycle at steps %s", method, strings.Join(cycleErr.StepKeys, ", "))
		}
		return nil, err
	}

	edgeModel := orm.RuntimeModelRef(WorkflowEdgeModelName, nil)
	revision, err := runWorkflowMutation(ctx, method, workflowID, expected, func(txCtx context.Context) error {
		stepKeys, err := workflowStepKeyIDs(txCtx, workflowID)
		if err != nil {
			return err
		}
		for _, item := range normalizedItems {
			if err := validateWorkflowEdgeStepKeys(method, stepKeys, stringValue(item["from_step_key"]), stringValue(item["to_step_key"])); err != nil {
				return err
			}
		}

		existingRows, err := orm.RuntimeRepositorySearchRead(txCtx, edgeModel, orm.RecordMap{
			"workflow_id": workflowID,
			"__order":     "id asc",
		}, []string{"id"})
		if err != nil {
			return err
		}
		existingIDs := make([]string, 0, len(existingRows))
		for _, row := range existingRows {
			if id := int64Value(row["id"]); id > 0 {
				existingIDs = append(existingIDs, strconv.FormatInt(id, 10))
			}
		}
		if len(existingIDs) > 0 {
			ok, err := orm.RuntimeRepositoryUnlink(txCtx, edgeModel, existingIDs)
			if err != nil {
				return err
			}
			if !ok {
				log.Errorf("%s failed to remove existing edges", method)
				return fmt.Errorf("%s failed to remove existing edges", method)
			}
		}

		for _, item := range normalizedItems {
			// Recreated edges take the workflow's company (plan T41).
			if err := inheritWorkflowChildCompanyID(txCtx, nil, item); err != nil {
				return err
			}
			if _, err := orm.RuntimeRepositoryCreate(txCtx, edgeModel, item); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return orm.RecordMap{"ok": true, "revision": revision}, nil
}

func workflowEdgeValuesListFromArgs(args []any, kwargs map[string]any) ([]orm.RecordMap, error) {
	if len(args) > 0 {
		if values, err := workflowActionValuesListFromAny(args[0]); err != nil {
			return nil, err
		} else if len(values) > 0 {
			return values, nil
		}
	}
	for _, key := range []string{"items", "values", "edges"} {
		if values, err := workflowActionValuesListFromAny(mapValue(kwargs, key)); err != nil {
			return nil, err
		} else if len(values) > 0 {
			return values, nil
		}
	}
	return nil, nil
}

func workflowActionValuesListFromArgs(args []any, kwargs map[string]any) ([]orm.RecordMap, error) {
	if len(args) > 0 {
		if values, err := workflowActionValuesListFromAny(args[0]); err != nil {
			return nil, err
		} else if len(values) > 0 {
			return values, nil
		}
	}
	for _, key := range []string{"items", "values", "steps"} {
		if values, err := workflowActionValuesListFromAny(mapValue(kwargs, key)); err != nil {
			return nil, err
		} else if len(values) > 0 {
			return values, nil
		}
	}
	return nil, nil
}

func workflowActionValuesListFromAny(raw any) ([]orm.RecordMap, error) {
	switch typed := raw.(type) {
	case []orm.RecordMap:
		out := make([]orm.RecordMap, 0, len(typed))
		for _, item := range typed {
			out = append(out, cloneRecordMap(item))
		}
		return out, nil
	case []map[string]any:
		out := make([]orm.RecordMap, 0, len(typed))
		for _, item := range typed {
			out = append(out, orm.RecordMap(item))
		}
		return out, nil
	case []any:
		out := make([]orm.RecordMap, 0, len(typed))
		for _, item := range typed {
			switch mapped := item.(type) {
			case orm.RecordMap:
				out = append(out, cloneRecordMap(mapped))
			case map[string]any:
				out = append(out, orm.RecordMap(mapped))
			default:
				log.Errorf("base.workflow.replace_action_sequence item contains %T, want object", item)
				return nil, orm.InvalidParamsf("base.workflow.replace_action_sequence item contains %T, want object", item)
			}
		}
		return out, nil
	default:
		if raw == nil {
			return nil, nil
		}
		log.Errorf("base.workflow.replace_action_sequence requires an array, got %T", raw)
		return nil, orm.InvalidParamsf("base.workflow.replace_action_sequence requires an array, got %T", raw)
	}
}
