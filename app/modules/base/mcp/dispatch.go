package mcp

// File overview:
// - Responsibility: dispatches one base.* generic model/record MCP tool
//   call -- model introspection, generic record CRUD, and the
//   custom-model-definition wrapper tools (create_custom/add_field/
//   delete_field/rename_field/create_ui/publish/unpublish).
// - Key symbols: Dispatch.
// - Moved verbatim out of modules/base-mcp/routes/mcp_route.go's
//   dispatchMCPTool switch body, per the mcp-module-scaffold plan's Phase 5.
//   Every helper now calls internal/mcpkit directly (RecordDispatch/
//   ExecuteRuntimeMethod/ResolveModel/ResolveRuntimeMethod/etc.) instead of
//   this package's former private helpers of the same name, so ACL/
//   record-rule/company-scope enforcement stays byte-for-byte identical.
// - base.batch.execute is intentionally NOT handled in this Dispatch: its
//   orchestration wrapper stays in modules/base-mcp/routes/mcp_route.go
//   (mcpBatchExecute) since it needs the transport's own recursive
//   tools/call re-entry (mcpLookupToolProvider), not base-specific logic --
//   mcp_route.go's dispatchMCPTool intercepts that tool name before ever
//   calling this provider's Dispatch, so the "unknown tool" branch below is
//   defensive only and should never actually be reached for that name.

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/mcpkit"
	"git.thebase.vn/base_hub/internal/models"
	"git.thebase.vn/base_hub/internal/orm"
)

// toolHandler runs one base.* tool call.
type toolHandler func(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any) (any, error)

// recordTool pins a base.record.*/fields_get tool to its mcpkit.RecordDispatch method and mode.
func recordTool(method string, mode mcpkit.RecordMode) toolHandler {
	return func(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any) (any, error) {
		return mcpkit.RecordDispatch(ctx, identity, arguments, method, mode)
	}
}

// toolHandlers is the single dispatch table. base.batch.execute is deliberately
// absent: mcp_route.go intercepts it before reaching this provider.
var toolHandlers = map[string]toolHandler{
	"base.model.list": func(context.Context, *orm.RBACIdentity, map[string]any) (any, error) {
		return mcpModelList(), nil
	},
	"base.model.describe": func(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any) (any, error) {
		return mcpRequireModel(ctx, identity, arguments, "read")
	},
	"base.model.methods": func(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any) (any, error) {
		meta, err := mcpRequireModel(ctx, identity, arguments, "read")
		if err != nil {
			return nil, err
		}
		return meta.RuntimeMethods, nil
	},
	"base.model.fields_get":    recordTool("fields_get", mcpkit.RecordModeModelOnly),
	"base.record.search_read":  recordTool("search_read", mcpkit.RecordModeSearch),
	"base.record.read":         recordTool("read", mcpkit.RecordModeRead),
	"base.record.create":       recordTool("create", mcpkit.RecordModeCreate),
	"base.record.write":        recordTool("write", mcpkit.RecordModeWrite),
	"base.record.unlink":       recordTool("unlink", mcpkit.RecordModeUnlink),
	"base.record.call_method":  mcpRecordCallMethod,
	"base.model.create_custom": mcpModelCreateCustom,
	"base.model.add_field":     mcpModelAddField,
	"base.model.delete_field":  mcpModelDeleteField,
	"base.model.rename_field":  mcpModelRenameField,
	"base.model.create_ui":     mcpModelCreateUI,
	"base.model.publish":       mcpModelPublish,
	"base.model.unpublish":     mcpModelUnpublish,
}

// Dispatch routes one base.* MCP tool call. Registered as this provider's
// Dispatch func (see provider.go).
func Dispatch(ctx context.Context, identity *orm.RBACIdentity, toolName string, arguments map[string]any) (any, error) {
	handler, ok := toolHandlers[toolName]
	if !ok {
		return nil, mcpkit.NewToolError(404, "tool_not_found", fmt.Sprintf("unknown mcp tool %q", toolName))
	}
	return handler(ctx, identity, arguments)
}

