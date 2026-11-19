package models

// File overview:
// - Responsibility: defines the `workflow` module model contract for base workflow run, including field metadata, runtime methods, and module-specific invariants.
// - Key symbols: `WorkflowRunModel`, `createWorkflowRun`, `WorkflowRunRecord`, `WorkflowStepRun`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Uses Bun-facing model or repository patterns so schema mapping stays explicit and transaction-aware inside the backend data layer.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

const (
	WorkflowRunStatusPending      = "pending"
	WorkflowRunStatusRunning      = "running"
	WorkflowRunStatusSucceeded    = "succeeded"
	WorkflowRunStatusRetryWaiting = "waiting_retry"
	WorkflowRunStatusDeadLetter   = "dead_letter"
	WorkflowRunStatusFailed       = "failed"
	WorkflowRunStatusCancelled    = "cancelled"
	WorkflowRunStatusSkipped      = "skipped"

	WorkflowStepTypeAction = "action"
)

type WorkflowRunRecord struct {
	orm.BaseModel
	Name        string `bun:"name,unique" json:"name" meta:"unique,label:Request ID,help:Stores the workflow request ID for this run."`
	WorkflowID  *int64 `bun:"workflow_id" json:"workflow_id" meta:"kind:many2one,model:base.workflow,ondelete:CASCADE,index,label:Workflow,help:Links this run back to the workflow definition that produced it."`
	CompanyID   *int64 `bun:"company_id" json:"company_id" meta:"kind:many2one,model:tbl.company,ondelete:SET NULL,index,label:Company ID,help:Inherits the company scope from the parent workflow; not settable independently."`
	TriggerType string `bun:"trigger_type" json:"trigger_type" meta:"index,filter,label:Trigger Type,help:Stores which trigger source started this run." selection:"(webhook,Webhook),(kafka,Kafka),(mqtt,MQTT),(model,Model Event),(tcp,TCP),(manual_test,Manual Test)"`
	Topic       string `bun:"topic" json:"topic" meta:"index,filter,label:Topic,help:Stores the topic value for this record."`
	FlowVersion int    `bun:"flow_version" json:"flow_version" meta:"index,label:Flow Version,help:Stores the numeric value for flow version."`
	Status      string `bun:"status" json:"status" meta:"index,filter,label:Status,help:Stores the current status for this record."`
	CurrentStep string `bun:"current_step" json:"current_step" meta:"label:Current Step,help:Stores the current step value for this record."`
	// CurrentSteps is additive alongside CurrentStep (kept unmodified for
	// linear-mode display/back-compat): DAG mode writes the in-flight frontier
	// batch's step keys here instead, since more than one step can be running
	// at once. Never read for control flow — step_runs[] stays the source of
	// truth for what actually happened.
	CurrentSteps  []string               `bun:"current_steps,type:jsonb,nullzero" json:"current_steps" meta:"kind:json,label:Current Steps,help:Stores the DAG-mode in-flight frontier batch's step keys."`
	InputPayload  map[string]interface{} `bun:"input_payload,type:jsonb,nullzero" json:"input_payload" meta:"kind:json,label:Input Payload,help:Stores the input payload payload in JSON format."`
	OutputPayload map[string]interface{} `bun:"output_payload,type:jsonb,nullzero" json:"output_payload" meta:"kind:json,label:Output Payload,help:Stores the output payload payload in JSON format."`
	StepRuns      []WorkflowStepRun      `bun:"step_runs,type:jsonb,nullzero" json:"step_runs" meta:"kind:json,label:Step Runs,help:Stores the step runs payload in JSON format."`
	AttemptCount  int                    `bun:"attempt_count" json:"attempt_count" meta:"label:Attempt Count,help:Stores the numeric value for attempt count."`
	LastError     string                 `bun:"last_error" json:"last_error" meta:"label:Last Error,help:Stores the last error value for this record."`
	NextRetryAt   int64                  `bun:"next_retry_at" json:"next_retry_at" meta:"index,label:Next Retry At,help:Stores the numeric value for next retry at."`
	// AuditMode is the trigger's audit_payload mode at run creation, kept so a resumed run stores
	// its payloads the same way (plan T45). Empty on runs created before T45 (transport default).
	// LeaseOwner/LeaseUntil fence run execution (plan T49): only the holder of a live lease may
	// write the run; the engine renews it before every write and on a heartbeat during a step.
	// Empty/0 = no lease (not running, or a row from before T49).
	LeaseOwner string `bun:"lease_owner" json:"lease_owner" meta:"readonly,label:Lease Owner,help:Execution that currently holds this run."`
	LeaseUntil int64  `bun:"lease_until" json:"lease_until" meta:"readonly,index,label:Lease Until,help:Unix time until which the lease holder owns this run. A running run past it is reclaimed."`
	// CallerBound marks a run executed under the caller's identity (/wh, test-run): it retries in
	// place under that identity and is never resumed by the system cron, which would replay it as
	// sudo (plan T48/T49 review). TriggerChain is the model-trigger chain that started the run, kept
	// so a resumed run still stops recursion (plan T47).
	CallerBound  bool    `bun:"caller_bound" json:"caller_bound" meta:"readonly,label:Caller Bound,help:Runs under the identity of the caller that started it and is never resumed by the system."`
	TriggerChain []int64 `bun:"trigger_chain,type:jsonb,nullzero" json:"trigger_chain" meta:"kind:json,readonly,label:Trigger Chain,help:Model trigger ids whose runs led to this run."`
	AuditMode    string  `bun:"audit_mode" json:"audit_mode" meta:"readonly,label:Audit Mode,help:How this run stores its payloads. Full hides sensitive keys and hash keeps only a sha256 and the size and none keeps nothing." selection:"(full,Full),(hash,Hash),(none,None)"`
	// ParentRunID links a replay to the run it replays (plan T95, replay_run) and a sub-workflow
	// child to its calling run (plan T103); nil for every other run.
	// CancelRequested is set by cancel_run on a running run (plan T100): the executing engine reads it
	// on its next lease renewal (markRun, step heartbeat) and ends the run cancelled.
	ParentRunID     *int64 `bun:"parent_run_id" json:"parent_run_id" meta:"kind:many2one,model:base.workflow.run,ondelete:SET NULL,index,readonly,label:Parent Run,help:The run this run replays or the run whose call_workflow or for_each step started it as a sub-workflow. Empty for runs started by a trigger."`
	CancelRequested bool   `bun:"cancel_requested" json:"cancel_requested" meta:"readonly,label:Cancel Requested,help:An operator asked to cancel this run. The engine stops it at the next step boundary."`
	// DryRun marks a test run whose side-effect steps only recorded their intent (plan T101).
	DryRun bool `bun:"dry_run" json:"dry_run" meta:"readonly,label:Dry Run,help:A test run whose steps with external or persistent effects were skipped and only recorded what they would have done."`
	// ResumeTokenHash / ApprovalExpiresAt belong to a run parked at wait_approval@v1 (plan T103):
	// the sha256 of the resume token (the token itself is never stored) and the unix time the wait
	// expires (0 = never). Both are cleared when the wait ends.
	ResumeTokenHash   string `bun:"resume_token_hash" json:"resume_token_hash" meta:"readonly,label:Resume Token Hash,help:SHA-256 of the token that approves or rejects this waiting run. The token itself is never stored."`
	ApprovalExpiresAt int64  `bun:"approval_expires_at" json:"approval_expires_at" meta:"readonly,label:Approval Expires At,help:Unix time when the approval wait of this run expires and the run fails. 0 means it never expires."`
	// AdmissionPending marks a run that claim_due_retries/take_run_lease moved to running before
	// ResumeRun's max_concurrent_runs gate decided on it (plan T100 review): admit_run clears it
	// (admitted or parked) and does not count flagged runs, so a batch of claimed runs does not hold
	// the workflow's slots before any of them is admitted.
	AdmissionPending bool `bun:"admission_pending" json:"admission_pending" meta:"readonly,label:Admission Pending,help:Claimed for a resume and not yet admitted under the workflow concurrency limit."`
	// StartedByUID is the user whose identity started the run (0 = a system trigger or an
	// anonymous /wh caller; plan T103 review). Engine-only: a caller-bound run continues under it
	// after an approval decision, and that user may not approve their own run.
	StartedByUID int64 `bun:"started_by_uid" json:"started_by_uid" meta:"readonly,label:Started By User ID,help:ID of the user whose identity started this run. 0 means a system trigger or an anonymous caller."`
	// ApprovalGroupID is the approver_group_id of the wait_approval step the run last parked at
	// (engine-only, written by the park; nil when that step named no group). A decision reads it
	// from the parked run, never from the definition, whose published snapshot may have changed.
	// It is kept after the wait ends as a record of who could decide; it only grants anything
	// while status is waiting_approval.
	ApprovalGroupID *int64 `bun:"approval_group_id" json:"approval_group_id" meta:"kind:many2one,model:tbl.groups,ondelete:SET NULL,readonly,label:Approval Group,help:Group whose members with the workflow operator role may approve or reject this run's approval wait without the resume token."`
}

