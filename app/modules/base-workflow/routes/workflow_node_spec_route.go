package routes

// File overview:
// - Responsibility: exposes the workflow canvas node-spec HTTP endpoint so the frontend can discover trigger and action configuration metadata.
// - Key symbols: `HandleWorkflowNodeSpecs`, `buildWorkflowNodeSpecsResponse`.
// - Pattern: Keeps workflow-specific HTTP transport ownership inside the workflow module while reusing shared RPC auth and DB helpers from internal routes.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/modelruntime"
	"git.thebase.vn/base_hub/internal/orm"
	credentialmodels "git.thebase.vn/base_hub/modules/base-credential/models"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
	// The node files register the builtin definitions this endpoint serves.
	_ "git.thebase.vn/base_hub/modules/base-workflow/service/nodes"
	"github.com/gofiber/fiber/v3"
	"github.com/uptrace/bun"
)

var (
	workflowRouteBuildNodeSpecs  = buildWorkflowNodeSpecsResponse
	workflowRouteSearchSysModels = searchWorkflowSysModels
	workflowRouteLookupSysModel  = lookupWorkflowSysModelTemplate
)

type workflowSysModelSummary struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

type workflowSysModelTemplate struct {
	Name         string         `json:"name"`
	Label        string         `json:"label"`
	JSONTemplate map[string]any `json:"json_template"`
}

// HandleWorkflowNodeSpecs returns the workflow canvas trigger and action metadata.
func HandleWorkflowNodeSpecs(c fiber.Ctx) error {
	ctx, err := workflowRouteDBContext(c)
	if err != nil {
		return err
	}
	identity, err := workflowRequireIdentity(c, false)
	if err != nil {
		return err
	}
	ctx = workflowRouteContext(ctx, identity)
	return c.JSON(fiber.Map{"result": workflowRouteBuildNodeSpecs(ctx)})
}

