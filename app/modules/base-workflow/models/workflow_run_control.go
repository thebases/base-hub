package models

// File overview:
// - Responsibility: run control on base.workflow.run (plans T95 and T100): replay_run, cancel_run and
//   the engine-only admit_run (max_concurrent_runs gate).
// - The engine lives in service/engine, which imports this package, so replay and the in-process
//   cancel go through WorkflowRunControlHooks registered by engine.InitRuntime.
// - State transitions here are single conditional UPDATEs like the claim and lease methods: they
//   only move a run between states nobody else may hold at that moment (pending/waiting_retry to
//   cancelled, running to waiting_retry under the admission lock). Every other run write stays in
//   the engine's markRun/createWorkflowRun.
// - Key symbols: `ErrWorkflowRunConcurrencyLimit`, `WorkflowRunControlHooks`, `RegisterWorkflowRunControlHooks`.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

// ErrWorkflowRunConcurrencyLimit: the workflow already runs max_concurrent_runs runs and this run
// cannot wait (caller-bound or tcp). It is classified as a 409 Conflict.
var ErrWorkflowRunConcurrencyLimit = errors.New("workflow: max_concurrent_runs reached")

// WorkflowRunLeaseCancelRequested is what renew_run_lease answers instead of true when the lease
// is held but cancel_run flagged the run.
const WorkflowRunLeaseCancelRequested = "cancel_requested"

// Outcomes of admit_run.
const (
	WorkflowRunAdmitted = "admitted" // the run may execute (a pending run is now running)
	WorkflowRunParked   = "parked"   // a running (resumed) run was moved back to waiting_retry
	WorkflowRunRejected = "rejected" // a pending run is over the limit; nothing was written
	WorkflowRunLost     = "lost"     // the caller no longer holds the run's lease
)

func init() {
	orm.RegisterErrorClassifier(func(err error) *orm.Error {
		if errors.Is(err, ErrWorkflowRunConcurrencyLimit) {
			return orm.Conflictf("%s", err.Error())
		}
		return nil
	})
}

// WorkflowRunControlHooks are the engine entry points behind replay_run and cancel_run.
type WorkflowRunControlHooks struct {
	// ReplayRun starts a new run replaying parent runID (optionally from fromStepKey) under ctx's
	// identity and answers {run_id, parent_run_id, name, status, last_error}.
	ReplayRun func(ctx context.Context, runID int64, fromStepKey string) (orm.RecordMap, error)
	// RunCancelled is told after cancel_run's UPDATE: status is the run's new status (cancelled, or
	// running when only flagged) so the engine can stop a local execution or count the transition.
	RunCancelled func(ctx context.Context, runID int64, status, triggerType string)
	// DecideApproval claims a waiting_approval run for the decision and continues it (plan T103);
	// it answers {id, status, last_error}.
	DecideApproval func(ctx context.Context, runID int64, decision WorkflowApprovalDecision) (orm.RecordMap, error)
}

var workflowRunControlHooks WorkflowRunControlHooks

// RegisterWorkflowRunControlHooks installs the engine hooks (engine.InitRuntime); a zero value
// unregisters them.
func RegisterWorkflowRunControlHooks(hooks WorkflowRunControlHooks) {
	workflowRunControlHooks = hooks
}

// workflowRunPathID reads the single positive run id a UsesPathID method was called with.
func workflowRunPathID(method string, ids []string) (int64, error) {
	if len(ids) != 1 {
		return 0, orm.InvalidParamsf("base.workflow.run %s requires exactly one run id", method)
	}
	id, ok := int64FromAny(strings.TrimSpace(ids[0]))
	if !ok || id <= 0 {
		return 0, orm.InvalidParamsf("base.workflow.run %s requires a positive run id", method)
	}
	return id, nil
}

// replayWorkflowRun is base.workflow.run.replay_run (plan T95). The operator role is checked by the
// dispatcher (workflowRequiredRole); the caller must also be able to read the parent run.
func replayWorkflowRun(ctx context.Context, _ *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	id, err := workflowRunPathID("replay_run", ids)
	if err != nil {
		return nil, err
	}
	if err := orm.EnsureRecordReadAccess(ctx, WorkflowRunModelName, ids); err != nil {
		return nil, err
	}
	fromStepKey := strings.TrimSpace(stringValue(firstNonNil(mapValue(kwargs, "from_step_key"), argValue(args, 0))))
	if workflowRunControlHooks.ReplayRun == nil {
		return nil, orm.NewError(orm.ErrUnavailable, 0, "workflow engine is not running")
	}
	return workflowRunControlHooks.ReplayRun(ctx, id, fromStepKey)
}

