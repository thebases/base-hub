package mcp

// File overview:
// - Responsibility: the workflow.* MCP tool catalog (CRUD over
//   base.workflow.* models, plus pinned wrappers for export/import and
//   canvas action/edge replace).
// - Key symbols: Tools, workflowAllowedModels.
// - Moved verbatim (only the descriptor type changed, to
//   kernelmodules.MCPToolDescriptor) out of
//   modules/base-mcp/routes/mcp_tools_workflow.go, per the mcp-module-scaffold
//   plan's Phase 4 -- this move also removes the modules/base-workflow/models
//   inbound import base-mcp previously had (an undocumented module->module
//   coupling flagged in base-mcp-context.md), since that import is now
//   intra-module.

import (
	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

// workflowAllowedModels is the fixed set of runtime models the workflow.*
// record tools may target. An explicit allowlist (rather than a
// "base.workflow" name-prefix check) keeps the tool group precise.
var workflowAllowedModels = map[string]bool{
	workflowmodels.FlowModelName:                  true,
	workflowmodels.WorkflowActionModelName:        true,
	workflowmodels.WorkflowEdgeModelName:          true,
	workflowmodels.WorkflowTriggerModelName:       true,
	workflowmodels.WorkflowCredentialModelName:    true,
	workflowmodels.WorkflowRunModelName:           true,
	workflowmodels.OutboundDeliveryModelName:      true,
	workflowmodels.WorkflowActionCatalogModelName: true,
}

// Tools is the workflow.* MCPToolDescriptor catalog, registered with
// base-mcp via Register (provider.go) so it's appended to tools/list only
// when the base-workflow module is installed for the caller's tenant DB.
func Tools() []kernelmodules.MCPToolDescriptor {
	workflowModelEnum := []string{
		workflowmodels.FlowModelName, workflowmodels.WorkflowActionModelName,
		workflowmodels.WorkflowEdgeModelName, workflowmodels.WorkflowTriggerModelName,
		workflowmodels.WorkflowCredentialModelName, workflowmodels.WorkflowRunModelName,
		workflowmodels.OutboundDeliveryModelName, workflowmodels.WorkflowActionCatalogModelName,
	}
	recordModelProperty := map[string]any{"type": "string", "enum": workflowModelEnum}

	return []kernelmodules.MCPToolDescriptor{
		{
			Name:        "workflow.record.search_read",
			Description: "Search and read workflow module records (base.workflow, base.workflow.action, base.workflow.edge, base.workflow.trigger, base.workflow.credential, base.workflow.run, base.workflow.outbound, base.workflow.action.catalog), subject to ACL/record-rule/company scope. Same behavior as base.record.search_read, restricted to workflow.* models.",
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
			Name:        "workflow.record.read",
			Description: "Read specific workflow module records by id. Same behavior as base.record.read, restricted to workflow.* models.",
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
			Name:        "workflow.record.create",
			Description: "Create one workflow module record. Same behavior as base.record.create, restricted to workflow.* models. For authoring a whole workflow's actions/edges in one call, prefer workflow.replace_action_sequence/workflow.replace_workflow_edges. idempotency_key is required: a stable caller-chosen string so a retried call after a timeout replays the original result instead of creating a duplicate record.",
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
			Name:        "workflow.record.write",
			Description: "Update specific workflow module records by id. Same behavior as base.record.write (including the per-id batch values array form), restricted to workflow.* models.",
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
			Name:        "workflow.record.unlink",
			Description: "Delete specific workflow module records by id. Same behavior as base.record.unlink, restricted to workflow.* models.",
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
			Name:        "workflow.export",
			Description: "Export one base.workflow as a portable document (schema version 2): {version, flow, actions, edges, triggers, layout, credentials}. flow holds the settings (name, topic, description, execution_mode, max_parallel_steps, dag_failure_policy, audit_redact_keys) and the access group by external id (group; group_restricted when the group has none). Actions name their type as action = \"<action_code>@<version>\" instead of a database id; secrets appear only as @cred:/@syscred: references (credentials lists them; the target database needs credentials with those names). No database ids are exported. Pass the document unchanged to workflow.import.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"workflow_id": map[string]any{"type": "integer"}},
				"required":   []string{"workflow_id"},
			},
		},
		{
			Name:        "workflow.import",
			Description: "Create a new base.workflow from a document returned by workflow.export (schema version 1 or 2; a higher version is refused). The new workflow always gets a fresh unique name and topic (never the exported topic). Steps, DAG edges and triggers are created in one transaction: either everything is imported or nothing. Every non-webhook trigger (Kafka, MQTT, model, tcp) is created inactive so it does not consume the source's topic: enable it after review. The access group is resolved by external id; a group that cannot be resolved falls back to base.group_system. idempotency_key is required: a stable caller-chosen string so a retried call after a timeout replays the original result instead of creating a duplicate workflow.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"version":         map[string]any{"type": "integer", "description": "Export schema version (1 or 2). Missing means 1."},
					"flow":            map[string]any{"type": "object", "description": "The exported workflow settings (name, topic, description, execution_mode, max_parallel_steps, dag_failure_policy, audit_redact_keys, group)."},
					"actions":         map[string]any{"type": "array", "description": "The exported action step objects."},
					"edges":           map[string]any{"type": "array", "description": "The exported DAG edges (from_step_key, to_step_key, edge_type, condition_expr)."},
					"triggers":        map[string]any{"type": "array", "description": "The exported trigger objects."},
					"layout":          map[string]any{"type": "object", "description": "Optional canvas node-position layout."},
					"idempotency_key": map[string]any{"type": "string", "description": "Required. A stable caller-chosen string that dedupes retried calls."},
				},
				"required": []string{"flow", "idempotency_key"},
			},
		},
		{
			Name:        "workflow.replace_action_sequence",
			Description: "Replace a workflow's entire ordered action sequence in one transaction: deletes every existing base.workflow.action row for workflow_id, then creates steps in the given order (a step without its own sequence gets one from its position). step_key must be unique in the sequence; DAG edges of steps that are no longer present are deleted. Pass expected_revision (base.workflow.revision read before editing) to be refused with a conflict when someone saved the workflow in between. Returns {ok, revision}. Use this instead of individual workflow.record.create/write/unlink calls on base.workflow.action when authoring or rewriting a workflow's steps.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"workflow_id":       map[string]any{"type": "integer"},
					"steps":             map[string]any{"type": "array", "items": map[string]any{"type": "object"}, "description": "Ordered list of base.workflow.action field values (each becomes one row)."},
					"expected_revision": map[string]any{"type": "integer", "description": "Optional. The workflow revision the steps were based on."},
				},
				"required": []string{"workflow_id", "steps"},
			},
		},
		{
			Name:        "workflow.replace_workflow_edges",
			Description: "Replace a workflow's entire DAG edge set in one transaction: deletes every existing base.workflow.edge row for workflow_id, then creates the given edges. Every edge must name existing step keys of the workflow and the graph must be acyclic. Only meaningful when the workflow's execution_mode is \"dag\". Pass expected_revision to be refused with a conflict when someone saved the workflow in between. Returns {ok, revision}. Use this instead of individual workflow.record.create/write/unlink calls on base.workflow.edge.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"workflow_id":       map[string]any{"type": "integer"},
					"edges":             map[string]any{"type": "array", "items": map[string]any{"type": "object"}, "description": "List of base.workflow.edge field values (from_step_key, to_step_key, edge_type, ...)."},
					"expected_revision": map[string]any{"type": "integer", "description": "Optional. The workflow revision the edges were based on."},
				},
				"required": []string{"workflow_id", "edges"},
			},
		},
	}
}