// buildWorkflowNodeSpecsResponse sources the action list from the live
// base.workflow.action.catalog table (not just the ~10 hardcoded builtins) so any
// custom or duplicated catalog row still gets a matching canvas param spec, keyed by
// its resolved handler_key rather than requiring an exact builtin code match. Falls
// back to the static builtin list if the catalog query fails (e.g. DB unavailable)
// so the canvas still has something to render.
func buildWorkflowNodeSpecsResponse(ctx context.Context) workflowmodels.NodeSpecResponse {
	response := workflowmodels.NodeSpecResponse{
		Triggers: []workflowmodels.TriggerNodeSpec{
			{
				Type:  "webhook",
				Label: "Webhook",
				Params: []workflowmodels.NodeParamSpec{
					{
						Name:    "topic",
						Label:   "Webhook Path",
						Type:    "string",
						Help:    "Derived from the workflow topic and exposed at /wh/<topic>.",
						Default: "/wh/<topic>",
					},
					{
						Name:  "group_id",
						Label: "Access Group",
						Type:  "many2one",
						Help:  "Optional tbl.groups access gate for protected workflow execution.",
					},
				},
			},
			{
				Type:  "kafka",
				Label: "Kafka Topic",
				Params: []workflowmodels.NodeParamSpec{
					{
						Name:     "topic",
						Label:    "Kafka Topic",
						Type:     "string",
						Required: true,
						Help:     "Inbound Kafka topic that should execute this workflow.",
					},
					{
						// No default: an empty group means wf-<db>-<trigger id> (plan T54), never
						// the kernel consumer's group.
						Name:  "consumer_group",
						Label: "Consumer Group",
						Type:  "string",
						Help:  "Optional Kafka consumer group. Empty uses a group of its own (wf-<database>-<trigger id>).",
					},
					{
						Name:    "use_dedupe",
						Label:   "Use Dedupe",
						Type:    "boolean",
						Default: false,
						Help:    "Deprecated and ignored: each offset runs once because the run is named after it. Set dedupe_key on the trigger to deduplicate by a payload field.",
					},
				},
			},
			{
				Type:  "mqtt",
				Label: "MQTT Topic",
				Params: []workflowmodels.NodeParamSpec{
					{
						Name:     "topic",
						Label:    "MQTT Topic",
						Type:     "string",
						Required: true,
						Help:     "MQTT topic or wildcard pattern that should execute this workflow.",
					},
					{
						Name:    "qos",
						Label:   "QoS",
						Type:    "select",
						Default: "1",
						Options: []workflowmodels.NodeParamOption{
							{Value: "1", Label: "1"},
						},
						Help: "MQTT delivery quality of service; fixed at 1 (at-least-once) for every subscription.",
					},
				},
			},
			{
				Type:  "tcp",
				Label: "TCP",
				Params: []workflowmodels.NodeParamSpec{
					{
						Name:    "port",
						Label:   "Port",
						Type:    "number",
						Default: 10123,
						Help:    "Listen port for this trigger (one trigger per port). Must be inside the server's tcp.portMin-tcp.portMax range; requires tcp.useTcp.",
					},
					{
						Name:    "framing",
						Label:   "Framing",
						Type:    "select",
						Default: "len2_bcd",
						Options: []workflowmodels.NodeParamOption{
							{Value: "len2_bcd", Label: "2-byte BCD length"},
							{Value: "len2_bin", Label: "2-byte binary length"},
							{Value: "stx_etx", Label: "STX ... ETX"},
							{Value: "len4_ascii", Label: "4-digit ASCII length"},
						},
						Help: "How one message is delimited on the stream. The body is passed to the workflow untouched.",
					},
					{
						Name:    "len_includes_header",
						Label:   "Length Includes Header",
						Type:    "boolean",
						Default: false,
						Help:    "Set when the sender counts the length header bytes in the length value.",
					},
					{
						Name:    "tls_mode",
						Label:   "TLS",
						Type:    "select",
						Default: "none",
						Options: []workflowmodels.NodeParamOption{
							{Value: "none", Label: "None (plain TCP)"},
							{Value: "tls", Label: "TLS"},
							{Value: "mtls", Label: "Mutual TLS (client certificate)"},
						},
						Help: "TLS and mutual TLS use the certificate files from the server config (tcp.tlsCertFile, tcp.tlsKeyFile, tcp.tlsClientCaFile).",
					},
					{
						Name:    "reply_timeout_ms",
						Label:   "Reply Timeout (ms)",
						Type:    "number",
						Default: 30000,
						Help:    "Time the workflow has to produce the reply; on timeout the connection is closed without a reply.",
					},
				},
			},
			{
				Type:  "model",
				Label: "Model Event",
				Params: []workflowmodels.NodeParamSpec{
					{
						Name:     "model",
						Label:    "Model",
						Type:     "combobox",
						Required: true,
						Help:     "Runtime model to watch for create/write/unlink events. Options are sourced from GET /wf/sys-models.",
					},
					{
						Name:  "trigger_domain",
						Label: "Trigger Domain",
						Type:  "domain",
						Help:  "Optional domain condition that gates the whole workflow: evaluated on every create/write/unlink event for the selected model, independent of the Operation below. Leave empty to skip this gate.",
					},
					{
						Name:     "operation",
						Label:    "Operation",
						Type:     "select",
						Required: true,
						Default:  "after_create",
						Options: []workflowmodels.NodeParamOption{
							{Value: "before_create", Label: "Before Create"},
							{Value: "after_create", Label: "After Create"},
							{Value: "before_write", Label: "Before Update"},
							{Value: "after_write", Label: "After Update"},
							{Value: "before_unlink", Label: "Before Delete"},
							{Value: "after_unlink", Label: "After Delete"},
						},
						Help: "Record-lifecycle event that fires this workflow. \"before_*\" evaluates the domain against the pre-image (values before the write); \"after_*\" against the post-image (persisted record). Always fires after the write has committed — a model trigger can filter an event, never block it.",
					},
					{
						Name:  "domain",
						Label: "Domain",
						Type:  "domain",
						Help:  "Optional domain condition (same format as list-view filters) matched against the record snapshot for the selected Operation only. The workflow only runs when the domain matches — leave empty to fire on every occurrence of the selected operation.",
					},
				},
			},
		},
	}

	response.Actions = withDynamicActionParams(ctx, workflowCatalogActionSpecs(ctx))

	return response
}

