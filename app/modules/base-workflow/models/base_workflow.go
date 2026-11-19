package models

// File overview:
// - Responsibility: defines the `workflow` module model contract for base workflow, including field metadata, runtime methods, and module-specific invariants.
// - Key symbols: `FlowModel`, `createFlow`, `RetryPolicy`, `FlowRecord`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

type RetryPolicy struct {
	MaxAttempts   int `json:"max_attempts,omitempty"`
	RetryCoolDown int `json:"retry_cool_down,omitempty"`
}

// DefaultRetryPolicy is the single source of truth for retry behavior across
// the workflow module: retry 3 times with a 15-second cooldown between
// attempts. Every model that persists a retry_policy column (workflow
// actions, outbound delivery) falls back to this value, so changing it here
// changes retry behavior everywhere instead of drifting per-model.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{MaxAttempts: 3, RetryCoolDown: 15}
}

const (
	WorkflowExecutionModeLinear = "linear"
	WorkflowExecutionModeDAG    = "dag"

	WorkflowDagFailurePolicyFailBranch   = "fail_branch"
	WorkflowDagFailurePolicyFailWorkflow = "fail_workflow"

	// DefaultMaxParallelSteps caps how many DAG-mode frontier steps run
	// concurrently per run when a workflow does not set its own value.
	DefaultMaxParallelSteps = 4
)

