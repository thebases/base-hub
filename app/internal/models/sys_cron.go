package models

// File overview:
// - Responsibility: defines built-in runtime model metadata and server-side behavior for sys cron inside the kernel model catalog.
// - Key symbols: `init`, `createSysCron`, `writeSysCron`, `SysCronRecord`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// SysCronRecord stores one scheduled background job (SRS-F-015). The scheduler
// service in internal/services/cron claims due rows with FOR UPDATE SKIP LOCKED
// and executes the registered handler, so this model is the single source of
// truth for job state, cadence, and failure bookkeeping.
type SysCronRecord struct {
	orm.BaseModel
	Name string `bun:"name" json:"name" meta:"index,label:Name,help:Stores the human readable name for this scheduled job."`
	// Handler is the registered handler key resolved through the scheduler's
	// in-process registry; a job whose handler is not registered in the running
	// build is skipped, never failed, so builds without a module stay harmless.
	Handler         string    `bun:"handler" json:"handler" meta:"index,label:Handler,help:Stores the registered scheduler handler key executed by this job."`
	IntervalSeconds int64     `bun:"interval_seconds" json:"interval_seconds" meta:"label:Interval Seconds,help:Stores the execution cadence in seconds. Minimum 30."`
	NextCall        time.Time `bun:"next_call" json:"next_call" meta:"index,label:Next Call,help:Stores the next due execution time for this job."`
	FailCount       int64     `bun:"fail_count" json:"fail_count" meta:"label:Fail Count,help:Stores the consecutive failure count used for backoff and deactivation."`
	MaxFailures     int64     `bun:"max_failures" json:"max_failures" meta:"label:Max Failures,help:Stores how many consecutive failures deactivate this job. Default 5."`
	// Payload carries handler kwargs as JSONB so operators can tune a job (for
	// example the resume batch limit) without a code change.
	Payload map[string]any `bun:"payload,type:jsonb,nullzero" json:"payload" meta:"kind:json,label:Payload,help:Stores the handler keyword arguments in JSON format."`
}

const SysCronModelName = "sys.cron"

// SysCronMinIntervalSeconds enforces the SRS-F-015 floor so a misconfigured job
// cannot hot-loop the scheduler against the database.
const SysCronMinIntervalSeconds int64 = 30

// SysCronDefaultMaxFailures is the SRS-F-015 default deactivation threshold.
const SysCronDefaultMaxFailures int64 = 5

var SysCronModel = orm.Define[SysCronRecord](&Models, 310, orm.ModelSpec{
	Name:        SysCronModelName,
	Label:       "Scheduled Jobs",
	Description: "Kernel-owned scheduler jobs executed by the cron service (SRS-F-015)",
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "create", Public: true, ModelOnly: true, Call: createSysCron},
		{Name: "write", Public: true, UsesPathID: true, Call: writeSysCron},
	},
})

// Indirection breaks the SysCronModelSpec -> createSysCron -> SysCronModel
// initialization cycle, mirroring the sys_rule.go registration pattern.

// createSysCron creates the sys cron handled by this package.
func createSysCron(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	valuesList, err := recordMapListFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if len(valuesList) == 0 {
		log.Errorf("sys.cron.create requires values")
		return nil, fmt.Errorf("sys.cron.create requires values")
	}

	created := make([]orm.RecordMap, 0, len(valuesList))
	for _, values := range valuesList {
		normalized, err := normalizeSysCronValues(values, true)
		if err != nil {
			return nil, err
		}
		record, err := orm.CreateRecord(ctx, orm.RuntimeModelRef(SysCronModelName, model), normalized)
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

func writeSysCron(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	if len(ids) == 0 {
		log.Errorf("sys.cron.write requires ids")
		return nil, fmt.Errorf("sys.cron.write requires ids")
	}
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		log.Errorf("sys.cron.write requires values")
		return nil, fmt.Errorf("sys.cron.write requires values")
	}
	normalized, err := normalizeSysCronValues(values, false)
	if err != nil {
		return nil, err
	}
	return orm.WriteRecords(ctx, orm.RuntimeModelRef(SysCronModelName, model), ids, normalized)
}

func normalizeSysCronValues(values orm.RecordMap, creating bool) (orm.RecordMap, error) {
	working := cloneRecordMap(values)

	// Apply the SRS-F-015 defaults on create so seed rows and UI-created jobs
	// start from one consistent state without the caller spelling every field.
	if creating {
		if _, ok := working["active"]; !ok {
			working["active"] = true
		}
		if _, ok := working["max_failures"]; !ok {
			working["max_failures"] = SysCronDefaultMaxFailures
		}
		if _, ok := working["fail_count"]; !ok {
			working["fail_count"] = int64(0)
		}
		// A new job is due immediately by default; the first claim then pushes
		// next_call forward by the configured interval.
		if _, ok := working["next_call"]; !ok {
			working["next_call"] = time.Now().UTC()
		}
	}

	if name, ok := stringFromAny(working["name"]); ok {
		working["name"] = strings.TrimSpace(name)
	}
	if handler, ok := stringFromAny(working["handler"]); ok {
		working["handler"] = strings.TrimSpace(handler)
	}

	if err := validateSysCronValues(working, creating); err != nil {
		return nil, err
	}
	return working, nil
}

// validateSysCronValues validates the sys cron values handled by this package.
func validateSysCronValues(values orm.RecordMap, creating bool) error {
	if raw, ok := values["interval_seconds"]; ok {
		interval, parsed := int64FromAny(raw)
		// Enforce the interval floor on every write path so a UI edit can never
		// turn a job into a hot loop against the database.
		if !parsed || interval < SysCronMinIntervalSeconds {
			log.Errorf("sys.cron interval_seconds must be at least %d", SysCronMinIntervalSeconds)
			return fmt.Errorf("sys.cron interval_seconds must be at least %d", SysCronMinIntervalSeconds)
		}
		values["interval_seconds"] = interval
	} else if creating {
		log.Errorf("sys.cron interval_seconds is required")
		return fmt.Errorf("sys.cron interval_seconds is required")
	}

	if raw, ok := values["max_failures"]; ok {
		maxFailures, parsed := int64FromAny(raw)
		if !parsed || maxFailures <= 0 {
			log.Errorf("sys.cron max_failures must be a positive integer")
			return fmt.Errorf("sys.cron max_failures must be a positive integer")
		}
		values["max_failures"] = maxFailures
	}

	// Walk each required string field in order so validation and side effects stay scoped to the current element.
	for _, key := range []string{"name", "handler"} {
		raw, ok := values[key]
		if !ok {
			if creating {
				log.Errorf("sys.cron %s is required", key)
				return fmt.Errorf("sys.cron %s is required", key)
			}
			continue
		}
		if strings.TrimSpace(stringValue(raw)) == "" {
			log.Errorf("sys.cron %s must not be empty", key)
			return fmt.Errorf("sys.cron %s must not be empty", key)
		}
	}
	return nil
}
