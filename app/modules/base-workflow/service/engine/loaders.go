package engine

// File overview:
// - Responsibility: loads flows, steps, actions and runs from the database and resolves module actions.
// - Key symbols: `loadLatestActiveFlow`, `loadFlowByVersion`, `loadWorkflowActions`, `loadWorkflowRun`,
//   `Service.publishedRuntime`, `Service.draftRuntime`, `Service.versionRuntime`.
// - Versioned definitions (plan T97): a run executes a frozen snapshot, never the live rows, except
//   the test-run (draft, flow_version 0) and workflows from before T97 that the boot backfill has
//   not snapshotted yet (legacy fallback: rows, flow_version = the version column, as before).

import (
	"context"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/modelruntime"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
	"git.thebase.vn/base_hub/modules/base-workflow/service/nodes"
)

// loadRuntimeSteps loads the runtime steps handled by this package.
func (s *Service) loadRuntimeSteps(ctx context.Context, flow *workflowmodels.FlowRecord) ([]workflowmodels.Step, error) {
	if flow == nil {
		log.Errorf("workflow: workflow flow is required")
		return nil, fmt.Errorf("workflow: workflow flow is required")
	}
	if flow.ID <= 0 {
		log.Errorf("workflow: workflow %s@v%d is missing an id", strings.TrimSpace(flow.Topic), flow.Version)
		return nil, fmt.Errorf("workflow: workflow %s@v%d is missing an id", strings.TrimSpace(flow.Topic), flow.Version)
	}

	actions, err := s.loadFlowActions(ctx, flow.ID)
	if err != nil {
		return nil, err
	}
	if len(actions) == 0 {
		log.Errorf("%v: workflow %s@v%d has no active configured actions", ErrActionDefinitionNotFound, strings.TrimSpace(flow.Topic), flow.Version)
		return nil, fmt.Errorf("%w: workflow %s@v%d has no active configured actions", ErrActionDefinitionNotFound, strings.TrimSpace(flow.Topic), flow.Version)
	}

	steps := make([]workflowmodels.Step, 0, len(actions))
	for _, action := range actions {
		steps = append(steps, action.ResolvedStep())
	}
	return steps, nil
}

// loadLatestActiveFlow loads the latest active flow handled by this package.
func loadLatestActiveFlow(ctx context.Context, topic string) (*workflowmodels.FlowRecord, error) {
	flows, err := modelruntime.NewService().SearchRead(ctx, workflowmodels.FlowModelSpec.Name, orm.RecordMap{
		"topic":     strings.TrimSpace(topic),
		"is_active": true,
		"__order":   "version desc, id desc",
		"__limit":   1,
	}, nil)
	if err != nil {
		log.Errorf("workflow: load flow topic=%s: %v", topic, err)
		return nil, fmt.Errorf("workflow: load flow topic=%s: %w", topic, err)
	}
	if len(flows) == 0 {
		log.Errorf("%v: topic=%s", ErrFlowNotFound, strings.TrimSpace(topic))
		return nil, fmt.Errorf("%w: topic=%s", ErrFlowNotFound, strings.TrimSpace(topic))
	}
	delete(flows[0], "action_ids")
	return decodeRecord[workflowmodels.FlowRecord](flows[0])
}

// loadFlowByVersion loads the active workflow of topic that a run pinned to version belongs to.
// Since T97 the version is resolved against the stored snapshots (versionRuntime), not against the
// version column, which publish moves forward; version only has to be positive here.
func loadFlowByVersion(ctx context.Context, topic string, version int) (*workflowmodels.FlowRecord, error) {
	if version <= 0 {
		log.Errorf("%v: topic=%s version=%d", ErrFlowNotFound, strings.TrimSpace(topic), version)
		return nil, fmt.Errorf("%w: topic=%s version=%d", ErrFlowNotFound, strings.TrimSpace(topic), version)
	}
	return loadLatestActiveFlow(ctx, topic)
}