// mcpModelList backs base.model.list (SRS-F-004): any newly registered
// runtime model must appear here without adding MCP-specific code for it.
func mcpModelList() []map[string]any {
	regModels := orm.RegisteredModels()
	out := make([]map[string]any, 0, len(regModels))
	for _, meta := range regModels {
		out = append(out, map[string]any{
			"name":         meta.Name,
			"label":        meta.Label,
			"description":  meta.Description,
			"storage_name": meta.StorageName,
		})
	}
	return out
}

// mcpRequireModel resolves the target model and enforces the same model ACL
// used by runtime dispatch before returning introspection metadata
// (SRS-F-004 error condition: caller lacks read permission on metadata model).
func mcpRequireModel(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any, operation string) (*orm.ModelMetadata, error) {
	modelName := strings.TrimSpace(mcpkit.StringFromAny(arguments["model"]))
	if modelName == "" {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.model is required")
	}
	meta, err := mcpkit.ResolveModel(ctx, identity, modelName)
	if err != nil {
		return nil, err
	}
	requestCtx := mcpkit.RequestContext(ctx, identity)
	if err := orm.EnsureDefaultModelAccess(requestCtx, orm.EnvironmentFromContext(requestCtx), meta, operation, nil, nil); err != nil {
		return nil, mcpkit.NewToolError(403, "permission_denied", err.Error())
	}
	return meta, nil
}

// mcpRecordCallMethod backs base.record.call_method: an explicit escape hatch
// for invoking any allowed public runtime method by name, still going through
// the same ACL/record-rule enforcement as every other generic record tool.
func mcpRecordCallMethod(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any) (any, error) {
	methodName := strings.TrimSpace(mcpkit.StringFromAny(arguments["method"]))
	if methodName == "" {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.method is required")
	}
	modelName := strings.TrimSpace(mcpkit.StringFromAny(arguments["model"]))
	if modelName == "" {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.model is required")
	}
	meta, method, err := mcpkit.ResolveRuntimeMethod(ctx, identity, modelName, methodName)
	if err != nil {
		return nil, err
	}
	if !method.Public {
		return nil, mcpkit.NewToolError(403, "invalid_operation", fmt.Sprintf("runtime method %q is not callable", methodName))
	}

	ids, err := mcpkit.RequireIDsArg(arguments)
	if err != nil {
		return nil, err
	}
	if method.UsesPathID && len(ids) == 0 {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.ids is required for this method")
	}
	args, err := mcpkit.RequireArrayArg(arguments, "args")
	if err != nil {
		return nil, err
	}
	kwargs, err := mcpkit.RequireObjectArg(arguments, "kwargs")
	if err != nil {
		return nil, err
	}
	return mcpkit.ExecuteRuntimeMethod(ctx, identity, meta, method, ids, args, kwargs)
}

// mcpCustomModelNamePrefix is auto-prepended to any base.model.create_custom
// name that doesn't already start with it, so an AI caller doesn't need to
// already know this repo's dotted-namespace convention — every built-in
// model already lives under a prefix like this one (tbl./sys./ui.).
const mcpCustomModelNamePrefix = "tbl."

// mcpCustomModelNameMaxLen caps a base.model.create_custom name's final
// length (after space normalization and the tbl. prefix are applied, since
// both can lengthen what the caller typed) — canonicalModelTableName
// (internal/orm/model_schema.go) turns the name into its Postgres table
// name 1:1 by swapping '.' for '_' with no separate truncation step, and
// Postgres identifiers are capped at 63 bytes, so keeping the logical model
// name itself short leaves headroom for that table name plus any suffixes
// the runtime adds elsewhere (indexes, constraints).
const mcpCustomModelNameMaxLen = 24

// mcpNormalizeCustomModelName turns a caller-supplied base.model.create_custom
// name into its canonical form: every space character becomes a '.' (an AI
// caller thinking in words, e.g. "invoice items", gets "invoice.items"
// instead of a name with a raw space in it), then mcpCustomModelNamePrefix is
// prepended if the name doesn't already start with it. Returns an
// invalid_params error if the final name exceeds mcpCustomModelNameMaxLen —
// the cap applies to what actually gets stored, not the raw input, since
// both normalization steps above can only make the name longer.
func mcpNormalizeCustomModelName(name string) (string, error) {
	normalized := strings.ReplaceAll(name, " ", ".")
	if !strings.HasPrefix(normalized, mcpCustomModelNamePrefix) {
		normalized = mcpCustomModelNamePrefix + normalized
	}
	if len(normalized) > mcpCustomModelNameMaxLen {
		return "", mcpkit.NewToolError(422, "invalid_params", fmt.Sprintf(
			"model name %q is %d characters after normalization (spaces become '.', %q prefix added if missing); max is %d",
			normalized, len(normalized), mcpCustomModelNamePrefix, mcpCustomModelNameMaxLen,
		))
	}
	return normalized, nil
}