type WorkflowStepRun struct {
	StepName              string                 `json:"step_name"`
	StepKey               string                 `json:"step_key,omitempty"`
	StepType              string                 `json:"step_type,omitempty"`
	Action                string                 `json:"action"`
	ActionVersion         int                    `json:"action_version"`
	ImplementationKey     string                 `json:"implementation_key,omitempty"`
	ImplementationVersion int                    `json:"implementation_version,omitempty"`
	Attempt               int                    `json:"attempt"`
	Status                string                 `json:"status"`
	StartedAt             int64                  `json:"started_at"`
	FinishedAt            int64                  `json:"finished_at"`
	DurationMs            int64                  `json:"duration_ms"`
	ContinueOnError       bool                   `json:"continue_on_error,omitempty"`
	NextRetryAt           int64                  `json:"next_retry_at,omitempty"`
	InputSnapshot         map[string]interface{} `json:"input_snapshot,omitempty"`
	OutputSnapshot        map[string]interface{} `json:"output_snapshot,omitempty"`
	Error                 string                 `json:"error,omitempty"`
}

var WorkflowRunModelName = "base.workflow.run"

var WorkflowRunModelSpec = orm.ModelSpec{
	Name:        WorkflowRunModelName,
	Label:       "Workflow Run",
	Description: "Topic workflow execution audit and retry state",
	RuntimeMethods: []orm.RuntimeMethod{
		// Run state is owned by the engine: clients cannot create or rewrite a run or claim/reap runs
		// (plan T41). create is Internal because a client-built row (status waiting_retry, a forged
		// output_payload checkpoint, current_step, flow_version...) would be executed by the sudo
		// resume cron; the engine creates runs in WorkflowEngineContext and clients start runs through
		// test-run, replay_run or a trigger.
		{Name: "create", Public: true, Internal: true, ModelOnly: true, Call: createWorkflowRun},
		{Name: "write", Public: true, Internal: true, UsesPathID: true, Call: writeWorkflowRun},
		{Name: "claim_due_retries", Public: true, Internal: true, ModelOnly: true, Call: claimDueWorkflowRunRetries},
		{Name: "reap_stale_running", Public: true, Internal: true, ModelOnly: true, Call: reapStaleRunningWorkflowRuns},
		{Name: "renew_run_lease", Public: true, Internal: true, ModelOnly: true, Call: renewWorkflowRunLease},
		{Name: "take_run_lease", Public: true, Internal: true, ModelOnly: true, Call: takeWorkflowRunLease},
		{Name: "admit_run", Public: true, Internal: true, ModelOnly: true, Call: admitWorkflowRun},
		// Run control (plans T95/T100). Both are mutating, so the dispatcher checks them as "write",
		// which workflowRequiredRole maps to the operator role (the generic write is Internal).
		{
			Name: "replay_run", Public: true, UsesPathID: true, Call: replayWorkflowRun,
			Description: "Start a new run that replays this finished run (dead_letter, failed, succeeded or cancelled) from its stored input, or from the stored output of the step before from_step_key. Runs under the caller's identity like a test run and answers {run_id, parent_run_id, name, status, last_error}.",
		},
		{
			Name: "cancel_run", Public: true, UsesPathID: true, Call: cancelWorkflowRun,
			Description: "Cancel this run. A pending or waiting_retry or waiting_approval run becomes cancelled at once; a running run is flagged and stops at its next step boundary. Answers {id, status, cancel_requested}.",
		},
		// Approval decisions (plan T103): operator role (dispatched as "write") plus the run's resume
		// token; an author may decide without it.
		{
			Name: "approve", Public: true, UsesPathID: true, Call: approveWorkflowRun,
			Description: "Approve this run waiting at a wait_approval step. kwargs: token (the resume token; optional for base.group_system) and comment. The run continues after the step with _approval {approved, by, comment} in its payload. The user who started the run cannot approve it and a test run needs base.group_system. A system-trigger run is handed to the resume cron (answers status waiting_retry); a caller-bound run continues in the request as its starter (409 when the workflow concurrency limit is full). Answers {id, status, last_error}.",
		},
		{
			Name: "reject", Public: true, UsesPathID: true, Call: rejectWorkflowRun,
			Description: "Reject this run waiting at a wait_approval step. kwargs: token and comment. The run ends failed or continues at the step's on_error_step_key. Answers {id, status, last_error}.",
		},
		{Name: "claim_approval", Public: true, Internal: true, ModelOnly: true, Call: claimWorkflowApproval},
		{Name: "release_approval_claim", Public: true, Internal: true, ModelOnly: true, Call: releaseWorkflowApprovalClaim},
		{Name: "expire_approvals", Public: true, Internal: true, ModelOnly: true, Call: expireWorkflowApprovals},
	},
}

