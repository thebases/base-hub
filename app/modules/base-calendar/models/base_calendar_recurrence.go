package models

// File overview:
// - Responsibility: generic, model-agnostic recurrence engine backing the generic calendar
//   view (SRS: .agents/knowledge/SRS/2026-08-03-frontend-admin-calendar-view-srs.md, SRS-F-006/007).
//   A recurrence rule references its target by (sys_model, record_id) rather than a physical FK,
//   since the target can be any registered model's table.
// - Key symbols: `BaseCalendarRecurrenceModel`, `prepareBaseCalendarRecurrence`, `BaseCalendarRecurrenceRecord`, `expandCalendarOccurrences`,
//   `occurrencesBaseCalendarRecurrence`, `rescheduleOccurrenceBaseCalendarRecurrence`.
// - Pattern: occurrences are expanded lazily at read time, bounded to the requested date range
//   (RFC 5545-style), never pre-materialized, so an indefinite rule (no end_date) stays cheap.
// - Deviation from SRS 6.1: the SRS's read contract names the path
//   `/rpc/base.calendar/occurrences`, but `base.calendar` itself is not a registered model (only
//   `base.calendar.recurrence`/`.exception` are) — the equivalent method is registered here as a
//   model-only RuntimeMethod on `base.calendar.recurrence` (`/rpc/base.calendar.recurrence/occurrences`).
// - Deviation from SRS 5.1/SRS-F-006: master-record deletion cascade is NOT a DB-level `ON DELETE
//   CASCADE`, since `record_id` is a generic (sys_model, record_id) reference, not a physical FK to
//   an arbitrary target table. `occurrencesBaseCalendarRecurrence` skips (never renders) a rule whose
//   master record no longer exists or is no longer readable; actual deletion of such orphaned rules
//   runs periodically as a base-automation go_handler (service/cleanup_cron.go), not inline on this
//   read path — reusing the module dependency on base-automation's cron/dispatch bridge instead of a
//   bespoke per-request goroutine. A stale rule can survive until the next cleanup tick, which is
//   acceptable since a deleted master record is never rendered on the calendar either way.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"git.thebase.vn/base_hub/internal/utils/anyconv"
)

// BaseCalendarRecurrenceRecord stores one recurrence rule (SRS-F-007, table
// `base.calendar.recurrence`), model-agnostic: sys_model/record_id name the
// master record on ANY registered model, not just base module models.
type BaseCalendarRecurrenceRecord struct {
	orm.BaseModel
	SysModel   string     `bun:"sys_model" json:"sys_model" meta:"index,required,label:Model,help:Stores the target model name this recurrence rule generates occurrences for."`
	RecordID   int64      `bun:"record_id" json:"record_id" meta:"index,required,label:Record ID,help:Stores the master record id on sys_model this rule generates occurrences for."`
	Frequency  string     `bun:"frequency" json:"frequency" meta:"required,label:Frequency,help:Stores the recurrence pattern kind." selection:"(daily,Daily),(weekly,Weekly),(biweekly,Every 2 Weeks),(monthly,Monthly),(yearly,Yearly),(weekday,Specific Weekday),(monthday,Specific Day of Month)"`
	Interval   int        `bun:"interval,default:1" json:"interval" meta:"label:Interval,help:Repeats every N units of the chosen frequency."`
	ByWeekday  []int      `bun:"by_weekday,type:jsonb,nullzero" json:"by_weekday" meta:"kind:json,label:By Weekday,help:Stores the 0=Sunday..6=Saturday weekdays this rule occurs on (weekly/biweekly/weekday)."`
	ByMonthday *int       `bun:"by_monthday" json:"by_monthday" meta:"label:By Month Day,help:Stores the day of month (1-31) this rule occurs on (monthday); clamped to the last day of shorter months."`
	StartDate  time.Time  `bun:"start_date" json:"start_date" meta:"kind:date,required,label:Start Date,help:Stores the first date this recurrence rule is active from."`
	EndDate    *time.Time `bun:"end_date" json:"end_date" meta:"kind:date,label:End Date,help:Stores the last date this recurrence rule is active until. Empty means it repeats indefinitely."`
	CompanyID  *int64     `bun:"company_id" json:"company_id" meta:"kind:many2one,model:tbl.company,ondelete:SET NULL,index,label:Company ID,help:Links this record to the related tbl.company record for company id."`
}