type FlowRecord struct {
	orm.BaseModel
	Name        string         `bun:"name,unique" json:"name" meta:"label:Name,help:Stores the unique name for this record."`
	Topic       string         `bun:"topic,unique" json:"topic" meta:"index,unique,filter,label:Topic,help:Stores the topic value for this record."`
	Version     int            `bun:"version" json:"version" meta:"index,label:Version,help:Stores the numeric value for version."`
	IsActive    bool           `bun:"is_active" json:"is_active" meta:"index,label:Is Active,help:Controls whether is active is enabled for this record."`
	GroupID     *int64         `bun:"group_id" json:"group_id" meta:"kind:many2one,model:tbl.groups,ondelete:SET NULL,index,label:Group ID,help:Restricts workflow access to one tbl.groups record. Leave empty to make the workflow public."`
	CompanyID   *int64         `bun:"company_id" json:"company_id" meta:"kind:many2one,model:tbl.company,ondelete:SET NULL,index,label:Company ID,help:Links this record to the related tbl.company record for company id."`
	Description string         `bun:"description" json:"description" meta:"label:Description,help:Stores the descriptive notes for this record."`
	FlowMap     string         `bun:"flow_map" json:"flow_map" meta:"readonly,label:Flow Map,help:Stores generated markdown with a Mermaid diagram that describes this workflow action sequence."`
	Layout      map[string]any `bun:"layout,type:jsonb,nullzero" json:"layout" meta:"kind:json,label:Canvas Layout,help:Stores visual node positions and wiring for the canvas UI. Does not affect execution."`
	// ExecutionMode gates the hybrid linear/DAG engine split in service/engine's
	// executeRun: "linear" (default) keeps sequence-ordered execution byte-identical
	// to pre-DAG behavior; "dag" only takes effect once base_workflow_edge rows exist.
	ExecutionMode string `bun:"execution_mode,default:'linear'" json:"execution_mode" meta:"label:Execution Mode,help:Controls whether this workflow runs its steps in linear sequence order or as a DAG scheduled by Kahn's algorithm." selection:"(linear,Linear),(dag,DAG)"`
	// MaxParallelSteps bounds the errgroup concurrency for each DAG frontier
	// batch; irrelevant in linear mode.
	MaxParallelSteps int `bun:"max_parallel_steps,default:4" json:"max_parallel_steps" meta:"label:Max Parallel Steps,help:Caps how many DAG frontier steps run concurrently per execution batch."`
	// DagFailurePolicy controls whether a failed DAG step only skips its own
	// downstream dependents (fail_branch, default) or aborts the whole run
	// (fail_workflow) once the current frontier batch finishes.
	DagFailurePolicy string `bun:"dag_failure_policy,default:'fail_branch'" json:"dag_failure_policy" meta:"label:DAG Failure Policy,help:Controls whether a failed DAG step only skips its downstream branch or aborts the whole run." selection:"(fail_branch,Fail Branch Only),(fail_workflow,Fail Whole Workflow)"`
	// AuditRedactKeys adds payload keys to the built-in audit redaction list (plan T45).
	AuditRedactKeys string `bun:"audit_redact_keys" json:"audit_redact_keys" meta:"label:Audit Redact Keys,help:Extra payload keys hidden in run history (one per line or comma separated in the value). A bare name matches at any depth and a dotted path such as customer.phone matches from the root. A built-in list of credential and card data keys is always hidden."`
	// Revision is bumped inside the locked transaction of every graph save (plan T59, workflow_tx.go);
	// a save sent with an older expected_revision is refused with a Conflict.
	Revision int64 `bun:"revision,default:0" json:"revision" meta:"readonly,label:Revision,help:Increases on every canvas save of the steps and edges; a save based on an older revision is refused."`
	// MaxRunSeconds bounds one execution of a run (plan T100): the engine runs it under
	// context.WithTimeout and ends it failed on expiry. 0 = no limit.
	MaxRunSeconds int `bun:"max_run_seconds,default:0" json:"max_run_seconds" meta:"label:Max Run Seconds,help:Longest time one execution of a run may take before it ends failed. 0 means no limit."`
	// MaxConcurrentRuns caps the runs of this workflow executing at once (plan T100), enforced at
	// run start under an advisory lock. 0 = unlimited.
	MaxConcurrentRuns int `bun:"max_concurrent_runs,default:0" json:"max_concurrent_runs" meta:"label:Max Concurrent Runs,help:Most runs of this workflow that may execute at the same time. Runs from Kafka or MQTT or model triggers wait and start later while callers of the webhook or a test run get a conflict. 0 means unlimited."`
	// Versioned definitions (plan T97, workflow_publish.go). Triggers and /wh run the published
	// snapshot; edits to steps, edges or execution settings only flip State to draft. The column
	// default is published so rows that existed before T97 count as published (the boot backfill in
	// base_workflow_version.go snapshots them); createFlow sets draft explicitly.
	State            string `bun:"state,default:'published'" json:"state" meta:"readonly,index,label:State,help:Draft means the steps or settings changed since the last publish. Triggers and webhooks always run the published version." selection:"(draft,Draft),(published,Published)"`
	PublishedVersion int    `bun:"published_version,default:0" json:"published_version" meta:"readonly,label:Published Version,help:Version number of the definition that triggers and webhooks run. 0 means never published."`
	// PublishedAt is a pointer like the other optional datetimes: a zero time.Time marshals as
	// "0001-01-01T00:00:00Z" on struct read paths (Bun fallback search, snapshots), which escapes
	// the zero -> false rule of base.TimesToUnix; nil marshals as null.
	PublishedAt       *time.Time     `bun:"published_at" json:"published_at" meta:"readonly,label:Published At,help:When the current published version was published."`
	PublishedSnapshot map[string]any `bun:"published_snapshot,type:jsonb,nullzero" json:"published_snapshot" meta:"kind:json,readonly,label:Published Snapshot,help:Frozen copy of the steps and edges and execution settings of the published version."`
	ActionIDs         []int64        `bun:"action_ids,type:jsonb,nullzero" json:"action_ids" meta:"kind:one2many,model:base.workflow.action,inverse:workflow_id,stored:false,label:Action Steps,help:Lists the configured workflow actions linked back through workflow_id."`
	RunIDs            []int64        `bun:"run_ids,type:jsonb,nullzero" json:"run_ids" meta:"kind:one2many,model:base.workflow.run,inverse:workflow_id,stored:false,label:Runs,help:Lists workflow execution runs linked back through workflow_id."`

	// IsTemplate marks a gallery template (plan T101): it is never started by a trigger, /wh or a
	// test run; instantiate_template copies it into a normal inactive workflow.
	IsTemplate bool `bun:"is_template,default:false" json:"is_template" meta:"index,label:Is Template,help:Marks this workflow as a template in the gallery. A template never runs. Instantiate it to get an editable copy."`
}