// workflowCatalogActionSpecs reads every active base.workflow.action.catalog row and
// builds one ActionNodeSpec per row, keyed by the row's own Code (so it matches
// base.workflow.action.action_code for the frontend's actionSpecMap lookup) but with
// category/params taken from the node definition its HandlerKey@HandlerVersion names — so a
// custom or duplicated catalog entry that reuses a builtin handler (e.g. a "Webhook" row with
// handler_key "post_output_object") still gets Target URL / Headers / Authorization fields on the
// canvas instead of silently rendering none. parse_payload's own spec is still
// included here (the frontend's actionSpecMap needs it to resolve the always-prepended
// Parse Payload node) — only the drag-palette filters it out, unchanged from before.
// Falls back to the static builtin list if the catalog query fails so the canvas still
// has something to render.
func workflowCatalogActionSpecs(ctx context.Context) []workflowmodels.ActionNodeSpec {
	rows, err := modelruntime.NewService().SearchRead(ctx, workflowmodels.WorkflowActionCatalogModelName, orm.RecordMap{
		"is_active": true,
		"__order":   "id asc",
		"__limit":   500,
	}, []string{"id", "code", "name", "handler_key", "handler_version"})
	if err != nil {
		return workflowBuiltinActionSpecs()
	}

	specs := make([]workflowmodels.ActionNodeSpec, 0, len(rows))
	for _, row := range rows {
		code := strings.TrimSpace(stringValue(row["code"]))
		if code == "" {
			continue
		}
		handlerKey := strings.TrimSpace(stringValue(row["handler_key"]))
		if handlerKey == "" {
			handlerKey = code
		}
		label := strings.TrimSpace(stringValue(row["name"]))
		if label == "" {
			label = code
		}
		specs = append(specs, workflowCatalogActionSpec(code, int64RowValue(row["id"]), label, handlerKey, int(int64RowValue(row["handler_version"]))))
	}
	if len(specs) == 0 {
		return workflowBuiltinActionSpecs()
	}
	return specs
}

// workflowCatalogActionSpec is one catalog row's spec: the row's code, id and label over the
// handler's node definition. A handler with no definition renders as a param-less "Action".
func workflowCatalogActionSpec(code string, catalogID int64, label, handlerKey string, handlerVersion int) workflowmodels.ActionNodeSpec {
	spec := workflowmodels.ActionNodeSpec{Category: "Action"}
	if def, ok := workflowmodels.LookupNodeDef(handlerKey, handlerVersion); ok {
		spec = def.ActionSpec()
	}
	spec.Code = code
	spec.CatalogID = catalogID
	spec.Label = label
	return spec
}

// workflowBuiltinActionSpecs is the pre-catalog-driven fallback: the registered node
// definitions, used when the catalog table can't be read (e.g. DB unavailable) or is empty.
func workflowBuiltinActionSpecs() []workflowmodels.ActionNodeSpec {
	defs := workflowmodels.NodeDefs()
	specs := make([]workflowmodels.ActionNodeSpec, 0, len(defs))
	for _, def := range defs {
		specs = append(specs, def.ActionSpec())
	}
	return specs
}

// HandleWorkflowCredentials returns the list of named credentials available in the current
// database scope. Encrypted values are never included in the response — only name, type,
// and description are returned so the canvas param panel can populate a credential picker.
func HandleWorkflowCredentials(c fiber.Ctx) error {
	ctx, err := workflowRouteDBContext(c)
	if err != nil {
		return err
	}
	identity, err := workflowRequireIdentity(c, false)
	if err != nil {
		return err
	}
	ctx = workflowRouteContext(ctx, identity)
	// Canvas picker routes read through runtime methods that skip model ACL, so the workflow
	// role (T94) is checked here.
	if err := workflowRouteRequireRole(ctx, workflowmodels.WorkflowRoleViewer); err != nil {
		return err
	}
	rows, err := workflowRouteSearchRead(ctx, workflowmodels.WorkflowCredentialModelName, orm.RecordMap{
		"__order": "name asc",
		"__limit": 500,
	}, []string{"id", "name", "credential_type", "description", "workflow_id"})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	type credentialSummary struct {
		ID             any    `json:"id"`
		Name           string `json:"name"`
		CredentialType string `json:"credential_type"`
		Description    string `json:"description"`
		WorkflowID     any    `json:"workflow_id"`
	}
	result := make([]credentialSummary, 0, len(rows))
	for _, row := range rows {
		result = append(result, credentialSummary{
			ID:             row["id"],
			Name:           strings.TrimSpace(stringValue(row["name"])),
			CredentialType: strings.TrimSpace(stringValue(row["credential_type"])),
			Description:    strings.TrimSpace(stringValue(row["description"])),
			WorkflowID:     row["workflow_id"],
		})
	}
	return c.JSON(fiber.Map{"result": result})
}

