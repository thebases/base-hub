package mcp

// File overview:
// - Responsibility: argument-validation coverage for the calendar.* MCP tool
//   group, reachable without a DB connection.

import (
	"strings"
	"testing"
)

// TestCalendarRecordDispatchRejectsUnknownModel covers
// requireCalendarAllowedModel.
func TestCalendarRecordDispatchRejectsUnknownModel(t *testing.T) {
	t.Parallel()

	for _, tool := range []string{
		"calendar.record.search_read", "calendar.record.read",
		"calendar.record.create", "calendar.record.write", "calendar.record.unlink",
	} {
		if _, err := Dispatch(t.Context(), nil, tool, map[string]any{"model": "tbl.partner"}); err == nil {
			t.Fatalf("%s: expected an error for a non-calendar model", tool)
		} else if !strings.Contains(err.Error(), "is not one of the calendar.* models") {
			t.Fatalf("%s: unexpected error: %v", tool, err)
		}
	}

	if _, err := Dispatch(t.Context(), nil, "calendar.record.search_read", map[string]any{}); err == nil {
		t.Fatal("expected an error when arguments.model is missing")
	} else if !strings.Contains(err.Error(), "arguments.model is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestCalendarOccurrencesValidation covers mcpCalendarOccurrences' required
// argument checks.
func TestCalendarOccurrencesValidation(t *testing.T) {
	t.Parallel()

	if _, err := Dispatch(t.Context(), nil, "calendar.occurrences", map[string]any{}); err == nil {
		t.Fatal("expected an error when arguments.sys_model is missing")
	} else if !strings.Contains(err.Error(), "arguments.sys_model is required") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := Dispatch(t.Context(), nil, "calendar.occurrences", map[string]any{"sys_model": "tbl.partner"}); err == nil {
		t.Fatal("expected an error when arguments.date_from is missing")
	} else if !strings.Contains(err.Error(), "arguments.date_from is required") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := Dispatch(t.Context(), nil, "calendar.occurrences", map[string]any{"sys_model": "tbl.partner", "date_from": "2026-01-01"}); err == nil {
		t.Fatal("expected an error when arguments.date_to is missing")
	} else if !strings.Contains(err.Error(), "arguments.date_to is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestCalendarRescheduleOccurrenceValidation covers
// mcpCalendarRescheduleOccurrence's required-argument and scope checks.
func TestCalendarRescheduleOccurrenceValidation(t *testing.T) {
	t.Parallel()

	if _, err := Dispatch(t.Context(), nil, "calendar.reschedule_occurrence", map[string]any{}); err == nil {
		t.Fatal("expected an error when arguments.recurrence_id is missing")
	} else if !strings.Contains(err.Error(), "arguments.recurrence_id is required") {
		t.Fatalf("unexpected error: %v", err)
	}

	base := map[string]any{
		"recurrence_id":            float64(1),
		"original_occurrence_date": "2026-01-01",
		"new_datetime":             "2026-01-02",
		"scope":                    "sometime",
	}
	if _, err := Dispatch(t.Context(), nil, "calendar.reschedule_occurrence", base); err == nil {
		t.Fatal("expected an error for an invalid scope")
	} else if !strings.Contains(err.Error(), `arguments.scope must be`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestCalendarToolsUnknownName covers Dispatch's default case.
func TestCalendarToolsUnknownName(t *testing.T) {
	t.Parallel()

	if _, err := Dispatch(t.Context(), nil, "calendar.does_not_exist", map[string]any{}); err == nil {
		t.Fatal("expected an error for an unknown calendar.* tool name")
	} else if !strings.Contains(err.Error(), "unknown mcp tool") {
		t.Fatalf("unexpected error: %v", err)
	}
}
