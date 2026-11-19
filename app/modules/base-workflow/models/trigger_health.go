package models

// Trigger health (plan T99, WF-U-9 / WF-X-12): base.workflow.trigger.health.
//
// The counters live in the engine's trigger registry (this package cannot import it), which
// registers a provider here. Health is per process: each node of a multi-instance deployment
// answers for its own registry, and `instance` says which node that was.
//
// Webhook triggers have no registry entry (a /wh request runs under the caller and nothing is
// subscribed), so they come back with applicable=false and zero counters. Run history already
// holds their deliveries, and a latest-run lookup would cost one query per webhook row while
// mixing run state with delivery health.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"

	"git.thebase.vn/base_hub/internal/orm"
)

// WorkflowTriggerHealth is one row of the health response. Times are UTC Unix seconds, 0 = never.
type WorkflowTriggerHealth struct {
	TriggerID    int64  `json:"trigger_id"`
	TriggerType  string `json:"trigger_type"`
	Topic        string `json:"topic"`
	Applicable   bool   `json:"applicable"`
	Registered   bool   `json:"registered"`
	RegisteredAt int64  `json:"registered_at"`
	LastEventAt  int64  `json:"last_event_at"`
	LastError    string `json:"last_error"`
	LastErrorAt  int64  `json:"last_error_at"`
	Events       int64  `json:"events"`
	Failures     int64  `json:"failures"`
	Instance     string `json:"instance"`
}

// WorkflowTriggerHealthProvider returns the in-memory health of ids in one logical database.
type WorkflowTriggerHealthProvider func(dbName string, ids []int64) map[int64]WorkflowTriggerHealth

var workflowTriggerHealthProvider atomic.Pointer[WorkflowTriggerHealthProvider]

// RegisterWorkflowTriggerHealthProvider is called by the engine at init.
func RegisterWorkflowTriggerHealthProvider(provider WorkflowTriggerHealthProvider) {
	workflowTriggerHealthProvider.Store(&provider)
}

// workflowInstanceID names this process (host:pid) in health rows.
var workflowInstanceID = func() string {
	host, _ := os.Hostname()
	return fmt.Sprintf("%s:%d", host, os.Getpid())
}()

// healthWorkflowTrigger lists health rows. kwargs: `ids` (trigger ids) and/or `workflow_id`;
// without either it covers every trigger the caller can read. Rows are read through the caller's
// context, then each is checked with orm.EnsureRecordAccess so record rules and company scope
// hide triggers the caller cannot read (denied rows are dropped, not an error).
func healthWorkflowTrigger(ctx context.Context, model *orm.ModelMetadata, _ []string, _ []any, kwargs map[string]any) (any, error) {
	if err := EnsureWorkflowRole(ctx, WorkflowRoleViewer); err != nil {
		return nil, err
	}
	filter := orm.RecordMap{"__order": "id asc"}
	if workflowID := int64Value(kwargs["workflow_id"]); workflowID > 0 {
		filter["workflow_id"] = workflowID
	}
	if raw, ok := kwargs["ids"].([]any); ok && len(raw) > 0 {
		ids := make([]string, 0, len(raw))
		for _, item := range raw {
			if id := int64Value(item); id > 0 {
				ids = append(ids, fmt.Sprint(id))
			}
		}
		if len(ids) == 0 {
			return []WorkflowTriggerHealth{}, nil
		}
		filter["__ids"] = ids
	}
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WorkflowTriggerModelName, model), filter,
		[]string{"id", "trigger_type", "topic"})
	if err != nil {
		return nil, err
	}
	readable, err := readableTriggerRows(ctx, rows)
	if err != nil {
		return nil, err
	}
	return buildTriggerHealthRows(orm.ResolveLogicalDBName(orm.EnvironmentFromContext(ctx).DBName()), readable), nil
}

// readableTriggerRows keeps the rows the caller may read; any non-forbidden error aborts.
func readableTriggerRows(ctx context.Context, rows []orm.RecordMap) ([]orm.RecordMap, error) {
	out := make([]orm.RecordMap, 0, len(rows))
	for _, row := range rows {
		err := orm.EnsureRecordAccess(ctx, WorkflowTriggerModelName, "read", []string{fmt.Sprint(int64Value(row["id"]))})
		if err == nil {
			out = append(out, row)
			continue
		}
		var typed *orm.Error
		if errors.As(err, &typed) && typed.Code == orm.ErrForbidden {
			continue
		}
		return nil, err
	}
	return out, nil
}

func buildTriggerHealthRows(dbName string, rows []orm.RecordMap) []WorkflowTriggerHealth {
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, int64Value(row["id"]))
	}
	var live map[int64]WorkflowTriggerHealth
	if provider := workflowTriggerHealthProvider.Load(); provider != nil && *provider != nil {
		live = (*provider)(dbName, ids)
	}
	out := make([]WorkflowTriggerHealth, 0, len(rows))
	for _, row := range rows {
		id := int64Value(row["id"])
		item := live[id]
		item.TriggerID = id
		item.TriggerType = strings.TrimSpace(stringValue(row["trigger_type"]))
		item.Topic = stringValue(row["topic"])
		item.Applicable = item.TriggerType != "webhook"
		item.Instance = workflowInstanceID
		out = append(out, item)
	}
	return out
}