// WorkflowRunModel should evolve through composition-style helpers near the
// spec so each workflow invariant remains isolated, reviewable, and reusable.
var WorkflowRunModel = orm.Define[WorkflowRunRecord](&Models, 130, WorkflowRunModelSpec)

// createWorkflowRun creates the workflow run handled by this package.
func createWorkflowRun(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	valuesList, err := recordMapListFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if len(valuesList) == 0 {
		log.Errorf("base.workflow.run.create requires values")
		return nil, orm.InvalidParamsf("base.workflow.run.create requires values")
	}

	created := make([]orm.RecordMap, 0, len(valuesList))
	for _, values := range valuesList {
		normalized, err := normalizeWorkflowRunValues(values, nil, true)
		if err != nil {
			return nil, err
		}
		if err := inheritWorkflowChildCompanyID(ctx, model, normalized); err != nil {
			return nil, err
		}
		record, err := orm.RuntimeRepositoryCreate(ctx, orm.RuntimeModelRef(WorkflowRunModelName, model), normalized)
		if err != nil {
			return nil, err
		}
		created = append(created, record)
	}
	if len(created) == 1 {
		return created[0], nil
	}
	return created, nil
}

func writeWorkflowRun(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	if len(ids) == 0 {
		log.Errorf("base.workflow.run.write requires ids")
		return nil, orm.InvalidParamsf("base.workflow.run.write requires ids")
	}
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		log.Errorf("base.workflow.run.write requires values")
		return nil, orm.InvalidParamsf("base.workflow.run.write requires values")
	}

	beforeRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(WorkflowRunModelName, model), ids, nil)
	if err != nil {
		return nil, err
	}
	if len(beforeRows) == 0 {
		return false, nil
	}

	current := workflowRunStateFromRecord(beforeRows[0])
	normalized, err := normalizeWorkflowRunValues(values, &current, false)
	if err != nil {
		return nil, err
	}
	return orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(WorkflowRunModelName, model), ids, normalized)
}