func (r *FlowRecord) UnmarshalJSON(data []byte) error {
	type flowRecordAlias FlowRecord
	type flowRecordPayload struct {
		flowRecordAlias
		GroupID   json.RawMessage `json:"group_id"`
		CompanyID json.RawMessage `json:"company_id"`
		ActionIDs json.RawMessage `json:"action_ids"`
		RunIDs    json.RawMessage `json:"run_ids"`
	}

	var payload flowRecordPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}

	*r = FlowRecord(payload.flowRecordAlias)
	// A many2one read back through the runtime service is [id, display_name] (or false), not a
	// bare id, so company_id is decoded by hand instead of through the *int64 tag.
	r.CompanyID = flowRelationID(payload.CompanyID)
	if len(payload.GroupID) == 0 || string(payload.GroupID) == "null" || string(payload.GroupID) == "false" {
		r.GroupID = nil
	}
	actionIDs, err := flowRelationIDsFromJSON(payload.ActionIDs)
	if err != nil {
		return err
	}
	runIDs, err := flowRelationIDsFromJSON(payload.RunIDs)
	if err != nil {
		return err
	}
	r.ActionIDs = actionIDs
	r.RunIDs = runIDs
	return nil
}

// flowRelationID reads one many2one value (bare id, [id, name] pair, or null/false) as an id.
func flowRelationID(raw json.RawMessage) *int64 {
	ids, err := flowRelationIDsFromJSON(append(append([]byte("["), raw...), ']'))
	if err != nil || len(ids) == 0 || len(raw) == 0 {
		return nil
	}
	return &ids[0]
}

func flowRelationIDsFromJSON(raw json.RawMessage) ([]int64, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" || trimmed == "false" {
		return nil, nil
	}

	var decoded []any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(decoded))
	for _, entry := range decoded {
		switch typed := entry.(type) {
		case float64:
			ids = append(ids, int64(typed))
		case []any:
			if len(typed) == 0 {
				continue
			}
			id, ok := int64FromAny(typed[0])
			if !ok {
				return nil, orm.InvalidParamsf("base.workflow relation entry must start with an integer id")
			}
			ids = append(ids, id)
		default:
			id, ok := int64FromAny(entry)
			if !ok {
				return nil, orm.InvalidParamsf("base.workflow relation entries must be ids or [id, name] tuples")
			}
			ids = append(ids, id)
		}
	}
	return ids, nil
}

var FlowModelName = "base.workflow"

var FlowModelSpec = orm.ModelSpec{
	Name:        FlowModelName,
	Label:       "Workflow",
	Description: "Topic-driven workflow definition",
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "create", Public: true, ModelOnly: true, Call: createFlow},
		{Name: "write", Public: true, UsesPathID: true, Call: writeFlow},
		// Operator action (workflow_access.go): switch the workflow on or off without authoring rights.
		{Name: workflowSetActiveMethod, Public: true, UsesPathID: true, Call: setFlowActive,
			Description: "Turn the workflow on or off (is_active). Needs the workflow operator role. kwargs {active: bool}."},
		{Name: "copy_data", Public: true, ReadOnly: true, UsesPathID: true, Call: copyDataFlow},
		{Name: "copy", Public: true, UsesPathID: true, Call: copyFlow},
		{Name: "writeWorkflowFlowMap", Public: true, Call: writeWorkflowFlowMapMethod},
		{Name: "replace_action_sequence", Public: true, Call: replaceActionSequenceFlow},
		{Name: "replace_workflow_edges", Public: true, Call: replaceWorkflowEdgesFlow},
		{Name: "export", Public: true, ReadOnly: true, UsesPathID: true, Call: exportFlow},
		{Name: "validate", Public: true, ReadOnly: true, UsesPathID: true, Call: validateWorkflow},
		{Name: "import", Public: true, ModelOnly: true, Call: importFlow},
		// Mutating, so the dispatcher checks it as "write" = the authoring role (plan T97).
		{
			Name: "publish", Public: true, UsesPathID: true, Call: publishFlow,
			Description: "Validate the saved definition and freeze it as the next published version that triggers and webhooks run. Refused while validate reports errors. Optional expected_revision. Answers {ok, workflow_id, state, published_version, published_at, revision, warnings}.",
		},
		{Name: "list_templates", Public: true, ReadOnly: true, ModelOnly: true, Call: listWorkflowTemplates},
		{Name: "instantiate_template", Public: true, UsesPathID: true, Call: instantiateWorkflowTemplate},
	},
}

