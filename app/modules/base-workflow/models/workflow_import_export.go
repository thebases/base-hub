package models

// File overview:
// - Responsibility: `base.workflow` export/import: a version-tagged JSON snapshot of one workflow
//   (settings, actions, DAG edges, triggers, layout) and recreating a workflow from it.
// - Key symbols: `exportFlow`, `importFlow`, `workflowExportVersion`.
// - Schema v2 (plan T61) is portable across databases: no database ids. An action is identified by
//   `action` = "<action_code>@<version>" (the catalog row is looked up again in the target
//   database), the access group by its external id (`flow.group`), secrets only as `@cred:` /
//   `@syscred:` references (a literal left on an unmigrated row is replaced by a placeholder,
//   plan T40) and `credentials` lists the references the target needs. v1 documents (no edges,
//   database ids) are still accepted.
// - Pattern: import always mints a fresh name/topic (never the exported topic), like `copyFlow`,
//   so an import can never take over a live webhook path; it runs in one transaction (plan T59),
//   reuses the per-row create paths of copy (same validation: secret refs, unique step keys,
//   edges naming existing steps) and creates every non-webhook trigger inactive.

import (
	"context"
	"fmt"
	"maps"
	"sort"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// workflowExportVersion is the schema version written by export; import accepts 1..this.
const workflowExportVersion = 2

const (
	workflowGroupModelName = "tbl.groups"
	sysModelDataModelName  = "sys.model.data"
	// workflowRestrictedGroupXMLID is the group an imported workflow falls back to when the source
	// was restricted to a group this database cannot resolve: never more open than the source.
	workflowRestrictedGroupXMLID = "base.group_system"
)

// workflowExportedFlowFields are the base.workflow settings carried by a v2 export.
var workflowExportedFlowFields = []string{"name", "topic", "description", "execution_mode", "max_parallel_steps", "dag_failure_policy", "audit_redact_keys"}

// workflowRowDropOnExport are database-local columns never written to an export.
var workflowRowDropOnExport = []string{"id", "workflow_id", "company_id", "catalog_id", "created_at", "updated_at", "active"}

// workflowImportExportPayload resolves the flat export document passed to importFlow, whether it
// arrived as a bare positional arg (route, direct Go caller), under a "payload"/"values" kwarg, or
// as the kwargs themselves (MCP workflow.import passes {version, flow, actions, edges, ...}).
func workflowImportExportPayload(args []any, kwargs map[string]any) map[string]any {
	if payload := workflowRecordMapFromAny(argValue(args, 0)); payload != nil {
		return payload
	}
	for _, key := range []string{"payload", "values"} {
		if payload := workflowRecordMapFromAny(mapValue(kwargs, key)); payload != nil {
			return payload
		}
	}
	if _, ok := kwargs["flow"]; ok {
		return kwargs
	}
	return map[string]any{}
}

// workflowRecordMapFromAny unwraps a plain nested object (orm.RecordMap or map[string]any)
// without imposing any many2one/array shape on its values.
func workflowRecordMapFromAny(raw any) map[string]any {
	switch typed := raw.(type) {
	case orm.RecordMap:
		return map[string]any(typed)
	case map[string]any:
		return typed
	default:
		return nil
	}
}

// exportFlow serializes one workflow as a schema v2 document.
func exportFlow(ctx context.Context, model *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	if len(ids) == 0 {
		log.Errorf("base.workflow.export requires an id")
		return nil, orm.InvalidParamsf("base.workflow.export requires an id")
	}
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(FlowModelName, model), ids, nil)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, orm.NotFoundf("base.workflow.export: workflow %s not found", ids[0])
	}
	flowRow := cloneRecordMap(rows[0])
	workflowID := int64Value(flowRow["id"])

	layout, _ := flowRow["layout"].(map[string]any)

	flowExport := orm.RecordMap{}
	for _, field := range workflowExportedFlowFields {
		if value, exists := flowRow[field]; exists {
			flowExport[field] = value
		}
	}
	groupXMLID, restricted, err := workflowGroupExternalID(ctx, flowRow["group_id"])
	if err != nil {
		return nil, err
	}
	flowExport["group"] = groupXMLID
	// A group without an external id cannot be named portably; the import then falls back to
	// the system group instead of making the workflow public.
	flowExport["group_restricted"] = restricted && groupXMLID == ""

	credentialRefs := map[string]struct{}{}

	actionRows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WorkflowActionModelName, nil), orm.RecordMap{
		"workflow_id": workflowID,
		"__order":     "sequence asc, id asc",
	}, nil)
	if err != nil {
		return nil, err
	}
	actions := make([]orm.RecordMap, 0, len(actionRows))
	for _, row := range actionRows {
		exported := cloneRecordMap(row)
		for _, field := range workflowRowDropOnExport {
			delete(exported, field)
		}
		redactWorkflowActionLiteralSecrets(exported)
		code := strings.TrimSpace(stringValue(exported["action_code"]))
		version := int64Value(exported["action_version"])
		if version <= 0 {
			version = 1
		}
		exported["action_code"] = code
		exported["action_version"] = version
		exported["action"] = fmt.Sprintf("%s@%d", code, version)
		collectWorkflowActionCredentialRefs(exported, credentialRefs)
		actions = append(actions, exported)
	}

	edgeRows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WorkflowEdgeModelName, nil), orm.RecordMap{
		"workflow_id": workflowID,
		"__order":     "id asc",
	}, nil)
	if err != nil {
		return nil, err
	}
	edges := make([]orm.RecordMap, 0, len(edgeRows))
	for _, row := range edgeRows {
		values := workflowEdgeCopyValues(row, 0)
		delete(values, "workflow_id")
		edges = append(edges, values)
	}

	triggerRows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WorkflowTriggerModelName, nil), orm.RecordMap{
		"workflow_id": workflowID,
		"__order":     "id asc",
	}, nil)
	if err != nil {
		return nil, err
	}
	triggers := make([]orm.RecordMap, 0, len(triggerRows))
	for _, row := range triggerRows {
		exported := cloneRecordMap(row)
		for _, field := range workflowRowDropOnExport {
			delete(exported, field)
		}
		if config, ok := exported["config"].(map[string]any); ok {
			if ref, ok := config[WebhookTriggerConfigHMACSecret].(string); ok && IsWorkflowCredentialRef(ref) {
				credentialRefs[strings.TrimSpace(ref)] = struct{}{}
			}
		}
		triggers = append(triggers, exported)
	}

	credentials := make([]string, 0, len(credentialRefs))
	for ref := range credentialRefs {
		credentials = append(credentials, ref)
	}
	sort.Strings(credentials)

	return orm.RecordMap{
		"version":     workflowExportVersion,
		"flow":        flowExport,
		"actions":     actions,
		"edges":       edges,
		"triggers":    triggers,
		"layout":      layout,
		"credentials": credentials,
	}, nil
}

