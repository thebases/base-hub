package mcp

// File overview:
// - Responsibility: the auth.* MCP tool catalog -- record CRUD restricted to
//   sys.oauth_client, the one base-auth model meant for AI/automation
//   management.
// - Key symbols: Tools, authAllowedModels.
// - Deliberately excludes sys.activation_code, sys.oauth_auth_code, and
//   sys.oauth_refresh_token (hashed, internal-only token bookkeeping tables
//   with no legitimate AI-facing read/write use case) and any pinned tool for
//   sys.oauth_client's generate_client_secret runtime method -- per an
//   explicit product decision, session/credential-sensitive actions on this
//   module stay reachable only through base.record.call_method (normal RBAC
//   checks, no dedicated advertised tool), not through a first-class auth.*
//   tool name.

import (
	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	authmodels "git.thebase.vn/base_hub/modules/base-auth/models"
)

// authAllowedModels is the fixed set of runtime models the auth.* record
// tools may target. An explicit allowlist (rather than a "sys." name-prefix
// check) keeps the tool group precise and excludes the sensitive token
// tables above.
var authAllowedModels = map[string]bool{
	authmodels.SysOauthClientModelName: true,
}

// Tools is the auth.* MCPToolDescriptor catalog, registered with base-mcp via
// Register (provider.go) so it's appended to tools/list only when the
// base-auth module is installed for the caller's tenant DB.
func Tools() []kernelmodules.MCPToolDescriptor {
	recordModelProperty := map[string]any{"type": "string", "enum": []string{authmodels.SysOauthClientModelName}}

	return []kernelmodules.MCPToolDescriptor{
		{
			Name:        "auth.record.search_read",
			Description: "Search and read sys.oauth_client records (registered OAuth client_credentials callers), subject to ACL/record-rule/company scope. Same behavior as base.record.search_read, restricted to sys.oauth_client. client_secret is never returned -- only client_secret_hash's presence.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model":  recordModelProperty,
					"domain": map[string]any{"type": "array"},
					"fields": map[string]any{"type": "array"},
					"limit":  map[string]any{"type": "integer", "default": 20},
					"offset": map[string]any{"type": "integer"},
					"order":  map[string]any{"type": "string"},
				},
				"required": []string{"model"},
			},
		},
		{
			Name:        "auth.record.read",
			Description: "Read specific sys.oauth_client records by id. Same behavior as base.record.read, restricted to sys.oauth_client.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model":  recordModelProperty,
					"ids":    map[string]any{"type": "array"},
					"fields": map[string]any{"type": "array"},
				},
				"required": []string{"model", "ids"},
			},
		},
		{
			Name:        "auth.record.create",
			Description: "Register a new sys.oauth_client caller. Same behavior as base.record.create, restricted to sys.oauth_client. If client_secret is omitted, one is generated. The plaintext client_secret is only ever returned in this create response -- it is never stored or returned again afterward. idempotency_key is required: a stable caller-chosen string so a retried call after a timeout replays the original result instead of registering a duplicate client.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model":           recordModelProperty,
					"values":          map[string]any{"type": "object"},
					"idempotency_key": map[string]any{"type": "string", "description": "Required. A stable caller-chosen string that dedupes retried calls."},
				},
				"required": []string{"model", "values", "idempotency_key"},
			},
		},
		{
			Name:        "auth.record.write",
			Description: "Update specific sys.oauth_client records by id (e.g. service_name, role_id, is_superadmin, redirect_uris, active). Same behavior as base.record.write, restricted to sys.oauth_client. To rotate a client's secret, prefer base.record.call_method with method=\"generate_client_secret\" rather than writing client_secret directly.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model": recordModelProperty,
					"ids":   map[string]any{"type": "array"},
					"values": map[string]any{
						"description": "One object applied to every id, or an array of objects (one per id, same order) for a per-record batch write.",
						"oneOf": []map[string]any{
							{"type": "object"},
							{"type": "array", "items": map[string]any{"type": "object"}},
						},
					},
				},
				"required": []string{"model", "ids", "values"},
			},
		},
		{
			Name:        "auth.record.unlink",
			Description: "Delete specific sys.oauth_client records by id, revoking that caller's ability to authenticate. Same behavior as base.record.unlink, restricted to sys.oauth_client.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model": recordModelProperty,
					"ids":   map[string]any{"type": "array"},
				},
				"required": []string{"model", "ids"},
			},
		},
	}
}