var FlowModel = orm.Define[FlowRecord](&Models, 110, FlowModelSpec)

// createFlow creates the flow handled by this package.
func createFlow(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	valuesList, err := recordMapListFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if len(valuesList) == 0 {
		log.Errorf("base.workflow.create requires values")
		return nil, orm.InvalidParamsf("base.workflow.create requires values")
	}

	created := make([]orm.RecordMap, 0, len(valuesList))
	for _, values := range valuesList {
		normalized, err := normalizeFlowValues(values, nil, true)
		if err != nil {
			return nil, err
		}
		// The guarded create pins and validates company_id like the default create() does.
		record, err := orm.CreateRecord(ctx, orm.RuntimeModelRef(FlowModelName, model), normalized)
		if err != nil {
			return nil, err
		}
		if err := writeWorkflowFlowMap(ctx, int64Value(record["id"])); err != nil {
			return nil, err
		}
		created = append(created, record)
	}
	if len(created) == 1 {
		return created[0], nil
	}
	return created, nil
}

const workflowSetActiveMethod = "set_active"

// setFlowActive writes only is_active. The role check ran in the access handler; the caller must
// also see the row (record rules), then the write runs as the engine so writeFlow's side effects
// (trigger registry re-sync) apply unchanged.
func setFlowActive(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	if len(ids) != 1 {
		return nil, orm.InvalidParamsf("base.workflow.set_active requires exactly one id")
	}
	raw, ok := kwargs["active"]
	if !ok && len(args) > 0 {
		raw, ok = args[0], true
	}
	active, isBool := raw.(bool)
	if !ok || !isBool {
		return nil, orm.InvalidParamsf("base.workflow.set_active requires active (bool)")
	}
	if err := orm.EnsureRecordAccess(ctx, FlowModelName, "read", ids); err != nil {
		return nil, err
	}
	if _, err := writeFlow(WorkflowEngineContext(ctx), model, ids, nil, map[string]any{"values": map[string]any{"is_active": active}}); err != nil {
		return nil, err
	}
	return map[string]any{"id": ids[0], "is_active": active}, nil
}

func writeFlow(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	if len(ids) == 0 {
		log.Errorf("base.workflow.write requires ids")
		return nil, orm.InvalidParamsf("base.workflow.write requires ids")
	}
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		log.Errorf("base.workflow.write requires values")
		return nil, orm.InvalidParamsf("base.workflow.write requires values")
	}

	// Read only the scalar fields needed for normalization so a layout-only write
	// does not trigger one2many inverse hydration while the canvas is rebuilding steps.
	beforeRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(FlowModelName, model), ids, []string{
		"id",
		"topic",
		"version",
		"is_active",
		"group_id",
		"company_id",
	})
	if err != nil {
		return nil, err
	}
	if len(beforeRows) == 0 {
		return false, nil
	}

	current := flowStateFromRecord(beforeRows[0])
	normalized, err := normalizeFlowValues(values, &current, false)
	if err != nil {
		return nil, err
	}
	// Execution settings are part of the published snapshot (plan T97): changing one leaves the
	// running version alone and marks the definition as an unpublished draft.
	if workflowSnapshotFieldsTouched(normalized) {
		normalized["state"] = WorkflowStateDraft
	}
	ok, err := orm.WriteRecords(ctx, orm.RuntimeModelRef(FlowModelName, model), ids, normalized)
	if err != nil {
		return nil, err
	}
	if !ok {
		return ok, nil
	}
	// A company change moves the workflow's children with it (plan T41).
	if raw, exists := normalized["company_id"]; exists {
		newCompanyID, _ := int64PointerValue(raw)
		for _, row := range beforeRows {
			if sameInt64Pointer(int64PointerFromAny(row["company_id"]), newCompanyID) {
				continue
			}
			if err := cascadeWorkflowCompany(ctx, int64Value(row["id"]), newCompanyID); err != nil {
				return nil, err
			}
		}
	}
	_, activeChanged := normalized["is_active"]
	_, templateChanged := normalized["is_template"]
	for _, id := range ids {
		if numericID, ok := int64FromAny(id); ok {
			if err := writeWorkflowFlowMap(ctx, numericID); err != nil {
				return nil, err
			}
			// The registry only consumes for workflows that can run (active, not a template).
			if activeChanged || templateChanged {
				syncWorkflowTriggers(ctx, numericID)
			}
		}
	}
	return ok, nil
}

