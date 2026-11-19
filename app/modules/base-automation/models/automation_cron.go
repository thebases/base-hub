package models

// File overview:
// - Responsibility: defines the `automation.cron` model — a user-owned schedule that points at one
//   automation.action. Execution is driven by a single sys.cron bridge handler
//   (service/cron_dispatch.go), not by this model directly, so a failing action row never trips the
//   outer scheduler's own backoff/deactivation state.
// - Key symbols: `AutomationCronRecord`, `AutomationCronModel`, `prepareAutomationCron`.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
)

// AutomationCronMinIntervalSeconds mirrors the kernel sys.cron floor
// (internal/models/sys_cron.go SysCronMinIntervalSeconds) so a misconfigured
// automation cron cannot hot-loop the dispatcher any faster than a kernel job could.
const AutomationCronMinIntervalSeconds int64 = 30

// AutomationCronDefaultMaxFailures mirrors the kernel sys.cron default.
const AutomationCronDefaultMaxFailures int64 = 5

// AutomationCronRecord stores one user-defined internal cron. FailCount,
// LastRunStatus and (after create) NextCall are only ever mutated by the bridge
// handler in service/cron_dispatch.go; client create/write strips them
// (normalizeAutomationCronValues). Active is a user toggle: setting it true
// resets FailCount, so re-enabling is an explicit operator decision.
type AutomationCronRecord struct {
	orm.BaseModel
	Name            string    `bun:"name" json:"name" meta:"index,label:Name,help:Stores the human readable name for this scheduled automation."`
	ActionID        *int64    `bun:"action_id" json:"action_id" meta:"kind:many2one,model:automation.action,ondelete:CASCADE,index,label:Action,help:Links this schedule to the automation action it executes when due."`
	IntervalSeconds int64     `bun:"interval_seconds" json:"interval_seconds" meta:"label:Interval Seconds,help:Stores the execution cadence in seconds. Minimum 30."`
	NextCall        time.Time `bun:"next_call" json:"next_call" meta:"index,label:Next Call,help:Stores the next due execution time for this schedule."`
	FailCount       int64     `bun:"fail_count" json:"fail_count" meta:"readonly,label:Fail Count,help:Stores the consecutive failure count used for deactivation."`
	MaxFailures     int64     `bun:"max_failures" json:"max_failures" meta:"label:Max Failures,help:Stores how many consecutive failures deactivate this schedule. Default 5."`
	LastRunStatus   string    `bun:"last_run_status,nullzero" json:"last_run_status" meta:"readonly,label:Last Run Status,help:Stores whether the most recent execution succeeded or failed." selection:"(ok,Ok),(error,Error)"`
	CompanyID       *int64    `bun:"company_id" json:"company_id" meta:"kind:many2one,model:tbl.company,ondelete:SET NULL,index,label:Company ID,help:Inherits the company scope from the parent automation.action; not settable independently."`
}

const AutomationCronModelName = "automation.cron"

var AutomationCronModel = orm.Define[AutomationCronRecord](&Models, 11, orm.ModelSpec{
	Name:        AutomationCronModelName,
	Label:       "Automation Crons",
	Description: "User-defined internal schedules dispatched through the kernel scheduler bridge handler",
	Prepare:     prepareAutomationCron,
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "create", Public: true, ModelOnly: true, Call: createAutomationCron},
	},
})

// createAutomationCron keeps the list payload the default create does not accept; each row
// goes through the guarded create (Prepare, company check).
func createAutomationCron(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	return createRecordsFromArgs(ctx, orm.RuntimeModelRef(AutomationCronModelName, model), args, kwargs)
}

// prepareAutomationCron runs inside the guarded create/write: bookkeeping strip, interval floor
// and defaults, then company inheritance. A cron's company must always match its parent
// action's, never the caller's own session company, so a create inherits (CreateRecord pins the
// active company only when company_id is absent, so the inherited value is kept and then checked
// against the caller's allowed companies) and re-pointing a schedule at another action re-checks and
// re-inherits from it.
func prepareAutomationCron(ctx context.Context, op orm.WriteOp) error {
	normalized, err := normalizeAutomationCronValues(op.Values, op.Create)
	if err != nil {
		return err
	}
	replaceRecordMap(op.Values, normalized)
	if _, repoint := op.Values["action_id"]; op.Create || repoint {
		return inheritAutomationCronCompanyID(ctx, op.Values)
	}
	return nil
}

