package mcp

// File overview:
// - Responsibility: the credential.* MCP tool catalog -- record CRUD
//   restricted to sys.credential.
// - Key symbols: Tools, credentialAllowedModels.
// - Deliberately excludes any pinned tool for sys.credential's "login"
//   runtime method -- per an explicit product decision, credential-sensitive
//   actions on this module stay reachable only through
//   base.record.call_method (normal RBAC checks, no dedicated advertised
//   tool), not through a first-class credential.* tool name. Secret fields
//   (password, client_secret, access_token, refresh_token) are always
//   AES-GCM encrypted at rest and stripped from every runtime response by
//   the model layer itself, so credential.record.* never echoes plaintext
//   secrets back to a caller.

import (
	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	credentialmodels "git.thebase.vn/base_hub/modules/base-credential/models"
)

// credentialAllowedModels is the fixed set of runtime models the
// credential.record.* tools may target.
var credentialAllowedModels = map[string]bool{
	credentialmodels.SysCredentialModelName: true,
}

// Tools is the credential.* MCPToolDescriptor catalog, registered with
// base-mcp via Register (provider.go) so it's appended to tools/list only
// when the base-credential module is installed for the caller's tenant DB.
func Tools() []kernelmodules.MCPToolDescriptor {
	recordModelProperty := map[string]any{"type": "string", "enum": []string{credentialmodels.SysCredentialModelName}}

	return []kernelmodules.MCPToolDescriptor{
		{
			Name:        "credential.record.search_read",
			Description: "Search and read sys.credential records (named credentials used to authenticate against another system), subject to ACL/record-rule/company scope. Same behavior as base.record.search_read, restricted to sys.credential. Secret fields (password, client_secret, access_token, refresh_token) are never returned.",
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
			Name:        "credential.record.read",
			Description: "Read specific sys.credential records by id. Same behavior as base.record.read, restricted to sys.credential. Secret fields are never returned.",
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
			Name:        "credential.record.create",
			Description: "Create one sys.credential record (name, credential_type, auth_url, user_name/password or client_id/client_secret depending on credential_type). Same behavior as base.record.create, restricted to sys.credential. Secret fields you provide are encrypted at rest and never echoed back. idempotency_key is required: a stable caller-chosen string so a retried call after a timeout replays the original result instead of creating a duplicate credential.",
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
			Name:        "credential.record.write",
			Description: "Update specific sys.credential records by id. Same behavior as base.record.write, restricted to sys.credential. To force an immediate (re-)authentication, use base.record.call_method with method=\"login\" instead of writing token fields directly.",
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
			Name:        "credential.record.unlink",
			Description: "Delete specific sys.credential records by id. Same behavior as base.record.unlink, restricted to sys.credential.",
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