// claimDueWorkflowRunRetries claims the due workflow run retries handled by this package.
func claimDueWorkflowRunRetries(ctx context.Context, meta *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	limit := int64(100)
	if raw := firstNonNil(argValue(args, 0), mapValue(kwargs, "limit")); raw != nil {
		parsed, ok := int64FromAny(raw)
		if !ok || parsed <= 0 {
			log.Errorf("base.workflow.run claim_due_retries limit must be a positive integer")
			return nil, orm.InvalidParamsf("base.workflow.run claim_due_retries limit must be a positive integer")
		}
		limit = parsed
	}
	if limit > 500 {
		limit = 500
	}

	beforeTs := int64Value(firstNonNil(mapValue(kwargs, "before_ts"), argValue(args, 1)))
	if beforeTs <= 0 {
		beforeTs = time.Now().UTC().Unix()
	}
	// The claimer becomes the lease holder of every claimed run (plan T49).
	leaseOwner, leaseSeconds := runLeaseArgs(kwargs)
	now := time.Now().UTC()

	type claimedRow struct {
		ID int64 `bun:"id"`
	}

	rows := make([]claimedRow, 0, limit)
	runClaim := func(ctx context.Context, db bun.IDB) error {
		query := fmt.Sprintf(
			`SELECT id
			 FROM %s
			 WHERE status = ?
			   AND next_retry_at > 0
			   AND next_retry_at <= ?
			 ORDER BY next_retry_at ASC, id ASC
			 FOR UPDATE SKIP LOCKED
			 LIMIT ?`,
			meta.StorageName,
		)
		if err := db.NewRaw(query, WorkflowRunStatusRetryWaiting, beforeTs, limit).Scan(ctx, &rows); err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}

		ids := make([]int64, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}

		updateQuery := fmt.Sprintf(
			`UPDATE %s
			 SET status = ?, next_retry_at = 0, lease_owner = ?, lease_until = ?, updated_at = ?, admission_pending = true
			 WHERE status = ?
			   AND id IN (?)`,
			meta.StorageName,
		)
		if _, err := db.ExecContext(ctx, updateQuery, WorkflowRunStatusRunning, leaseOwner, now.Unix()+leaseSeconds, now, WorkflowRunStatusRetryWaiting, bun.In(ids)); err != nil {
			return err
		}
		return nil
	}
	err := workflowRunWithRequestTx(ctx, runClaim)
	if err != nil {
		log.Errorf("base.workflow.run claim_due_retries: %v", err)
		return nil, fmt.Errorf("base.workflow.run claim_due_retries: %w", err)
	}

	claimed := make([]orm.RecordMap, 0, len(rows))
	for _, row := range rows {
		claimed = append(claimed, orm.RecordMap{"id": row.ID})
	}
	return claimed, nil
}

// reapStaleRunningWorkflowRuns claims workflow runs orphaned at
// status=running whose lease expired (plan T49; rows without a lease fall back to
// updated_at older than stale_after_seconds) — previously only updated_at, so a long
// step was reaped while still running —
// status=running (worker crash or a hung step handler mid-attempt) and hands
// them back to the retry-waiting queue so the existing resume-due-runs cron
// picks them up on its next tick. Without this, a run whose process died
// between the pre-attempt markRun and the post-attempt markRun has no path
// back to succeeded/failed and stays running forever.
func reapStaleRunningWorkflowRuns(ctx context.Context, meta *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	limit := int64(100)
	if raw := firstNonNil(argValue(args, 0), mapValue(kwargs, "limit")); raw != nil {
		parsed, ok := int64FromAny(raw)
		if !ok || parsed <= 0 {
			log.Errorf("base.workflow.run reap_stale_running limit must be a positive integer")
			return nil, orm.InvalidParamsf("base.workflow.run reap_stale_running limit must be a positive integer")
		}
		limit = parsed
	}
	if limit > 500 {
		limit = 500
	}

	staleAfterSeconds := int64Value(firstNonNil(mapValue(kwargs, "stale_after_seconds"), argValue(args, 1)))
	if staleAfterSeconds <= 0 {
		staleAfterSeconds = 300
	}
	cutoff := time.Now().UTC().Add(-time.Duration(staleAfterSeconds) * time.Second)

	type staleRow struct {
		ID int64 `bun:"id"`
	}

	rows := make([]staleRow, 0, limit)
	runReap := func(ctx context.Context, db bun.IDB) error {
		query := fmt.Sprintf(
			`SELECT id
			 FROM %s
			 WHERE status = ?
			   AND (
			     (COALESCE(lease_until, 0) > 0 AND lease_until <= ?)
			     OR (COALESCE(lease_until, 0) = 0 AND updated_at <= ?)
			   )
			 ORDER BY updated_at ASC, id ASC
			 FOR UPDATE SKIP LOCKED
			 LIMIT ?`,
			meta.StorageName,
		)
		if err := db.NewRaw(query, WorkflowRunStatusRunning, time.Now().UTC().Unix(), cutoff, limit).Scan(ctx, &rows); err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}

		ids := make([]int64, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}

		now := time.Now().UTC().Unix()
		updateQuery := fmt.Sprintf(
			`UPDATE %s
			 SET status = ?, next_retry_at = ?, lease_owner = '', lease_until = 0
			 WHERE status = ?
			   AND id IN (?)`,
			meta.StorageName,
		)
		if _, err := db.ExecContext(ctx, updateQuery, WorkflowRunStatusRetryWaiting, now, WorkflowRunStatusRunning, bun.In(ids)); err != nil {
			return err
		}
		return nil
	}
	err := workflowRunWithRequestTx(ctx, runReap)
	if err != nil {
		log.Errorf("base.workflow.run reap_stale_running: %v", err)
		return nil, fmt.Errorf("base.workflow.run reap_stale_running: %w", err)
	}

	reaped := make([]orm.RecordMap, 0, len(rows))
	for _, row := range rows {
		reaped = append(reaped, orm.RecordMap{"id": row.ID})
	}
	return reaped, nil
}

