package mcp

// File overview:
// - Responsibility: argument-validation coverage for the workflow.* MCP tool
//   group, moved verbatim (dispatchMCPTool(...) calls become direct
//   Dispatch(...) calls) out of
//   modules/base-mcp/routes/mcp_tools_workflow_test.go, per the
//   mcp-module-scaffold plan's Phase 4.

import (
	"strings"
	"testing"
)

// TestWorkflowRecordDispatchRejectsUnknownModel covers requireWorkflowAllowedModel:
// workflow.record.* must refuse any model outside the fixed base.workflow.*
// set, even one that would otherwise be a valid base.record.* target.
func TestWorkflowRecordDispatchRejectsUnknownModel(t *testing.T) {
	t.Parallel()

	for _, tool := range []string{
		"workflow.record.search_read", "workflow.record.read",
		"workflow.record.create", "workflow.record.write", "workflow.record.unlink",
	} {
		if _, err := Dispatch(t.Context(), nil, tool, map[string]any{"model": "tbl.partner"}); err == nil {
			t.Fatalf("%s: expected an error for a non-workflow model", tool)
		} else if !strings.Contains(err.Error(), "is not one of the workflow.* models") {
			t.Fatalf("%s: unexpected error: %v", tool, err)
		}
	}

	if _, err := Dispatch(t.Context(), nil, "workflow.record.search_read", map[string]any{}); err == nil {
		t.Fatal("expected an error when arguments.model is missing")
	} else if !strings.Contains(err.Error(), "arguments.model is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestWorkflowExportValidation covers mcpWorkflowExport's required-argument check.
func TestWorkflowExportValidation(t *testing.T) {
	t.Parallel()

	if _, err := Dispatch(t.Context(), nil, "workflow.export", map[string]any{}); err == nil {
		t.Fatal("expected an error when arguments.workflow_id is missing")
	} else if !strings.Contains(err.Error(), "arguments.workflow_id is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestWorkflowReplaceActionSequenceValidation covers
// mcpWorkflowReplaceActionSequence's required-argument checks.
func TestWorkflowReplaceActionSequenceValidation(t *testing.T) {
	t.Parallel()

	if _, err := Dispatch(t.Context(), nil, "workflow.replace_action_sequence", map[string]any{"steps": []any{}}); err == nil {
		t.Fatal("expected an error when arguments.workflow_id is missing")
	} else if !strings.Contains(err.Error(), "arguments.workflow_id is required") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := Dispatch(t.Context(), nil, "workflow.replace_action_sequence", map[string]any{"workflow_id": 1, "steps": "not-an-array"}); err == nil {
		t.Fatal("expected an error when arguments.steps is not an array")
	} else if !strings.Contains(err.Error(), "arguments.steps must be an array") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestWorkflowReplaceWorkflowEdgesValidation covers
// mcpWorkflowReplaceWorkflowEdges's required-argument checks.
func TestWorkflowReplaceWorkflowEdgesValidation(t *testing.T) {
	t.Parallel()

	if _, err := Dispatch(t.Context(), nil, "workflow.replace_workflow_edges", map[string]any{"edges": []any{}}); err == nil {
		t.Fatal("expected an error when arguments.workflow_id is missing")
	} else if !strings.Contains(err.Error(), "arguments.workflow_id is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestWorkflowToolsUnknownName proves an unrecognized workflow.* tool name
// still produces the generic tool_not_found error instead of a panic.
func TestWorkflowToolsUnknownName(t *testing.T) {
	t.Parallel()

	if _, err := Dispatch(t.Context(), nil, "workflow.does_not_exist", map[string]any{}); err == nil {
		t.Fatal("expected an error for an unknown workflow.* tool name")
	} else if !strings.Contains(err.Error(), "unknown mcp tool") {
		t.Fatalf("unexpected error: %v", err)
	}
}