// inheritAutomationCronCompanyID overwrites working["company_id"] with the
// parent automation.action's own company_id, read from working["action_id"],
// mirroring tbl.website_page inheriting from its parent site.
func inheritAutomationCronCompanyID(ctx context.Context, working orm.RecordMap) error {
	actionID, ok := int64FromAny(working["action_id"])
	if !ok || actionID <= 0 {
		return orm.InvalidParamsf("automation.cron action_id is required to resolve company scope")
	}
	// The parent is read through the repository (no record rules); a schedule
	// runs its action, so the caller must be allowed to write that action.
	if err := parentRecordAccess(ctx, AutomationActionModelName, "write", []string{fmt.Sprint(actionID)}); err != nil {
		return err
	}
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(AutomationActionModelName, nil), []string{fmt.Sprint(actionID)}, []string{"company_id"})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return orm.NotFoundf("automation.action %d not found", actionID)
	}
	working["company_id"] = companyIDValue(rows[0]["company_id"])
	return nil
}

// automationCronBookkeepingFields are owned by the dispatch bridge
// (service/cron_dispatch.go, which writes them with raw SQL, not through these
// methods) and stripped from client create/write. next_call is stripped on
// write only: a create may still choose the first run time.
var automationCronBookkeepingFields = []string{"fail_count", "last_run_status"}

// normalizeAutomationCronValues applies the same interval-floor and
// required-field invariants as the kernel sys.cron model so a user-defined
// schedule cannot hot-loop the bridge handler.
func normalizeAutomationCronValues(values orm.RecordMap, creating bool) (orm.RecordMap, error) {
	working := cloneRecordMap(values)
	for _, field := range automationCronBookkeepingFields {
		delete(working, field)
	}
	if !creating {
		// A client next_call write would race the claim query's lease push.
		delete(working, "next_call")
		// active stays a user toggle (pause/resume, and toggle_active writes it
		// anyway), but re-enabling must start a fresh failure budget, otherwise a
		// row auto-deactivated at max_failures is deactivated again by its first
		// failure.
		if active, ok := working["active"].(bool); ok && active {
			working["fail_count"] = int64(0)
		}
	}

	if creating {
		if _, ok := working["active"]; !ok {
			working["active"] = true
		}
		if _, ok := working["max_failures"]; !ok {
			working["max_failures"] = AutomationCronDefaultMaxFailures
		}
		if _, ok := working["fail_count"]; !ok {
			working["fail_count"] = int64(0)
		}
		if _, ok := working["next_call"]; !ok {
			working["next_call"] = time.Now().UTC()
		}
	}

	if name, ok := stringFromAny(working["name"]); ok {
		working["name"] = strings.TrimSpace(name)
	} else if creating {
		return nil, orm.InvalidParamsf("automation.cron name is required")
	}

	if raw, ok := working["interval_seconds"]; ok {
		interval, parsed := int64FromAny(raw)
		if !parsed || interval < AutomationCronMinIntervalSeconds {
			return nil, orm.InvalidParamsf("automation.cron interval_seconds must be at least %d", AutomationCronMinIntervalSeconds)
		}
		working["interval_seconds"] = interval
	} else if creating {
		return nil, orm.InvalidParamsf("automation.cron interval_seconds is required")
	}

	if raw, ok := working["max_failures"]; ok {
		maxFailures, parsed := int64FromAny(raw)
		if !parsed || maxFailures <= 0 {
			return nil, orm.InvalidParamsf("automation.cron max_failures must be a positive integer")
		}
		working["max_failures"] = maxFailures
	}

	if _, ok := working["action_id"]; !ok && creating {
		return nil, orm.InvalidParamsf("automation.cron action_id is required")
	}

	return working, nil
}