// Run lease defaults (plan T49). The engine renews well before expiry (heartbeat every third of it).
const (
	DefaultWorkflowRunLeaseSeconds = int64(120)
	maxWorkflowRunLeaseSeconds     = int64(3600)
)

// runLeaseArgs reads lease_owner and lease_seconds (bounded) from kwargs.
func runLeaseArgs(kwargs map[string]any) (string, int64) {
	owner := strings.TrimSpace(stringValue(mapValue(kwargs, "lease_owner")))
	seconds := int64Value(mapValue(kwargs, "lease_seconds"))
	if seconds <= 0 {
		seconds = DefaultWorkflowRunLeaseSeconds
	}
	return owner, min(seconds, maxWorkflowRunLeaseSeconds)
}

// renewWorkflowRunLease extends the lease of run `id` held by `lease_owner`. It returns false when
// the caller no longer holds it (reaped, claimed by another worker, or the run left running): the
// caller must stop writing the run. One UPDATE, so renew-and-check is atomic.
func renewWorkflowRunLease(ctx context.Context, meta *orm.ModelMetadata, _ []string, _ []any, kwargs map[string]any) (any, error) {
	id := int64Value(mapValue(kwargs, "id"))
	owner, seconds := runLeaseArgs(kwargs)
	if id <= 0 || owner == "" {
		return nil, orm.InvalidParamsf("base.workflow.run renew_run_lease requires id and lease_owner")
	}
	// The renewal also reads cancel_requested (plan T100): every run write and every step heartbeat
	// renews first, so a cancel set on another instance is seen at the next step boundary for free.
	type renewedRow struct {
		ID              int64 `bun:"id"`
		CancelRequested bool  `bun:"cancel_requested"`
	}
	var rows []renewedRow
	err := workflowRunWithRequestTx(ctx, func(ctx context.Context, db bun.IDB) error {
		return db.NewRaw(fmt.Sprintf(
			`UPDATE %s SET lease_until = ?, updated_at = ?
			 WHERE id = ? AND lease_owner = ? AND status IN (?, ?)
			 RETURNING id, COALESCE(cancel_requested, false) AS cancel_requested`, meta.StorageName),
			time.Now().UTC().Unix()+seconds, time.Now().UTC(), id, owner, WorkflowRunStatusPending, WorkflowRunStatusRunning).Scan(ctx, &rows)
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		log.Errorf("base.workflow.run lease: %v", err)
		return nil, fmt.Errorf("base.workflow.run lease: %w", err)
	}
	if len(rows) == 0 {
		return false, nil
	}
	if rows[0].CancelRequested {
		return WorkflowRunLeaseCancelRequested, nil
	}
	return true, nil
}

// takeWorkflowRunLease gives `lease_owner` the lease of run `id` when nobody holds a live one
// (a direct ResumeRun without a claim). Like a claim it moves a waiting_retry run to running, and
// it never leases a finished run. It returns false while another execution's lease is valid.
func takeWorkflowRunLease(ctx context.Context, meta *orm.ModelMetadata, _ []string, _ []any, kwargs map[string]any) (any, error) {
	id := int64Value(mapValue(kwargs, "id"))
	owner, seconds := runLeaseArgs(kwargs)
	if id <= 0 || owner == "" {
		return nil, orm.InvalidParamsf("base.workflow.run take_run_lease requires id and lease_owner")
	}
	now := time.Now().UTC()
	return updateWorkflowRunLease(ctx, meta, fmt.Sprintf(
		`UPDATE %s SET status = ?, next_retry_at = 0, lease_owner = ?, lease_until = ?, updated_at = ?, admission_pending = true
		 WHERE id = ?
		   AND status IN (?, ?)
		   AND (COALESCE(lease_owner, '') = '' OR lease_owner = ? OR COALESCE(lease_until, 0) <= ?)
		 RETURNING id`, meta.StorageName),
		WorkflowRunStatusRunning, owner, now.Unix()+seconds, now, id,
		WorkflowRunStatusRunning, WorkflowRunStatusRetryWaiting, owner, now.Unix())
}

func updateWorkflowRunLease(ctx context.Context, _ *orm.ModelMetadata, query string, args ...any) (any, error) {
	var ids []int64
	err := workflowRunWithRequestTx(ctx, func(ctx context.Context, db bun.IDB) error {
		return db.NewRaw(query, args...).Scan(ctx, &ids)
	})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		log.Errorf("base.workflow.run lease: %v", err)
		return nil, fmt.Errorf("base.workflow.run lease: %w", err)
	}
	return len(ids) > 0, nil
}

