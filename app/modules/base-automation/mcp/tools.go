package mcp

// File overview:
// - Responsibility: the automation.* MCP tool catalog (record CRUD over
//   automation.action/automation.cron, plus a pinned wrapper for
//   automation.action's "run" runtime method).
// - Key symbols: Tools, automationAllowedModels.

import (
	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	automationmodels "git.thebase.vn/base_hub/modules/base-automation/models"
)

// automationAllowedModels is the fixed set of runtime models the
// automation.* record tools may target.
var automationAllowedModels = map[string]bool{
	automationmodels.AutomationActionModelName: true,
	automationmodels.AutomationCronModelName:   true,
}

// Tools is the automation.* MCPToolDescriptor catalog, registered with
// base-mcp via Register (provider.go) so it's appended to tools/list only
// when the base-automation module is installed for the caller's tenant DB.
func Tools() []kernelmodules.MCPToolDescriptor {
	automationModelEnum := []string{automationmodels.AutomationActionModelName, automationmodels.AutomationCronModelName}
	recordModelProperty := map[string]any{"type": "string", "enum": automationModelEnum}

	return []kernelmodules.MCPToolDescriptor{
		{
			Name:        "automation.record.search_read",
			Description: "Search and read automation module records (automation.action, automation.cron), subject to ACL/record-rule/company scope. Same behavior as base.record.search_read, restricted to automation.* models.",
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
			Name:        "automation.record.read",
			Description: "Read specific automation module records by id. Same behavior as base.record.read, restricted to automation.* models.",
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
			Name:        "automation.record.create",
			Description: "Create one automation module record (automation.action, automation.cron). Same behavior as base.record.create, restricted to automation.* models. idempotency_key is required: a stable caller-chosen string so a retried call after a timeout replays the original result instead of creating a duplicate record.",
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
			Name:        "automation.record.write",
			Description: "Update specific automation module records by id. Same behavior as base.record.write (including the per-id batch values array form), restricted to automation.* models.",
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
			Name:        "automation.record.unlink",
			Description: "Delete specific automation module records by id. Same behavior as base.record.unlink, restricted to automation.* models.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model": recordModelProperty,
					"ids":   map[string]any{"type": "array"},
				},
				"required": []string{"model", "ids"},
			},
		},
		{
			Name:        "automation.action.run",
			Description: "Manually run one automation.action now, the same code path a due automation.cron row would take -- a faithful on-demand preview/trigger of the configured lua/sys_model/go_handler behavior.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action_id": map[string]any{"type": "integer"},
					"payload":   map[string]any{"type": "object", "description": "Optional payload passed through to the action (used by go_handler actions)."},
				},
				"required": []string{"action_id"},
			},
		},
	}
}