// HandleWorkflowSysCredentials returns the list of sys.credential records available in the
// current database scope, for the canvas Authorization combobox (the `authorization` action
// param resolves the selected credential's decrypted access token at execution time via
// @syscred:<name>, see resolveCredentialRefs). Only name/type/description are requested — the
// encrypted access_token and other secret columns are never read or exposed here.
func HandleWorkflowSysCredentials(c fiber.Ctx) error {
	ctx, err := workflowRouteDBContext(c)
	if err != nil {
		return err
	}
	identity, err := workflowRequireIdentity(c, false)
	if err != nil {
		return err
	}
	ctx = workflowRouteContext(ctx, identity)
	// Canvas picker routes read through runtime methods that skip model ACL, so the workflow
	// role (T94) is checked here.
	if err := workflowRouteRequireRole(ctx, workflowmodels.WorkflowRoleViewer); err != nil {
		return err
	}
	rows, err := workflowRouteSearchRead(ctx, credentialmodels.SysCredentialModelName, orm.RecordMap{
		"__order": "name asc",
		"__limit": 500,
	}, []string{"id", "name", "credential_type", "description"})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	type sysCredentialSummary struct {
		ID             any    `json:"id"`
		Name           string `json:"name"`
		CredentialType string `json:"credential_type"`
		Description    string `json:"description"`
	}
	result := make([]sysCredentialSummary, 0, len(rows))
	for _, row := range rows {
		result = append(result, sysCredentialSummary{
			ID:             row["id"],
			Name:           strings.TrimSpace(stringValue(row["name"])),
			CredentialType: strings.TrimSpace(stringValue(row["credential_type"])),
			Description:    strings.TrimSpace(stringValue(row["description"])),
		})
	}
	return c.JSON(fiber.Map{"result": result})
}

// HandleWorkflowSysModels returns searchable sys.model options for workflow node model pickers.
func HandleWorkflowSysModels(c fiber.Ctx) error {
	ctx, err := workflowRouteDBContext(c)
	if err != nil {
		return err
	}
	identity, err := workflowRequireIdentity(c, false)
	if err != nil {
		return err
	}
	if err := workflowRouteRequireRole(workflowRouteContext(ctx, identity), workflowmodels.WorkflowRoleViewer); err != nil {
		return err
	}

	query := strings.TrimSpace(c.Query("q"))
	modelName := strings.TrimSpace(c.Query("model_name"))
	limit := fiber.Query[int](c, "limit", 25)
	if limit <= 0 {
		limit = 25
	}
	if limit > 100 {
		limit = 100
	}

	if modelName != "" {
		template, err := workflowRouteLookupSysModel(ctx, identity.UserID, modelName)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, err.Error())
		}
		return c.JSON(fiber.Map{"result": template})
	}

	options, err := workflowRouteSearchSysModels(ctx, identity.UserID, query, limit)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	return c.JSON(fiber.Map{"result": options})
}

func searchWorkflowSysModels(ctx context.Context, userID int64, query string, limit int) ([]workflowSysModelSummary, error) {
	env := orm.EnvironmentFromContext(ctx)
	if env == nil {
		return nil, fmt.Errorf("workflow sys models: runtime environment is not initialized")
	}
	sudoEnv := env.WithUser(userID, true)
	if sudoEnv == nil || sudoEnv.DB() == nil {
		return nil, fmt.Errorf("workflow sys models: database is not initialized")
	}

	rows := make([]workflowSysModelSummary, 0, limit)
	sqlQuery := strings.TrimSpace(query)
	searchTerm := "%" + sqlQuery + "%"
	db := sudoEnv.DB()
	stmt := db.NewSelect().
		Model((*workflowSysModelSummary)(nil)).
		ModelTableExpr("sys_model").
		ColumnExpr("name").
		ColumnExpr("label").
		Where("name <> ''")
	if sqlQuery != "" {
		stmt = stmt.WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.WhereOr("name ILIKE ?", searchTerm).
				WhereOr("label ILIKE ?", searchTerm)
		})
	}
	if err := stmt.
		OrderExpr("label ASC NULLS LAST, name ASC").
		Limit(limit).
		Scan(ctx, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func lookupWorkflowSysModelTemplate(ctx context.Context, userID int64, modelName string) (*workflowSysModelTemplate, error) {
	env := orm.EnvironmentFromContext(ctx)
	if env == nil {
		return nil, fmt.Errorf("workflow sys models: runtime environment is not initialized")
	}
	sudoEnv := env.WithUser(userID, true)
	if sudoEnv == nil || sudoEnv.DB() == nil {
		return nil, fmt.Errorf("workflow sys models: database is not initialized")
	}

	row := new(workflowSysModelTemplate)
	if err := sudoEnv.DB().NewSelect().
		Model(row).
		ModelTableExpr("sys_model").
		ColumnExpr("name").
		ColumnExpr("label").
		ColumnExpr("COALESCE(json_dto, '{}'::jsonb) AS json_template").
		Where("name = ?", strings.TrimSpace(modelName)).
		Limit(1).
		Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if row.JSONTemplate == nil {
		row.JSONTemplate = map[string]any{}
	}
	return row, nil
}

func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// int64RowValue converts a SearchRead row's raw "id" value (int64, float64, or numeric
// string depending on driver/JSON round-trip) to int64, returning 0 for anything else.
func int64RowValue(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	default:
		return 0
	}
}

