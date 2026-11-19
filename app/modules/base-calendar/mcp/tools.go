package mcp

// File overview:
// - Responsibility: the calendar.* MCP tool catalog -- record CRUD over
//   base.calendar.recurrence/base.calendar.recurrence.exception, plus pinned
//   wrappers for base.calendar.recurrence's occurrences/reschedule_occurrence
//   runtime methods.
// - Key symbols: Tools, calendarAllowedModels.

import (
	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	calendarmodels "git.thebase.vn/base_hub/modules/base-calendar/models"
)

// calendarAllowedModels is the fixed set of runtime models the
// calendar.record.* tools may target.
var calendarAllowedModels = map[string]bool{
	calendarmodels.BaseCalendarRecurrenceModelName:          true,
	calendarmodels.BaseCalendarRecurrenceExceptionModelName: true,
}

// Tools is the calendar.* MCPToolDescriptor catalog, registered with
// base-mcp via Register (provider.go) so it's appended to tools/list only
// when the base-calendar module is installed for the caller's tenant DB.
func Tools() []kernelmodules.MCPToolDescriptor {
	calendarModelEnum := []string{
		calendarmodels.BaseCalendarRecurrenceModelName,
		calendarmodels.BaseCalendarRecurrenceExceptionModelName,
	}
	recordModelProperty := map[string]any{"type": "string", "enum": calendarModelEnum}

	return []kernelmodules.MCPToolDescriptor{
		{
			Name:        "calendar.record.search_read",
			Description: "Search and read calendar module records (base.calendar.recurrence, base.calendar.recurrence.exception), subject to ACL/record-rule/company scope. Same behavior as base.record.search_read, restricted to calendar.* models. To render an actual calendar view for a date range, prefer calendar.occurrences.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model":  recordModelProperty,
					"domain": map[string]any{"type": "array"},
					"fields": map[string]any{"type": "array"},
					"limit":  map[string]any{"type": "integer", "default": 20},
					"offset": map[string]any{"type": "integer"},
					"order":  map[string]any{"type": "string"},
				},
				"required": []string{"model"},
			},
		},
		{
			Name:        "calendar.record.read",
			Description: "Read specific calendar module records by id. Same behavior as base.record.read, restricted to calendar.* models.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model":  recordModelProperty,
					"ids":    map[string]any{"type": "array"},
					"fields": map[string]any{"type": "array"},
				},
				"required": []string{"model", "ids"},
			},
		},
		{
			Name:        "calendar.record.create",
			Description: "Create one calendar module record (typically base.calendar.recurrence: sys_model, record_id, frequency, interval, by_weekday/by_monthday, start_date, end_date). Same behavior as base.record.create, restricted to calendar.* models. idempotency_key is required: a stable caller-chosen string so a retried call after a timeout replays the original result instead of creating a duplicate record.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model":           recordModelProperty,
					"values":          map[string]any{"type": "object"},
					"idempotency_key": map[string]any{"type": "string", "description": "Required. A stable caller-chosen string that dedupes retried calls."},
				},
				"required": []string{"model", "values", "idempotency_key"},
			},
		},
		{
			Name:        "calendar.record.write",
			Description: "Update specific calendar module records by id. Same behavior as base.record.write (including the per-id batch values array form), restricted to calendar.* models. To move/reschedule a specific occurrence of a recurring rule, prefer calendar.reschedule_occurrence.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model": recordModelProperty,
					"ids":   map[string]any{"type": "array"},
					"values": map[string]any{
						"description": "One object applied to every id, or an array of objects (one per id, same order) for a per-record batch write.",
						"oneOf": []map[string]any{
							{"type": "object"},
							{"type": "array", "items": map[string]any{"type": "object"}},
						},
					},
				},
				"required": []string{"model", "ids", "values"},
			},
		},
		{
			Name:        "calendar.record.unlink",
			Description: "Delete specific calendar module records by id. Same behavior as base.record.unlink, restricted to calendar.* models.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model": recordModelProperty,
					"ids":   map[string]any{"type": "array"},
				},
				"required": []string{"model", "ids"},
			},
		},
		{
			Name:        "calendar.occurrences",
			Description: "Return plain records for [date_from, date_to) on sys_model, merged with expanded virtual occurrences from that model's active recurrence rules. Each returned record carries is_recurring, and recurring ones also carry recurrence_id/master_record_id/occurrence_date.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"sys_model": map[string]any{"type": "string", "description": "The target model name whose calendar-visible records/occurrences to return."},
					"date_from": map[string]any{"type": "string", "description": "RFC3339 datetime or YYYY-MM-DD date, inclusive."},
					"date_to":   map[string]any{"type": "string", "description": "RFC3339 datetime or YYYY-MM-DD date, exclusive; must be after date_from."},
					"domain":    map[string]any{"type": "array", "description": "Optional search domain applied to the plain (non-recurring) records on sys_model."},
				},
				"required": []string{"sys_model", "date_from", "date_to"},
			},
		},
		{
			Name:        "calendar.reschedule_occurrence",
			Description: "Reschedule one occurrence of a recurring rule (drag-and-drop style). scope=\"this\" moves only this one occurrence (writes an exception); scope=\"following\" ends the current rule the day before this occurrence and starts a new rule from the new date; scope=\"all\" shifts the whole rule's start_date (and end_date) by the same delta.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"recurrence_id":            map[string]any{"type": "integer", "description": "The base.calendar.recurrence id."},
					"original_occurrence_date": map[string]any{"type": "string", "description": "RFC3339 datetime or YYYY-MM-DD date identifying which occurrence is being moved."},
					"new_datetime":             map[string]any{"type": "string", "description": "RFC3339 datetime or YYYY-MM-DD date this occurrence moves to."},
					"scope":                    map[string]any{"type": "string", "enum": []string{"this", "following", "all"}},
				},
				"required": []string{"recurrence_id", "original_occurrence_date", "new_datetime", "scope"},
			},
		},
	}
}
