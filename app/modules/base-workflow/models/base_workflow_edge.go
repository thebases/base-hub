package models

// File overview:
// - Responsibility: defines the `base.workflow.edge` model — the DAG-mode execution graph.
//   Each row is one directed `from_step_key -> to_step_key` dependency. Only consulted when
//   the owning workflow's `execution_mode == "dag"`; linear-mode workflows never read this
//   table (execution truth stays `base_workflow_action.sequence` for them).
// - Key symbols: `WorkflowEdgeModel`, `createWorkflowEdge`, `WorkflowEdgeRecord`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and
//   behavior hooks stay centralized instead of being scattered across handlers.

import (
	"context"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

const (
	WorkflowEdgeTypeSuccess   = "success"
	WorkflowEdgeTypeFailure   = "failure"
	WorkflowEdgeTypeCondition = "condition"
	WorkflowEdgeTypeAlways    = "always"
	WorkflowEdgeTypeManual    = "manual"
)

type WorkflowEdgeRecord struct {
	orm.BaseModel
	WorkflowID    *int64 `bun:"workflow_id" json:"workflow_id" meta:"kind:many2one,model:base.workflow,ondelete:CASCADE,index,label:Workflow,help:Links this edge back to the workflow it belongs to."`
	CompanyID     *int64 `bun:"company_id" json:"company_id" meta:"kind:many2one,model:tbl.company,ondelete:SET NULL,index,label:Company ID,help:Inherits the company scope from the parent workflow; not settable independently."`
	FromStepKey   string `bun:"from_step_key" json:"from_step_key" meta:"index,label:From Step,help:Step key this edge departs from."`
	ToStepKey     string `bun:"to_step_key" json:"to_step_key" meta:"index,label:To Step,help:Step key this edge arrives at."`
	EdgeType      string `bun:"edge_type,default:'success'" json:"edge_type" meta:"label:Edge Type,help:Controls when this edge is followed." selection:"(success,Success),(failure,Failure),(condition,Condition),(always,Always),(manual,Manual)"`
	ConditionExpr string `bun:"condition_expr,default:''" json:"condition_expr" meta:"label:Condition Expression,help:Optional expression gating a condition-type edge."`
}

const WorkflowEdgeModelName = "base.workflow.edge"

var WorkflowEdgeModelSpec = orm.ModelSpec{
	Name:        WorkflowEdgeModelName,
	Label:       "Workflow Edge",
	Description: "Directed step dependency used only by DAG-mode execution scheduling",
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "create", Public: true, ModelOnly: true, Call: createWorkflowEdge},
		{Name: "write", Public: true, UsesPathID: true, Call: writeWorkflowEdge},
		{Name: "unlink", Public: true, UsesPathID: true, Call: unlinkWorkflowEdge},
	},
}

var WorkflowEdgeModel = orm.Define[WorkflowEdgeRecord](&Models, 125, WorkflowEdgeModelSpec)

// createWorkflowEdge creates the workflow edge handled by this package.
func createWorkflowEdge(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	valuesList, err := recordMapListFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if len(valuesList) == 0 {
		log.Errorf("base.workflow.edge.create requires values")
		return nil, orm.InvalidParamsf("base.workflow.edge.create requires values")
	}

	created := make([]orm.RecordMap, 0, len(valuesList))
	for _, values := range valuesList {
		normalized, err := normalizeWorkflowEdgeValues(values, nil, true)
		if err != nil {
			return nil, err
		}
		// company_id follows the parent workflow (plan T41).
		if err := inheritWorkflowChildCompanyID(ctx, model, normalized); err != nil {
			return nil, err
		}
		if err := guardWorkflowEdgeUnique(ctx, normalized, 0); err != nil {
			return nil, err
		}
		// Both ends must be steps of the workflow (plan T62).
		if err := guardWorkflowEdgeStepsExist(ctx, "base.workflow.edge.create", normalized); err != nil {
			return nil, err
		}
		record, err := orm.CreateRecord(ctx, orm.RuntimeModelRef(WorkflowEdgeModelName, model), normalized)
		if err != nil {
			return nil, err
		}
		// Edges are part of the published snapshot (plan T97).
		if err := markWorkflowDraft(ctx, workflowIDFromAny(normalized["workflow_id"])); err != nil {
			return nil, err
		}
		created = append(created, record)
	}
	if len(created) == 1 {
		return created[0], nil
	}
	return created, nil
}