// dynamicActionParamOptions loads the options of the action params whose choices are rows of the
// tenant (keyed by param name): stored lua automation.action rows for `automation_action_id`
// (lua_script@v1 and catalog rows reusing its handler), published workflows for
// `target_workflow_id` (call_workflow@v1, for_each@v1, plan T103), tbl.groups rows for
// `approver_group_id` (wait_approval@v1).
var dynamicActionParamOptions = map[string]func(context.Context) []workflowmodels.NodeParamOption{
	"automation_action_id":                luaActionOptions,
	"target_workflow_id":                  publishedWorkflowOptions,
	workflowmodels.WaitApprovalGroupParam: approverGroupOptions,
}

// withDynamicActionParams fills the options of every param listed in dynamicActionParamOptions, so
// the canvas offers a select of live rows. The param itself comes from the node definition; a
// failed lookup just leaves the select empty. Each list is loaded at most once per request. specs
// own their Params slices (NodeDef.ActionSpec copies), so the registered definition is never mutated.
func withDynamicActionParams(ctx context.Context, specs []workflowmodels.ActionNodeSpec) []workflowmodels.ActionNodeSpec {
	loaded := map[string][]workflowmodels.NodeParamOption{}
	for i := range specs {
		for j := range specs[i].Params {
			name := specs[i].Params[j].Name
			load, dynamic := dynamicActionParamOptions[name]
			if !dynamic {
				continue
			}
			options, done := loaded[name]
			if !done {
				options = load(ctx)
				loaded[name] = options
			}
			specs[i].Params[j].Options = options
		}
	}
	return specs
}

// publishedWorkflowOptions lists the active, non-template workflows that have a published version
// (what a sub-workflow call runs), read through the caller's ACL.
func publishedWorkflowOptions(ctx context.Context) []workflowmodels.NodeParamOption {
	rows, err := modelruntime.NewService().SearchRead(ctx, workflowmodels.FlowModelName, orm.RecordMap{
		"is_active":   true,
		"is_template": false,
		"__order":     "name asc",
		"__limit":     500,
	}, []string{"id", "name", "state", "published_version"})
	if err != nil {
		return nil
	}
	var options []workflowmodels.NodeParamOption
	for _, row := range rows {
		published := int64RowValue(row["published_version"]) > 0 || stringValue(row["state"]) == workflowmodels.WorkflowStatePublished
		if id := int64RowValue(row["id"]); id > 0 && published {
			options = append(options, workflowmodels.NodeParamOption{Value: strconv.FormatInt(id, 10), Label: stringValue(row["name"])})
		}
	}
	return options
}

// approverGroupOptions lists the tbl.groups rows the caller can read (the canvas is an author tool,
// so this is every group for base.group_system).
func approverGroupOptions(ctx context.Context) []workflowmodels.NodeParamOption {
	rows, err := modelruntime.NewService().SearchRead(ctx, "tbl.groups", orm.RecordMap{
		"__order": "name asc",
		"__limit": 500,
	}, []string{"id", "name", "full_name"})
	if err != nil {
		return nil
	}
	var options []workflowmodels.NodeParamOption
	for _, row := range rows {
		label := stringValue(row["full_name"])
		if label == "" {
			label = stringValue(row["name"])
		}
		if id := int64RowValue(row["id"]); id > 0 {
			options = append(options, workflowmodels.NodeParamOption{Value: strconv.FormatInt(id, 10), Label: label})
		}
	}
	return options
}

func luaActionOptions(ctx context.Context) []workflowmodels.NodeParamOption {
	rows, err := modelruntime.NewService().SearchRead(ctx, "automation.action", orm.RecordMap{
		"action_type": "lua",
		"__order":     "name asc",
		"__limit":     500,
	}, []string{"id", "name"})
	if err != nil {
		return nil
	}
	var options []workflowmodels.NodeParamOption
	for _, row := range rows {
		if id := int64RowValue(row["id"]); id > 0 {
			options = append(options, workflowmodels.NodeParamOption{Value: strconv.FormatInt(id, 10), Label: stringValue(row["name"])})
		}
	}
	return options
}