// loadRunFlow loads the workflow a stored run belongs to (resume, replay, approval decision) by the
// run's workflow_id, never by its topic: after a topic is renamed and reused, a topic lookup
// would run the other workflow's pinned version. The workflow id is authoritative, so a topic
// renamed since the run started does not matter (refusing it would close every in-flight run of
// the workflow as dead_letter); the run is refused (ErrFlowNotFound) only when the workflow is
// gone, inactive or a template. legacy resolves runs stored without a workflow_id by their topic.
func (s *Service) loadRunFlow(ctx context.Context, run *workflowmodels.WorkflowRunRecord, legacy func() (*workflowmodels.FlowRecord, error)) (*workflowmodels.FlowRecord, error) {
	if run.WorkflowID == nil || *run.WorkflowID <= 0 {
		return legacy()
	}
	return s.loadFlowByID(ctx, *run.WorkflowID)
}

// publishedRuntime is what triggers and /wh execute: the published snapshot. A workflow created
// after T97 and never published has none and does not run.
func (s *Service) publishedRuntime(ctx context.Context, flow *workflowmodels.FlowRecord) (flowRuntime, error) {
	snapshot, err := workflowmodels.DecodeWorkflowSnapshot(flow.PublishedSnapshot)
	if err != nil {
		return flowRuntime{}, fmt.Errorf("workflow %s: %w", strings.TrimSpace(flow.Topic), err)
	}
	if snapshot != nil {
		return snapshotRuntime(*flow, snapshot)
	}
	if strings.TrimSpace(flow.State) == workflowmodels.WorkflowStateDraft {
		log.Warnf("%v: workflow %s has never been published", ErrFlowNotFound, strings.TrimSpace(flow.Topic))
		return flowRuntime{}, fmt.Errorf("%w: workflow %s has never been published", ErrFlowNotFound, strings.TrimSpace(flow.Topic))
	}
	// Published before T97 and not snapshotted yet: run the rows, as before.
	return s.rowsRuntime(ctx, *flow)
}

// draftRuntime runs the current rows (test-run). flow_version 0 marks the run as unversioned: it
// retries in place and is never resumed, since the draft it ran may change at any time.
func (s *Service) draftRuntime(ctx context.Context, flow *workflowmodels.FlowRecord) (flowRuntime, error) {
	draft := *flow
	draft.Version = 0
	return s.rowsRuntime(ctx, draft)
}

// versionRuntime is the definition a run pinned to version executes (resume, replay). A version
// that is no longer stored is an error: the run must never continue on another definition.
func (s *Service) versionRuntime(ctx context.Context, flow *workflowmodels.FlowRecord, version int) (flowRuntime, error) {
	if version <= 0 {
		return s.draftRuntime(ctx, flow)
	}
	published, err := workflowmodels.DecodeWorkflowSnapshot(flow.PublishedSnapshot)
	if err != nil {
		return flowRuntime{}, fmt.Errorf("workflow %s: %w", strings.TrimSpace(flow.Topic), err)
	}
	switch {
	case published != nil && published.Version == version:
		return snapshotRuntime(*flow, published)
	case published == nil && strings.TrimSpace(flow.State) != workflowmodels.WorkflowStateDraft && flow.Version == version:
		// Legacy row not snapshotted yet: its rows are the version its runs were pinned to.
		return s.rowsRuntime(ctx, *flow)
	}
	snapshot, err := s.loadSnapshot(ctx, flow.ID, version)
	if err != nil {
		return flowRuntime{}, err
	}
	if snapshot == nil {
		log.Errorf("%v: workflow %s version %d is not stored", ErrFlowNotFound, strings.TrimSpace(flow.Topic), version)
		return flowRuntime{}, fmt.Errorf("%w: workflow %s version %d is not stored", ErrFlowNotFound, strings.TrimSpace(flow.Topic), version)
	}
	return snapshotRuntime(*flow, snapshot)
}

// rowsRuntime loads the steps from the action rows; DAG edges are read when the run starts.
func (s *Service) rowsRuntime(ctx context.Context, flow workflowmodels.FlowRecord) (flowRuntime, error) {
	steps, err := s.loadRuntimeSteps(ctx, &flow)
	if err != nil {
		return flowRuntime{}, err
	}
	return flowRuntime{flow: flow, steps: steps}, nil
}