// mcpModelCreateCustom backs base.model.create_custom: a thin, ergonomic
// wrapper over the existing sys.model + sys.model.field record-CRUD pipeline
// (internal/models/sys_model.go, sys_model_field.go). It exists because that
// pipeline already lets an AI define a custom model end to end through the
// generic base.record.create tool, but only if the caller already knows to
// use two separate models and hand-generates a unique bid (the marker
// that makes a sys.model/sys.model.field row caller-owned instead of a
// read-only registry-sync snapshot — see guardSysModelRowsCustom). This tool
// removes that friction: one call creates the model row and, optionally, its
// field rows, with bid always server-generated — the caller never
// supplies one, eliminating any risk of a mistyped or colliding value.
//
// This only writes metadata catalog rows (sys_model/sys_model_field), the
// same as calling base.record.create directly — it does not provision a live
// Postgres table or register a queryable runtime model; there is no such
// pipeline in this codebase today.
func mcpModelCreateCustom(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any) (any, error) {
	name := strings.TrimSpace(mcpkit.StringFromAny(arguments["name"]))
	if name == "" {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.name is required")
	}
	name, err := mcpNormalizeCustomModelName(name)
	if err != nil {
		return nil, err
	}
	if _, exists := orm.LookupModel(name); exists {
		return nil, mcpkit.NewToolError(409, "model_exists", fmt.Sprintf("model %q already exists", name))
	}

	label := strings.TrimSpace(mcpkit.StringFromAny(arguments["label"]))
	if label == "" {
		label = name
	}
	bid := models.GenerateBID(name)

	meta, method, err := mcpkit.LookupRuntimeMethod(models.SysModelModelName, "create")
	if err != nil {
		return nil, err
	}
	modelValues := map[string]any{
		"bid":         bid,
		"name":        name,
		"label":       label,
		"description": strings.TrimSpace(mcpkit.StringFromAny(arguments["description"])),
	}
	modelRecord, err := mcpkit.ExecuteRuntimeMethod(ctx, identity, meta, method, nil, []any{modelValues}, nil)
	if err != nil {
		return nil, err
	}

	fieldDefs, _ := arguments["fields"].([]any)

	// Unlike orm.BaseModel's id/active/created_at/updated_at, a "name" field is
	// not implicit anywhere in the runtime model struct — buildDynamicStructType
	// (internal/orm/model_publish.go) only ever sees rows that exist in
	// sys_model_field. Without this, a caller who creates a custom model with
	// no fields (or forgets a "name" column) gets a model that can never be
	// published: "orm: model has no publishable fields beyond the standard
	// id/active/created_at/updated_at columns". Auto-add a default text "name"
	// field whenever the caller didn't already declare one, so a fresh custom
	// model is publishable out of the box like the built-in models are.
	hasNameField := false
	for _, raw := range fieldDefs {
		if fieldMap, ok := raw.(map[string]any); ok {
			if strings.EqualFold(strings.TrimSpace(mcpkit.StringFromAny(fieldMap["field_name"])), "name") {
				hasNameField = true
				break
			}
		}
	}
	if !hasNameField {
		fieldDefs = append([]any{map[string]any{
			"field_name":  "name",
			"label":       "Name",
			"data_type":   "text",
			"is_required": true,
		}}, fieldDefs...)
	}

	createdFields := make([]any, 0, len(fieldDefs))
	for _, raw := range fieldDefs {
		fieldMap, ok := raw.(map[string]any)
		if !ok {
			return nil, mcpkit.NewToolError(422, "invalid_params", "each entry in arguments.fields must be an object")
		}
		fieldRecord, err := mcpCreateCustomField(ctx, identity, name, fieldMap)
		if err != nil {
			return nil, err
		}
		createdFields = append(createdFields, fieldRecord)
	}

	return map[string]any{"model": modelRecord, "fields": createdFields}, nil
}