// collectWorkflowActionCredentialRefs records the @cred:/@syscred: references an action uses.
func collectWorkflowActionCredentialRefs(action orm.RecordMap, into map[string]struct{}) {
	add := func(raw any) {
		if ref, ok := raw.(string); ok && IsWorkflowCredentialRef(ref) {
			into[strings.TrimSpace(ref)] = struct{}{}
		}
	}
	for _, field := range workflowActionSecretColumns {
		add(action[field])
	}
	if extra, ok := action["extra_params"].(map[string]any); ok {
		for _, key := range workflowActionSecretExtraParamKeys {
			add(extra[key])
		}
	}
}

// workflowGroupExternalID returns the external id (sys.model.data complete_name) of the workflow's
// access group, and whether the workflow is restricted to a group at all.
func workflowGroupExternalID(ctx context.Context, rawGroupID any) (string, bool, error) {
	groupID := workflowIDFromAny(rawGroupID)
	if groupID <= 0 {
		return "", false, nil
	}
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(sysModelDataModelName, nil), orm.RecordMap{
		"model":    workflowGroupModelName,
		"model_id": strconv.FormatInt(groupID, 10),
		"__order":  "id asc",
		"__limit":  1,
	}, []string{"complete_name"})
	if err != nil {
		return "", true, err
	}
	if len(rows) == 0 {
		return "", true, nil
	}
	return strings.TrimSpace(stringValue(rows[0]["complete_name"])), true, nil
}