func writeWorkflowEdge(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	if len(ids) == 0 {
		log.Errorf("base.workflow.edge.write requires ids")
		return nil, orm.InvalidParamsf("base.workflow.edge.write requires ids")
	}
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		log.Errorf("base.workflow.edge.write requires values")
		return nil, orm.InvalidParamsf("base.workflow.edge.write requires values")
	}

	beforeRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(WorkflowEdgeModelName, model), ids, nil)
	if err != nil {
		return nil, err
	}
	if len(beforeRows) == 0 {
		return false, nil
	}

	current := workflowEdgeStateFromRecord(beforeRows[0])
	normalized, err := normalizeWorkflowEdgeValues(values, &current, false)
	if err != nil {
		return nil, err
	}
	if err := applyWorkflowChildCompanyOnWrite(ctx, normalized); err != nil {
		return nil, err
	}
	if err := guardWorkflowEdgeUnique(ctx, normalized, int64Value(beforeRows[0]["id"])); err != nil {
		return nil, err
	}
	if err := guardWorkflowEdgeStepsExist(ctx, "base.workflow.edge.write", normalized); err != nil {
		return nil, err
	}
	ok, err := orm.WriteRecords(ctx, orm.RuntimeModelRef(WorkflowEdgeModelName, model), ids, normalized)
	if err != nil || !ok {
		return ok, err
	}
	return ok, markWorkflowDraftOfRows(ctx, append(beforeRows, orm.RecordMap{"workflow_id": normalized["workflow_id"]}))
}

func unlinkWorkflowEdge(ctx context.Context, model *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	if len(ids) == 0 {
		log.Errorf("base.workflow.edge.unlink requires ids")
		return nil, orm.InvalidParamsf("base.workflow.edge.unlink requires ids")
	}
	beforeRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(WorkflowEdgeModelName, model), ids, []string{"id", "workflow_id"})
	if err != nil {
		return nil, err
	}
	ok, err := orm.RuntimeRepositoryUnlink(ctx, orm.RuntimeModelRef(WorkflowEdgeModelName, model), ids)
	if err != nil || !ok {
		return ok, err
	}
	return ok, markWorkflowDraftOfRows(ctx, beforeRows)
}

// markWorkflowDraftOfRows marks draft every workflow the edge rows belong to (plan T97).
func markWorkflowDraftOfRows(ctx context.Context, rows []orm.RecordMap) error {
	for workflowID := range workflowIDsFromActionRows(rows) {
		if err := markWorkflowDraft(ctx, workflowID); err != nil {
			return err
		}
	}
	return nil
}

type workflowEdgeState struct {
	WorkflowID    *int64
	FromStepKey   string
	ToStepKey     string
	EdgeType      string
	ConditionExpr string
}

func workflowEdgeStateFromRecord(record orm.RecordMap) workflowEdgeState {
	return workflowEdgeState{
		WorkflowID:    int64PointerFromAny(record["workflow_id"]),
		FromStepKey:   strings.TrimSpace(stringValue(record["from_step_key"])),
		ToStepKey:     strings.TrimSpace(stringValue(record["to_step_key"])),
		EdgeType:      strings.TrimSpace(stringValue(record["edge_type"])),
		ConditionExpr: stringValue(record["condition_expr"]),
	}
}