func workflowRunWithRequestTx(ctx context.Context, fn func(context.Context, bun.IDB) error) error {
	if fn == nil {
		return nil
	}
	if env := orm.EnvironmentFromContext(ctx); env != nil && env.DB() != nil {
		switch db := env.DB().(type) {
		case bun.Tx:
			return fn(ctx, db)
		case *bun.Tx:
			return fn(ctx, db)
		case *bun.DB:
			return db.RunInTx(ctx, nil, func(txCtx context.Context, tx bun.Tx) error {
				return fn(txCtx, tx)
			})
		default:
			return fn(ctx, db)
		}
	}
	db := orm.GetBunDB()
	if db == nil {
		log.Errorf("base.workflow.run claim_due_retries requires a database")
		return fmt.Errorf("base.workflow.run claim_due_retries requires a database")
	}
	return db.RunInTx(ctx, nil, func(txCtx context.Context, tx bun.Tx) error {
		return fn(txCtx, tx)
	})
}

type workflowRunState struct {
	WorkflowID   *int64
	TriggerType  string
	Status       string
	FlowVersion  int64
	CurrentStep  string
	AttemptCount int64
	NextRetryAt  int64
}

func workflowRunStateFromRecord(record orm.RecordMap) workflowRunState {
	return workflowRunState{
		WorkflowID:   int64PointerFromAny(record["workflow_id"]),
		TriggerType:  strings.TrimSpace(stringValue(record["trigger_type"])),
		Status:       strings.TrimSpace(stringValue(record["status"])),
		FlowVersion:  int64Value(record["flow_version"]),
		CurrentStep:  strings.TrimSpace(stringValue(record["current_step"])),
		AttemptCount: int64Value(record["attempt_count"]),
		NextRetryAt:  int64Value(record["next_retry_at"]),
	}
}