// workflowGroupIDByExternalID resolves a tbl.groups external id in the current database.
func workflowGroupIDByExternalID(ctx context.Context, xmlID string) (int64, error) {
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(sysModelDataModelName, nil), orm.RecordMap{
		"complete_name": xmlID,
		"model":         workflowGroupModelName,
		"__order":       "id asc",
		"__limit":       1,
	}, []string{"model_id"})
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	id, _ := int64FromAny(strings.TrimSpace(fmt.Sprint(rows[0]["model_id"])))
	return id, nil
}

// importedWorkflowGroupID maps the exported access group onto this database. Database ids are never
// taken verbatim: v2 names the group by external id; a v1 document (raw id) or a v2 group without
// an external id falls back to the system group so the import is never more public than its source.
func importedWorkflowGroupID(ctx context.Context, version int64, flow map[string]any) (any, error) {
	xmlID := ""
	restricted := false
	if version >= 2 {
		xmlID = strings.TrimSpace(stringValue(flow["group"]))
		restricted = boolValue(flow["group_restricted"])
	} else if groupID := workflowIDFromAny(flow["group_id"]); groupID > 0 {
		restricted = true
	}
	if xmlID == "" && !restricted {
		return nil, nil
	}
	if xmlID == "" {
		log.Warnf("base.workflow.import: source group cannot be mapped to this database; using %s", workflowRestrictedGroupXMLID)
		xmlID = workflowRestrictedGroupXMLID
	}
	groupID, err := workflowGroupIDByExternalID(ctx, xmlID)
	if err != nil {
		return nil, err
	}
	if groupID <= 0 {
		log.Errorf("base.workflow.import group %q does not exist in this database", xmlID)
		return nil, orm.InvalidParamsf("base.workflow.import group %q does not exist in this database", xmlID)
	}
	return groupID, nil
}

// workflowImportVersion reads and checks the document version (missing = 1).
func workflowImportVersion(payload map[string]any) (int64, error) {
	raw, exists := payload["version"]
	if !exists || raw == nil {
		return 1, nil
	}
	version, ok := int64FromAny(raw)
	if !ok || version < 1 || version > workflowExportVersion {
		log.Errorf("base.workflow.import unsupported export version %v", raw)
		return 0, orm.InvalidParamsf("base.workflow.import unsupported export version %v (this server reads versions 1 to %d)", raw, workflowExportVersion)
	}
	return version, nil
}

// importedWorkflowActionValues prepares one exported action for createWorkflowAction in this
// database: the source catalog id is dropped and the catalog row is looked up by code and version.
func importedWorkflowActionValues(ctx context.Context, action orm.RecordMap, workflowID int64) (orm.RecordMap, error) {
	values := cloneRecordMap(action)
	for _, field := range workflowRowDropOnExport {
		delete(values, field)
	}
	if spec := strings.TrimSpace(stringValue(values["action"])); spec != "" {
		code, versionText, hasVersion := strings.Cut(spec, "@")
		values["action_code"] = strings.TrimSpace(code)
		if hasVersion {
			version, ok := int64FromAny(strings.TrimSpace(versionText))
			if !ok || version <= 0 {
				log.Errorf("base.workflow.import action %q has an invalid version", spec)
				return nil, orm.InvalidParamsf("base.workflow.import action %q has an invalid version", spec)
			}
			values["action_version"] = version
		}
	}
	delete(values, "action")
	dropImportedApproverGroup(values)
	code := strings.TrimSpace(stringValue(values["action_code"]))
	version := int64Value(values["action_version"])
	if version <= 0 {
		version = 1
	}
	values["action_version"] = version
	catalogID, err := workflowActionCatalogIDByCode(ctx, code, version)
	if err != nil {
		return nil, err
	}
	if catalogID > 0 {
		values["catalog_id"] = catalogID
	}
	values["workflow_id"] = workflowID
	return values, nil
}