// cancelWorkflowRun is base.workflow.run.cancel_run (plan T100), one conditional UPDATE:
//   - pending, waiting_retry or waiting_approval: becomes cancelled at once. No execution holds it
//     (waiting_retry, waiting_approval: its resume token is cleared, so a late approve finds nothing)
//     or its creator's next lease renewal requires pending/running and stops on the lost lease.
//     output_payload is replaced by input_payload: the resume checkpoint is complete (never passed
//     through the audit policy) while input_payload always is, and a final row keeps only that.
//   - running: cancel_requested is set; the executing engine (any instance) reads it on its next
//     lease renewal and ends the run cancelled, and the local execution is cancelled right away.
//
// A run in a final status answers Conflict.
func cancelWorkflowRun(ctx context.Context, meta *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	id, err := workflowRunPathID("cancel_run", ids)
	if err != nil {
		return nil, err
	}
	if err := orm.EnsureRecordReadAccess(ctx, WorkflowRunModelName, ids); err != nil {
		return nil, err
	}
	var result workflowRunCancelResult
	err = workflowRunWithRequestTx(ctx, func(ctx context.Context, db bun.IDB) error {
		var err error
		result, err = cancelWorkflowRunLocked(ctx, db, meta.StorageName, id)
		return err
	})
	if err != nil {
		log.Errorf("base.workflow.run cancel_run %d: %v", id, err)
		return nil, fmt.Errorf("base.workflow.run cancel_run %d: %w", id, err)
	}
	if !result.Found {
		return nil, orm.NotFoundf("base.workflow.run %d not found", id)
	}
	if !result.Cancelled {
		return nil, orm.Conflictf("base.workflow.run %d is %s and cannot be cancelled", id, result.Status)
	}
	if hook := workflowRunControlHooks.RunCancelled; hook != nil {
		hook(ctx, id, result.Status, result.TriggerType)
	}
	return orm.RecordMap{"id": id, "status": result.Status, "cancel_requested": true}, nil
}

// workflowRunCancelResult is what cancelWorkflowRunLocked found: Cancelled false with Found true
// means the run is already final (Status says which).
type workflowRunCancelResult struct {
	Found, Cancelled    bool
	Status, TriggerType string
}

// cancelWorkflowRunLocked is the conditional UPDATE behind cancel_run (see cancelWorkflowRun).
func cancelWorkflowRunLocked(ctx context.Context, db bun.IDB, table string, id int64) (workflowRunCancelResult, error) {
	type runRow struct {
		Status      string `bun:"status"`
		TriggerType string `bun:"trigger_type"`
	}
	var rows []runRow
	// stopNow are the statuses no execution holds: they become cancelled at once.
	stopNow := bun.In([]string{WorkflowRunStatusPending, WorkflowRunStatusRetryWaiting, WorkflowRunStatusWaitingApproval})
	if err := db.NewRaw(fmt.Sprintf(
		`UPDATE %s SET
		   status = CASE WHEN status IN (?) THEN ? ELSE status END,
		   next_retry_at = CASE WHEN status IN (?) THEN 0 ELSE next_retry_at END,
		   output_payload = CASE WHEN status IN (?) THEN input_payload ELSE output_payload END,
		   last_error = CASE WHEN status IN (?) THEN ? ELSE last_error END,
		   lease_owner = CASE WHEN status IN (?) THEN '' ELSE lease_owner END,
		   lease_until = CASE WHEN status IN (?) THEN 0 ELSE lease_until END,
		   resume_token_hash = '',
		   approval_expires_at = 0,
		   cancel_requested = true,
		   updated_at = ?
		 WHERE id = ? AND status IN (?, ?)
		 RETURNING status, COALESCE(trigger_type, '') AS trigger_type`, table),
		stopNow, WorkflowRunStatusCancelled,
		stopNow,
		stopNow,
		stopNow, workflowRunCancelledMessage,
		stopNow,
		stopNow,
		time.Now().UTC(), id,
		WorkflowRunStatusRunning, stopNow,
	).Scan(ctx, &rows); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return workflowRunCancelResult{}, err
	}
	if len(rows) > 0 {
		return workflowRunCancelResult{Found: true, Cancelled: true, Status: rows[0].Status, TriggerType: rows[0].TriggerType}, nil
	}
	// Nothing matched: tell a missing run from a finished one.
	if err := db.NewRaw(fmt.Sprintf(`SELECT status, COALESCE(trigger_type, '') AS trigger_type FROM %s WHERE id = ?`, table), id).Scan(ctx, &rows); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return workflowRunCancelResult{}, err
	}
	if len(rows) == 0 {
		return workflowRunCancelResult{}, nil
	}
	return workflowRunCancelResult{Found: true, Status: rows[0].Status}, nil
}

// workflowRunCancelledMessage is the last_error of a run cancelled by cancel_run.
const workflowRunCancelledMessage = "workflow: run cancelled by an operator"