const BaseCalendarRecurrenceModelName = "base.calendar.recurrence"

var baseCalendarRecurrenceValidFrequencies = map[string]bool{
	"daily": true, "weekly": true, "biweekly": true, "monthly": true,
	"yearly": true, "weekday": true, "monthday": true,
}

var BaseCalendarRecurrenceModel = orm.Define[BaseCalendarRecurrenceRecord](&Models, 330, orm.ModelSpec{
	Name:        BaseCalendarRecurrenceModelName,
	Label:       "Calendar Recurrences",
	Description: "Model-agnostic recurrence rules powering the generic calendar view",
	Prepare:     prepareBaseCalendarRecurrence,
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "occurrences", Public: true, ModelOnly: true, ReadOnly: true, Call: occurrencesBaseCalendarRecurrence},
		{Name: "reschedule_occurrence", Public: true, UsesPathID: true, Call: rescheduleOccurrenceBaseCalendarRecurrence},
	},
})

// prepareBaseCalendarRecurrence validates a rule inside the guarded create/write: target model
// registered + record exists and is readable by the caller (delegated ACL, mirrors
// sys.attachment's binding validation), frequency enum, required by_weekday/by_monthday per
// frequency, and date ordering. A write re-validates the target only when sys_model or
// record_id is in the payload, so a partial write (e.g. only end_date) does not force the
// caller to resend them.
//
// Known limitation: the target (sys_model, record_id) can be any registered model, not all of
// which carry company_id themselves, so the target record's company scope is not inherited:
// CreateRecord pins company_id from the caller's session company when the payload names none,
// and an explicit company_id must be one of the caller's allowed companies.
func prepareBaseCalendarRecurrence(ctx context.Context, op orm.WriteOp) error {
	if op.Create || hasRecordMapKey(op.Values, "sys_model") || hasRecordMapKey(op.Values, "record_id") {
		if err := validateBaseCalendarRecurrenceTarget(ctx, op.Values); err != nil {
			return err
		}
	}
	return validateBaseCalendarRecurrencePattern(op.Values)
}

// validateBaseCalendarRecurrenceTarget enforces the (sys_model, record_id)
// contract: a registered model, a positive record id the caller can read.
func validateBaseCalendarRecurrenceTarget(ctx context.Context, values orm.RecordMap) error {
	sysModel := strings.TrimSpace(stringValue(values["sys_model"]))
	recordID, _ := anyconv.Many2OneID(values["record_id"])
	if sysModel == "" || recordID <= 0 {
		log.Errorf("base.calendar.recurrence requires sys_model and a positive record_id")
		return orm.InvalidParamsf("base.calendar.recurrence requires sys_model and a positive record_id")
	}
	// Unknown sys_model is a validation error per SRS-F-007.
	if _, ok := orm.LookupModel(sysModel); !ok {
		log.Errorf("base.calendar.recurrence sys_model %q is not registered", sysModel)
		return orm.InvalidParamsf("base.calendar.recurrence sys_model %q is not registered", sysModel)
	}
	// A caller without read access to the target record must not be able to
	// discover its existence or schedule a series against it.
	if err := orm.EnsureRecordReadAccess(ctx, sysModel, []string{strconv.FormatInt(recordID, 10)}); err != nil {
		log.Errorf("permission denied: cannot create a recurrence on %s(%d)", sysModel, recordID)
		return orm.Forbiddenf("permission denied: cannot create a recurrence on %s(%d)", sysModel, recordID)
	}
	return nil
}

