package mcp

// File overview:
// - Responsibility: the bus.* MCP tool catalog -- read-only record inspection
//   of the durable base.service.bus event log, plus pinned wrappers for its
//   publish/poll/recent/last_id/get_default_channels runtime methods.
// - Key symbols: Tools, busAllowedModels.
// - Record create/write/unlink are deliberately not exposed: base.service.bus
//   is an append-only event log whose only legitimate write path is
//   bus.publish (channel fan-out + MQTT/pg_notify side effects), and mutating
//   or deleting past events directly would corrupt that audit trail.

import (
	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	busmodels "git.thebase.vn/base_hub/modules/base-bus/models"
)

// busAllowedModels is the fixed set of runtime models the bus.record.* tools
// may target.
var busAllowedModels = map[string]bool{
	busmodels.ServiceBusModelName: true,
}

// channelsArgSchema is shared by bus.publish/bus.poll/bus.recent: a single
// channel name, or an array of channel names/segments (mirrors
// NormalizeServiceBusChannels' accepted shapes).
var channelsArgSchema = map[string]any{
	"description": "A channel name, or an array of channel names/segments to normalize into channel names.",
	"oneOf": []map[string]any{
		{"type": "string"},
		{"type": "array"},
	},
}

// Tools is the bus.* MCPToolDescriptor catalog, registered with base-mcp via
// Register (provider.go) so it's appended to tools/list only when the
// base-bus module is installed for the caller's tenant DB.
func Tools() []kernelmodules.MCPToolDescriptor {
	recordModelProperty := map[string]any{"type": "string", "enum": []string{busmodels.ServiceBusModelName}}

	return []kernelmodules.MCPToolDescriptor{
		{
			Name:        "bus.record.search_read",
			Description: "Search and read base.service.bus event log records (base.group_system only), subject to ACL/record-rule/company scope. Same behavior as base.record.search_read, restricted to base.service.bus. Prefer bus.recent for a simple recent-events lookup.",
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
			Name:        "bus.record.read",
			Description: "Read specific base.service.bus event log records by id (base.group_system only). Same behavior as base.record.read, restricted to base.service.bus.",
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
			Name:        "bus.publish",
			Description: "Publish one event to the durable service bus on one or more channels (base.group_system only), notifying any live WebSocket/MQTT subscribers immediately and persisting a base.service.bus row per channel.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"channels":   channelsArgSchema,
					"event_type": map[string]any{"type": "string"},
					"payload":    map[string]any{"type": "object"},
				},
				"required": []string{"channels", "event_type", "payload"},
			},
		},
		{
			Name:        "bus.poll",
			Description: "Poll for service-bus events newer than last_id on the given channels (plus this caller's default channels); channels the caller may not receive are dropped. Returns an empty array when nothing new is available yet.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"channels": channelsArgSchema,
					"last_id":  map[string]any{"type": "integer", "description": "Only events with id greater than this are returned."},
				},
			},
		},
		{
			Name:        "bus.recent",
			Description: "Return the most recent service-bus events on the given channels (plus this caller's default channels), newest first, up to limit (default 50).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"channels": channelsArgSchema,
					"limit":    map[string]any{"type": "integer", "default": 50},
				},
			},
		},
		{
			Name:        "bus.last_id",
			Description: "Return the highest base.service.bus row id currently stored, useful as a starting cursor for bus.poll.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "bus.get_default_channels",
			Description: "Return the default service-bus channels for the calling user (the global channel, plus their per-user channel).",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}