// mcpModelAddField backs base.model.add_field: the single-field counterpart
// to mcpModelCreateCustom's inline "fields" list, for adding a field to a
// model after it already exists. Refuses to run against an already-published
// model (mcpGuardModelNotPublished) — see that helper's doc comment for why
// field mutations and live-schema DDL are kept as separate, explicit steps.
func mcpModelAddField(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any) (any, error) {
	modelName := strings.TrimSpace(mcpkit.StringFromAny(arguments["model"]))
	if modelName == "" {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.model is required")
	}
	if err := mcpGuardModelNotPublished(modelName, "add a field to"); err != nil {
		return nil, err
	}
	return mcpCreateCustomField(ctx, identity, modelName, arguments)
}

// mcpModelDeleteField backs base.model.delete_field: resolves the
// sys.model.field row for (model, field_name) and deletes it through the
// standard unlink runtime method, so the existing bid-custody guard
// (guardSysModelFieldRowsCustom — read-only registry-synced rows stay
// protected) and audit logging apply exactly as they do for any other
// sys.model.field unlink. Refuses to run against an already-published model,
// same as mcpModelAddField.
func mcpModelDeleteField(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any) (any, error) {
	modelName := strings.TrimSpace(mcpkit.StringFromAny(arguments["model"]))
	if modelName == "" {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.model is required")
	}
	fieldName := strings.TrimSpace(mcpkit.StringFromAny(arguments["field_name"]))
	if fieldName == "" {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.field_name is required")
	}
	if err := mcpGuardModelNotPublished(modelName, "delete a field from"); err != nil {
		return nil, err
	}

	fieldID, err := mcpFindFieldRowID(ctx, identity, modelName, fieldName)
	if err != nil {
		return nil, err
	}
	meta, method, err := mcpkit.LookupRuntimeMethod(models.SysModelFieldModelName, "unlink")
	if err != nil {
		return nil, err
	}
	if _, err := mcpkit.ExecuteRuntimeMethod(ctx, identity, meta, method, []string{fieldID}, nil, nil); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true, "model": modelName, "field_name": fieldName}, nil
}

// mcpModelRenameField backs base.model.rename_field: resolves the
// sys.model.field row for (model, field_name) and writes new_field_name
// (and/or label) through the standard write runtime method, reusing the same
// bid-custody guard and audit logging as any other sys.model.field write.
// Refuses to run against an already-published model, same as
// mcpModelAddField — see mcpGuardModelNotPublished.
func mcpModelRenameField(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any) (any, error) {
	modelName := strings.TrimSpace(mcpkit.StringFromAny(arguments["model"]))
	if modelName == "" {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.model is required")
	}
	fieldName := strings.TrimSpace(mcpkit.StringFromAny(arguments["field_name"]))
	if fieldName == "" {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.field_name is required")
	}
	newFieldName := strings.TrimSpace(mcpkit.StringFromAny(arguments["new_field_name"]))
	newLabel := strings.TrimSpace(mcpkit.StringFromAny(arguments["new_label"]))
	if newFieldName == "" && newLabel == "" {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.new_field_name or arguments.new_label is required")
	}
	if err := mcpGuardModelNotPublished(modelName, "rename a field on"); err != nil {
		return nil, err
	}

	fieldID, err := mcpFindFieldRowID(ctx, identity, modelName, fieldName)
	if err != nil {
		return nil, err
	}
	values := map[string]any{}
	if newFieldName != "" {
		values["field_name"] = newFieldName
	}
	if newLabel != "" {
		values["label"] = newLabel
	}
	meta, method, err := mcpkit.LookupRuntimeMethod(models.SysModelFieldModelName, "write")
	if err != nil {
		return nil, err
	}
	return mcpkit.ExecuteRuntimeMethod(ctx, identity, meta, method, []string{fieldID}, []any{values}, nil)
}

