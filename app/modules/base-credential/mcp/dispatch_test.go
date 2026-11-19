package mcp

// File overview:
// - Responsibility: argument-validation coverage for the credential.* MCP
//   tool group, reachable without a DB connection.

import (
	"strings"
	"testing"
)

// TestCredentialRecordDispatchRejectsUnknownModel covers
// requireCredentialAllowedModel.
func TestCredentialRecordDispatchRejectsUnknownModel(t *testing.T) {
	t.Parallel()

	for _, tool := range []string{
		"credential.record.search_read", "credential.record.read",
		"credential.record.create", "credential.record.write", "credential.record.unlink",
	} {
		if _, err := Dispatch(t.Context(), nil, tool, map[string]any{"model": "sys.oauth_client"}); err == nil {
			t.Fatalf("%s: expected an error for a non-credential model", tool)
		} else if !strings.Contains(err.Error(), "is not one of the credential.* models") {
			t.Fatalf("%s: unexpected error: %v", tool, err)
		}
	}

	if _, err := Dispatch(t.Context(), nil, "credential.record.search_read", map[string]any{}); err == nil {
		t.Fatal("expected an error when arguments.model is missing")
	} else if !strings.Contains(err.Error(), "arguments.model is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestCredentialToolsUnknownName covers Dispatch's default case.
func TestCredentialToolsUnknownName(t *testing.T) {
	t.Parallel()

	if _, err := Dispatch(t.Context(), nil, "credential.does_not_exist", map[string]any{}); err == nil {
		t.Fatal("expected an error for an unknown credential.* tool name")
	} else if !strings.Contains(err.Error(), "unknown mcp tool") {
		t.Fatalf("unexpected error: %v", err)
	}
}