// validateBaseCalendarRecurrencePattern enforces frequency enum,
// per-frequency required fields, and start/end date ordering (SRS-F-006).
func validateBaseCalendarRecurrencePattern(values orm.RecordMap) error {
	if hasRecordMapKey(values, "frequency") {
		frequency := strings.TrimSpace(stringValue(values["frequency"]))
		if !baseCalendarRecurrenceValidFrequencies[frequency] {
			log.Errorf("base.calendar.recurrence: invalid frequency %q", frequency)
			return orm.InvalidParamsf("base.calendar.recurrence: invalid frequency %q", frequency)
		}
		requiresWeekday := frequency == "weekly" || frequency == "biweekly" || frequency == "weekday"
		if requiresWeekday && !hasRecordMapKey(values, "by_weekday") {
			log.Errorf("base.calendar.recurrence: frequency %q requires by_weekday", frequency)
			return orm.InvalidParamsf("base.calendar.recurrence: frequency %q requires by_weekday", frequency)
		}
		if frequency == "monthday" && !hasRecordMapKey(values, "by_monthday") {
			log.Errorf("base.calendar.recurrence: frequency %q requires by_monthday", frequency)
			return orm.InvalidParamsf("base.calendar.recurrence: frequency %q requires by_monthday", frequency)
		}
	}
	if hasRecordMapKey(values, "interval") {
		interval, ok := int64FromAny(values["interval"])
		if !ok || interval < 1 || interval > maxCalendarRecurrenceInterval {
			return orm.InvalidParamsf("base.calendar.recurrence: interval must be an integer between 1 and %d", maxCalendarRecurrenceInterval)
		}
		values["interval"] = interval
	}
	startDate, hasStart := parseBaseCalendarDate(values["start_date"])
	endDate, hasEnd := parseBaseCalendarDate(values["end_date"])
	if hasStart && hasEnd && endDate.Before(startDate) {
		log.Errorf("base.calendar.recurrence: end_date must not be before start_date")
		return orm.InvalidParamsf("base.calendar.recurrence: end_date must not be before start_date")
	}
	return nil
}

// occurrencesBaseCalendarRecurrence is the read path behind
// `/rpc/base.calendar.recurrence/occurrences` (SRS 6.1): returns plain
// records for [date_from, date_to) via the target model's own search_read
// (full ACL/RBAC enforcement, same as any other RPC caller would get) merged
// with expanded virtual occurrences from active recurrence rules.
func occurrencesBaseCalendarRecurrence(ctx context.Context, _ *orm.ModelMetadata, _ []string, _ []any, kwargs map[string]any) (any, error) {
	sysModel := strings.TrimSpace(stringValue(kwargs["sys_model"]))
	if sysModel == "" {
		log.Errorf("base.calendar.recurrence.occurrences requires sys_model")
		return nil, orm.InvalidParamsf("base.calendar.recurrence.occurrences requires sys_model")
	}
	if _, ok := orm.LookupModel(sysModel); !ok {
		log.Errorf("base.calendar.recurrence.occurrences: sys_model %q is not registered", sysModel)
		return nil, orm.InvalidParamsf("base.calendar.recurrence.occurrences: sys_model %q is not registered", sysModel)
	}
	dateFrom, ok := parseBaseCalendarDate(kwargs["date_from"])
	if !ok {
		log.Errorf("base.calendar.recurrence.occurrences requires a valid date_from")
		return nil, orm.InvalidParamsf("base.calendar.recurrence.occurrences requires a valid date_from")
	}
	dateTo, ok := parseBaseCalendarDate(kwargs["date_to"])
	if !ok || !dateTo.After(dateFrom) {
		log.Errorf("base.calendar.recurrence.occurrences requires date_to after date_from")
		return nil, orm.InvalidParamsf("base.calendar.recurrence.occurrences requires date_to after date_from")
	}

	// Plain records: delegate entirely to the target model's own search_read
	// RuntimeMethod dispatch, so ACL/RBAC row rules apply exactly as they
	// would for any other caller of that model.
	domain := []any{}
	if rawDomain, ok := kwargs["domain"].([]any); ok {
		domain = rawDomain
	}
	plainResult, err := orm.ExecuteRuntimeMethod(ctx, sysModel, "search_read", nil, nil, map[string]any{"domain": domain})
	if err != nil {
		return nil, err
	}
	records := recordMapsFromAny(plainResult)

	rules, err := loadActiveBaseCalendarRecurrenceRules(ctx, sysModel, dateTo)
	if err != nil {
		return nil, err
	}
	out := make([]orm.RecordMap, 0, len(records))
	for _, record := range records {
		record["is_recurring"] = false
		out = append(out, record)
	}
	ruleIDs := make([]int64, 0, len(rules))
	recordIDs := make([]any, 0, len(rules))
	for _, rule := range rules {
		ruleIDs = append(ruleIDs, rule.ID)
		recordIDs = append(recordIDs, rule.RecordID)
	}
	exceptionsByRule, err := loadBaseCalendarRecurrenceExceptionsByRule(ctx, ruleIDs)
	if err != nil {
		return nil, err
	}
	masters, err := loadBaseCalendarMasterRecords(ctx, sysModel, recordIDs)
	if err != nil {
		return nil, err
	}
	for _, rule := range rules {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// A caller must still be able to read the master record for its
		// occurrences to appear: the batched search_read applies the target
		// model's ACL/record rules, so an unreadable or deleted master is simply
		// absent from `masters` and its rule is skipped (never rendered). Actual
		// deletion of orphaned rules is handled by the periodic
		// `base_calendar.cleanup_orphaned_recurrences` go_handler
		// (service/cleanup_cron.go), dispatched through base-automation's
		// sys.cron bridge (see file overview's cascade deviation note).
		master, ok := masters[rule.RecordID]
		if !ok {
			continue
		}
		exceptions := exceptionsByRule[rule.ID]
		occurrenceDates := expandCalendarOccurrences(rule, dateFrom, dateTo)
		for _, occDate := range occurrenceDates {
			effectiveDate, skip := applyBaseCalendarException(occDate, exceptions)
			if skip {
				continue
			}
			occurrence := cloneRecordMap(master)
			occurrence["id"] = fmt.Sprintf("%d:%s", rule.ID, occDate.Format("2006-01-02"))
			occurrence["is_recurring"] = true
			occurrence["recurrence_id"] = rule.ID
			occurrence["master_record_id"] = rule.RecordID
			occurrence["occurrence_date"] = effectiveDate.UTC().Unix()
			out = append(out, occurrence)
		}
	}
	return map[string]any{"records": out}, nil
}

