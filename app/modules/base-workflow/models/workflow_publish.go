package models

// Versioned workflow definitions: draft / publish (plan T97, WF-U-7, WF-X-11).
//
// Actions, edges and the execution settings of base.workflow are mutable rows, so before T97 a
// resumed or retried run executed whatever the rows said at that moment and a canvas save changed
// live traffic at once. Now:
//   - `publish` validates the saved definition (T98 validateWorkflowDefinition, errors refuse it)
//     and freezes it as a WorkflowSnapshot: the flow's execution settings, the resolved steps
//     (WorkflowActionRecord.ResolvedStep, the same Step the engine runs) and the edges. The snapshot
//     goes to base.workflow.published_snapshot (the live one) and to one base.workflow.version row
//     per version (history, so runs pinned to an older version still resume).
//   - Triggers, /wh and resume run the snapshot (service/engine/loaders.go); a run stores
//     flow_version = the snapshot version. The test-run executes the draft rows (flow_version 0).
//   - Every graph or settings edit flips state to draft and leaves the published version running.
//     Trigger rows are NOT part of the snapshot: they are transport config (topic, port, auth) that
//     the trigger registry applies as soon as they are saved, so editing one does not flip state.
//
// Publish runs in runInWorkflowTx: FOR UPDATE on the workflow row serialises it with canvas saves,
// so the snapshot is exactly the definition at the locked revision. It does not bump revision (the
// definition did not change), so a canvas open on that revision can still save.

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

const (
	WorkflowStateDraft     = "draft"
	WorkflowStatePublished = "published"
)

// workflowPublishRecordAccess is the record-rule check of publish (test seam).
var workflowPublishRecordAccess = orm.EnsureRecordAccess

// workflowPublicationFields are server-owned: only publish (and the boot backfill) set them.
var workflowPublicationFields = []string{"state", "published_version", "published_at", "published_snapshot"}

// workflowSnapshotFlowFields are the base.workflow columns frozen in a snapshot; editing one only
// takes effect at the next publish.
var workflowSnapshotFlowFields = []string{
	"execution_mode", "max_parallel_steps", "dag_failure_policy", "audit_redact_keys",
	"max_run_seconds", "max_concurrent_runs",
}

// WorkflowSnapshot is one published version. Flow carries only the execution settings
// (workflowSnapshotFlowFields); identity, access and on/off (id, topic, is_active, group_id,
// company_id) stay live on the workflow row so an operator can still stop or restrict it.
type WorkflowSnapshot struct {
	Version  int                 `json:"version"`
	Revision int64               `json:"revision"`
	Flow     FlowRecord          `json:"flow"`
	Steps    []Step              `json:"steps"`
	Edges    []WorkflowGraphEdge `json:"edges"`
}

// RuntimeFlow returns live with the snapshot's execution settings and version: the FlowRecord a
// run of this snapshot executes with.
func (s *WorkflowSnapshot) RuntimeFlow(live FlowRecord) FlowRecord {
	flow := live
	flow.ExecutionMode = s.Flow.ExecutionMode
	flow.MaxParallelSteps = s.Flow.MaxParallelSteps
	flow.DagFailurePolicy = s.Flow.DagFailurePolicy
	flow.AuditRedactKeys = s.Flow.AuditRedactKeys
	flow.MaxRunSeconds = s.Flow.MaxRunSeconds
	flow.MaxConcurrentRuns = s.Flow.MaxConcurrentRuns
	flow.Version = s.Version
	return flow
}

// DecodeWorkflowSnapshot reads a stored snapshot (jsonb as a map, bytes or string); nil, nil when
// none is stored.
func DecodeWorkflowSnapshot(raw any) (*WorkflowSnapshot, error) {
	var data []byte
	switch typed := raw.(type) {
	case nil:
		return nil, nil
	case []byte:
		data = typed
	case string:
		data = []byte(typed)
	case json.RawMessage:
		data = typed
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return nil, fmt.Errorf("base.workflow snapshot: encode: %w", err)
		}
		data = encoded
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" || trimmed == "{}" || trimmed == "false" {
		return nil, nil
	}
	var snapshot WorkflowSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, fmt.Errorf("base.workflow snapshot: decode: %w", err)
	}
	if snapshot.Version <= 0 {
		return nil, fmt.Errorf("base.workflow snapshot: missing version")
	}
	return &snapshot, nil
}

