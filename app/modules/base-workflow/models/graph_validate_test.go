package models

// File overview:
// - Responsibility: locks down Kahn's-algorithm cycle validation and frontier-layer computation
//   (graph_validate.go) so DAG-mode save/execution paths keep detecting cycles correctly and
//   producing deterministic parallel batches.
// - Key symbols: focused Test cases exercising ValidateWorkflowGraph.

import "testing"

func TestValidateWorkflowGraphAcyclicBranchesProduceDeterministicLayers(t *testing.T) {
	// start -> {a, b} -> join : two parallel branches converging on one join step.
	edges := []WorkflowGraphEdge{
		{FromStepKey: "start", ToStepKey: "b"},
		{FromStepKey: "start", ToStepKey: "a"},
		{FromStepKey: "a", ToStepKey: "join"},
		{FromStepKey: "b", ToStepKey: "join"},
	}

	layers, err := ValidateWorkflowGraph(edges)
	if err != nil {
		t.Fatalf("expected acyclic graph to validate, got %v", err)
	}
	if len(layers) != 3 {
		t.Fatalf("expected 3 frontier layers, got %d: %#v", len(layers), layers)
	}
	if len(layers[0]) != 1 || layers[0][0] != "start" {
		t.Fatalf("expected layer 0 = [start], got %#v", layers[0])
	}
	// "a" sorts before "b" — deterministic tie-break by step_key within one layer.
	if len(layers[1]) != 2 || layers[1][0] != "a" || layers[1][1] != "b" {
		t.Fatalf("expected layer 1 = [a, b] sorted, got %#v", layers[1])
	}
	if len(layers[2]) != 1 || layers[2][0] != "join" {
		t.Fatalf("expected layer 2 = [join], got %#v", layers[2])
	}
}

func TestValidateWorkflowGraphRejectsCycle(t *testing.T) {
	edges := []WorkflowGraphEdge{
		{FromStepKey: "a", ToStepKey: "b"},
		{FromStepKey: "b", ToStepKey: "c"},
		{FromStepKey: "c", ToStepKey: "a"},
	}

	layers, err := ValidateWorkflowGraph(edges)
	if err == nil {
		t.Fatalf("expected cycle to be rejected, got layers %#v", layers)
	}
	cycleErr, ok := err.(*WorkflowGraphCycleError)
	if !ok {
		t.Fatalf("expected *WorkflowGraphCycleError, got %T: %v", err, err)
	}
	if len(cycleErr.StepKeys) != 3 {
		t.Fatalf("expected all 3 cyclic steps named, got %#v", cycleErr.StepKeys)
	}
	for _, want := range []string{"a", "b", "c"} {
		found := false
		for _, got := range cycleErr.StepKeys {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected cycle to name step %q, got %#v", want, cycleErr.StepKeys)
		}
	}
}

func TestValidateWorkflowGraphRejectsSelfLoopAsOneNodeCycle(t *testing.T) {
	edges := []WorkflowGraphEdge{
		{FromStepKey: "a", ToStepKey: "a"},
	}

	_, err := ValidateWorkflowGraph(edges)
	if err == nil {
		t.Fatalf("expected self-loop to be rejected")
	}
	cycleErr, ok := err.(*WorkflowGraphCycleError)
	if !ok {
		t.Fatalf("expected *WorkflowGraphCycleError, got %T: %v", err, err)
	}
	if len(cycleErr.StepKeys) != 1 || cycleErr.StepKeys[0] != "a" {
		t.Fatalf("expected 1-node cycle naming step a, got %#v", cycleErr.StepKeys)
	}
}

func TestValidateWorkflowGraphZeroEdgesReturnsNoLayers(t *testing.T) {
	layers, err := ValidateWorkflowGraph(nil)
	if err != nil {
		t.Fatalf("expected zero-edge graph to validate cleanly, got %v", err)
	}
	if len(layers) != 0 {
		t.Fatalf("expected no frontier layers for zero edges, got %#v", layers)
	}
}

func TestWorkflowGraphEdgesFromRecordsConvertsRows(t *testing.T) {
	rows := []struct {
		from string
		to   string
	}{
		{"a", "b"},
		{"b", "c"},
	}
	converted := make([]WorkflowGraphEdge, 0, len(rows))
	for _, row := range rows {
		converted = append(converted, WorkflowGraphEdge{FromStepKey: row.from, ToStepKey: row.to})
	}
	layers, err := ValidateWorkflowGraph(converted)
	if err != nil {
		t.Fatalf("expected chain a->b->c to validate, got %v", err)
	}
	if len(layers) != 3 {
		t.Fatalf("expected 3 sequential layers, got %#v", layers)
	}
}