// mcpGuardModelNotPublished rejects a sys.model.field mutation while
// modelName is currently a live, registered runtime model. PublishModel
// synthesizes its Go struct type once, at publish time, from the
// sys.model.field catalog snapshot; editing that snapshot afterward does not
// retroactively touch the already-registered type or its physical table
// columns. Rather than let the catalog and the live schema silently drift,
// base.model.add_field/delete_field/rename_field require the same
// unpublish-first discipline this codebase already enforces for deleting a
// published model's sys.model row (unlinkSysModel) — call base.model.unpublish,
// make the field change, then base.model.publish to re-synthesize the type
// and reconcile the physical table (ADD/RENAME/DROP COLUMN as needed) via the
// same AutoMigrateRegisteredModels sweep PublishModel already runs.
func mcpGuardModelNotPublished(modelName, verb string) error {
	if _, exists := orm.LookupModel(modelName); exists {
		return mcpkit.NewToolError(409, "model_published", fmt.Sprintf(
			"cannot %s %q while it is published; call base.model.unpublish first, make the field change, then base.model.publish to apply it to the database",
			verb, modelName,
		))
	}
	return nil
}

// mcpFindFieldRowID resolves the sys.model.field row id for (modelName,
// fieldName) via the standard search_read runtime method — the same
// ACL/record-rule boundary every other MCP tool goes through
// (mcpkit.ExecuteRuntimeMethod) — so base.model.delete_field/rename_field
// never need the caller to already know the row's numeric id.
func mcpFindFieldRowID(ctx context.Context, identity *orm.RBACIdentity, modelName, fieldName string) (string, error) {
	meta, method, err := mcpkit.LookupRuntimeMethod(models.SysModelFieldModelName, "search_read")
	if err != nil {
		return "", err
	}
	kwargs := map[string]any{
		"domain": []any{
			[]any{"model_name", "=", modelName},
			[]any{"field_name", "=", fieldName},
		},
		"fields": []any{"id", "bid"},
		"limit":  1,
	}
	result, err := mcpkit.ExecuteRuntimeMethod(ctx, identity, meta, method, nil, nil, kwargs)
	if err != nil {
		return "", err
	}
	rows, _ := result.([]orm.RecordMap)
	if len(rows) == 0 {
		return "", mcpkit.NewToolError(404, "field_not_found", fmt.Sprintf("field %q not found on model %q", fieldName, modelName))
	}
	id, ok := mcpkit.Int64FromArgument(rows[0]["id"])
	if !ok {
		return "", mcpkit.NewToolError(500, "invalid_operation", "resolved field row has no numeric id")
	}
	return strconv.FormatInt(id, 10), nil
}