// buildWorkflowSnapshot freezes flowRow (a base.workflow row) and def (its saved steps and edges)
// as version. Pure: shared by publish (runtime reads) and the boot backfill (raw rows).
func buildWorkflowSnapshot(flowRow orm.RecordMap, def workflowDefinition, version int, revision int64) (WorkflowSnapshot, error) {
	snapshot := WorkflowSnapshot{
		Version:  version,
		Revision: revision,
		Flow: FlowRecord{
			ExecutionMode:     strings.TrimSpace(stringValue(flowRow["execution_mode"])),
			MaxParallelSteps:  int(int64Value(flowRow["max_parallel_steps"])),
			DagFailurePolicy:  strings.TrimSpace(stringValue(flowRow["dag_failure_policy"])),
			AuditRedactKeys:   stringValue(flowRow["audit_redact_keys"]),
			MaxRunSeconds:     int(int64Value(flowRow["max_run_seconds"])),
			MaxConcurrentRuns: int(int64Value(flowRow["max_concurrent_runs"])),
		},
		Steps: []Step{},
		Edges: []WorkflowGraphEdge{},
	}
	for _, step := range def.Steps {
		// Same rows the engine's row loader runs: active actions only, in sequence order.
		if workflowRowDisabled(step.Columns, "is_active") || workflowRowDisabled(step.Columns, "active") {
			continue
		}
		columns := cloneRecordMap(step.Columns)
		// Not needed by ResolvedStep; raw rows carry them in a format time.Time cannot decode.
		delete(columns, "created_at")
		delete(columns, "updated_at")
		action, err := orm.DecodeRecord[WorkflowActionRecord](columns)
		if err != nil {
			return snapshot, fmt.Errorf("base.workflow snapshot: step %q: %w", step.StepKey, err)
		}
		snapshot.Steps = append(snapshot.Steps, action.ResolvedStep())
	}
	for _, edge := range def.Edges {
		snapshot.Edges = append(snapshot.Edges, WorkflowGraphEdge{
			FromStepKey: edge.From, ToStepKey: edge.To, EdgeType: edge.EdgeType, ConditionExpr: edge.ConditionExpr,
		})
	}
	return snapshot, nil
}

// workflowRowDisabled reports an explicit false in row[key] (absent counts as enabled, matching
// the column defaults).
func workflowRowDisabled(row orm.RecordMap, key string) bool {
	value, ok := row[key].(bool)
	return ok && !value
}

// workflowSnapshotMap is the jsonb value stored for a snapshot.
func workflowSnapshotMap(snapshot WorkflowSnapshot) (map[string]any, error) {
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("base.workflow snapshot: encode: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		return nil, fmt.Errorf("base.workflow snapshot: encode: %w", err)
	}
	return out, nil
}