// snapshotRuntime runs snapshot with the live identity, access and on/off of flow.
func snapshotRuntime(flow workflowmodels.FlowRecord, snapshot *workflowmodels.WorkflowSnapshot) (flowRuntime, error) {
	runtimeFlow := snapshot.RuntimeFlow(flow)
	if len(snapshot.Steps) == 0 {
		log.Errorf("%v: workflow %s@v%d has no active configured actions", ErrActionDefinitionNotFound, strings.TrimSpace(flow.Topic), snapshot.Version)
		return flowRuntime{}, fmt.Errorf("%w: workflow %s@v%d has no active configured actions", ErrActionDefinitionNotFound, strings.TrimSpace(flow.Topic), snapshot.Version)
	}
	return flowRuntime{
		flow:   runtimeFlow,
		steps:  append([]workflowmodels.Step(nil), snapshot.Steps...),
		edges:  append([]workflowmodels.WorkflowGraphEdge(nil), snapshot.Edges...),
		pinned: true,
	}, nil
}

// loadWorkflowActions loads the workflow actions handled by this package.
func loadWorkflowActions(ctx context.Context, workflowID int64) ([]workflowmodels.WorkflowActionRecord, error) {
	if workflowID <= 0 {
		log.Errorf("workflow: load workflow actions requires a positive workflow id")
		return nil, fmt.Errorf("workflow: load workflow actions requires a positive workflow id")
	}

	rows, err := modelruntime.NewService().SearchRead(ctx, workflowmodels.WorkflowActionModelName, orm.RecordMap{
		"workflow_id": workflowID,
		"is_active":   true,
		"__order":     "sequence asc, id asc",
	}, nil)
	if err != nil {
		log.Errorf("workflow: load workflow actions workflow_id=%d: %v", workflowID, err)
		return nil, fmt.Errorf("workflow: load workflow actions workflow_id=%d: %w", workflowID, err)
	}

	actions := make([]workflowmodels.WorkflowActionRecord, 0, len(rows))
	for _, row := range rows {
		delete(row, "workflow_id")
		delete(row, "catalog_id")
		action, err := decodeRecord[workflowmodels.WorkflowActionRecord](row)
		if err != nil {
			return nil, err
		}
		actions = append(actions, *action)
	}
	return actions, nil
}

// loadWorkflowRun loads the workflow run handled by this package.
func loadWorkflowRun(ctx context.Context, runID int64) (*workflowmodels.WorkflowRunRecord, error) {
	rows, err := modelruntime.NewService().Read(ctx, workflowmodels.WorkflowRunModelName, []string{fmt.Sprint(runID)}, nil)
	if err != nil {
		log.Errorf("workflow: read workflow run %d: %v", runID, err)
		return nil, fmt.Errorf("workflow: read workflow run %d: %w", runID, err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	// "read" renders many2one fields as [id, name] tuples, which decodeRecord's
	// generic JSON round-trip can't unmarshal into *int64. workflow_id is unused
	// downstream here, so drop it before decoding (same pattern as
	// loadWorkflowActions/loadLatestActiveFlow below).
	delete(rows[0], "workflow_id")
	delete(rows[0], "parent_run_id") // also a many2one tuple; unused by the engine
	return decodeRecord[workflowmodels.WorkflowRunRecord](rows[0])
}

// resolveWorkflowModuleAction resolves the workflow module action handled by this package.
func resolveWorkflowModuleAction(name string, version int) (ActionHandler, bool) {
	handler, ok := nodes.ResolveWorkflowActionHandler(strings.TrimSpace(name), version)
	if !ok || handler == nil {
		return nil, false
	}
	return func(ctx context.Context, req ActionRequest) ([]byte, error) {
		return handler(ctx, nodes.WorkflowActionRequest{
			Topic: req.Topic,
			Action: workflowmodels.WorkflowActionRecord{
				ActionCode:    strings.TrimSpace(req.Step.Action),
				ActionVersion: req.Step.ResolvedActionVersion(),
				StepKey:       req.Step.Key,
				Name:          req.Step.Name,
			},
			Step:           req.Step,
			Payload:        append([]byte(nil), req.Payload...),
			CredentialRefs: req.CredentialRefs,
		})
	}, true
}