// mcpModelCreateUI backs base.model.create_ui (the `create_ui` scaffold
// command): given a registered model name, it creates a list ui.view, a form
// ui.view, and one ui.menu entry that navigates to "/web/<model>" — the same
// route_path pattern this repo's own seed menus use, so no ui.actions.window
// row is needed for the menu to resolve default views by model+type. Arch
// bodies reuse models.SynthesizeViewArch, the exact fallback generator
// resolveUIView already falls back to when no stored view exists, so a
// freshly scaffolded screen renders identically to what the model would
// already show with zero views — this tool just persists that baseline as
// real, editable rows. The new menu is always parented under menu.main
// (models.LookupMenuMainID) — there is deliberately no caller-supplied
// parent_id — so this tool can never produce an orphan top-level menu or
// nest one somewhere unexpected. Runs through mcpkit.ExecuteRuntimeMethod
// (not models.CreateUIForModel) for the view/menu creates so the caller's
// identity still gets the normal ACL check; only the menu.main lookup and
// the has_ui stamp are shared with models.CreateUIForModel, the sys.model
// "Create UI" object button's equivalent internal-admin path.
func mcpModelCreateUI(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any) (any, error) {
	modelName := strings.TrimSpace(mcpkit.StringFromAny(arguments["model"]))
	if modelName == "" {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.model is required")
	}
	targetMeta, err := mcpkit.ResolveModel(ctx, identity, modelName)
	if err != nil {
		return nil, err
	}

	label := strings.TrimSpace(mcpkit.StringFromAny(arguments["label"]))
	if label == "" {
		label = models.ViewLabelForModel(targetMeta)
	}

	viewMeta, viewMethod, err := mcpkit.LookupRuntimeMethod(models.UIViewModelName, "create")
	if err != nil {
		return nil, err
	}
	listView, err := mcpCreateUIView(ctx, identity, viewMeta, viewMethod, targetMeta, modelName, "list")
	if err != nil {
		return nil, err
	}
	formView, err := mcpCreateUIView(ctx, identity, viewMeta, viewMethod, targetMeta, modelName, "form")
	if err != nil {
		return nil, err
	}

	// LookupMenuMainID/MarkSysModelHasUI read/write through the tenant DB
	// attached to identity, same as every mcpkit.ExecuteRuntimeMethod call
	// above — the raw incoming ctx has no environment attached yet (that only
	// happens inside mcpkit.ExecuteRuntimeMethod/mcpkit.RequestContext), so
	// using it directly here would resolve against the wrong (default)
	// database in a multi-tenant deployment.
	requestCtx := mcpkit.RequestContext(ctx, identity)
	mainMenuID, err := models.LookupMenuMainID(requestCtx)
	if err != nil {
		return nil, mcpkit.NewToolError(500, "internal_error", err.Error())
	}
	if mainMenuID <= 0 {
		return nil, mcpkit.NewToolError(424, "invalid_state", "main menu (menu.main) is not registered; cannot scaffold ui.menu")
	}

	menuMeta, menuMethod, err := mcpkit.LookupRuntimeMethod(models.UIMenuModelName, "create")
	if err != nil {
		return nil, err
	}
	menuValues := map[string]any{
		"name":       label,
		"route_path": "/web/" + modelName,
		"parent_id":  mainMenuID,
	}
	menuRecord, err := mcpkit.ExecuteRuntimeMethod(ctx, identity, menuMeta, menuMethod, nil, []any{menuValues}, nil)
	if err != nil {
		return nil, err
	}

	if err := models.MarkSysModelHasUI(requestCtx, modelName); err != nil {
		return nil, mcpkit.NewToolError(500, "internal_error", err.Error())
	}

	return map[string]any{"menu": menuRecord, "views": []any{listView, formView}}, nil
}

// mcpModelPublish backs base.model.publish: promotes a metadata-only model
// (created via base.model.create_custom + base.model.add_field, the same
// sys.model/sys.model.field catalog rows the admin frontend's sys.model
// create screen writes) into a live, DB-backed, queryable runtime model —
// closing the gap mcpModelCreateCustom's own doc comment calls out. This
// calls the exact same orm.PublishModel the admin-only /rpc_meta/model/publish
// RPC route calls, so both transports share one implementation. Table/schema
// provisioning is real DDL, so this is gated to superadmin identities only,
// unlike the metadata-only create_custom/add_field tools.
func mcpModelPublish(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any) (any, error) {
	if identity == nil || !identity.IsSuperadmin {
		return nil, mcpkit.NewToolError(403, "permission_denied", "base.model.publish requires a superadmin identity")
	}
	name := strings.TrimSpace(mcpkit.StringFromAny(arguments["model"]))
	if name == "" {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.model is required")
	}
	meta, err := orm.PublishModel(mcpkit.RequestContext(ctx, identity), name)
	if err != nil {
		return nil, mcpkit.NewToolError(400, "invalid_operation", err.Error())
	}
	return map[string]any{"model": meta}, nil
}

// mcpModelUnpublish backs base.model.unpublish: the rollback counterpart to
// base.model.publish. Takes a published custom model back out of the live
// runtime registry (so base.record.* stops seeing it) without touching its
// sys.model catalog row or its physical table/data — calling
// base.model.publish again re-registers it from the same field catalog.
// Deleting the model entirely (base.record.unlink on sys.model) requires the
// model to already be unpublished — this tool is also the required first step
// before that delete, not just a way to take a model offline while keeping
// its definition. Gated to superadmin identities, same as publish.
func mcpModelUnpublish(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any) (any, error) {
	if identity == nil || !identity.IsSuperadmin {
		return nil, mcpkit.NewToolError(403, "permission_denied", "base.model.unpublish requires a superadmin identity")
	}
	name := strings.TrimSpace(mcpkit.StringFromAny(arguments["model"]))
	if name == "" {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.model is required")
	}
	if err := orm.UnpublishModel(mcpkit.RequestContext(ctx, identity), name); err != nil {
		return nil, mcpkit.NewToolError(400, "invalid_operation", err.Error())
	}
	return map[string]any{"unpublished": true, "model": name}, nil
}