// publishFlow is base.workflow.publish (plan T97). Kwargs: expected_revision (optional). It answers
// {ok, workflow_id, state, published_version, published_at, revision, warnings}; validation errors
// refuse it with InvalidParams listing them (base.workflow.validate returns them per node).
func publishFlow(ctx context.Context, _ *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	const method = "base.workflow.publish"
	workflowID, ok := workflowIDFromMethodCall(ids, args, kwargs)
	if !ok || workflowID <= 0 {
		log.Errorf("%s requires a valid workflow id", method)
		return nil, orm.InvalidParamsf("%s requires a valid workflow id", method)
	}
	expected, err := expectedRevisionFromKwargs(method, kwargs)
	if err != nil {
		return nil, err
	}
	idText := strconv.FormatInt(workflowID, 10)
	// The dispatcher checked model write access (= author role); record rules gate this row.
	if err := workflowPublishRecordAccess(ctx, FlowModelName, "write", []string{idText}); err != nil {
		return nil, err
	}

	var result orm.RecordMap
	err = runInWorkflowTx(ctx, workflowID, func(txCtx context.Context, revision int64) error {
		if expected != nil && *expected != revision {
			log.Warnf("%s: workflow %d revision conflict (expected %d, stored %d)", method, workflowID, *expected, revision)
			return orm.Conflictf("%s: workflow %d revision conflict: it was saved by someone else (expected revision %d, current %d); reload it and publish again", method, workflowID, *expected, revision)
		}
		flowRef := orm.RuntimeModelRef(FlowModelName, nil)
		rows, err := orm.RuntimeRepositoryRead(txCtx, flowRef, []string{idText}, append([]string{"id", "version", "state", "published_version", "company_id"}, workflowSnapshotFlowFields...))
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return orm.NotFoundf("%s: workflow %d not found", method, workflowID)
		}
		def, err := loadWorkflowDefinition(txCtx, workflowID)
		if err != nil {
			return err
		}
		issues := validateWorkflowDefinition(txCtx, def)
		warnings := []WorkflowIssue{}
		var problems []string
		for _, issue := range issues {
			if issue.Severity == WorkflowIssueError {
				problems = append(problems, issue.Code+": "+issue.Message)
			} else {
				warnings = append(warnings, issue)
			}
		}
		if len(problems) > 0 {
			log.Warnf("%s: workflow %d refused: %d validation error(s)", method, workflowID, len(problems))
			return orm.InvalidParamsf("%s: workflow %d has %d validation error(s); fix them and publish again: %s", method, workflowID, len(problems), strings.Join(problems, "; "))
		}

		next := nextWorkflowPublishedVersion(rows[0])
		snapshot, err := buildWorkflowSnapshot(rows[0], def, next, revision)
		if err != nil {
			return err
		}
		stored, err := workflowSnapshotMap(snapshot)
		if err != nil {
			return err
		}
		if _, err := orm.RuntimeRepositoryCreate(txCtx, orm.RuntimeModelRef(WorkflowVersionModelName, nil), orm.RecordMap{
			"workflow_id": workflowID,
			"company_id":  companyIDValue(int64PointerFromAny(rows[0]["company_id"])),
			"version":     int64(next),
			"revision":    revision,
			"snapshot":    stored,
		}); err != nil {
			return err
		}
		publishedAt := time.Now().UTC()
		if _, err := orm.RuntimeRepositoryWrite(txCtx, flowRef, []string{idText}, orm.RecordMap{
			"state":              WorkflowStatePublished,
			"published_version":  int64(next),
			"published_at":       publishedAt,
			"published_snapshot": stored,
			// `version` tracks the live version so lists and run filters show the same number.
			"version": int64(next),
		}); err != nil {
			return err
		}
		result = orm.RecordMap{
			"ok":                true,
			"workflow_id":       workflowID,
			"state":             WorkflowStatePublished,
			"published_version": next,
			"published_at":      publishedAt, // time.Time: answered as Unix seconds like every API time
			"revision":          revision,
			"warnings":          warnings,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	log.Infof("%s: workflow %d published as version %v", method, workflowID, result["published_version"])
	// After commit: a never-published workflow's triggers were skipped by the registry and can
	// start consuming now.
	syncWorkflowTriggers(ctx, workflowID)
	return result, nil
}

// nextWorkflowPublishedVersion numbers the next publish of flowRow. A workflow that existed before
// T97 and was never snapshotted (published, no published_version) has runs pinned to its version
// column, so its first publish goes above that number instead of reusing it.
func nextWorkflowPublishedVersion(flowRow orm.RecordMap) int {
	published := int(int64Value(flowRow["published_version"]))
	if published == 0 && stringValue(flowRow["state"]) != WorkflowStateDraft {
		return int(max(int64Value(flowRow["version"]), 0)) + 1
	}
	return published + 1
}

// markWorkflowDraft flags a workflow whose definition changed after its last publish (plan T97).
// Server-owned field, written below the RPC layer like flow_map.
//
// The write is unconditional on purpose: reading state first and skipping a row already in draft
// raced publish, which reads the rows under FOR UPDATE and sets state=published at commit. A
// change committed while publish was still open read state=draft, skipped the write, and publish
// then left state=published over rows that differ from its snapshot. A plain UPDATE blocks on
// publish's row lock and lands after it, so the workflow ends draft.
func markWorkflowDraft(ctx context.Context, workflowID int64) error {
	if workflowID <= 0 {
		return nil
	}
	ids := []string{strconv.FormatInt(workflowID, 10)}
	if _, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(FlowModelName, nil), ids, orm.RecordMap{"state": WorkflowStateDraft}); err != nil {
		log.Errorf("base.workflow mark workflow %d draft: %v", workflowID, err)
		return fmt.Errorf("base.workflow mark workflow %d draft: %w", workflowID, err)
	}
	return nil
}

// workflowSnapshotFieldsTouched reports whether values set an execution setting frozen in snapshots.
func workflowSnapshotFieldsTouched(values orm.RecordMap) bool {
	for _, field := range workflowSnapshotFlowFields {
		if _, ok := values[field]; ok {
			return true
		}
	}
	return false
}

// LoadWorkflowVersionSnapshot reads version of workflowID from base.workflow.version; nil, nil when
// that version was never stored (the engine then refuses to run the pinned run on other rows).
func LoadWorkflowVersionSnapshot(ctx context.Context, workflowID int64, version int) (*WorkflowSnapshot, error) {
	if workflowID <= 0 || version <= 0 {
		return nil, nil
	}
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WorkflowVersionModelName, nil), orm.RecordMap{
		"workflow_id": workflowID,
		"version":     version,
		"__limit":     1,
	}, []string{"snapshot"})
	if err != nil {
		log.Errorf("base.workflow.version load workflow %d version %d: %v", workflowID, version, err)
		return nil, fmt.Errorf("base.workflow.version load workflow %d version %d: %w", workflowID, version, err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return DecodeWorkflowSnapshot(rows[0]["snapshot"])
}