// rescheduleOccurrenceBaseCalendarRecurrence implements SRS 6.2's three
// drag-and-drop scopes for a recurring occurrence: "this" writes a per-date
// exception (override or skip), "following" ends the current rule the day
// before the dragged occurrence and creates a new rule from the new date
// carrying the same pattern/end, "all" shifts the rule's own start_date (and
// end_date, if set) by the same delta applied to every future occurrence.
func rescheduleOccurrenceBaseCalendarRecurrence(ctx context.Context, model *orm.ModelMetadata, ids []string, _ []any, kwargs map[string]any) (any, error) {
	if len(ids) == 0 {
		log.Errorf("base.calendar.recurrence.reschedule_occurrence requires a recurrence id")
		return nil, orm.InvalidParamsf("base.calendar.recurrence.reschedule_occurrence requires a recurrence id")
	}
	originalDate, ok := parseBaseCalendarDate(kwargs["original_occurrence_date"])
	if !ok {
		log.Errorf("base.calendar.recurrence.reschedule_occurrence requires a valid original_occurrence_date")
		return nil, orm.InvalidParamsf("base.calendar.recurrence.reschedule_occurrence requires a valid original_occurrence_date")
	}
	newDatetime, ok := parseBaseCalendarDateTime(kwargs["new_datetime"])
	if !ok {
		log.Errorf("base.calendar.recurrence.reschedule_occurrence requires a valid new_datetime")
		return nil, orm.InvalidParamsf("base.calendar.recurrence.reschedule_occurrence requires a valid new_datetime")
	}
	scope := strings.TrimSpace(stringValue(kwargs["scope"]))
	// The rule is loaded through the repository (no record rules), so check the
	// caller may write this rule first: every scope edits the series or adds to it.
	if err := parentRecordAccess(ctx, BaseCalendarRecurrenceModelName, "write", ids[:1]); err != nil {
		return nil, err
	}
	rule, err := loadBaseCalendarRecurrenceRuleByID(ctx, ids[0])
	if err != nil {
		return nil, err
	}
	if rule == nil {
		log.Errorf("base.calendar.recurrence.reschedule_occurrence: recurrence %s not found", ids[0])
		return nil, orm.NotFoundf("base.calendar.recurrence.reschedule_occurrence: recurrence %s not found", ids[0])
	}
	if err := validateBaseCalendarRecurrenceTarget(ctx, orm.RecordMap{"sys_model": rule.SysModel, "record_id": rule.RecordID}); err != nil {
		return nil, err
	}

	switch scope {
	case "this":
		return rescheduleBaseCalendarSingleOccurrence(ctx, model, rule, originalDate, newDatetime)
	case "following":
		return rescheduleBaseCalendarFollowingOccurrences(ctx, model, rule, originalDate, newDatetime)
	case "all":
		return rescheduleBaseCalendarAllOccurrences(ctx, model, rule, originalDate, newDatetime)
	default:
		log.Errorf("base.calendar.recurrence.reschedule_occurrence: invalid scope %q", scope)
		return nil, orm.InvalidParamsf("base.calendar.recurrence.reschedule_occurrence: invalid scope %q, must be this|following|all", scope)
	}
}