type flowState struct {
	Topic    string
	Version  int
	IsActive bool
	GroupID  *int64
}

func flowStateFromRecord(record orm.RecordMap) flowState {
	version := int(int64Value(record["version"]))
	if version < 0 {
		version = 0
	}
	groupID, _ := int64PointerValue(record["group_id"])
	return flowState{
		Topic:    strings.TrimSpace(stringValue(record["topic"])),
		Version:  version,
		IsActive: boolValue(record["is_active"]),
		GroupID:  groupID,
	}
}

func normalizeFlowValues(values orm.RecordMap, current *flowState, creating bool) (orm.RecordMap, error) {
	normalized := cloneRecordMap(values)
	delete(normalized, "flow_map")
	// revision is server-owned (bumped by the graph mutations in workflow_tx.go).
	delete(normalized, "revision")
	delete(normalized, "expected_revision")
	// Publication fields are server-owned (publish, plan T97). A new workflow (create, copy, import)
	// starts as a draft with no snapshot, so it runs nowhere until it is published.
	for _, field := range workflowPublicationFields {
		delete(normalized, field)
	}
	if creating {
		normalized["state"] = WorkflowStateDraft
	}

	if rawLayout, exists := normalized["layout"]; exists {
		layout, err := normalizeObjectMap(rawLayout)
		if err != nil {
			log.Errorf("base.workflow layout must be an object: %v", err)
			return nil, orm.InvalidParamsf("base.workflow layout must be an object: %v", err)
		}
		normalized["layout"] = layout
	}

	if name, ok := stringFromAny(normalized["name"]); ok {
		normalized["name"] = name
	} else if creating {
		normalized["name"] = timestampedUUID4Name("workflow")
	}

	if topic, ok := stringFromAny(normalized["topic"]); ok {
		topic = strings.TrimSpace(topic)
		if topic == "" {
			log.Errorf("base.workflow topic is required")
			return nil, orm.InvalidParamsf("base.workflow topic is required")
		}
		normalized["topic"] = topic
	} else if creating && strings.TrimSpace(currentFlowTopic(current)) == "" {
		log.Errorf("base.workflow topic is required")
		return nil, orm.InvalidParamsf("base.workflow topic is required")
	}

	if version, exists := normalized["version"]; exists {
		number, ok := int64FromAny(version)
		if !ok || number <= 0 {
			log.Errorf("base.workflow version must be a positive integer")
			return nil, orm.InvalidParamsf("base.workflow version must be a positive integer")
		}
		normalized["version"] = number
	} else if creating {
		normalized["version"] = int64(1)
	}

	if _, exists := normalized["is_active"]; !exists && creating {
		normalized["is_active"] = true
	}
	if description, ok := stringFromAny(normalized["description"]); ok {
		normalized["description"] = strings.TrimSpace(description)
	}
	if executionMode, ok := stringFromAny(normalized["execution_mode"]); ok {
		executionMode = strings.ToLower(strings.TrimSpace(executionMode))
		if executionMode != WorkflowExecutionModeLinear && executionMode != WorkflowExecutionModeDAG {
			log.Errorf("base.workflow execution_mode must be linear or dag")
			return nil, orm.InvalidParamsf("base.workflow execution_mode must be linear or dag")
		}
		normalized["execution_mode"] = executionMode
	} else if creating {
		normalized["execution_mode"] = WorkflowExecutionModeLinear
	}
	if rawMaxParallel, exists := normalized["max_parallel_steps"]; exists {
		number, ok := int64FromAny(rawMaxParallel)
		if !ok || number <= 0 {
			log.Errorf("base.workflow max_parallel_steps must be a positive integer")
			return nil, orm.InvalidParamsf("base.workflow max_parallel_steps must be a positive integer")
		}
		normalized["max_parallel_steps"] = number
	} else if creating {
		normalized["max_parallel_steps"] = int64(DefaultMaxParallelSteps)
	}
	if failurePolicy, ok := stringFromAny(normalized["dag_failure_policy"]); ok {
		failurePolicy = strings.ToLower(strings.TrimSpace(failurePolicy))
		if failurePolicy != WorkflowDagFailurePolicyFailBranch && failurePolicy != WorkflowDagFailurePolicyFailWorkflow {
			log.Errorf("base.workflow dag_failure_policy must be fail_branch or fail_workflow")
			return nil, orm.InvalidParamsf("base.workflow dag_failure_policy must be fail_branch or fail_workflow")
		}
		normalized["dag_failure_policy"] = failurePolicy
	} else if creating {
		normalized["dag_failure_policy"] = WorkflowDagFailurePolicyFailBranch
	}
	// Run control limits (plan T100): whole numbers, 0 = no limit.
	for _, field := range []string{"max_run_seconds", "max_concurrent_runs"} {
		raw, exists := normalized[field]
		if !exists {
			continue
		}
		number, ok := int64FromNonNegativeAny(raw)
		if !ok {
			log.Errorf("base.workflow %s must be a non-negative integer", field)
			return nil, orm.InvalidParamsf("base.workflow %s must be a non-negative integer", field)
		}
		normalized[field] = number
	}
	if redactKeys, ok := stringFromAny(normalized["audit_redact_keys"]); ok {
		normalized["audit_redact_keys"] = strings.TrimSpace(redactKeys)
	}
	if groupID, exists := normalized["group_id"]; exists {
		switch {
		case groupID == nil:
			normalized["group_id"] = nil
		case groupID == false:
			// false is the sentinel for "clear this relation field";
			// treat it as null here so the ORM normalization rule (a) is respected
			// even inside model-specific hooks that run before RuntimeWrite.
			normalized["group_id"] = nil
		default:
			number, ok := int64FromAny(groupID)
			if !ok || number <= 0 {
				log.Errorf("base.workflow group_id must be null or a positive integer")
				return nil, orm.InvalidParamsf("base.workflow group_id must be null or a positive integer")
			}
			normalized["group_id"] = number
		}
	}
	return normalized, nil
}

