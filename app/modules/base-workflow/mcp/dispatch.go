package mcp

// File overview:
// - Responsibility: dispatches one workflow.* MCP tool call.
// - Key symbols: Dispatch.
// - Moved verbatim out of modules/base-mcp/routes/mcp_tools_workflow.go, per
//   the mcp-module-scaffold plan's Phase 4 -- every helper now calls
//   internal/mcpkit directly instead of this package's former private
//   helpers of the same name (mcpRecordDispatch, mcpLookupRuntimeMethod,
//   mcpExecuteRuntimeMethod, requireArrayArg, int64FromArgument), so
//   ACL/record-rule/company-scope enforcement stays byte-for-byte identical.

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/mcpkit"
	"git.thebase.vn/base_hub/internal/orm"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

// Dispatch routes one workflow.* MCP tool call. Registered as this
// provider's Dispatch func (see provider.go) -- base-mcp's provider cache
// already confirmed the base-workflow module is installed for the caller's
// tenant DB before ever reaching here.
func Dispatch(ctx context.Context, identity *orm.RBACIdentity, toolName string, arguments map[string]any) (any, error) {
	switch toolName {
	case "workflow.record.search_read":
		return workflowRecordDispatch(ctx, identity, arguments, "search_read", mcpkit.RecordModeSearch)
	case "workflow.record.read":
		return workflowRecordDispatch(ctx, identity, arguments, "read", mcpkit.RecordModeRead)
	case "workflow.record.create":
		return workflowRecordDispatch(ctx, identity, arguments, "create", mcpkit.RecordModeCreate)
	case "workflow.record.write":
		return workflowRecordDispatch(ctx, identity, arguments, "write", mcpkit.RecordModeWrite)
	case "workflow.record.unlink":
		return workflowRecordDispatch(ctx, identity, arguments, "unlink", mcpkit.RecordModeUnlink)
	case "workflow.export":
		return mcpWorkflowExport(ctx, identity, arguments)
	case "workflow.import":
		return mcpWorkflowImport(ctx, identity, arguments)
	case "workflow.replace_action_sequence":
		return mcpWorkflowReplaceActionSequence(ctx, identity, arguments)
	case "workflow.replace_workflow_edges":
		return mcpWorkflowReplaceWorkflowEdges(ctx, identity, arguments)
	default:
		return nil, mcpkit.NewToolError(404, "tool_not_found", fmt.Sprintf("unknown mcp tool %q", toolName))
	}
}

// workflowRecordDispatch backs workflow.record.*: validates arguments.model
// is one of the workflow.* models, then delegates to the exact same
// mcpkit.RecordDispatch base.record.* already uses.
func workflowRecordDispatch(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any, methodName string, mode mcpkit.RecordMode) (any, error) {
	if err := requireWorkflowAllowedModel(arguments); err != nil {
		return nil, err
	}
	return mcpkit.RecordDispatch(ctx, identity, arguments, methodName, mode)
}

func requireWorkflowAllowedModel(arguments map[string]any) error {
	modelName := strings.TrimSpace(mcpkit.StringFromAny(arguments["model"]))
	if modelName == "" {
		return mcpkit.NewToolError(422, "invalid_params", "arguments.model is required")
	}
	if !workflowAllowedModels[modelName] {
		names := make([]string, 0, len(workflowAllowedModels))
		for name := range workflowAllowedModels {
			names = append(names, name)
		}
		sort.Strings(names)
		return mcpkit.NewToolError(422, "invalid_params", fmt.Sprintf(
			"model %q is not one of the workflow.* models this tool group accepts (%s)", modelName, strings.Join(names, ", "),
		))
	}
	return nil
}

// mcpWorkflowExport backs workflow.export: a thin, pinned-method wrapper over
// base.workflow's "export" runtime method.
func mcpWorkflowExport(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any) (any, error) {
	workflowID, ok := mcpkit.Int64FromArgument(arguments["workflow_id"])
	if !ok || workflowID <= 0 {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.workflow_id is required")
	}
	meta, method, err := mcpkit.LookupRuntimeMethod(workflowmodels.FlowModelName, "export")
	if err != nil {
		return nil, err
	}
	return mcpkit.ExecuteRuntimeMethod(ctx, identity, meta, method, []string{strconv.FormatInt(workflowID, 10)}, nil, nil)
}

// mcpWorkflowImport backs workflow.import: a thin, pinned-method wrapper over
// base.workflow's "import" runtime method, which expects the flat document
// workflow.export returns ({version, flow, actions, edges, triggers, layout}),
// not the "values"-keyed create shape. It is passed as "payload" so the method
// reads it the same way as the /wf/workflows/import route.
func mcpWorkflowImport(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any) (any, error) {
	document := map[string]any{}
	for _, key := range []string{"version", "flow", "actions", "edges", "triggers", "layout"} {
		if value, ok := arguments[key]; ok {
			document[key] = value
		}
	}
	kwargs := map[string]any{"payload": document}
	meta, method, err := mcpkit.LookupRuntimeMethod(workflowmodels.FlowModelName, "import")
	if err != nil {
		return nil, err
	}
	return mcpkit.ExecuteRuntimeMethod(ctx, identity, meta, method, nil, nil, kwargs)
}

// mcpWorkflowReplaceActionSequence backs workflow.replace_action_sequence: a
// thin, pinned-method wrapper over base.workflow's "replace_action_sequence"
// runtime method, which unlink-then-recreates the whole ordered action
// sequence for one workflow id.
func mcpWorkflowReplaceActionSequence(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any) (any, error) {
	workflowID, ok := mcpkit.Int64FromArgument(arguments["workflow_id"])
	if !ok || workflowID <= 0 {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.workflow_id is required")
	}
	steps, err := mcpkit.RequireArrayArg(arguments, "steps")
	if err != nil {
		return nil, err
	}
	meta, method, err := mcpkit.LookupRuntimeMethod(workflowmodels.FlowModelName, "replace_action_sequence")
	if err != nil {
		return nil, err
	}
	kwargs := map[string]any{"id": workflowID, "steps": steps}
	if expected, ok := arguments["expected_revision"]; ok {
		kwargs["expected_revision"] = expected
	}
	return mcpkit.ExecuteRuntimeMethod(ctx, identity, meta, method, nil, nil, kwargs)
}

// mcpWorkflowReplaceWorkflowEdges backs workflow.replace_workflow_edges: a
// thin, pinned-method wrapper over base.workflow's "replace_workflow_edges"
// runtime method, which unlink-then-recreates the whole DAG edge set for one
// workflow id.
func mcpWorkflowReplaceWorkflowEdges(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any) (any, error) {
	workflowID, ok := mcpkit.Int64FromArgument(arguments["workflow_id"])
	if !ok || workflowID <= 0 {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.workflow_id is required")
	}
	edges, err := mcpkit.RequireArrayArg(arguments, "edges")
	if err != nil {
		return nil, err
	}
	meta, method, err := mcpkit.LookupRuntimeMethod(workflowmodels.FlowModelName, "replace_workflow_edges")
	if err != nil {
		return nil, err
	}
	kwargs := map[string]any{"id": workflowID, "edges": edges}
	if expected, ok := arguments["expected_revision"]; ok {
		kwargs["expected_revision"] = expected
	}
	return mcpkit.ExecuteRuntimeMethod(ctx, identity, meta, method, nil, nil, kwargs)
}