// rescheduleBaseCalendarSingleOccurrence writes/updates one exception row
// (override_date) for the original occurrence date, leaving the master rule
// untouched (SRS 6.2 scope "this").
func rescheduleBaseCalendarSingleOccurrence(ctx context.Context, _ *orm.ModelMetadata, rule *BaseCalendarRecurrenceRecord, originalDate, newDatetime time.Time) (any, error) {
	existing, err := loadBaseCalendarRecurrenceExceptionByDate(ctx, rule.ID, originalDate)
	if err != nil {
		return nil, err
	}
	values := orm.RecordMap{
		"recurrence_id":            rule.ID,
		"original_occurrence_date": originalDate.Format("2006-01-02"),
		"override_date":            newDatetime.Format(time.RFC3339),
		"is_skipped":               false,
	}
	if existing != nil {
		exceptionRef := orm.RuntimeModelRef(BaseCalendarRecurrenceExceptionModelName, nil)
		if _, err := orm.RuntimeRepositoryWrite(ctx, exceptionRef, []string{strconv.FormatInt(existing.ID, 10)}, values); err != nil {
			return nil, err
		}
		return map[string]any{"exception_id": existing.ID}, nil
	}
	created, err := orm.RuntimeRepositoryCreate(ctx, orm.RuntimeModelRef(BaseCalendarRecurrenceExceptionModelName, nil), values)
	if err != nil {
		return nil, err
	}
	return map[string]any{"exception_id": created["id"]}, nil
}

// rescheduleBaseCalendarFollowingOccurrences ends the current rule the day
// before the dragged occurrence and creates a new rule starting at the new
// date, carrying the same pattern/end (SRS 6.2 scope "following").
func rescheduleBaseCalendarFollowingOccurrences(ctx context.Context, model *orm.ModelMetadata, rule *BaseCalendarRecurrenceRecord, originalDate, newDatetime time.Time) (any, error) {
	recurrenceRef := orm.RuntimeModelRef(BaseCalendarRecurrenceModelName, model)
	cutoff := originalDate.AddDate(0, 0, -1)
	// Ending the old rule before it starts would leave an empty series, and a
	// new rule starting after the inherited end_date would never occur: refuse
	// both instead of writing a rule that ends before it begins.
	if daysBetween(rule.StartDate, cutoff) < 0 {
		return nil, orm.InvalidParamsf("base.calendar.recurrence.reschedule_occurrence: cannot reschedule the first occurrence with scope following, use scope all")
	}
	if rule.EndDate != nil && daysBetween(newDatetime, *rule.EndDate) < 0 {
		return nil, orm.InvalidParamsf("base.calendar.recurrence.reschedule_occurrence: new date is after the series end_date")
	}
	if _, err := orm.RuntimeRepositoryWrite(ctx, recurrenceRef, []string{strconv.FormatInt(rule.ID, 10)}, orm.RecordMap{
		"end_date": cutoff.Format("2006-01-02"),
	}); err != nil {
		return nil, err
	}
	newRuleValues := orm.RecordMap{
		"sys_model":  rule.SysModel,
		"record_id":  rule.RecordID,
		"frequency":  rule.Frequency,
		"interval":   rule.Interval,
		"by_weekday": rule.ByWeekday,
		"start_date": newDatetime.Format("2006-01-02"),
	}
	if rule.ByMonthday != nil {
		newRuleValues["by_monthday"] = *rule.ByMonthday
	}
	if rule.EndDate != nil {
		newRuleValues["end_date"] = rule.EndDate.Format("2006-01-02")
	}
	// The split-off rule keeps the series' company, not none.
	if rule.CompanyID != nil {
		newRuleValues["company_id"] = *rule.CompanyID
	}
	created, err := orm.RuntimeRepositoryCreate(ctx, recurrenceRef, newRuleValues)
	if err != nil {
		return nil, err
	}
	return map[string]any{"recurrence_id": created["id"]}, nil
}

