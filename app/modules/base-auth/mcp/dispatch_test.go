package mcp

// File overview:
// - Responsibility: argument-validation coverage for the auth.* MCP tool
//   group, reachable without a DB connection.

import (
	"strings"
	"testing"
)

// TestAuthRecordDispatchRejectsUnknownModel covers requireAuthAllowedModel:
// auth.record.* must refuse any model outside sys.oauth_client, even one
// that would otherwise be a valid base.record.* target.
func TestAuthRecordDispatchRejectsUnknownModel(t *testing.T) {
	t.Parallel()

	for _, tool := range []string{
		"auth.record.search_read", "auth.record.read",
		"auth.record.create", "auth.record.write", "auth.record.unlink",
	} {
		if _, err := Dispatch(t.Context(), nil, tool, map[string]any{"model": "sys.credential"}); err == nil {
			t.Fatalf("%s: expected an error for a non-auth model", tool)
		} else if !strings.Contains(err.Error(), "is not one of the auth.* models") {
			t.Fatalf("%s: unexpected error: %v", tool, err)
		}
	}

	if _, err := Dispatch(t.Context(), nil, "auth.record.search_read", map[string]any{}); err == nil {
		t.Fatal("expected an error when arguments.model is missing")
	} else if !strings.Contains(err.Error(), "arguments.model is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestAuthRecordDispatchRejectsSensitiveModels proves the internal token
// bookkeeping tables (sys.activation_code, sys.oauth_auth_code,
// sys.oauth_refresh_token) are never reachable through auth.record.*, even
// though they belong to this module -- only sys.oauth_client is exposed.
func TestAuthRecordDispatchRejectsSensitiveModels(t *testing.T) {
	t.Parallel()

	for _, model := range []string{"sys.activation_code", "sys.oauth_auth_code", "sys.oauth_refresh_token"} {
		if _, err := Dispatch(t.Context(), nil, "auth.record.search_read", map[string]any{"model": model}); err == nil {
			t.Fatalf("model %q: expected an error", model)
		} else if !strings.Contains(err.Error(), "is not one of the auth.* models") {
			t.Fatalf("model %q: unexpected error: %v", model, err)
		}
	}
}

// TestAuthToolsUnknownName covers Dispatch's default case.
func TestAuthToolsUnknownName(t *testing.T) {
	t.Parallel()

	if _, err := Dispatch(t.Context(), nil, "auth.does_not_exist", map[string]any{}); err == nil {
		t.Fatal("expected an error for an unknown auth.* tool name")
	} else if !strings.Contains(err.Error(), "unknown mcp tool") {
		t.Fatalf("unexpected error: %v", err)
	}
}
