package mcp

// File overview:
// - Responsibility: argument-validation coverage for the automation.* MCP
//   tool group, reachable without a DB connection.

import (
	"strings"
	"testing"
)

// TestAutomationRecordDispatchRejectsUnknownModel covers
// requireAutomationAllowedModel.
func TestAutomationRecordDispatchRejectsUnknownModel(t *testing.T) {
	t.Parallel()

	for _, tool := range []string{
		"automation.record.search_read", "automation.record.read",
		"automation.record.create", "automation.record.write", "automation.record.unlink",
	} {
		if _, err := Dispatch(t.Context(), nil, tool, map[string]any{"model": "tbl.partner"}); err == nil {
			t.Fatalf("%s: expected an error for a non-automation model", tool)
		} else if !strings.Contains(err.Error(), "is not one of the automation.* models") {
			t.Fatalf("%s: unexpected error: %v", tool, err)
		}
	}

	if _, err := Dispatch(t.Context(), nil, "automation.record.search_read", map[string]any{}); err == nil {
		t.Fatal("expected an error when arguments.model is missing")
	} else if !strings.Contains(err.Error(), "arguments.model is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestAutomationActionRunValidation covers mcpAutomationActionRun's
// required-argument check.
func TestAutomationActionRunValidation(t *testing.T) {
	t.Parallel()

	if _, err := Dispatch(t.Context(), nil, "automation.action.run", map[string]any{}); err == nil {
		t.Fatal("expected an error when arguments.action_id is missing")
	} else if !strings.Contains(err.Error(), "arguments.action_id is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestAutomationToolsUnknownName covers Dispatch's default case.
func TestAutomationToolsUnknownName(t *testing.T) {
	t.Parallel()

	if _, err := Dispatch(t.Context(), nil, "automation.does_not_exist", map[string]any{}); err == nil {
		t.Fatal("expected an error for an unknown automation.* tool name")
	} else if !strings.Contains(err.Error(), "unknown mcp tool") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestToolHandlersMatchToolsCatalog(t *testing.T) {
	names := map[string]bool{}
	for _, tool := range Tools() {
		names[tool.Name] = true
	}
	for name := range toolHandlers {
		if !names[name] {
			t.Errorf("handler %q has no Tools() entry", name)
		}
		delete(names, name)
	}
	for name := range names {
		t.Errorf("Tools() entry %q has no handler", name)
	}
}