// rescheduleBaseCalendarAllOccurrences shifts the rule's own start_date (and
// end_date, if set) by the delta between the dragged occurrence's original
// and new date, applying to every occurrence rather than persisting one
// exception per instance (SRS 6.2 scope "all").
func rescheduleBaseCalendarAllOccurrences(ctx context.Context, model *orm.ModelMetadata, rule *BaseCalendarRecurrenceRecord, originalDate, newDatetime time.Time) (any, error) {
	deltaDays := daysBetween(originalDate, newDatetime)
	values := orm.RecordMap{
		"start_date": rule.StartDate.AddDate(0, 0, deltaDays).Format("2006-01-02"),
	}
	if rule.EndDate != nil {
		values["end_date"] = rule.EndDate.AddDate(0, 0, deltaDays).Format("2006-01-02")
	}
	recurrenceRef := orm.RuntimeModelRef(BaseCalendarRecurrenceModelName, model)
	if _, err := orm.RuntimeRepositoryWrite(ctx, recurrenceRef, []string{strconv.FormatInt(rule.ID, 10)}, values); err != nil {
		return nil, err
	}
	return map[string]any{"recurrence_id": rule.ID}, nil
}

// clampCalendarMonthDay returns monthStart's given day-of-month, clamped to
// the last real day of that month (e.g. day=31 in February -> Feb 28/29).
func clampCalendarMonthDay(monthStart time.Time, day int) time.Time {
	lastDay := time.Date(monthStart.Year(), monthStart.Month()+1, 0, 0, 0, 0, 0, monthStart.Location()).Day()
	if day > lastDay {
		day = lastDay
	}
	if day < 1 {
		day = 1
	}
	return time.Date(monthStart.Year(), monthStart.Month(), day, monthStart.Hour(), monthStart.Minute(), monthStart.Second(), 0, monthStart.Location())
}

// applyBaseCalendarException resolves one occurrence date against its
// loaded exceptions: skipped occurrences are dropped, overridden occurrences
// use the exception's date/time (SRS-F-006 step 4).
func applyBaseCalendarException(occDate time.Time, exceptions []BaseCalendarRecurrenceExceptionRecord) (effective time.Time, skip bool) {
	for _, exception := range exceptions {
		if !exception.OriginalOccurrenceDate.Equal(occDate.Truncate(24 * time.Hour)) {
			continue
		}
		if exception.IsSkipped {
			return occDate, true
		}
		if exception.OverrideDate != nil {
			return *exception.OverrideDate, false
		}
	}
	return occDate, false
}

// parseBaseCalendarDate parses a date value from an RPC payload or a DB row:
// a time.Time (what pgx returns for date/timestamp columns), an RFC3339
// datetime or a bare "2006-01-02" date. Going through stringValue here once
// turned every time.Time into "" so stored rules lost their start/end dates.
func parseBaseCalendarDate(raw any) (time.Time, bool) {
	return anyconv.Time(raw)
}

// parseBaseCalendarDateTime parses a full datetime value, defaulting to
// midnight UTC when only a bare date is supplied.
func parseBaseCalendarDateTime(raw any) (time.Time, bool) {
	return parseBaseCalendarDate(raw)
}

// recordMapsFromAny normalizes the heterogeneous return shapes of
// ExecuteRuntimeMethod (search_read/read) into a plain []orm.RecordMap.
func recordMapsFromAny(value any) []orm.RecordMap {
	switch typed := value.(type) {
	case []orm.RecordMap:
		return typed
	case orm.RecordMap:
		return []orm.RecordMap{typed}
	case []map[string]any:
		out := make([]orm.RecordMap, 0, len(typed))
		for _, row := range typed {
			out = append(out, orm.RecordMap(row))
		}
		return out
	case map[string]any:
		return []orm.RecordMap{orm.RecordMap(typed)}
	case []any:
		out := make([]orm.RecordMap, 0, len(typed))
		for _, item := range typed {
			if row, ok := item.(orm.RecordMap); ok {
				out = append(out, row)
			} else if row, ok := item.(map[string]any); ok {
				out = append(out, orm.RecordMap(row))
			}
		}
		return out
	default:
		return nil
	}
}