// mcpCreateUIView creates one ui.view row of viewType for modelName, using
// models.SynthesizeViewArch to generate its arch. Shared by both the list and
// form leg of mcpModelCreateUI.
func mcpCreateUIView(ctx context.Context, identity *orm.RBACIdentity, viewMeta *orm.ModelMetadata, method orm.RuntimeMethod, targetMeta *orm.ModelMetadata, modelName, viewType string) (any, error) {
	values := map[string]any{
		"name":  fmt.Sprintf("%s.%s", modelName, viewType),
		"model": modelName,
		"type":  viewType,
		"arch":  models.SynthesizeViewArch(targetMeta, viewType),
	}
	return mcpkit.ExecuteRuntimeMethod(ctx, identity, viewMeta, method, nil, []any{values}, nil)
}

// mcpRelationModelAliasKeys lists every input key accepted as the "which
// model does this relation point to" argument. Different MCP callers reach
// for different vocabulary ("comodel", Django-style
// "related_model", generic "target_model", or the canonical "relation_model"
// this file persists) — accepting all of them removes a class of tool-call
// retries where the model guessed the wrong key name. First non-empty match
// wins, checked in this fixed order for determinism.
var mcpRelationModelAliasKeys = []string{"relation_model", "relation", "comodel", "related_model", "target_model"}

// mcpRelationDataTypes are the data_type values buildDynamicStructType
// recognizes as relation kinds (see internal/orm/model_publish.go). Kept
// separate from that package's own set since this package cannot import
// unexported orm identifiers — this only needs to know which values require
// a non-empty relation_model, not how each is provisioned.
var mcpRelationDataTypes = map[string]bool{
	"many2one": true, "one2one": true, "one2many": true, "many2many": true,
}

// mcpMaxFieldsPerModel caps how many sys.model.field rows a single custom
// model may accumulate. Nothing in this codebase gives an end user a way to
// add fields directly (the only creation path is this AI-facing MCP tool
// pair), so there is no legitimate design here that needs anywhere near this
// many; the cap exists purely to fail fast — with a clear error naming the
// model — on a caller that retries base.model.add_field/create_custom
// per-event or per-retry with varying field names, instead of silently
// piling up sys.model.field rows that only surface as a bare Postgres
// SQLSTATE 54011 later, at publish time, once the synthesized struct
// (buildDynamicStructType) finally hits the real table-creation guard in
// internal/orm/model_registry_migrate.go.
const mcpMaxFieldsPerModel = 1000

// mcpListFieldNamesForModel returns every live sys.model.field field_name
// currently recorded for modelName, used by mcpCreateCustomField to dedupe
// a repeated field_name (case/whitespace-insensitively) and to cap the
// per-model field count before insert — see mcpMaxFieldsPerModel.
func mcpListFieldNamesForModel(ctx context.Context, identity *orm.RBACIdentity, modelName string) ([]string, error) {
	meta, method, err := mcpkit.LookupRuntimeMethod(models.SysModelFieldModelName, "search_read")
	if err != nil {
		return nil, err
	}
	kwargs := map[string]any{
		"domain": []any{[]any{"model_name", "=", modelName}},
		"fields": []any{"field_name"},
		"limit":  mcpMaxFieldsPerModel + 1,
	}
	result, err := mcpkit.ExecuteRuntimeMethod(ctx, identity, meta, method, nil, nil, kwargs)
	if err != nil {
		return nil, err
	}
	rows, _ := result.([]orm.RecordMap)
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, mcpkit.StringFromAny(row["field_name"]))
	}
	return names, nil
}

