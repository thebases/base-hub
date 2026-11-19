package models

// File overview:
// - Responsibility: renders and persists `base.workflow.flow_map`, the stored Mermaid markdown
//   diagram that describes a workflow's ordered action sequence. Presentation-only: never
//   influences execution order, which stays driven by `sequence` on `base.workflow.action` rows.
// - Key symbols: `writeWorkflowFlowMap`, `renderWorkflowFlowMapMarkdown`.
// - Pattern: split out of `base_workflow.go` so the `FlowRecord` model file stays limited to
//   persistence/CRUD; every action/flow CRUD path that needs a fresh flow_map still calls
//   `writeWorkflowFlowMap` directly since it's an intra-package (not cross-package) dependency.

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

func writeWorkflowFlowMapMethod(ctx context.Context, _ *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	if len(ids) == 0 {
		workflowID, ok := workflowIDFromMethodCall(ids, args, kwargs)
		if !ok || workflowID <= 0 {
			log.Errorf("base.workflow.writeWorkflowFlowMap requires a valid workflow id")
			return nil, orm.InvalidParamsf("base.workflow.writeWorkflowFlowMap requires a valid workflow id")
		}
		if err := writeWorkflowFlowMap(ctx, workflowID); err != nil {
			return nil, err
		}
		return true, nil
	}

	for _, id := range ids {
		workflowID, ok := int64FromAny(id)
		if !ok || workflowID <= 0 {
			log.Errorf("base.workflow.writeWorkflowFlowMap invalid id %q", id)
			return nil, orm.InvalidParamsf("base.workflow.writeWorkflowFlowMap invalid id %q", id)
		}
		if err := writeWorkflowFlowMap(ctx, workflowID); err != nil {
			return nil, err
		}
	}
	return true, nil
}

func writeWorkflowFlowMap(ctx context.Context, workflowID int64) error {
	if workflowID <= 0 {
		return nil
	}

	// Read only the scalar fields used by the markdown renderer so runtime
	// relation hydration does not touch one2many inverse metadata here.
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(FlowModelName, nil), []string{strconv.FormatInt(workflowID, 10)}, []string{
		"id",
		"name",
		"topic",
		"version",
	})
	if err != nil {
		log.Errorf("base.workflow flow_map read workflow %d: %v", workflowID, err)
		return fmt.Errorf("base.workflow flow_map read workflow %d: %w", workflowID, err)
	}
	if len(rows) == 0 {
		return nil
	}

	flow := rows[0]
	actions, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WorkflowActionModelName, nil), orm.RecordMap{
		"workflow_id": workflowID,
		"is_active":   true,
		"__order":     "sequence asc, id asc",
	}, nil)
	if err != nil {
		log.Errorf("base.workflow flow_map load actions workflow %d: %v", workflowID, err)
		return fmt.Errorf("base.workflow flow_map load actions workflow %d: %w", workflowID, err)
	}

	flowMap := renderWorkflowFlowMapMarkdown(flow, actions)
	ok, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(FlowModelName, nil), []string{strconv.FormatInt(workflowID, 10)}, orm.RecordMap{
		"flow_map": flowMap,
	})
	if err != nil {
		log.Errorf("base.workflow flow_map write workflow %d: %v", workflowID, err)
		return fmt.Errorf("base.workflow flow_map write workflow %d: %w", workflowID, err)
	}
	if !ok {
		log.Errorf("base.workflow flow_map write workflow %d was not acknowledged", workflowID)
		return fmt.Errorf("base.workflow flow_map write workflow %d was not acknowledged", workflowID)
	}
	return nil
}

func renderWorkflowFlowMapMarkdown(flow orm.RecordMap, actionRows []orm.RecordMap) string {
	topic := strings.TrimSpace(stringValue(flow["topic"]))
	name := strings.TrimSpace(stringValue(flow["name"]))
	version := int64Value(flow["version"])
	lines := []string{
		fmt.Sprintf("# Workflow Flow Map: %s", firstNonEmpty(name, topic, "workflow")),
		"",
		fmt.Sprintf("- Topic: `%s`", firstNonEmpty(topic, "unknown")),
		fmt.Sprintf("- Version: `%d`", version),
		fmt.Sprintf("- Active action count: `%d`", len(actionRows)),
		"",
		"```mermaid",
		"flowchart TD",
		fmt.Sprintf("    trigger[\"/wh/%s\"]", mermaidLabel(firstNonEmpty(topic, "unknown"))),
	}

	if len(actionRows) == 0 {
		lines = append(lines, "    empty[\"No active actions configured\"]")
		lines = append(lines, "    trigger --> empty")
		lines = append(lines, "```")
		return strings.Join(lines, "\n")
	}

	type actionNode struct {
		nodeID string
		label  string
	}
	nodes := make([]actionNode, 0, len(actionRows))
	for index, row := range actionRows {
		state := workflowActionStateFromRecord(row)
		sequence := state.Sequence
		if sequence <= 0 {
			sequence = int64((index + 1) * 10)
		}
		nodeID := workflowFlowNodeID(index, state)
		label := fmt.Sprintf("%d. %s<br/>%s@v%d", sequence, firstNonEmpty(state.Name, state.StepKey, state.ActionCode, "step"), firstNonEmpty(state.ActionCode, "action"), state.ActionVersion)
		if len(state.ExtraParams) > 0 {
			if _, ok := state.ExtraParams["condition"]; ok {
				label += "<br/>[conditional]"
			}
		}
		nodes = append(nodes, actionNode{
			nodeID: nodeID,
			label:  mermaidLabel(label),
		})
	}
	for _, node := range nodes {
		lines = append(lines, fmt.Sprintf("    %s[\"%s\"]", node.nodeID, node.label))
	}

	lines = append(lines, fmt.Sprintf("    trigger --> %s", nodes[0].nodeID))
	for index := 1; index < len(nodes); index++ {
		lines = append(lines, fmt.Sprintf("    %s --> %s", nodes[index-1].nodeID, nodes[index].nodeID))
	}
	lines = append(lines, "```")
	return strings.Join(lines, "\n")
}

func workflowFlowNodeID(index int, state workflowActionState) string {
	base := firstNonEmpty(state.StepKey, state.Name, state.ActionCode, fmt.Sprintf("step_%d", index+1))
	base = strings.ToLower(base)
	base = strings.ReplaceAll(base, " ", "_")
	base = strings.ReplaceAll(base, "-", "_")
	base = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '_':
			return r
		default:
			return -1
		}
	}, base)
	if base == "" {
		base = fmt.Sprintf("step_%d", index+1)
	}
	if base[0] >= '0' && base[0] <= '9' {
		base = "step_" + base
	}
	return base
}

func mermaidLabel(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\"", "\\\"")
	return value
}
