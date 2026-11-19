package models

// File overview:
// - Responsibility: implements tbl.project_task recurrence by delegating
//   rule storage to modules/base-calendar's generic base.calendar.recurrence
//   model (per plan §2) instead of a bespoke tbl.project_task_recurrence
//   table — this module only owns the "spawn the next task occurrence when
//   the current one closes" business logic, calling base.calendar.recurrence
//   purely through orm.ExecuteRuntimeMethod (never a direct Go import of
//   modules/base-calendar, same cross-module-RPC convention every other
//   module in this repo uses). Ownership: project module.
//   Audit-sensitivity: low.
// - Key symbols: `ensureProjectTaskRecurrenceRule`,
//   `spawnNextProjectTaskOccurrenceIfRecurring`.
// - Simplification vs. reference: the next occurrence's due date is computed
//   with plain repeat_interval/repeat_unit arithmetic here rather than
//   calling into base-calendar's own (unexported) RFC-5545-style expansion —
//   that engine serves a different purpose (lazy virtual occurrences for
//   calendar display); materializing a real new tbl.project_task row on
//   close is project's own concern.

import (
	"context"
	"fmt"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

const baseCalendarRecurrenceModelName = "base.calendar.recurrence"

// projectTaskRepeatUnitToFrequency maps this module's repeat_unit values onto
// base.calendar.recurrence's frequency enum.
var projectTaskRepeatUnitToFrequency = map[string]string{
	"day":   "daily",
	"week":  "weekly",
	"month": "monthly",
	"year":  "yearly",
}

// ensureProjectTaskRecurrenceRule creates the backing base.calendar.recurrence
// row for a newly-created recurring task and writes its id back onto
// recurrence_id, best-effort (a failure here does not fail the task create,
// mirroring helpdesk's own best-effort side-effect pattern).
func ensureProjectTaskRecurrenceRule(ctx context.Context, taskID int64, created orm.RecordMap) error {
	if int64Value(created["recurrence_id"]) > 0 {
		return nil
	}
	frequency := projectTaskRepeatUnitToFrequency[stringValue(created["repeat_unit"])]
	if frequency == "" {
		frequency = "weekly"
	}
	startDate := time.Now().UTC()
	if deadline, ok := created["date_deadline"].(time.Time); ok && !deadline.IsZero() {
		startDate = deadline
	}
	values := map[string]any{
		"sys_model":  ProjectTaskModelName,
		"record_id":  taskID,
		"frequency":  frequency,
		"interval":   int64Value(created["repeat_interval"]),
		"start_date": startDate.Format("2006-01-02"),
	}
	if stringValue(created["repeat_type"]) == "until" {
		if until, ok := created["repeat_until"].(time.Time); ok && !until.IsZero() {
			values["end_date"] = until.Format("2006-01-02")
		}
	}
	result, err := orm.ExecuteRuntimeMethod(ctx, baseCalendarRecurrenceModelName, "create", nil, []any{values}, nil)
	if err != nil {
		return fmt.Errorf("tbl.project_task: create recurrence rule: %w", err)
	}
	ruleMap, ok := result.(map[string]any)
	if !ok {
		return fmt.Errorf("tbl.project_task: unexpected recurrence create result")
	}
	ruleID := int64Value(ruleMap["id"])
	if ruleID <= 0 {
		return nil
	}
	_, err = orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(ProjectTaskModelName, nil), []string{fmt.Sprint(taskID)}, orm.RecordMap{"recurrence_id": ruleID})
	return err
}

// spawnNextProjectTaskOccurrenceIfRecurring is called after a task
// transitions into a closed stage; when it is a recurring task, it creates
// the next occurrence (copying name/project_id/tags/assignees/milestone,
// resetting stage/dates), best-effort — logged, not propagated, since the
// task's own close already succeeded.
func spawnNextProjectTaskOccurrenceIfRecurring(ctx context.Context, taskID int64) {
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(ProjectTaskModelName, nil), []string{fmt.Sprint(taskID)}, []string{
		"name", "project_id", "tag_ids", "user_ids", "milestone_id", "role_ids",
		"recurring_task", "recurrence_id", "repeat_interval", "repeat_unit", "repeat_type", "repeat_until", "date_deadline",
	})
	if err != nil || len(rows) == 0 || !boolValue(rows[0]["recurring_task"]) {
		return
	}
	task := rows[0]

	nextDeadline := nextProjectTaskOccurrenceDate(task)
	if until, ok := task["repeat_until"].(time.Time); stringValue(task["repeat_type"]) == "until" && ok && !until.IsZero() && nextDeadline.After(until) {
		return
	}

	values := orm.RecordMap{
		"name":            stringValue(task["name"]),
		"project_id":      task["project_id"],
		"tag_ids":         task["tag_ids"],
		"user_ids":        task["user_ids"],
		"role_ids":        task["role_ids"],
		"milestone_id":    task["milestone_id"],
		"recurring_task":  true,
		"recurrence_id":   task["recurrence_id"],
		"repeat_interval": task["repeat_interval"],
		"repeat_unit":     task["repeat_unit"],
		"repeat_type":     task["repeat_type"],
		"repeat_until":    task["repeat_until"],
		"date_deadline":   nextDeadline,
	}
	if _, err := orm.RuntimeRepositoryCreate(ctx, orm.RuntimeModelRef(ProjectTaskModelName, nil), values); err != nil {
		log.Warnf("tbl.project_task %d: failed to spawn next recurring occurrence: %v", taskID, err)
	}
}

// nextProjectTaskOccurrenceDate advances task's date_deadline (defaulting to
// now) by repeat_interval units of repeat_unit.
func nextProjectTaskOccurrenceDate(task orm.RecordMap) time.Time {
	base := time.Now().UTC()
	if deadline, ok := task["date_deadline"].(time.Time); ok && !deadline.IsZero() {
		base = deadline
	}
	interval := int(int64Value(task["repeat_interval"]))
	if interval < 1 {
		interval = 1
	}
	switch stringValue(task["repeat_unit"]) {
	case "day":
		return base.AddDate(0, 0, interval)
	case "month":
		return base.AddDate(0, interval, 0)
	case "year":
		return base.AddDate(interval, 0, 0)
	default: // "week"
		return base.AddDate(0, 0, 7*interval)
	}
}