func normalizeWorkflowRunValues(values orm.RecordMap, current *workflowRunState, creating bool) (orm.RecordMap, error) {
	normalized := cloneRecordMap(values)
	workflowRequestID, _ := stringFromAny(normalized["workflow_request_id"])
	requestID, _ := stringFromAny(normalized["request_id"])
	name, _ := stringFromAny(normalized["name"])
	workflowRequestID = strings.TrimSpace(workflowRequestID)
	requestID = strings.TrimSpace(requestID)
	name = strings.TrimSpace(name)
	delete(normalized, "workflow_request_id")
	delete(normalized, "request_id")

	if creating {
		// Internal workflow entrypoints can pin the lifecycle request ID here so
		// resumed retries can rehydrate the same downstream correlation context.
		if workflowRequestID != "" {
			normalized["name"] = workflowRequestID
		} else if name != "" {
			normalized["name"] = name
		} else if requestID != "" {
			normalized["name"] = requestID
		} else {
			normalized["name"] = uuid4String()
		}
	} else if name != "" {
		normalized["name"] = name
	} else if requestID != "" {
		normalized["name"] = requestID
	}

	if rawWorkflowID, exists := normalized["workflow_id"]; exists {
		ptr, ok := int64PointerValue(rawWorkflowID)
		if !ok || ptr == nil || *ptr <= 0 {
			log.Errorf("base.workflow.run workflow_id must be a workflow id")
			return nil, orm.InvalidParamsf("base.workflow.run workflow_id must be a workflow id")
		}
		normalized["workflow_id"] = ptr
	} else if current != nil && current.WorkflowID != nil {
		normalized["workflow_id"] = *current.WorkflowID
	}

	triggerType := strings.TrimSpace(currentWorkflowRunTriggerType(current))
	if rawTriggerType, exists := normalized["trigger_type"]; exists {
		triggerTypeValue, ok := stringFromAny(rawTriggerType)
		if !ok {
			log.Errorf("base.workflow.run trigger_type must be a string")
			return nil, orm.InvalidParamsf("base.workflow.run trigger_type must be a string")
		}
		triggerType = strings.TrimSpace(triggerTypeValue)
	}
	if triggerType != "" {
		switch strings.TrimSpace(triggerType) {
		case WorkflowTriggerTypeWebhook, WorkflowTriggerTypeKafka, WorkflowTriggerTypeMQTT, WorkflowTriggerTypeModel, WorkflowTriggerTypeTCP, WorkflowTriggerTypeManualTest:
		default:
			log.Errorf("base.workflow.run unsupported trigger_type %q", triggerType)
			return nil, orm.InvalidParamsf("base.workflow.run unsupported trigger_type %q", triggerType)
		}
		normalized["trigger_type"] = triggerType
	}

	if topic, ok := stringFromAny(normalized["topic"]); ok {
		normalized["topic"] = strings.TrimSpace(topic)
	}
	if rawVersion, exists := normalized["flow_version"]; exists {
		version, ok := int64FromNonNegativeAny(rawVersion)
		if !ok {
			log.Errorf("base.workflow.run flow_version must be a non-negative integer")
			return nil, orm.InvalidParamsf("base.workflow.run flow_version must be a non-negative integer")
		}
		normalized["flow_version"] = version
	} else if creating {
		normalized["flow_version"] = int64(0)
	}

	status := strings.TrimSpace(currentWorkflowRunStatus(current))
	if rawStatus, exists := normalized["status"]; exists {
		statusValue, ok := stringFromAny(rawStatus)
		if !ok {
			log.Errorf("base.workflow.run status must be a string")
			return nil, orm.InvalidParamsf("base.workflow.run status must be a string")
		}
		status = strings.TrimSpace(statusValue)
	}
	if status == "" && creating {
		status = WorkflowRunStatusPending
	}
	if status != "" {
		if !isValidWorkflowRunStatus(status) {
			log.Errorf("base.workflow.run unsupported status %q", status)
			return nil, orm.InvalidParamsf("base.workflow.run unsupported status %q", status)
		}
		normalized["status"] = status
	}

	if currentStep, ok := stringFromAny(normalized["current_step"]); ok {
		normalized["current_step"] = strings.TrimSpace(currentStep)
	} else if creating {
		normalized["current_step"] = ""
	}

	if rawCurrentSteps, exists := normalized["current_steps"]; exists {
		normalized["current_steps"] = stringSliceFromAny(rawCurrentSteps)
	} else if creating {
		normalized["current_steps"] = []string{}
	}

	if rawPayload, exists := normalized["input_payload"]; exists {
		payload, err := normalizeObjectMap(rawPayload)
		if err != nil {
			log.Errorf("base.workflow.run input_payload must be an object")
			return nil, orm.InvalidParamsf("base.workflow.run input_payload must be an object")
		}
		normalized["input_payload"] = payload
	} else if creating {
		normalized["input_payload"] = map[string]any{}
	}
	if rawPayload, exists := normalized["output_payload"]; exists {
		payload, err := normalizeObjectMap(rawPayload)
		if err != nil {
			log.Errorf("base.workflow.run output_payload must be an object")
			return nil, orm.InvalidParamsf("base.workflow.run output_payload must be an object")
		}
		normalized["output_payload"] = payload
	} else if creating {
		normalized["output_payload"] = map[string]any{}
	}

	if rawStepRuns, exists := normalized["step_runs"]; exists {
		stepRuns, err := normalizeWorkflowStepRuns(rawStepRuns)
		if err != nil {
			return nil, err
		}
		normalized["step_runs"] = stepRuns
	} else if creating {
		normalized["step_runs"] = []WorkflowStepRun{}
	}

	attemptCount := currentWorkflowRunAttemptCount(current)
	if rawAttempts, exists := normalized["attempt_count"]; exists {
		value, ok := int64FromNonNegativeAny(rawAttempts)
		if !ok {
			log.Errorf("base.workflow.run attempt_count must be a non-negative integer")
			return nil, orm.InvalidParamsf("base.workflow.run attempt_count must be a non-negative integer")
		}
		attemptCount = value
		normalized["attempt_count"] = value
	} else if creating {
		normalized["attempt_count"] = int64(0)
	}

	if lastError, ok := stringFromAny(normalized["last_error"]); ok {
		normalized["last_error"] = strings.TrimSpace(lastError)
	} else if creating {
		normalized["last_error"] = ""
	}

	nextRetryAt := currentWorkflowRunNextRetryAt(current)
	if rawNextRetry, exists := normalized["next_retry_at"]; exists {
		value, ok := int64FromNonNegativeAny(rawNextRetry)
		if !ok {
			log.Errorf("base.workflow.run next_retry_at must be a non-negative integer")
			return nil, orm.InvalidParamsf("base.workflow.run next_retry_at must be a non-negative integer")
		}
		nextRetryAt = value
		normalized["next_retry_at"] = value
	} else if creating {
		normalized["next_retry_at"] = int64(0)
	}

	if status == WorkflowRunStatusRetryWaiting && nextRetryAt <= 0 {
		log.Errorf("base.workflow.run waiting_retry rows require next_retry_at")
		return nil, fmt.Errorf("base.workflow.run waiting_retry rows require next_retry_at")
	}
	if isWorkflowRunTerminalStatus(status) && nextRetryAt > 0 {
		log.Errorf("base.workflow.run terminal statuses cannot retain next_retry_at")
		return nil, fmt.Errorf("base.workflow.run terminal statuses cannot retain next_retry_at")
	}
	if attemptCount == 0 && status == WorkflowRunStatusRunning {
		normalized["attempt_count"] = int64(0)
	}
	return normalized, nil
}

