package mcp

// File overview:
// - Responsibility: dispatches one calendar.* MCP tool call (record CRUD,
//   plus the pinned occurrences/reschedule_occurrence wrappers).
// - Key symbols: Dispatch.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"git.thebase.vn/base_hub/internal/mcpkit"
	"git.thebase.vn/base_hub/internal/orm"
	calendarmodels "git.thebase.vn/base_hub/modules/base-calendar/models"
)

// Dispatch routes one calendar.* MCP tool call. Registered as this
// provider's Dispatch func (see provider.go) -- base-mcp's provider cache
// already confirmed the base-calendar module is installed for the caller's
// tenant DB before ever reaching here.
func Dispatch(ctx context.Context, identity *orm.RBACIdentity, toolName string, arguments map[string]any) (any, error) {
	switch toolName {
	case "calendar.record.search_read":
		return calendarRecordDispatch(ctx, identity, arguments, "search_read", mcpkit.RecordModeSearch)
	case "calendar.record.read":
		return calendarRecordDispatch(ctx, identity, arguments, "read", mcpkit.RecordModeRead)
	case "calendar.record.create":
		return calendarRecordDispatch(ctx, identity, arguments, "create", mcpkit.RecordModeCreate)
	case "calendar.record.write":
		return calendarRecordDispatch(ctx, identity, arguments, "write", mcpkit.RecordModeWrite)
	case "calendar.record.unlink":
		return calendarRecordDispatch(ctx, identity, arguments, "unlink", mcpkit.RecordModeUnlink)
	case "calendar.occurrences":
		return mcpCalendarOccurrences(ctx, identity, arguments)
	case "calendar.reschedule_occurrence":
		return mcpCalendarRescheduleOccurrence(ctx, identity, arguments)
	default:
		return nil, mcpkit.NewToolError(404, "tool_not_found", fmt.Sprintf("unknown mcp tool %q", toolName))
	}
}

// calendarRecordDispatch backs calendar.record.*: validates arguments.model
// is one of the calendar.* models, then delegates to the exact same
// mcpkit.RecordDispatch base.record.* already uses.
func calendarRecordDispatch(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any, methodName string, mode mcpkit.RecordMode) (any, error) {
	if err := requireCalendarAllowedModel(arguments); err != nil {
		return nil, err
	}
	return mcpkit.RecordDispatch(ctx, identity, arguments, methodName, mode)
}

func requireCalendarAllowedModel(arguments map[string]any) error {
	modelName := strings.TrimSpace(mcpkit.StringFromAny(arguments["model"]))
	if modelName == "" {
		return mcpkit.NewToolError(422, "invalid_params", "arguments.model is required")
	}
	if !calendarAllowedModels[modelName] {
		names := make([]string, 0, len(calendarAllowedModels))
		for name := range calendarAllowedModels {
			names = append(names, name)
		}
		sort.Strings(names)
		return mcpkit.NewToolError(422, "invalid_params", fmt.Sprintf(
			"model %q is not one of the calendar.* models this tool group accepts (%s)", modelName, strings.Join(names, ", "),
		))
	}
	return nil
}

// mcpCalendarOccurrences backs calendar.occurrences: a thin, pinned-method
// wrapper over base.calendar.recurrence's "occurrences" runtime method.
func mcpCalendarOccurrences(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any) (any, error) {
	sysModel := strings.TrimSpace(mcpkit.StringFromAny(arguments["sys_model"]))
	if sysModel == "" {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.sys_model is required")
	}
	dateFrom := strings.TrimSpace(mcpkit.StringFromAny(arguments["date_from"]))
	if dateFrom == "" {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.date_from is required")
	}
	dateTo := strings.TrimSpace(mcpkit.StringFromAny(arguments["date_to"]))
	if dateTo == "" {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.date_to is required")
	}
	meta, method, err := mcpkit.LookupRuntimeMethod(calendarmodels.BaseCalendarRecurrenceModelName, "occurrences")
	if err != nil {
		return nil, err
	}
	kwargs := map[string]any{"sys_model": sysModel, "date_from": dateFrom, "date_to": dateTo}
	if domain, ok := arguments["domain"]; ok {
		kwargs["domain"] = domain
	}
	return mcpkit.ExecuteRuntimeMethod(ctx, identity, meta, method, nil, nil, kwargs)
}

// mcpCalendarRescheduleOccurrence backs calendar.reschedule_occurrence: a
// thin, pinned-method wrapper over base.calendar.recurrence's
// "reschedule_occurrence" runtime method.
func mcpCalendarRescheduleOccurrence(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any) (any, error) {
	recurrenceID, ok := mcpkit.Int64FromArgument(arguments["recurrence_id"])
	if !ok || recurrenceID <= 0 {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.recurrence_id is required")
	}
	originalDate := strings.TrimSpace(mcpkit.StringFromAny(arguments["original_occurrence_date"]))
	if originalDate == "" {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.original_occurrence_date is required")
	}
	newDatetime := strings.TrimSpace(mcpkit.StringFromAny(arguments["new_datetime"]))
	if newDatetime == "" {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.new_datetime is required")
	}
	scope := strings.TrimSpace(mcpkit.StringFromAny(arguments["scope"]))
	switch scope {
	case "this", "following", "all":
	default:
		return nil, mcpkit.NewToolError(422, "invalid_params", `arguments.scope must be "this", "following", or "all"`)
	}
	meta, method, err := mcpkit.LookupRuntimeMethod(calendarmodels.BaseCalendarRecurrenceModelName, "reschedule_occurrence")
	if err != nil {
		return nil, err
	}
	kwargs := map[string]any{
		"original_occurrence_date": originalDate,
		"new_datetime":             newDatetime,
		"scope":                    scope,
	}
	return mcpkit.ExecuteRuntimeMethod(ctx, identity, meta, method, []string{fmt.Sprint(recurrenceID)}, nil, kwargs)
}