// workflowCompanyID reads the company_id of a base.workflow row so child
// records (actions, triggers, runs, workflow-scoped credentials) can inherit
// it instead of trusting the caller's own session company, matching how
// tbl_website_page inherits from its parent tbl_website_site.
func workflowCompanyID(ctx context.Context, workflowID int64) (*int64, error) {
	if workflowID <= 0 {
		return nil, nil
	}
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(FlowModelName, nil), []string{fmt.Sprint(workflowID)}, []string{"company_id"})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, orm.NotFoundf("base.workflow %d not found", workflowID)
	}
	companyID, _ := int64PointerValue(rows[0]["company_id"])
	return companyID, nil
}

// inheritWorkflowChildCompanyID overwrites normalized["company_id"] with the
// parent workflow's own company_id, read from normalized["workflow_id"]. Used
// by base.workflow.action/trigger/run create paths so a child record's
// company can never diverge from its parent's, regardless of the creating
// caller's own session company. Falls back to the generic per-caller pin when
// no parent workflow is set yet (workflow_id is nullable at the schema level).
func inheritWorkflowChildCompanyID(ctx context.Context, model *orm.ModelMetadata, normalized orm.RecordMap) error {
	workflowID, ok := int64PointerValue(normalized["workflow_id"])
	if !ok || workflowID == nil {
		return orm.ApplyImplicitCompanyOnCreate(ctx, model, normalized)
	}
	companyID, err := workflowCompanyID(ctx, *workflowID)
	if err != nil {
		return err
	}
	normalized["company_id"] = companyIDValue(companyID)
	return nil
}

func currentFlowTopic(current *flowState) string {
	if current == nil {
		return ""
	}
	return current.Topic
}
