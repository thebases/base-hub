package models

// File overview:
// - Responsibility: read-only `base.workflow.validate` (plan T98): checks the SAVED definition of a
//   workflow and returns per-node / per-edge / per-trigger issues the canvas can badge.
// - Layout: `validateWorkflow` loads rows through record-rule-respecting reads; the pure
//   `validateWorkflowDefinition` does every check, so it is table-testable without a DB.
// - Codes are stable snake_case (the SPA translates them); messages are plain English.

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
)

const (
	WorkflowIssueError   = "error"
	WorkflowIssueWarning = "warning"
)

// WorkflowIssue is one finding. Only the fields that apply to the finding are set.
type WorkflowIssue struct {
	Severity  string `json:"severity"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	StepKey   string `json:"step_key,omitempty"`
	EdgeID    int64  `json:"edge_id,omitempty"`
	TriggerID int64  `json:"trigger_id,omitempty"`
	Param     string `json:"param,omitempty"`
}

// workflowDefStep is the slice of an action row the checks need. Columns is the whole row so
// ValidateStepParams can read column-backed params.
type workflowDefStep struct {
	StepKey    string
	Name       string
	Code       string
	Version    int
	HasCatalog bool
	Columns    map[string]any
	Extra      map[string]any
}

type workflowDefEdge struct {
	ID            int64
	From, To      string
	EdgeType      string
	ConditionExpr string
}

type workflowDefTrigger struct {
	ID          int64
	TriggerType string
	Topic       string
	IsActive    bool
	Config      map[string]any
}

type workflowDefinition struct {
	// ExecutionMode is the workflow's execution_mode (linear|dag).
	ExecutionMode string
	Steps         []workflowDefStep
	Edges         []workflowDefEdge
	Triggers      []workflowDefTrigger
	// GroupIDs holds the wait_approval approver_group_id values that exist in tbl.groups (loaded by
	// loadWorkflowDefinition, so the checks below stay DB-free).
	GroupIDs map[int64]bool
}

func validateWorkflow(ctx context.Context, model *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	if len(ids) == 0 {
		return nil, orm.InvalidParamsf("base.workflow.validate requires an id")
	}
	if err := EnsureWorkflowRole(ctx, WorkflowRoleViewer); err != nil {
		return nil, err
	}
	// Record rules on the workflow gate its children: the child reads below filter by workflow_id only.
	if err := orm.EnsureRecordAccess(ctx, FlowModelName, "read", ids[:1]); err != nil {
		return nil, err
	}
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(FlowModelName, model), ids[:1], []string{"id"})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, orm.NotFoundf("base.workflow.validate: workflow %s not found", ids[0])
	}
	def, err := loadWorkflowDefinition(ctx, int64Value(rows[0]["id"]))
	if err != nil {
		return nil, err
	}
	issues := validateWorkflowDefinition(ctx, def)
	ok := true
	for _, issue := range issues {
		if issue.Severity == WorkflowIssueError {
			ok = false
		}
	}
	return map[string]any{"ok": ok, "issues": issues}, nil
}

func loadWorkflowDefinition(ctx context.Context, workflowID int64) (workflowDefinition, error) {
	var def workflowDefinition
	flowRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(FlowModelName, nil), []string{fmt.Sprint(workflowID)}, []string{"execution_mode"})
	if err != nil {
		return def, err
	}
	if len(flowRows) > 0 {
		def.ExecutionMode = strings.TrimSpace(stringValue(flowRows[0]["execution_mode"]))
	}
	children := func(model, order string) ([]orm.RecordMap, error) {
		return orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(model, nil), orm.RecordMap{
			"workflow_id": workflowID,
			"__order":     order,
		}, nil)
	}
	actions, err := children(WorkflowActionModelName, "sequence asc, id asc")
	if err != nil {
		return def, err
	}
	for _, row := range actions {
		extra, _ := row["extra_params"].(map[string]any)
		version := int(int64Value(row["action_version"]))
		def.Steps = append(def.Steps, workflowDefStep{
			StepKey:    strings.TrimSpace(stringValue(row["step_key"])),
			Name:       strings.TrimSpace(stringValue(row["name"])),
			Code:       strings.TrimSpace(stringValue(row["action_code"])),
			Version:    version,
			HasCatalog: int64Value(row["catalog_id"]) > 0,
			Columns:    row,
			Extra:      extra,
		})
	}
	edges, err := children(WorkflowEdgeModelName, "id asc")
	if err != nil {
		return def, err
	}
	for _, row := range edges {
		def.Edges = append(def.Edges, workflowDefEdge{
			ID:            int64Value(row["id"]),
			From:          strings.TrimSpace(stringValue(row["from_step_key"])),
			To:            strings.TrimSpace(stringValue(row["to_step_key"])),
			EdgeType:      strings.TrimSpace(stringValue(row["edge_type"])),
			ConditionExpr: stringValue(row["condition_expr"]),
		})
	}
	if def.GroupIDs, err = existingApproverGroups(ctx, def.Steps); err != nil {
		return def, err
	}
	triggers, err := children(WorkflowTriggerModelName, "id asc")
	if err != nil {
		return def, err
	}
	for _, row := range triggers {
		config, _ := row["config"].(map[string]any)
		def.Triggers = append(def.Triggers, workflowDefTrigger{
			ID:          int64Value(row["id"]),
			TriggerType: stringValue(row["trigger_type"]),
			Topic:       stringValue(row["topic"]),
			IsActive:    boolValue(row["is_active"]),
			Config:      config,
		})
	}
	return def, nil
}

// validateWorkflowDefinition runs every check; it reads no DB and never mutates def. ctx is only
// forwarded to the trigger normalizer.
func validateWorkflowDefinition(ctx context.Context, def workflowDefinition) []WorkflowIssue {
	issues := []WorkflowIssue{}
	if len(def.Steps) == 0 {
		issues = append(issues, WorkflowIssue{Severity: WorkflowIssueWarning, Code: "no_steps", Message: "The workflow has no steps."})
	}
	stepKeys, issues := validateWorkflowSteps(def.Steps, issues)
	issues = validateWorkflowOnErrorTargets(def.Steps, issues)
	issues = validateWorkflowApprovalSteps(def, issues)
	issues = validateWorkflowEdges(def.Edges, stepKeys, issues)
	issues = validateWorkflowGraphShape(def, stepKeys, issues)
	return validateWorkflowTriggers(ctx, def.Triggers, issues)
}

func validateWorkflowSteps(steps []workflowDefStep, issues []WorkflowIssue) (map[string]bool, []WorkflowIssue) {
	keys := map[string]bool{}
	for _, step := range steps {
		switch {
		case step.StepKey == "":
			issues = append(issues, WorkflowIssue{Severity: WorkflowIssueError, Code: "step_key_empty", Message: "A step has an empty step key."})
		case keys[step.StepKey]:
			issues = append(issues, WorkflowIssue{Severity: WorkflowIssueError, Code: "step_key_duplicate", Message: fmt.Sprintf("Step key %q is used by more than one step.", step.StepKey), StepKey: step.StepKey})
		default:
			keys[step.StepKey] = true
		}
		issues = validateWorkflowStepParams(step, issues)
	}
	return keys, issues
}

// validateWorkflowOnErrorTargets checks extra_params.on_error_step_key (plan T103): the target must
// exist and come after the step in sequence order, so the error path can never loop.
func validateWorkflowOnErrorTargets(steps []workflowDefStep, issues []WorkflowIssue) []WorkflowIssue {
	// Positions are keyed by the step key the engine resolves (onErrorTarget matches
	// Step.ResolvedKey), so a row without step_key is found under its derived key.
	position := make(map[string]int, len(steps))
	for i, step := range steps {
		if key := step.resolvedKey(i); key != "" {
			if _, seen := position[key]; !seen {
				position[key] = i
			}
		}
	}
	for i, step := range steps {
		target := strings.TrimSpace(stringValue(step.Extra[StepOnErrorParam]))
		if target == "" {
			continue
		}
		at, ok := position[target]
		switch {
		case !ok:
			issues = append(issues, WorkflowIssue{Severity: WorkflowIssueError, Code: "on_error_target_missing", Message: fmt.Sprintf("Step %q sends errors to missing step %q.", step.StepKey, target), StepKey: step.StepKey, Param: StepOnErrorParam})
		case at <= i:
			issues = append(issues, WorkflowIssue{Severity: WorkflowIssueError, Code: "on_error_target_backward", Message: fmt.Sprintf("Step %q sends errors to step %q, which does not come after it.", step.StepKey, target), StepKey: step.StepKey, Param: StepOnErrorParam})
		}
	}
	return issues
}

// resolvedKey is the key the engine runs this row under: ResolvedStep's key, then ResolvedKey.
func (s workflowDefStep) resolvedKey(index int) string {
	key := WorkflowActionRecord{StepKey: s.StepKey, ActionCode: s.Code, Name: s.Name}.ResolvedStepKey()
	return Step{Key: key, Name: s.Name, Action: s.Code}.ResolvedKey(index)
}

// validateWorkflowApprovalSteps flags an approver_group_id that is not a tbl.groups row and refuses
// wait_approval@v1 in a DAG run (plan T103): a parked run
// resumes from one current_step, which a DAG layer does not have. A DAG run is execution_mode dag
// with at least one edge, exactly the rule the engine applies.
func validateWorkflowApprovalSteps(def workflowDefinition, issues []WorkflowIssue) []WorkflowIssue {
	// An approver group that does not exist would leave the wait decidable only with the token or by
	// base.group_system, silently; refuse it at validate/publish instead.
	for _, step := range def.Steps {
		if id := step.approverGroupID(); id > 0 && !def.GroupIDs[id] {
			issues = append(issues, WorkflowIssue{Severity: WorkflowIssueError, Code: "invalid_param", Message: fmt.Sprintf("Step %q names approver group %d, which does not exist.", step.StepKey, id), StepKey: step.StepKey, Param: WaitApprovalGroupParam})
		}
	}
	if def.ExecutionMode != WorkflowExecutionModeDAG || len(def.Edges) == 0 {
		return issues
	}
	for _, step := range def.Steps {
		if step.Code == WaitApprovalActionCode {
			issues = append(issues, WorkflowIssue{Severity: WorkflowIssueError, Code: "wait_approval_in_dag", Message: fmt.Sprintf("Step %q waits for approval, which only linear workflows support.", step.StepKey), StepKey: step.StepKey})
		}
	}
	return issues
}

// approverGroupID is a wait_approval step's approver_group_id (0 when unset or not an id; the
// param type check reports a malformed value).
func (s workflowDefStep) approverGroupID() int64 {
	if s.Code != WaitApprovalActionCode {
		return 0
	}
	id, _ := positiveIntValue(s.Extra[WaitApprovalGroupParam])
	return max(id, 0)
}

// existingApproverGroups reads which approver groups named by wait_approval steps exist. The read
// runs as the engine: a viewer validating the workflow may not read tbl.groups, and existence is
// all it reveals.
func existingApproverGroups(ctx context.Context, steps []workflowDefStep) (map[int64]bool, error) {
	var ids []string
	for _, step := range steps {
		if id := step.approverGroupID(); id > 0 {
			ids = append(ids, strconv.FormatInt(id, 10))
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := orm.RuntimeRepositoryRead(WorkflowEngineContext(ctx), orm.RuntimeModelRef(workflowGroupModelName, nil), ids, []string{"id"})
	if err != nil {
		return nil, err
	}
	found := make(map[int64]bool, len(rows))
	for _, row := range rows {
		found[int64Value(row["id"])] = true
	}
	return found, nil
}

func validateWorkflowStepParams(step workflowDefStep, issues []WorkflowIssue) []WorkflowIssue {
	if _, known := LookupNodeDef(step.Code, step.Version); !known {
		// A catalog row without a NodeDef is a legacy action: params cannot be checked, not an error.
		if !step.HasCatalog {
			issues = append(issues, WorkflowIssue{Severity: WorkflowIssueError, Code: "unknown_action", Message: fmt.Sprintf("Unknown action %q version %d.", step.Code, step.Version), StepKey: step.StepKey})
		}
		return issues
	}
	for _, p := range ValidateStepParams(step.Code, step.Version, step.Columns, step.Extra) {
		issues = append(issues, WorkflowIssue{Severity: WorkflowIssueError, Code: "invalid_param", Message: p.Message, StepKey: step.StepKey, Param: p.Param})
	}
	return issues
}

func validateWorkflowEdges(edges []workflowDefEdge, stepKeys map[string]bool, issues []WorkflowIssue) []WorkflowIssue {
	for _, edge := range edges {
		for _, end := range []string{edge.From, edge.To} {
			if !stepKeys[end] {
				issues = append(issues, WorkflowIssue{Severity: WorkflowIssueError, Code: "edge_missing_step", Message: fmt.Sprintf("Edge %d points at missing step %q.", edge.ID, end), EdgeID: edge.ID, StepKey: end})
			}
		}
		if edge.EdgeType == "condition" && strings.TrimSpace(edge.ConditionExpr) == "" {
			issues = append(issues, WorkflowIssue{Severity: WorkflowIssueError, Code: "condition_expr_empty", Message: fmt.Sprintf("Condition edge %d has no condition expression.", edge.ID), EdgeID: edge.ID, StepKey: edge.From})
		}
	}
	return issues
}

// validateWorkflowGraphShape covers DAG mode (a workflow with edges): cycles, and steps that no
// edge touches (they are outside the graph, so a DAG run never reaches them).
func validateWorkflowGraphShape(def workflowDefinition, stepKeys map[string]bool, issues []WorkflowIssue) []WorkflowIssue {
	if len(def.Edges) == 0 {
		return issues
	}
	graph := make([]WorkflowGraphEdge, 0, len(def.Edges))
	touched := map[string]bool{}
	for _, edge := range def.Edges {
		graph = append(graph, WorkflowGraphEdge{FromStepKey: edge.From, ToStepKey: edge.To, EdgeType: edge.EdgeType, ConditionExpr: edge.ConditionExpr})
		touched[edge.From], touched[edge.To] = true, true
	}
	if _, err := ValidateWorkflowGraph(graph); err != nil {
		if cycle, isCycle := err.(*WorkflowGraphCycleError); isCycle {
			for _, key := range cycle.StepKeys {
				issues = append(issues, WorkflowIssue{Severity: WorkflowIssueError, Code: "cycle", Message: cycle.Error(), StepKey: key})
			}
		}
	}
	for _, step := range def.Steps {
		if step.StepKey != "" && stepKeys[step.StepKey] && !touched[step.StepKey] {
			issues = append(issues, WorkflowIssue{Severity: WorkflowIssueWarning, Code: "step_unreachable", Message: fmt.Sprintf("Step %q is not connected by any edge.", step.StepKey), StepKey: step.StepKey})
		}
	}
	return issues
}

func validateWorkflowTriggers(ctx context.Context, triggers []workflowDefTrigger, issues []WorkflowIssue) []WorkflowIssue {
	// The webhook trigger is implicit: /wh serves a workflow with no non-webhook trigger row even
	// without a webhook row (routes.workflowWebhookSettingsFor: hasActiveWebhook ||
	// !hasOtherTransport), so such a workflow has an active trigger. Keep both rules in step.
	active, hasOtherTransport := false, false
	for _, trigger := range triggers {
		active = active || trigger.IsActive
		if !strings.EqualFold(strings.TrimSpace(trigger.TriggerType), WorkflowTriggerTypeWebhook) {
			hasOtherTransport = true
		}
		// Reuse the save-path normalizer so the findings match what a save would reject;
		// its result is discarded, nothing is written.
		_, err := normalizeWorkflowTriggerValues(ctx, orm.RecordMap{
			"workflow_id":  int64(1),
			"trigger_type": trigger.TriggerType,
			"topic":        trigger.Topic,
			"config":       trigger.Config,
		}, nil, false)
		if err != nil {
			issues = append(issues, WorkflowIssue{Severity: WorkflowIssueError, Code: "trigger_config_invalid", Message: err.Error(), TriggerID: trigger.ID})
		}
	}
	if !active && hasOtherTransport {
		issues = append(issues, WorkflowIssue{Severity: WorkflowIssueWarning, Code: "no_active_trigger", Message: "The workflow has no active trigger."})
	}
	return issues
}