func normalizeWorkflowStepRuns(raw any) ([]WorkflowStepRun, error) {
	stepRuns, err := workflowStepRunsFromAny(raw)
	if err != nil {
		log.Errorf("base.workflow.run step_runs %v", err)
		return nil, fmt.Errorf("base.workflow.run step_runs %w", err)
	}

	normalized := make([]WorkflowStepRun, 0, len(stepRuns))
	for index, run := range stepRuns {
		run.StepName = strings.TrimSpace(run.StepName)
		run.StepKey = firstNonEmpty(run.StepKey, run.StepName)
		run.StepType = firstNonEmpty(run.StepType, WorkflowStepTypeAction)
		run.Action = strings.TrimSpace(run.Action)
		run.ImplementationKey = strings.TrimSpace(run.ImplementationKey)
		run.Error = strings.TrimSpace(run.Error)
		if run.StepKey == "" {
			log.Errorf("must include a step key for step_runs[%d]", index)
			return nil, fmt.Errorf("must include a step key for step_runs[%d]", index)
		}
		if run.Status != "" && !isValidWorkflowRunStatus(run.Status) {
			log.Errorf("contains unsupported status %q for step_runs[%d]", run.Status, index)
			return nil, orm.InvalidParamsf("contains unsupported status %q for step_runs[%d]", run.Status, index)
		}
		if run.Attempt < 0 {
			log.Errorf("attempt must be non-negative for step_runs[%d]", index)
			return nil, orm.InvalidParamsf("attempt must be non-negative for step_runs[%d]", index)
		}
		if run.ActionVersion < 0 {
			log.Errorf("action_version must be non-negative for step_runs[%d]", index)
			return nil, orm.InvalidParamsf("action_version must be non-negative for step_runs[%d]", index)
		}
		if run.ImplementationVersion < 0 {
			log.Errorf("implementation_version must be non-negative for step_runs[%d]", index)
			return nil, orm.InvalidParamsf("implementation_version must be non-negative for step_runs[%d]", index)
		}
		if run.StartedAt < 0 || run.FinishedAt < 0 || run.DurationMs < 0 || run.NextRetryAt < 0 {
			log.Errorf("timestamps must be non-negative for step_runs[%d]", index)
			return nil, orm.InvalidParamsf("timestamps must be non-negative for step_runs[%d]", index)
		}
		if run.InputSnapshot != nil {
			snapshot, err := normalizeObjectMap(run.InputSnapshot)
			if err != nil {
				log.Errorf("input_snapshot must be an object for step_runs[%d]", index)
				return nil, orm.InvalidParamsf("input_snapshot must be an object for step_runs[%d]", index)
			}
			run.InputSnapshot = snapshot
		}
		if run.OutputSnapshot != nil {
			snapshot, err := normalizeObjectMap(run.OutputSnapshot)
			if err != nil {
				log.Errorf("output_snapshot must be an object for step_runs[%d]", index)
				return nil, orm.InvalidParamsf("output_snapshot must be an object for step_runs[%d]", index)
			}
			run.OutputSnapshot = snapshot
		}
		normalized = append(normalized, run)
	}
	return normalized, nil
}

func workflowStepRunsFromAny(raw any) ([]WorkflowStepRun, error) {
	switch typed := raw.(type) {
	case nil:
		return nil, nil
	case []WorkflowStepRun:
		return append([]WorkflowStepRun(nil), typed...), nil
	case []any:
		out := make([]WorkflowStepRun, 0, len(typed))
		for _, entry := range typed {
			run, err := workflowStepRunFromAny(entry)
			if err != nil {
				return nil, err
			}
			out = append(out, run)
		}
		return out, nil
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil, nil
		}
		var decoded []WorkflowStepRun
		if err := json.Unmarshal([]byte(typed), &decoded); err != nil {
			log.Errorf("must be a step run array")
			return nil, orm.InvalidParamsf("must be a step run array")
		}
		return decoded, nil
	default:
		log.Errorf("must be a step run array, got %T", raw)
		return nil, orm.InvalidParamsf("must be a step run array, got %T", raw)
	}
}

func workflowStepRunFromAny(raw any) (WorkflowStepRun, error) {
	switch typed := raw.(type) {
	case WorkflowStepRun:
		return typed, nil
	case map[string]any:
		var stepRun WorkflowStepRun
		data, err := json.Marshal(typed)
		if err != nil {
			return WorkflowStepRun{}, err
		}
		if err := json.Unmarshal(data, &stepRun); err != nil {
			log.Errorf("must contain step run objects")
			return WorkflowStepRun{}, orm.InvalidParamsf("must contain step run objects")
		}
		return stepRun, nil
	default:
		log.Errorf("must contain step run objects, got %T", raw)
		return WorkflowStepRun{}, orm.InvalidParamsf("must contain step run objects, got %T", raw)
	}
}

func isValidWorkflowRunStatus(status string) bool {
	switch status {
	case WorkflowRunStatusPending,
		WorkflowRunStatusRunning,
		WorkflowRunStatusSucceeded,
		WorkflowRunStatusRetryWaiting,
		WorkflowRunStatusDeadLetter,
		WorkflowRunStatusFailed,
		WorkflowRunStatusCancelled,
		WorkflowRunStatusSkipped,
		WorkflowRunStatusWaitingApproval:
		return true
	default:
		return false
	}
}

func isWorkflowRunTerminalStatus(status string) bool {
	switch status {
	case WorkflowRunStatusSucceeded,
		WorkflowRunStatusDeadLetter,
		WorkflowRunStatusFailed,
		WorkflowRunStatusCancelled:
		return true
	default:
		return false
	}
}

func currentWorkflowRunStatus(current *workflowRunState) string {
	if current == nil {
		return ""
	}
	return current.Status
}

func currentWorkflowRunTriggerType(current *workflowRunState) string {
	if current == nil {
		return ""
	}
	return current.TriggerType
}

func currentWorkflowRunAttemptCount(current *workflowRunState) int64 {
	if current == nil {
		return 0
	}
	return current.AttemptCount
}

func currentWorkflowRunNextRetryAt(current *workflowRunState) int64 {
	if current == nil {
		return 0
	}
	return current.NextRetryAt
}
