package engine

// File overview:
// - Responsibility: owns `workflow` module orchestration for runtime, where long-running workflow logic and operational state transitions are coordinated.
// - Key symbols: `InitRuntime`.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.
// - Pattern: Follows a state-machine style flow where persisted workflow state determines whether execution starts fresh, resumes, retries, or hands work to another step.

import (
	"context"

	"git.thebase.vn/base_hub/internal/modelruntime"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	automationservice "git.thebase.vn/base_hub/modules/base-automation/service"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
	"git.thebase.vn/base_hub/modules/base-workflow/service/libs"
	"git.thebase.vn/base_hub/modules/base-workflow/service/nodes"
)

// InitRuntime initializes the runtime handled by this package.
func InitRuntime() func() {
	nodes.RegisterWorkflowActionRuntimeHooks(nodes.WorkflowActionRuntimeHooks{
		CreateModelRecord: func(ctx context.Context, modelName string, values map[string]any) (map[string]any, error) {
			created, err := modelruntime.NewService().Create(ctx, modelName, orm.RecordMap(values))
			if err != nil {
				return nil, err
			}
			return created, nil
		},
		SearchModelRecords: func(ctx context.Context, modelName string, filter map[string]any, fields []string) ([]map[string]any, error) {
			records, err := modelruntime.NewService().SearchRead(ctx, modelName, orm.RecordMap(filter), fields)
			if err != nil {
				return nil, err
			}
			out := make([]map[string]any, 0, len(records))
			for _, record := range records {
				out = append(out, map[string]any(record))
			}
			return out, nil
		},
		WriteModelRecords: func(ctx context.Context, modelName string, ids []string, values map[string]any) (bool, error) {
			return modelruntime.NewService().Write(ctx, modelName, ids, orm.RecordMap(values))
		},
		DeleteModelRecords: func(ctx context.Context, modelName string, ids []string) (bool, error) {
			return modelruntime.NewService().Unlink(ctx, modelName, ids)
		},
		QueueHTTPJSON: func(ctx context.Context, topic string, rawURL string, headers map[string]string, authorizationRef string, body map[string]any) error {
			return libs.NewOutboundDeliveryService().QueueHTTPJSON(ctx, topic, rawURL, headers, authorizationRef, body)
		},
		PostHTTPJSON: libs.PostWorkflowHTTPJSON,
		FlattenJSON:  libs.FlattenJSON,
		HMACAction:   libs.HmacAction,
		AESAction:    libs.AesAction,
		AESDecryptAction: func(key string, encryptedData string) (string, error) {
			return libs.DecryptAesAction(key, encryptedData)
		},
		EvaluateCondition: func(expression any, bindings map[string]any) (bool, error) {
			return evaluateWorkflowCondition(expression, bindings)
		},
		// lua_script@v1 runs a stored base-automation lua action; base.call goes through the run's
		// own ctx, so a script can only touch the database (and environment) the run is bound to.
		RunLuaAction:      automationservice.RunLuaActionByID,
		RunLuaActionByRef: automationservice.RunLuaActionByRef,
		// call_workflow@v1 / for_each@v1 (plan T103): child runs go through the normal engine path.
		CallWorkflow: func(ctx context.Context, workflowID int64, payload []byte, label string) ([]byte, error) {
			return NewService().CallWorkflow(ctx, workflowID, payload, label)
		},
	})
	// replay_run and cancel_run (plans T95/T100) reach the engine through these hooks.
	workflowmodels.RegisterWorkflowRunControlHooks(workflowmodels.WorkflowRunControlHooks{
		ReplayRun: func(ctx context.Context, runID int64, fromStepKey string) (orm.RecordMap, error) {
			return NewService().ReplayRun(ctx, runID, fromStepKey)
		},
		RunCancelled: onRunCancelled,
		// approve/reject of a run parked at wait_approval@v1 (plan T103).
		DecideApproval: func(ctx context.Context, runID int64, decision workflowmodels.WorkflowApprovalDecision) (orm.RecordMap, error) {
			return NewService().DecideApproval(ctx, runID, decision)
		},
	})
	workflowmodels.RegisterWorkflowTriggerRuntimeHooks(workflowmodels.WorkflowTriggerRuntimeHooks{
		// The *FromWrite variants also publish workflow.trigger.changed so every other instance
		// reloads the row (plan T55).
		RegisterTrigger: func(ctx context.Context, trigger workflowmodels.WorkflowTriggerRecord) error {
			return DefaultTriggerRegistry().RegisterFromWrite(ctx, trigger)
		},
		UnregisterTrigger: func(ctx context.Context, triggerID int64) error {
			return DefaultTriggerRegistry().UnregisterFromWrite(ctx, triggerID)
		},
		SyncWorkflowTriggers: func(ctx context.Context, workflowID int64) {
			DefaultTriggerRegistry().SyncWorkflowFromWrite(ctx, workflowID)
		},
	})
	// Register the scheduler handler that drives workflow resume (SRS-F-015 /
	// plan 2.3) as a base-automation go_handler: base-automation's own
	// `automation.dispatch_due_crons` sys.cron bridge drives it via the seeded
	// `workflow.automation.action.resume_due_runs` / `.cron.resume_due_runs`
	// rows (data/17_automation_resume_due_runs.json), replacing workflow's own
	// direct sys.cron registration so cron ownership stays centralized in
	// base-automation. RegisterGoHandler only errors on duplicate registration,
	// which would mean two owners fight over one job key — fail loudly then.
	if err := automationservice.RegisterGoHandler("workflow.resume_due_runs", func(ctx context.Context, payload map[string]any) error {
		limit := resumeDueRunsLimit(payload)
		_, err := NewService().ResumeDueRuns(ctx, limit)
		return err
	}); err != nil {
		log.Fatalf("workflow: register go handler: %v", err)
	}
	// Register the reaper that reclaims runs stranded at status=running by a
	// dead worker or a hung step handler. It has no other path back to
	// succeeded/failed/waiting_retry: claim_due_retries only ever looks at
	// waiting_retry rows, so a running row with no live worker behind it
	// would otherwise sit there forever.
	if err := automationservice.RegisterGoHandler("workflow.reap_stale_running", func(ctx context.Context, payload map[string]any) error {
		limit := resumeDueRunsLimit(payload)
		staleAfterSeconds := reapStaleAfterSeconds(payload)
		_, err := NewService().ReapStaleRuns(ctx, limit, staleAfterSeconds)
		return err
	}); err != nil {
		log.Fatalf("workflow: register go handler: %v", err)
	}
	// Outbox sweeper for notify deliveries (plan T53): claims queued rows the fast path never
	// sent, stale sending rows and due retryable_failed rows, per database, via the seeded
	// `workflow.automation.cron.sweep_outbound` row.
	if err := automationservice.RegisterGoHandler("workflow.sweep_outbound", func(ctx context.Context, payload map[string]any) error {
		_, err := libs.NewOutboundDeliveryService().SweepDueDeliveries(ctx, int(resumeDueRunsLimit(payload)))
		return err
	}); err != nil {
		log.Fatalf("workflow: register go handler: %v", err)
	}
	// Boot-time pass over every database already connected. Tenant databases discovered later in
	// bootstrapServerDatabases are picked up by the post-migration SyncWorkflowTriggers call; a
	// per-database failure (including schema lag) is logged there instead of crashing the boot.
	DefaultTriggerRegistry().SyncAllDatabases(context.Background())
	// Follow trigger changes made through other instances, plus a periodic reconcile (plan T55).
	stopTriggerWatcher := DefaultTriggerRegistry().StartTriggerWatcher()
	return func() {
		stopTriggerWatcher()
		nodes.RegisterWorkflowActionRuntimeHooks(nodes.WorkflowActionRuntimeHooks{})
		workflowmodels.RegisterWorkflowTriggerRuntimeHooks(workflowmodels.WorkflowTriggerRuntimeHooks{})
		workflowmodels.RegisterWorkflowRunControlHooks(workflowmodels.WorkflowRunControlHooks{})
		// Unregister symmetrically so a later InitRuntime (tests, restarts) does
		// not trip the duplicate-registration guard.
		automationservice.UnregisterGoHandler("workflow.resume_due_runs")
		automationservice.UnregisterGoHandler("workflow.reap_stale_running")
		automationservice.UnregisterGoHandler("workflow.sweep_outbound")
		DefaultTriggerRegistry().Shutdown()
	}
}

// resumeDueRunsLimit reads the batch limit from the cron job payload, falling
// back to the plan default of 50 when absent or invalid.
func resumeDueRunsLimit(payload map[string]any) int64 {
	return payloadPositiveInt(payload, "limit", 50)
}

// reapStaleAfterSeconds reads the stale-run age threshold from the cron job
// payload, falling back to 5 minutes when absent or invalid.
func reapStaleAfterSeconds(payload map[string]any) int64 {
	return payloadPositiveInt(payload, "stale_after_seconds", 300)
}

// payloadPositiveInt returns payload[key] when it is a positive whole number, else defaultValue.
// JSONB numbers decode as float64, so that form is accepted too.
func payloadPositiveInt(payload map[string]any, key string, defaultValue int64) int64 {
	switch typed := payload[key].(type) {
	case int64:
		if typed > 0 {
			return typed
		}
	case int:
		if typed > 0 {
			return int64(typed)
		}
	case float64:
		if typed > 0 {
			return int64(typed)
		}
	}
	return defaultValue
}
