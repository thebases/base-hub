package mcp

// File overview:
// - Responsibility: the project.* MCP tool catalog -- record CRUD restricted
//   to every model owned by the project module.
// - Key symbols: Tools, projectAllowedModels.

import (
	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	projectmodels "git.thebase.vn/base_hub/modules/project/models"
)

// projectAllowedModelNames lists every runtime model the project.record.*
// tools may target.
var projectAllowedModelNames = []string{
	projectmodels.ProjectModelName,
	projectmodels.ProjectStageModelName,
	projectmodels.ProjectTaskTypeModelName,
	projectmodels.ProjectTaskStagePersonalModelName,
	projectmodels.ProjectTagModelName,
	projectmodels.ProjectMilestoneModelName,
	projectmodels.ProjectUpdateModelName,
	projectmodels.ProjectCollaboratorModelName,
	projectmodels.ProjectRoleModelName,
	projectmodels.ProjectTaskModelName,
}

// projectAllowedModels is the fixed set of runtime models the project.record.*
// tools may target.
var projectAllowedModels = func() map[string]bool {
	out := make(map[string]bool, len(projectAllowedModelNames))
	for _, name := range projectAllowedModelNames {
		out[name] = true
	}
	return out
}()

// Tools is the project.* MCPToolDescriptor catalog, registered with base-mcp
// via Register (provider.go) so it's appended to tools/list only when the
// project module is installed for the caller's tenant DB.
func Tools() []kernelmodules.MCPToolDescriptor {
	recordModelProperty := map[string]any{"type": "string", "enum": projectAllowedModelNames}

	return []kernelmodules.MCPToolDescriptor{
		{
			Name:        "project.record.search_read",
			Description: "Search and read records of any project-module model (project, stages, tags, milestones, updates, roles, tasks, etc.), subject to ACL/record-rule/company scope. Same behavior as base.record.search_read.",
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
			Name:        "project.record.read",
			Description: "Read specific records of any project-module model by id. Same behavior as base.record.read.",
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
			Name:        "project.record.create",
			Description: "Create one record of any project-module model. Same behavior as base.record.create. idempotency_key is required: a stable caller-chosen string so a retried call after a timeout replays the original result instead of creating a duplicate record.",
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
			Name:        "project.record.write",
			Description: "Update specific records of any project-module model by id. Same behavior as base.record.write.",
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
			Name:        "project.record.unlink",
			Description: "Delete specific records of any project-module model by id. Same behavior as base.record.unlink.",
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
