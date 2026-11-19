package models

// File overview:
// - Responsibility: Kahn's-algorithm cycle validation and frontier-layer computation for
//   DAG-mode workflows. Lives in `models` (not `service/engine`) so both the save-path
//   validation (`workflow_copy.go`'s `replaceWorkflowEdgesFlow`, same package) and the engine's
//   execution-time re-validation (`service/engine/graph_schedule.go`, which already imports
//   this package as `workflowmodels`) can call the same pure function without creating a
//   models<->engine import cycle (engine imports models, never the other way around).
// - Key symbols: `WorkflowGraphEdge`, `WorkflowGraphCycleError`, `ValidateWorkflowGraph`.
// - Pattern: pure, side-effect-free BFS — callers own loading edges from storage and owns
//   deciding what to do with a cycle error (reject a save, fail a run, etc).

import (
	"fmt"
	"sort"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
)

// WorkflowGraphEdge is the minimal shape Kahn's algorithm needs: one directed
// step dependency. Callers convert persisted base.workflow.edge rows (or
// in-flight unsaved edge values) into this shape before validating.
// EdgeType/ConditionExpr ride along unused by ValidateWorkflowGraph itself
// (cycle detection and layering are purely structural) but are consulted by
// the engine's per-layer dispatch (service/engine/graph_schedule.go) to
// decide whether a resolved predecessor actually satisfies this edge.
type WorkflowGraphEdge struct {
	FromStepKey   string
	ToStepKey     string
	EdgeType      string
	ConditionExpr string
}

// WorkflowGraphCycleError names the step keys that never reached in-degree
// zero — i.e. the cycle (or, for a self-loop, the single offending node) —
// so callers can surface a specific, actionable rejection instead of a bare
// "graph is invalid" message.
type WorkflowGraphCycleError struct {
	StepKeys []string
}

func (e *WorkflowGraphCycleError) Error() string {
	if e == nil || len(e.StepKeys) == 0 {
		return "workflow: cycle detected in DAG edges"
	}
	return fmt.Sprintf("workflow: cycle detected among steps: %s", strings.Join(e.StepKeys, ", "))
}

// ValidateWorkflowGraph runs Kahn's BFS over the given edges and returns the
// frontier batches (parallel-safe execution layers) in dependency order.
// Each layer is sorted by step_key for a deterministic tie-break, matching
// the plan's testing requirement. A cycle (including a 1-node self-loop,
// since a self-referencing edge never lets its own node reach in-degree
// zero) returns a *WorkflowGraphCycleError naming every step that stayed
// stuck instead of a generic failure.
func ValidateWorkflowGraph(edges []WorkflowGraphEdge) ([][]string, error) {
	dependents := map[string][]string{}
	inDegree := map[string]int{}
	nodeSet := map[string]struct{}{}

	for _, edge := range edges {
		from := strings.TrimSpace(edge.FromStepKey)
		to := strings.TrimSpace(edge.ToStepKey)
		if from == "" || to == "" {
			continue
		}
		nodeSet[from] = struct{}{}
		nodeSet[to] = struct{}{}
		dependents[from] = append(dependents[from], to)
		inDegree[to]++
		if _, exists := inDegree[from]; !exists {
			inDegree[from] = 0
		}
	}

	queue := make([]string, 0, len(nodeSet))
	for node := range nodeSet {
		if inDegree[node] == 0 {
			queue = append(queue, node)
		}
	}
	sort.Strings(queue)

	layers := make([][]string, 0)
	processed := 0
	for len(queue) > 0 {
		layer := append([]string(nil), queue...)
		sort.Strings(layer)
		layers = append(layers, layer)

		next := make([]string, 0)
		for _, node := range layer {
			processed++
			for _, dep := range dependents[node] {
				inDegree[dep]--
				if inDegree[dep] == 0 {
					next = append(next, dep)
				}
			}
		}
		queue = next
	}

	if processed < len(nodeSet) {
		unprocessed := make([]string, 0, len(nodeSet)-processed)
		for node := range nodeSet {
			if inDegree[node] > 0 {
				unprocessed = append(unprocessed, node)
			}
		}
		sort.Strings(unprocessed)
		return nil, &WorkflowGraphCycleError{StepKeys: unprocessed}
	}
	return layers, nil
}

// WorkflowGraphEdgesFromRecords converts persisted base.workflow.edge rows
// (as returned by orm.RuntimeRepositorySearchRead) into the pure validation
// shape, ignoring fields ValidateWorkflowGraph doesn't need.
func WorkflowGraphEdgesFromRecords(rows []orm.RecordMap) []WorkflowGraphEdge {
	edges := make([]WorkflowGraphEdge, 0, len(rows))
	for _, row := range rows {
		edges = append(edges, WorkflowGraphEdge{
			FromStepKey:   strings.TrimSpace(stringValue(row["from_step_key"])),
			ToStepKey:     strings.TrimSpace(stringValue(row["to_step_key"])),
			EdgeType:      strings.TrimSpace(stringValue(row["edge_type"])),
			ConditionExpr: stringValue(row["condition_expr"]),
		})
	}
	return edges
}