// dropImportedApproverGroup removes a wait_approval approver_group_id from an imported action.
// Database ids are never taken verbatim (see importedWorkflowGroupID): the id could name a
// different group here and let its members decide without the token. Without it the wait falls
// back to token-only decisions until an author picks the group again.
func dropImportedApproverGroup(values orm.RecordMap) {
	extra, ok := values["extra_params"].(map[string]any)
	if !ok || extra[WaitApprovalGroupParam] == nil {
		return
	}
	extra = maps.Clone(extra)
	delete(extra, WaitApprovalGroupParam)
	values["extra_params"] = extra
	log.Warnf("base.workflow.import step %q: approver_group_id dropped (database ids are not portable); set the approver group again", stringValue(values["step_key"]))
}

// workflowActionCatalogIDByCode finds this database's catalog row for code@version (0 = none; the
// action is then stored by action_code/action_version only, like a builtin without a catalog row).
func workflowActionCatalogIDByCode(ctx context.Context, code string, version int64) (int64, error) {
	if code == "" {
		return 0, nil
	}
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WorkflowActionCatalogModelName, nil), orm.RecordMap{
		"code":    code,
		"version": version,
		"__order": "id asc",
		"__limit": 1,
	}, []string{"id"})
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return int64Value(rows[0]["id"]), nil
}

// importFlow recreates a workflow from a document produced by exportFlow (v1 or v2). The new
// workflow always gets a fresh, unique name/topic, and everything is created in one transaction.
func importFlow(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	payload := workflowImportExportPayload(args, kwargs)
	version, err := workflowImportVersion(payload)
	if err != nil {
		return nil, err
	}

	flowValues := workflowRecordMapFromAny(payload["flow"])
	if flowValues == nil {
		log.Errorf("base.workflow.import requires a flow object")
		return nil, orm.InvalidParamsf("base.workflow.import requires a flow object")
	}
	actions, err := workflowActionValuesListFromAny(payload["actions"])
	if err != nil {
		return nil, err
	}
	edges, err := workflowActionValuesListFromAny(payload["edges"])
	if err != nil {
		return nil, err
	}
	triggers, err := workflowActionValuesListFromAny(payload["triggers"])
	if err != nil {
		return nil, err
	}
	layout, _ := payload["layout"].(map[string]any)

	groupID, err := importedWorkflowGroupID(ctx, version, flowValues)
	if err != nil {
		return nil, err
	}

	createValues := orm.RecordMap{
		"name":        workflowCopyUniqueText(mapValue(flowValues, "name"), "workflow"),
		"topic":       workflowCopyUniqueText(mapValue(flowValues, "topic"), "workflow"),
		"description": mapValue(flowValues, "description"),
		"group_id":    groupID,
		"layout":      layout,
	}
	for _, field := range []string{"execution_mode", "max_parallel_steps", "dag_failure_policy", "audit_redact_keys"} {
		if value, exists := flowValues[field]; exists && value != nil && value != false {
			createValues[field] = value
		}
	}

	var targetWorkflowID int64
	err = runInWorkflowTx(ctx, 0, func(txCtx context.Context, _ int64) error {
		createdAny, err := createFlow(txCtx, model, nil, []any{createValues}, nil)
		if err != nil {
			return err
		}
		created, ok := createdAny.(orm.RecordMap)
		if !ok {
			return fmt.Errorf("base.workflow.import expected one created record, got %T", createdAny)
		}
		targetWorkflowID = int64Value(created["id"])
		targetTopic := stringValue(created["topic"])

		for _, action := range actions {
			values, err := importedWorkflowActionValues(txCtx, action, targetWorkflowID)
			if err != nil {
				return err
			}
			if _, err := createWorkflowAction(txCtx, nil, nil, []any{values}, nil); err != nil {
				return err
			}
		}
		for _, edge := range edges {
			if _, err := createWorkflowEdge(txCtx, nil, nil, []any{workflowEdgeCopyValues(edge, targetWorkflowID)}, nil); err != nil {
				return err
			}
		}
		for _, trigger := range triggers {
			values := cloneWorkflowTriggerCopyValues(trigger, targetWorkflowID, targetTopic)
			if _, err := createWorkflowTrigger(txCtx, nil, nil, []any{values}, nil); err != nil {
				return err
			}
		}
		return writeWorkflowFlowMap(txCtx, targetWorkflowID)
	})
	if err != nil {
		return nil, err
	}
	return orm.RecordMap{"id": targetWorkflowID, "workflow_id": targetWorkflowID}, nil
}