// mcpCreateCustomField creates one sys.model.field row for modelName. bid
// is always server-generated — the caller never supplies one. Shared by
// mcpModelCreateCustom's inline "fields" list and mcpModelAddField.
func mcpCreateCustomField(ctx context.Context, identity *orm.RBACIdentity, modelName string, fieldMap map[string]any) (any, error) {
	// Hard invariant, independent of mcpGuardModelNotPublished's registry-
	// membership check: a system model (sys.model, sys.model.field, and every
	// other statically Go-registered model — see ModelDefinitionFromRecord)
	// must never receive a sys.model.field row at all. This is what let a
	// looped/misdirected base.model.add_field call against "sys.model" itself
	// grow that table's live column count into the thousands in production —
	// see .agents/review/2026-09-02-sys-model-1600-column-prod-incident.md.
	if meta, exists := orm.LookupModel(modelName); exists && meta.Definition() != nil && meta.Definition().IsSystem {
		return nil, mcpkit.NewToolError(403, "system_model_protected", fmt.Sprintf(
			"model %q is a system model and cannot receive custom fields", modelName,
		))
	}

	fieldName := strings.TrimSpace(mcpkit.StringFromAny(fieldMap["field_name"]))
	if fieldName == "" {
		return nil, mcpkit.NewToolError(422, "invalid_params", "arguments.field_name is required")
	}

	label := strings.TrimSpace(mcpkit.StringFromAny(fieldMap["label"]))
	if label == "" {
		label = fieldName
	}

	// relation_type (or the many2one:true shorthand) selects the relation
	// kind; it wins over an explicitly-passed data_type of the same family so
	// callers can pass either "relation_type":"many2one" or
	// "data_type":"many2one" interchangeably.
	relationType := strings.ToLower(strings.TrimSpace(mcpkit.StringFromAny(fieldMap["relation_type"])))
	if many2one, _ := fieldMap["many2one"].(bool); many2one && relationType == "" {
		relationType = "many2one"
	}
	dataType := strings.ToLower(strings.TrimSpace(mcpkit.StringFromAny(fieldMap["data_type"])))
	if relationType != "" {
		dataType = relationType
	}
	if dataType == "" {
		dataType = "text"
	}

	relationModel := ""
	for _, key := range mcpRelationModelAliasKeys {
		if v := strings.TrimSpace(mcpkit.StringFromAny(fieldMap[key])); v != "" {
			relationModel = v
			break
		}
	}
	if mcpRelationDataTypes[dataType] && relationModel == "" {
		return nil, mcpkit.NewToolError(422, "invalid_params", fmt.Sprintf(
			"data_type %q requires a relation target model (arguments.relation_model, or an alias: relation, comodel, related_model, target_model)", dataType,
		))
	}

	// Argument validation above is pure and DB-free; only now — right before
	// the actual write — pay for the search_read this dedupe/cap check needs.
	existingNames, err := mcpListFieldNamesForModel(ctx, identity, modelName)
	if err != nil {
		return nil, err
	}
	// Case/whitespace-insensitive dedupe: the DB's unique index on
	// (model_name, field_name) only rejects an exact, same-case collision, so
	// a caller retrying with e.g. "Email" after "email" would otherwise add a
	// second, effectively-duplicate row instead of getting a clear error.
	for _, existing := range existingNames {
		if strings.EqualFold(strings.TrimSpace(existing), fieldName) {
			return nil, mcpkit.NewToolError(409, "field_exists", fmt.Sprintf(
				"field %q already exists on model %q", fieldName, modelName,
			))
		}
	}
	if len(existingNames) >= mcpMaxFieldsPerModel {
		return nil, mcpkit.NewToolError(422, "too_many_fields", fmt.Sprintf(
			"model %q already has %d fields, at the %d-field safety cap — remove unused fields before adding more",
			modelName, len(existingNames), mcpMaxFieldsPerModel,
		))
	}

	bid := models.GenerateBID(modelName)
	isRequired, _ := fieldMap["is_required"].(bool)
	isPrimaryKey, _ := fieldMap["is_primary_key"].(bool)

	meta, method, err := mcpkit.LookupRuntimeMethod(models.SysModelFieldModelName, "create")
	if err != nil {
		return nil, err
	}
	values := map[string]any{
		"bid":            bid,
		"model_name":     modelName,
		"field_name":     fieldName,
		"label":          label,
		"data_type":      dataType,
		"relation_model": relationModel,
		"is_required":    isRequired,
		"is_primary_key": isPrimaryKey,
	}
	return mcpkit.ExecuteRuntimeMethod(ctx, identity, meta, method, nil, []any{values}, nil)
}