// A self-referencing edge (from == to) is rejected here up front — Kahn's cycle
// check in graph_validate.go would also catch it (in-degree never reaches 0),
// but failing fast on create/write gives an immediate, specific error instead
// of a generic cycle report naming a single node.
func normalizeWorkflowEdgeValues(values orm.RecordMap, current *workflowEdgeState, creating bool) (orm.RecordMap, error) {
	normalized := cloneRecordMap(values)

	if rawWorkflowID, exists := normalized["workflow_id"]; exists {
		ptr, ok := int64PointerValue(rawWorkflowID)
		if !ok || ptr == nil || *ptr <= 0 {
			log.Errorf("base.workflow.edge workflow_id must be a workflow id")
			return nil, orm.InvalidParamsf("base.workflow.edge workflow_id must be a workflow id")
		}
		normalized["workflow_id"] = ptr
	} else if current != nil && current.WorkflowID != nil {
		normalized["workflow_id"] = *current.WorkflowID
	} else {
		log.Errorf("base.workflow.edge workflow_id is required")
		return nil, orm.InvalidParamsf("base.workflow.edge workflow_id is required")
	}

	fromStepKey := currentWorkflowEdgeStepKey(normalized, current, "from_step_key", func(s workflowEdgeState) string { return s.FromStepKey })
	if fromStepKey == "" {
		log.Errorf("base.workflow.edge from_step_key is required")
		return nil, orm.InvalidParamsf("base.workflow.edge from_step_key is required")
	}
	normalized["from_step_key"] = fromStepKey

	toStepKey := currentWorkflowEdgeStepKey(normalized, current, "to_step_key", func(s workflowEdgeState) string { return s.ToStepKey })
	if toStepKey == "" {
		log.Errorf("base.workflow.edge to_step_key is required")
		return nil, orm.InvalidParamsf("base.workflow.edge to_step_key is required")
	}
	normalized["to_step_key"] = toStepKey

	if fromStepKey == toStepKey {
		log.Errorf("base.workflow.edge from_step_key and to_step_key must differ (self-loop rejected as a 1-node cycle)")
		return nil, orm.InvalidParamsf("base.workflow.edge from_step_key and to_step_key must differ (self-loop rejected as a 1-node cycle)")
	}

	edgeType := strings.ToLower(strings.TrimSpace(stringValue(normalized["edge_type"])))
	if edgeType == "" {
		if current != nil && current.EdgeType != "" {
			edgeType = current.EdgeType
		} else {
			edgeType = WorkflowEdgeTypeSuccess
		}
	}
	switch edgeType {
	case WorkflowEdgeTypeSuccess, WorkflowEdgeTypeFailure, WorkflowEdgeTypeCondition, WorkflowEdgeTypeAlways, WorkflowEdgeTypeManual:
	default:
		log.Errorf("base.workflow.edge edge_type %q is not supported", edgeType)
		return nil, orm.InvalidParamsf("base.workflow.edge edge_type %q is not supported", edgeType)
	}
	normalized["edge_type"] = edgeType

	if conditionExpr, ok := stringFromAny(normalized["condition_expr"]); ok {
		normalized["condition_expr"] = strings.TrimSpace(conditionExpr)
	} else if creating {
		normalized["condition_expr"] = ""
	}

	return normalized, nil
}

func currentWorkflowEdgeStepKey(values orm.RecordMap, current *workflowEdgeState, field string, pick func(workflowEdgeState) string) string {
	if value, ok := stringFromAny(values[field]); ok {
		return strings.TrimSpace(value)
	}
	if current != nil {
		return pick(*current)
	}
	return ""
}

// guardWorkflowEdgeUnique enforces the (workflow_id, from_step_key, to_step_key,
// edge_type) uniqueness the plan's SQL sketch expressed as a DB unique index —
// this repo's dotted models auto-migrate their tables from Go struct tags with
// no hand-written SQL migration path, so the constraint is enforced in the
// write path instead of a `.sql` migration.
func guardWorkflowEdgeUnique(ctx context.Context, normalized orm.RecordMap, excludeID int64) error {
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WorkflowEdgeModelName, nil), orm.RecordMap{
		"workflow_id":   normalized["workflow_id"],
		"from_step_key": normalized["from_step_key"],
		"to_step_key":   normalized["to_step_key"],
		"edge_type":     normalized["edge_type"],
	}, []string{"id"})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if int64Value(row["id"]) != excludeID {
			log.Errorf("base.workflow.edge duplicate edge %s->%s (%s) already exists for this workflow", normalized["from_step_key"], normalized["to_step_key"], normalized["edge_type"])
			return orm.Conflictf("base.workflow.edge duplicate edge %s->%s (%s) already exists for this workflow", normalized["from_step_key"], normalized["to_step_key"], normalized["edge_type"])
		}
	}
	return nil
}
