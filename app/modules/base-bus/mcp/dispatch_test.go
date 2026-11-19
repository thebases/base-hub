package mcp

// File overview:
// - Responsibility: argument-validation coverage for the bus.* MCP tool
//   group, reachable without a DB connection.

import (
	"strings"
	"testing"
)

// TestBusRecordDispatchRejectsUnknownModel covers busRecordDispatch's model
// allowlist check.
func TestBusRecordDispatchRejectsUnknownModel(t *testing.T) {
	t.Parallel()

	for _, tool := range []string{"bus.record.search_read", "bus.record.read"} {
		if _, err := Dispatch(t.Context(), nil, tool, map[string]any{"model": "tbl.partner"}); err == nil {
			t.Fatalf("%s: expected an error for a non-bus model", tool)
		} else if !strings.Contains(err.Error(), "is not one of the bus.* models") {
			t.Fatalf("%s: unexpected error: %v", tool, err)
		}
	}

	if _, err := Dispatch(t.Context(), nil, "bus.record.search_read", map[string]any{}); err == nil {
		t.Fatal("expected an error when arguments.model is missing")
	} else if !strings.Contains(err.Error(), "arguments.model is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestBusPublishValidation covers mcpBusPublish's required-argument checks.
func TestBusPublishValidation(t *testing.T) {
	t.Parallel()

	if _, err := Dispatch(t.Context(), nil, "bus.publish", map[string]any{}); err == nil {
		t.Fatal("expected an error when arguments.channels is missing")
	} else if !strings.Contains(err.Error(), "arguments.channels is required") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := Dispatch(t.Context(), nil, "bus.publish", map[string]any{"channels": "global"}); err == nil {
		t.Fatal("expected an error when arguments.event_type is missing")
	} else if !strings.Contains(err.Error(), "arguments.event_type is required") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := Dispatch(t.Context(), nil, "bus.publish", map[string]any{"channels": "global", "event_type": "ping"}); err == nil {
		t.Fatal("expected an error when arguments.payload is missing")
	} else if !strings.Contains(err.Error(), "arguments.payload is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestBusToolsUnknownName covers Dispatch's default case.
func TestBusToolsUnknownName(t *testing.T) {
	t.Parallel()

	if _, err := Dispatch(t.Context(), nil, "bus.does_not_exist", map[string]any{}); err == nil {
		t.Fatal("expected an error for an unknown bus.* tool name")
	} else if !strings.Contains(err.Error(), "unknown mcp tool") {
		t.Fatalf("unexpected error: %v", err)
	}
}
