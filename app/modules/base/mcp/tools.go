package mcp

// File overview:
// - Responsibility: the base.* generic model/record/batch MCP tool catalog.
// - Key symbols: Tools.
// - Moved verbatim (only the descriptor type changed, to
//   kernelmodules.MCPToolDescriptor) out of
//   modules/base-mcp/routes/mcp_route.go's defaultTools(), per the
//   mcp-module-scaffold plan's Phase 5. base.batch.execute's descriptor is
//   included here too (it belongs in this module's catalog for tools/list
//   purposes) even though its actual dispatch stays intercepted earlier, in
//   mcp_route.go's dispatchMCPTool, before ever reaching this package's
//   Dispatch (see dispatch.go's file header).

import (
	kernelmodules "git.thebase.vn/base_hub/internal/modules"
)

// Tools is the base.* MCPToolDescriptor catalog, registered with base-mcp
// via Register (provider.go). Unlike every other migrated tool group, base's
// tools are always present in tools/list whenever the "base" module itself
// is installed -- which is true for every tenant DB (base is the kernel
// module every other module depends on), so in practice this catalog is
// always visible.
func Tools() []kernelmodules.MCPToolDescriptor {
	return []kernelmodules.MCPToolDescriptor{
		{
			Name:        "base.model.list",
			Description: "List registered runtime models available to the authenticated caller.",
		},
		{
			Name:        "base.model.describe",
			Description: "Return one model's metadata: fields, relations, and runtime flags.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"model": map[string]any{"type": "string"}},
				"required":   []string{"model"},
			},
		},
		{
			Name:        "base.model.methods",
			Description: "Return the registered runtime methods for a model.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"model": map[string]any{"type": "string"}},
				"required":   []string{"model"},
			},
		},
		{
			Name:        "base.model.fields_get",
			Description: "Return field metadata for a model, compatible with runtime/UI expectations.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"model": map[string]any{"type": "string"}},
				"required":   []string{"model"},
			},
		},
		{
			Name:        "base.record.search_read",
			Description: "Search and read records through the runtime ORM, subject to ACL/record-rule/company scope.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model":  map[string]any{"type": "string"},
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
			Name:        "base.record.read",
			Description: "Read specific records by id through the runtime ORM.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model":  map[string]any{"type": "string"},
					"ids":    map[string]any{"type": "array"},
					"fields": map[string]any{"type": "array"},
				},
				"required": []string{"model", "ids"},
			},
		},
		{
			Name:        "base.record.create",
			Description: "Create one record through the runtime ORM. idempotency_key is required: a stable caller-chosen string (e.g. a UUID) so a retried call after a timeout replays the original result instead of creating a duplicate record. Reusing the same key with different values still replays the first result.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model":           map[string]any{"type": "string"},
					"values":          map[string]any{"type": "object"},
					"idempotency_key": map[string]any{"type": "string", "description": "Required. A stable caller-chosen string that dedupes retried calls."},
				},
				"required": []string{"model", "values", "idempotency_key"},
			},
		},
		{
			Name:        "base.record.write",
			Description: "Update specific records by id through the runtime ORM. values can be one object applied to every id, or (for a per-record batch) an array of objects with the same length and order as ids — each ids[i] gets values[i]. A batch write runs inside one DB transaction: if any record's write fails, every write already applied earlier in the same call is rolled back.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model": map[string]any{"type": "string"},
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
			Name:        "base.record.unlink",
			Description: "Delete specific records by id through the runtime ORM. Special case for model: \"sys.model\" (deleting a custom model's catalog row): the model must already be unpublished (base.model.unpublish) or this fails — unlink never implicitly takes a live model offline. The physical table/data is left untouched by default; pass confirm_drop_table: true to also permanently DROP the table (irreversible) once the catalog row is deleted.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model":              map[string]any{"type": "string"},
					"ids":                map[string]any{"type": "array"},
					"confirm_drop_table": map[string]any{"type": "boolean", "description": "sys.model only: also DROP the physical table (irreversible) after deleting the catalog row. Requires the model to already be unpublished."},
				},
				"required": []string{"model", "ids"},
			},
		},
		{
			Name:        "base.model.create_custom",
			Description: "Create a custom model definition (sys.model catalog row), optionally with inline fields. This registers metadata only — it does not provision a live database table. Call base.model.publish afterward (once every field you need exists) to provision the physical table and register a queryable runtime model. bid is always server-generated, never caller-supplied. To rename or delete an existing custom model, use base.record.write/unlink with model: \"sys.model\" (find its id via base.record.search_read). idempotency_key is required: a stable caller-chosen string so a retried call after a timeout replays the original result instead of creating a duplicate model. name is normalized before use: every space becomes '.', and a \"tbl.\" prefix is added automatically if the name doesn't already start with it (e.g. \"invoice items\" -> \"tbl.invoice.items\"); the final normalized name must be 24 characters or fewer. The physical/published table name is the normalized name with every '.' replaced by '_' (e.g. tbl.invoice.items -> tbl_invoice_items).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":            map[string]any{"type": "string", "description": "Model name, e.g. \"invoice items\" or \"tbl.partner.address\". Spaces become '.'; a \"tbl.\" prefix is added automatically if missing. Max 24 characters after normalization."},
					"label":           map[string]any{"type": "string"},
					"description":     map[string]any{"type": "string"},
					"idempotency_key": map[string]any{"type": "string", "description": "Required. A stable caller-chosen string that dedupes retried calls."},
					"fields": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"field_name":     map[string]any{"type": "string"},
								"label":          map[string]any{"type": "string"},
								"data_type":      map[string]any{"type": "string", "description": "Defaults to \"text\". Relation kinds: many2one, one2one (publishable); one2many, many2many (metadata only — base.model.publish rejects them today)."},
								"relation_model": map[string]any{"type": "string", "description": "Target model name, required when data_type is a relation kind. Aliases: relation, comodel, related_model, target_model."},
								"relation_type":  map[string]any{"type": "string", "description": "Alternate way to set a relation data_type, e.g. \"many2one\"."},
								"many2one":       map[string]any{"type": "boolean", "description": "Shorthand for relation_type: \"many2one\"."},
								"is_required":    map[string]any{"type": "boolean"},
								"is_primary_key": map[string]any{"type": "boolean"},
							},
							"required": []string{"field_name"},
						},
					},
				},
				"required": []string{"name", "idempotency_key"},
			},
		},
		{
			Name:        "base.model.add_field",
			Description: "Add one field to an existing model (sys.model.field catalog row). Metadata only, same scope note as base.model.create_custom. bid is always server-generated, never caller-supplied. Supports relation fields via data_type many2one/one2one (publishable as a live FK column) or one2many/many2many (catalog-only today — base.model.publish rejects them); relation_model (aliases: relation, comodel, related_model, target_model) names the target model and is required for any relation data_type. Rejected while the model is already published — call base.model.unpublish first, add the field, then base.model.publish to apply it to the database. Rejected if field_name already exists on the model (case/whitespace-insensitive) — a retried call with the same field_name is a no-op error, not a duplicate. Rejected once the model already has 1000 fields (safety cap, well under Postgres's 1600-column hard limit).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model":          map[string]any{"type": "string"},
					"field_name":     map[string]any{"type": "string"},
					"label":          map[string]any{"type": "string"},
					"data_type":      map[string]any{"type": "string", "description": "Defaults to \"text\". Relation kinds: many2one, one2one (publishable); one2many, many2many (metadata only)."},
					"relation_model": map[string]any{"type": "string", "description": "Target model name, required when data_type is a relation kind. Aliases: relation, comodel, related_model, target_model."},
					"relation_type":  map[string]any{"type": "string", "description": "Alternate way to set a relation data_type, e.g. \"many2one\"."},
					"many2one":       map[string]any{"type": "boolean", "description": "Shorthand for relation_type: \"many2one\"."},
					"is_required":    map[string]any{"type": "boolean"},
					"is_primary_key": map[string]any{"type": "boolean"},
				},
				"required": []string{"model", "field_name"},
			},
		},
		{
			Name:        "base.model.delete_field",
			Description: "Delete one field from a model (sys.model.field catalog row), resolved by field_name — no id lookup needed. Only custom fields (created via base.model.add_field/create_custom) can be deleted; registry-synced fields of a Go-coded model are read-only. Rejected while the model is already published — call base.model.unpublish first, delete the field, then base.model.publish to drop the column from the database.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model":      map[string]any{"type": "string"},
					"field_name": map[string]any{"type": "string"},
				},
				"required": []string{"model", "field_name"},
			},
		},
		{
			Name:        "base.model.rename_field",
			Description: "Rename a field (new_field_name) and/or relabel it (new_label) on a model, resolved by its current field_name. Only custom fields can be renamed; registry-synced fields of a Go-coded model are read-only. Rejected while the model is already published — call base.model.unpublish first, rename the field, then base.model.publish to apply the rename to the database column.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model":          map[string]any{"type": "string"},
					"field_name":     map[string]any{"type": "string", "description": "Current field_name to rename."},
					"new_field_name": map[string]any{"type": "string"},
					"new_label":      map[string]any{"type": "string"},
				},
				"required": []string{"model", "field_name"},
			},
		},
		{
			Name:        "base.model.create_ui",
			Description: "Scaffold a default admin screen for a registered model (the `create_ui` command): creates a list ui.view and a form ui.view, both auto-generated from the model's field metadata, plus one ui.menu entry that opens \"/web/<model>\". The menu is always nested under the root \"Main\" menu (menu.main) — there is no way to pick a different parent. Also stamps sys.model.has_ui=true for the model. A thin wrapper over base.record.create for ui.view/ui.menu, not a replacement for hand-authored views — running it again for the same model creates additional views/menu entries rather than updating existing ones.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model": map[string]any{"type": "string", "description": "Registered model name, e.g. tbl.partner.address"},
					"label": map[string]any{"type": "string", "description": "Display label for the views and menu entry; defaults to the model's registered label"},
				},
				"required": []string{"model"},
			},
		},
		{
			Name:        "base.model.publish",
			Description: "Promote a metadata-only custom model (created via base.model.create_custom / base.model.add_field) into a live, DB-backed, queryable runtime model: provisions the physical Postgres table from its sys.model.field catalog rows and registers it so base.record.* and base.model.create_ui work against it. Requires a superadmin identity. Supported field data_type values: text, integer, float, boolean, date, datetime, json, many2one, one2one (relation kinds need relation_model set) — any other value, including one2many/many2many, fails the publish. Not persisted across a server restart yet (re-publish after a restart); use base.model.unpublish to take it offline again. To add, delete, or rename a field after publishing, unpublish first (base.model.add_field/delete_field/rename_field all refuse to run on a published model), make the change, then publish again — republishing reconciles the physical table (ADD/RENAME/DROP COLUMN as needed) from the updated catalog rows, so a field delete does drop the column and its data. To delete the model entirely, unpublish it first, then base.record.unlink on sys.model (unlink refuses to run while still published); pass confirm_drop_table: true on that unlink call to also permanently drop the physical table, otherwise the table/data is left untouched.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model": map[string]any{"type": "string", "description": "Custom model name to publish, e.g. tbl.account"},
				},
				"required": []string{"model"},
			},
		},
		{
			Name:        "base.model.unpublish",
			Description: "Take a published custom model back out of the live runtime registry (base.record.* stops seeing it) without deleting its sys.model catalog row or touching its physical table/data. Call base.model.publish again later to re-register it from the same field catalog. Requires a superadmin identity. This is also a required first step before deleting the model: base.record.unlink on sys.model refuses to run while the model is still published.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model": map[string]any{"type": "string", "description": "Published custom model name to unpublish, e.g. tbl.account"},
				},
				"required": []string{"model"},
			},
		},
		{
			Name:        "base.record.call_method",
			Description: "Invoke an allowed public runtime method on a model, subject to the same ACL enforcement.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"model":  map[string]any{"type": "string"},
					"method": map[string]any{"type": "string"},
					"ids":    map[string]any{"type": "array"},
					"args":   map[string]any{"type": "array"},
					"kwargs": map[string]any{"type": "object"},
				},
				"required": []string{"model", "method"},
			},
		},
		{
			Name:        "base.batch.execute",
			Description: "Run an ordered list of tool calls (each {name, arguments}, same shape as a normal tools/call) inside one database transaction. If any step fails, every step already applied earlier in the same batch is rolled back — use this for multi-step flows that must all succeed together (e.g. base.model.create_custom, then base.model.add_field, then base.model.publish; or several base.record.create/write calls that belong to one logical change), instead of issuing them as separate tools/call round-trips where a mid-sequence failure leaves partial state. Steps run in order; base.batch.execute cannot be nested inside itself. Each step is subject to the exact same ACL/record-rule enforcement as calling it standalone. idempotency_key is required: reuse the same key when retrying a batch, and the replay returns the first batch result instead of applying the steps again.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"idempotency_key": map[string]any{"type": "string", "description": "Required. A stable caller-chosen string that dedupes retried batches; a replay returns the first batch result."},
					"steps": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"name":      map[string]any{"type": "string", "description": "Tool name, e.g. \"base.record.create\"."},
								"arguments": map[string]any{"type": "object", "description": "Arguments for this step, same shape as a standalone tools/call."},
							},
							"required": []string{"name"},
						},
					},
				},
				"required": []string{"steps", "idempotency_key"},
			},
		},
	}
}