// admitWorkflowRun is the engine-only max_concurrent_runs gate (plan T100). kwargs: id,
// workflow_id, limit, lease_owner, park_until (unix), reason.
//
// Why an advisory lock and not SELECT ... FOR UPDATE on the workflow row: graph saves
// (runInWorkflowTx) lock that row for their whole transaction, so run starts would queue behind
// canvas saves and the reverse; pg_advisory_xact_lock on a per-workflow key serializes only
// admissions, and is released at commit. Under the lock the gate counts the workflow's other
// running runs with a live lease that already passed admission (a crashed worker's run stops
// counting once its lease expires; a claimed run waiting for its own admission never counts)
// and, in the same transaction:
//   - below the limit: the run becomes running (a pending fresh run) or stays running (a resumed run);
//   - over it, resumed run (running, claimed by the cron): parked back to waiting_retry with
//     next_retry_at = park_until, attempt_count untouched, so the park charges no attempt. Parking
//     inside the lock matters: a decided run must leave running (or drop admission_pending) in
//     the same transaction, or the next admission would count it wrongly;
//   - over it, fresh run (pending): nothing is written and the engine closes or parks it.
func admitWorkflowRun(ctx context.Context, meta *orm.ModelMetadata, _ []string, _ []any, kwargs map[string]any) (any, error) {
	id := int64Value(mapValue(kwargs, "id"))
	workflowID := int64Value(mapValue(kwargs, "workflow_id"))
	limit := int64Value(mapValue(kwargs, "limit"))
	owner := strings.TrimSpace(stringValue(mapValue(kwargs, "lease_owner")))
	parkUntil := int64Value(mapValue(kwargs, "park_until"))
	reason := strings.TrimSpace(stringValue(mapValue(kwargs, "reason")))
	if id <= 0 || workflowID <= 0 || limit <= 0 || owner == "" || parkUntil <= 0 {
		return nil, orm.InvalidParamsf("base.workflow.run admit_run requires id, workflow_id, limit, lease_owner and park_until")
	}
	outcome := ""
	err := workflowRunWithRequestTx(ctx, func(ctx context.Context, db bun.IDB) error {
		var err error
		outcome, err = admitWorkflowRunLocked(ctx, db, meta.StorageName, id, workflowID, limit, owner, parkUntil, reason)
		return err
	})
	if err != nil {
		log.Errorf("base.workflow.run admit_run %d: %v", id, err)
		return nil, fmt.Errorf("base.workflow.run admit_run %d: %w", id, err)
	}
	return outcome, nil
}

func admitWorkflowRunLocked(ctx context.Context, db bun.IDB, table string, id, workflowID, limit int64, owner string, parkUntil int64, reason string) (string, error) {
	if _, err := db.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtext(?))", fmt.Sprintf("%s|admit|%d", table, workflowID)); err != nil {
		return "", err
	}
	var self []struct {
		Status string `bun:"status"`
	}
	if err := db.NewRaw(fmt.Sprintf(`SELECT status FROM %s WHERE id = ? AND lease_owner = ? FOR UPDATE`, table), id, owner).Scan(ctx, &self); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if len(self) == 0 || (self[0].Status != WorkflowRunStatusPending && self[0].Status != WorkflowRunStatusRunning) {
		return WorkflowRunLost, nil
	}
	now := time.Now().UTC()
	var active int64
	// Runs a claim moved to running but not yet admitted (admission_pending) hold no slot: counting
	// them let a batch of claimed runs park each other on every tick and refuse fresh callers.
	if err := db.NewRaw(fmt.Sprintf(
		`SELECT count(*) FROM %s WHERE workflow_id = ? AND id <> ? AND status = ? AND lease_until > ?
		 AND NOT COALESCE(admission_pending, false)`, table),
		workflowID, id, WorkflowRunStatusRunning, now.Unix()).Scan(ctx, &active); err != nil {
		return "", err
	}
	if active < limit {
		if _, err := db.ExecContext(ctx, fmt.Sprintf(
			`UPDATE %s SET status = ?, updated_at = ?, admission_pending = false WHERE id = ? AND lease_owner = ? AND status IN (?, ?)`, table),
			WorkflowRunStatusRunning, now, id, owner, WorkflowRunStatusPending, WorkflowRunStatusRunning); err != nil {
			return "", err
		}
		return WorkflowRunAdmitted, nil
	}
	if self[0].Status == WorkflowRunStatusPending {
		return WorkflowRunRejected, nil
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf(
		`UPDATE %s SET status = ?, next_retry_at = ?, last_error = ?, lease_owner = '', lease_until = 0, updated_at = ?,
		 admission_pending = false WHERE id = ? AND lease_owner = ? AND status = ?`, table),
		WorkflowRunStatusRetryWaiting, parkUntil, reason, now, id, owner, WorkflowRunStatusRunning); err != nil {
		return "", err
	}
	return WorkflowRunParked, nil
}