// loadActiveBaseCalendarRecurrenceRules loads the recurrence rules of sysModel
// that can produce an occurrence before dateTo (start_date < dateTo), using the
// RuntimeRepositorySearchRead helper so the same ACL checks as any other model
// call on base.calendar.recurrence apply. Rules already ended before the window
// are cheap to drop in expandCalendarOccurrences, so only the start bound is
// pushed into the domain (end_date is nullable).
func loadActiveBaseCalendarRecurrenceRules(ctx context.Context, sysModel string, dateTo time.Time) ([]*BaseCalendarRecurrenceRecord, error) {
	domain, err := orm.ParseDomain([]any{
		[]any{"sys_model", "=", sysModel},
		[]any{"start_date", "<", dateTo.UTC().Format("2006-01-02")},
	})
	if err != nil {
		return nil, err
	}
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(BaseCalendarRecurrenceModelName, nil), orm.RecordMap{"__domain": domain}, nil)
	if err != nil {
		return nil, err
	}
	out := make([]*BaseCalendarRecurrenceRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, baseCalendarRecurrenceRecordFromMap(row))
	}
	return out, nil
}

// loadBaseCalendarMasterRecords reads all master records of the rules in one
// search_read (id IN ...) on the target model, keyed by id. It replaces a
// per-rule access check + read (N+1); the target model's own ACL/record rules
// still decide which masters the caller may see.
func loadBaseCalendarMasterRecords(ctx context.Context, sysModel string, recordIDs []any) (map[int64]orm.RecordMap, error) {
	masters := make(map[int64]orm.RecordMap, len(recordIDs))
	if len(recordIDs) == 0 {
		return masters, nil
	}
	result, err := orm.ExecuteRuntimeMethod(ctx, sysModel, "search_read", nil, nil, map[string]any{
		"domain": []any{[]any{"id", "in", recordIDs}},
	})
	if err != nil {
		return nil, err
	}
	for _, row := range recordMapsFromAny(result) {
		if id, ok := anyconv.Many2OneID(row["id"]); ok {
			masters[id] = row
		}
	}
	return masters, nil
}

// loadBaseCalendarRecurrenceRuleByID loads one recurrence rule by id.
func loadBaseCalendarRecurrenceRuleByID(ctx context.Context, id string) (*BaseCalendarRecurrenceRecord, error) {
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(BaseCalendarRecurrenceModelName, nil), []string{id}, nil)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return baseCalendarRecurrenceRecordFromMap(rows[0]), nil
}

// baseCalendarRecurrenceRecordFromMap projects a generic RecordMap row into
// the typed struct used by the expansion/reschedule helpers above.
func baseCalendarRecurrenceRecordFromMap(row orm.RecordMap) *BaseCalendarRecurrenceRecord {
	record := &BaseCalendarRecurrenceRecord{
		SysModel:  stringValue(row["sys_model"]),
		Frequency: stringValue(row["frequency"]),
	}
	record.ID, _ = int64FromAny(row["id"])
	record.RecordID, _ = anyconv.Many2OneID(row["record_id"])
	record.Interval = 1
	if interval, ok := int64FromAny(row["interval"]); ok && interval > 0 {
		record.Interval = int(interval)
	}
	if monthday, ok := int64FromAny(row["by_monthday"]); ok {
		day := int(monthday)
		record.ByMonthday = &day
	}
	if startDate, ok := parseBaseCalendarDate(row["start_date"]); ok {
		record.StartDate = startDate
	}
	if endDate, ok := parseBaseCalendarDate(row["end_date"]); ok {
		record.EndDate = &endDate
	}
	rawCompany := row["company_id"]
	if ptr, ok := rawCompany.(*int64); ok && ptr != nil {
		rawCompany = *ptr
	}
	if companyID, ok := anyconv.Many2OneID(rawCompany); ok {
		record.CompanyID = &companyID
	}
	record.ByWeekday = int64SliceToInt(row["by_weekday"])
	return record
}

// hasRecordMapKey reports whether key is present in values (distinct from a
// present-but-nil value, e.g. an explicit `{"end_date": null}` clear).
func hasRecordMapKey(values orm.RecordMap, key string) bool {
	_, ok := values[key]
	return ok
}

// int64SliceToInt normalizes a jsonb-decoded weekday array (typically
// []any of float64/json.Number after JSON round-trip) into []int.
func int64SliceToInt(raw any) []int {
	values, ok := raw.([]any)
	if !ok {
		if intValues, ok := raw.([]int); ok {
			return intValues
		}
		return nil
	}
	out := make([]int, 0, len(values))
	for _, value := range values {
		if n, ok := int64FromAny(value); ok {
			out = append(out, int(n))
		}
	}
	return out
}
