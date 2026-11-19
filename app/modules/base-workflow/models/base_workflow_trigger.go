package models

// File overview:
// - Responsibility: defines the `workflow` module model contract for base workflow trigger, including field metadata, runtime methods, and module-specific invariants.
// - Key symbols: `WorkflowTriggerModel`, `createWorkflowTrigger`, `WorkflowTriggerRecord`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"context"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	mqttservice "git.thebase.vn/base_hub/internal/services/mqtt"
	"git.thebase.vn/base_hub/internal/services/tcpserver"
)

type WorkflowTriggerRecord struct {
	orm.BaseModel
	WorkflowID  *int64         `bun:"workflow_id" json:"workflow_id" meta:"kind:many2one,model:base.workflow,ondelete:CASCADE,index,label:Workflow,help:Links this trigger configuration to its parent workflow."`
	CompanyID   *int64         `bun:"company_id" json:"company_id" meta:"kind:many2one,model:tbl.company,ondelete:SET NULL,index,label:Company ID,help:Inherits the company scope from the parent workflow; not settable independently."`
	TriggerType string         `bun:"trigger_type" json:"trigger_type" meta:"index,label:Trigger Type,help:Stores the trigger protocol: webhook | kafka | mqtt | model | tcp." selection:"(webhook,Webhook),(kafka,Kafka),(mqtt,MQTT),(model,Model Event),(tcp,TCP)"`
	Topic       string         `bun:"topic" json:"topic" meta:"index,label:Topic,help:Kafka topic name, MQTT topic pattern, or the derived tcp:<port> label."`
	IsActive    bool           `bun:"is_active" json:"is_active" meta:"index,label:Is Active,help:Controls whether this trigger is registered and active."`
	Config      map[string]any `bun:"config,type:jsonb,nullzero" json:"config" meta:"kind:json,label:Config,help:Stores extra per-type trigger configuration in JSON format."`
}

const WorkflowTriggerModelName = "base.workflow.trigger"

var WorkflowTriggerDefinition = orm.ModelDefinition(WorkflowTriggerModelName, (*WorkflowTriggerRecord)(nil))

var WorkflowTriggerModelSpec = orm.ModelSpec{
	Name:        WorkflowTriggerModelName,
	Label:       "Workflow Trigger",
	Description: "Trigger configuration per workflow for webhook, Kafka, MQTT, model event, or raw TCP execution",
	Definition:  WorkflowTriggerDefinition,
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "create", Public: true, ModelOnly: true, Call: createWorkflowTrigger},
		{Name: "write", Public: true, UsesPathID: true, Call: writeWorkflowTrigger},
		{Name: "unlink", Public: true, UsesPathID: true, Call: unlinkWorkflowTrigger},
		// Live MQTT client + per-topic health for the monitor page; process-local, so each server
		// instance answers for itself. Gated only by this model's read ACL: base.group_system via this module's CSV plus the blanket
		// base.* read of base.group_user (authoring access guard restricts writes, not reads).
		{Name: "mqtt_metrics", Public: true, ModelOnly: true, ReadOnly: true, Call: mqttMetricsWorkflowTrigger},
		// Process-local liveness of each trigger (plan T99); viewer role, see trigger_health.go.
		{Name: "health", Public: true, ModelOnly: true, ReadOnly: true, Call: healthWorkflowTrigger},
	},
}

var WorkflowTriggerModel = orm.Define[WorkflowTriggerRecord](&Models, 120, WorkflowTriggerModelSpec)

type WorkflowTriggerRuntimeHooks struct {
	RegisterTrigger   func(context.Context, WorkflowTriggerRecord) error
	UnregisterTrigger func(context.Context, int64) error
	// SyncWorkflowTriggers reloads every trigger of one workflow (here and, through the trigger
	// change event, on every instance) after a change that decides whether the workflow can run:
	// publish, is_active, is_template. The registry skips triggers of a workflow that cannot run.
	SyncWorkflowTriggers func(context.Context, int64)
}

var workflowTriggerRuntimeHooks WorkflowTriggerRuntimeHooks

// RegisterWorkflowTriggerRuntimeHooks registers the workflow trigger runtime hooks handled by this package.
func RegisterWorkflowTriggerRuntimeHooks(hooks WorkflowTriggerRuntimeHooks) {
	workflowTriggerRuntimeHooks = hooks
}

// mqttMetricsWorkflowTrigger returns the kernel MQTT client snapshot (connection churn, resync,
// per-topic traffic, queue depth and run times).
func mqttMetricsWorkflowTrigger(_ context.Context, _ *orm.ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
	return mqttservice.Snapshot(), nil
}

// createWorkflowTrigger creates the workflow trigger handled by this package.
func createWorkflowTrigger(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	valuesList, err := recordMapListFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if len(valuesList) == 0 {
		log.Errorf("base.workflow.trigger.create requires values")
		return nil, orm.InvalidParamsf("base.workflow.trigger.create requires values")
	}

	created := make([]orm.RecordMap, 0, len(valuesList))
	for _, values := range valuesList {
		normalized, err := normalizeWorkflowTriggerValues(ctx, values, nil, true)
		if err != nil {
			return nil, err
		}
		if err := inheritWorkflowChildCompanyID(ctx, model, normalized); err != nil {
			return nil, err
		}
		record, err := orm.CreateRecord(ctx, orm.RuntimeModelRef(WorkflowTriggerModelName, model), normalized)
		if err != nil {
			return nil, err
		}
		if err := registerWorkflowTriggerRecord(ctx, workflowTriggerRecordFromRecord(record)); err != nil {
			return nil, err
		}
		created = append(created, record)
	}
	if len(created) == 1 {
		return created[0], nil
	}
	return created, nil
}

func writeWorkflowTrigger(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	if len(ids) == 0 {
		log.Errorf("base.workflow.trigger.write requires ids")
		return nil, orm.InvalidParamsf("base.workflow.trigger.write requires ids")
	}
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		log.Errorf("base.workflow.trigger.write requires values")
		return nil, orm.InvalidParamsf("base.workflow.trigger.write requires values")
	}

	beforeRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(WorkflowTriggerModelName, model), ids, nil)
	if err != nil {
		return nil, err
	}
	if len(beforeRows) == 0 {
		return false, nil
	}

	triggerID := int64Value(beforeRows[0]["id"])
	current := workflowTriggerStateFromRecord(beforeRows[0])
	normalized, err := normalizeWorkflowTriggerValues(ctx, values, &current, false)
	if err != nil {
		return nil, err
	}
	// company_id follows the parent workflow (plan T41).
	if err := applyWorkflowChildCompanyOnWrite(ctx, normalized); err != nil {
		return nil, err
	}
	ok, err := orm.WriteRecords(ctx, orm.RuntimeModelRef(WorkflowTriggerModelName, model), ids, normalized)
	if err != nil {
		return nil, err
	}
	if !ok {
		return ok, nil
	}

	if err := unregisterWorkflowTriggerID(ctx, triggerID); err != nil {
		return nil, err
	}
	afterRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(WorkflowTriggerModelName, model), ids[:1], nil)
	if err != nil {
		return nil, err
	}
	if len(afterRows) == 0 {
		return ok, nil
	}
	if err := registerWorkflowTriggerRecord(ctx, workflowTriggerRecordFromRecord(afterRows[0])); err != nil {
		return nil, err
	}
	return ok, nil
}

func unlinkWorkflowTrigger(ctx context.Context, model *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	if len(ids) == 0 {
		log.Errorf("base.workflow.trigger.unlink requires ids")
		return nil, orm.InvalidParamsf("base.workflow.trigger.unlink requires ids")
	}

	beforeRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(WorkflowTriggerModelName, model), ids, nil)
	if err != nil {
		return nil, err
	}
	ok, err := orm.RuntimeRepositoryUnlink(ctx, orm.RuntimeModelRef(WorkflowTriggerModelName, model), ids)
	if err != nil {
		return nil, err
	}
	if !ok {
		return ok, nil
	}
	for _, row := range beforeRows {
		if err := unregisterWorkflowTriggerID(ctx, int64Value(row["id"])); err != nil {
			return nil, err
		}
	}
	return ok, nil
}

type workflowTriggerState struct {
	WorkflowID  *int64
	TriggerType string
	Topic       string
	IsActive    bool
	Config      map[string]any
}

func workflowTriggerStateFromRecord(record orm.RecordMap) workflowTriggerState {
	config, _ := normalizeUIActionContext(record["config"])
	return workflowTriggerState{
		WorkflowID:  int64PointerFromAny(record["workflow_id"]),
		TriggerType: strings.TrimSpace(stringValue(record["trigger_type"])),
		Topic:       strings.TrimSpace(stringValue(record["topic"])),
		IsActive:    boolValue(record["is_active"]),
		Config:      config,
	}
}

func normalizeWorkflowTriggerValues(_ context.Context, values orm.RecordMap, current *workflowTriggerState, creating bool) (orm.RecordMap, error) {
	normalized := cloneRecordMap(values)

	if rawWorkflowID, exists := normalized["workflow_id"]; exists {
		ptr, ok := int64PointerValue(rawWorkflowID)
		if !ok || ptr == nil || *ptr <= 0 {
			log.Errorf("base.workflow.trigger workflow_id must be a workflow id")
			return nil, orm.InvalidParamsf("base.workflow.trigger workflow_id must be a workflow id")
		}
		normalized["workflow_id"] = ptr
	} else if current != nil && current.WorkflowID != nil {
		normalized["workflow_id"] = *current.WorkflowID
	} else {
		log.Errorf("base.workflow.trigger workflow_id is required")
		return nil, orm.InvalidParamsf("base.workflow.trigger workflow_id is required")
	}

	triggerType := currentWorkflowTriggerType(normalized, current)
	switch triggerType {
	case WorkflowTriggerTypeWebhook, WorkflowTriggerTypeKafka, WorkflowTriggerTypeMQTT, WorkflowTriggerTypeModel, WorkflowTriggerTypeTCP:
	default:
		log.Errorf("base.workflow.trigger trigger_type must be webhook, kafka, mqtt, model, or tcp")
		return nil, orm.InvalidParamsf("base.workflow.trigger trigger_type must be webhook, kafka, mqtt, model, or tcp")
	}
	normalized["trigger_type"] = triggerType

	// tcp triggers derive their topic (tcp:<port>) from config.port, like model triggers.
	// Model triggers have no webhook path / kafka topic / mqtt topic of their
	// own — the target model + operation already uniquely identifies them —
	// so topic is optional here and auto-derived below once modelConfig is
	// known, instead of forcing the caller to invent an unused label.
	if topic, ok := stringFromAny(normalized["topic"]); ok {
		topic = strings.TrimSpace(topic)
		if topic == "" && triggerType != WorkflowTriggerTypeModel && triggerType != WorkflowTriggerTypeTCP {
			log.Errorf("base.workflow.trigger topic is required")
			return nil, orm.InvalidParamsf("base.workflow.trigger topic is required")
		}
		normalized["topic"] = topic
	} else if current != nil && current.Topic != "" {
		normalized["topic"] = current.Topic
	} else if triggerType == WorkflowTriggerTypeModel || triggerType == WorkflowTriggerTypeTCP {
		normalized["topic"] = ""
	} else {
		log.Errorf("base.workflow.trigger topic is required")
		return nil, orm.InvalidParamsf("base.workflow.trigger topic is required")
	}

	if _, exists := normalized["is_active"]; !exists && creating {
		normalized["is_active"] = true
	}

	var configValues map[string]any
	if rawConfig, exists := normalized["config"]; exists {
		var err error
		configValues, err = normalizeUIActionContext(rawConfig)
		if err != nil {
			log.Errorf("base.workflow.trigger config must be an object")
			return nil, orm.InvalidParamsf("base.workflow.trigger config must be an object")
		}
	} else if current != nil {
		configValues = cloneActionParams(current.Config)
	} else {
		configValues = map[string]any{}
	}

	switch triggerType {
	case WorkflowTriggerTypeWebhook:
		webhookConfig, err := NormalizeWebhookTriggerConfig(configValues)
		if err != nil {
			return nil, err
		}
		normalized["config"] = webhookConfig
	case WorkflowTriggerTypeKafka:
		normalized["config"] = normalizeKafkaTriggerConfig(configValues)
	case WorkflowTriggerTypeMQTT:
		mqttConfig, err := normalizeMQTTTriggerConfig(configValues)
		if err != nil {
			log.Errorf("base.workflow.trigger %v", err)
			return nil, fmt.Errorf("base.workflow.trigger %w", err)
		}
		normalized["config"] = mqttConfig
	case WorkflowTriggerTypeModel:
		modelConfig, err := normalizeModelTriggerConfig(configValues)
		if err != nil {
			log.Errorf("base.workflow.trigger %v", err)
			return nil, fmt.Errorf("base.workflow.trigger %w", err)
		}
		normalized["config"] = modelConfig
		if topic, _ := stringFromAny(normalized["topic"]); strings.TrimSpace(topic) == "" {
			// Synthetic, human-readable label only — never matched against
			// anything at dispatch time (registerModelTrigger keys off
			// config.model/config.operation, not Topic).
			normalized["topic"] = fmt.Sprintf("model:%s:%s", modelConfig["model"], modelConfig["operation"])
		}
	case WorkflowTriggerTypeTCP:
		// Creating is refused while the transport is off; later edits (e.g. deactivating a row)
		// stay possible, and the registry skips such rows at boot.
		if creating && !config.Get().Tcp.UseTCP {
			return nil, orm.InvalidParamsf("tcp triggers require tcp.useTcp to be enabled in the server config")
		}
		tcpConfig, err := normalizeTCPTriggerConfig(configValues)
		if err != nil {
			log.Errorf("base.workflow.trigger %v", err)
			return nil, fmt.Errorf("base.workflow.trigger %w", err)
		}
		normalized["config"] = tcpConfig
		normalized["topic"] = fmt.Sprintf("tcp:%d", tcpConfig["port"])
	}
	// audit_payload (full|hash|none, plan T45) is valid on every transport; the per-type
	// normalizers above rebuild config from their own keys, so carry it over here.
	if typedConfig, ok := normalized["config"].(map[string]any); ok {
		if err := NormalizeTriggerAuditPayload(configValues, typedConfig); err != nil {
			return nil, err
		}
		// dedupe_key (plan T56): payload path whose value names the run of a kafka/mqtt delivery.
		if key := strings.TrimSpace(stringValue(configValues["dedupe_key"])); key != "" && (triggerType == WorkflowTriggerTypeKafka || triggerType == WorkflowTriggerTypeMQTT) {
			typedConfig["dedupe_key"] = key
		}
	}

	return normalized, nil
}

func currentWorkflowTriggerType(values orm.RecordMap, current *workflowTriggerState) string {
	if value, ok := stringFromAny(values["trigger_type"]); ok {
		return strings.ToLower(strings.TrimSpace(value))
	}
	if current != nil {
		return strings.ToLower(strings.TrimSpace(current.TriggerType))
	}
	return ""
}

func normalizeKafkaTriggerConfig(values map[string]any) map[string]any {
	config := map[string]any{}
	if group := strings.TrimSpace(stringValue(values["consumer_group"])); group != "" {
		config["consumer_group"] = group
	}
	if _, exists := values["use_dedupe"]; exists {
		config["use_dedupe"] = boolValue(values["use_dedupe"])
	}
	return config
}

// normalizeMQTTTriggerConfig pins qos to 1: every MQTT subscription runs at
// at-least-once, so any submitted qos (including legacy 0/2) is normalized away.
func normalizeMQTTTriggerConfig(_ map[string]any) (map[string]any, error) {
	return map[string]any{"qos": int64(1)}, nil
}

// normalizeModelTriggerConfig validates the config shape for trigger_type
// "model": a target model, the record-lifecycle operation to fire on, and two
// independent optional domain gates — `trigger_domain` (checked on every
// create/write/unlink event fired for the model, regardless of which
// operation this row is configured for) and `domain` (checked only for the
// selected operation, against the pre-image for before_* or the post-image
// for after_*, per orm.RecordEventHandler's doc comment). Both are filters
// over an already-committed event only — there is no separate blocking/veto
// hook here, so a model trigger can never abort the write it observes.
func normalizeModelTriggerConfig(values map[string]any) (map[string]any, error) {
	modelName := strings.TrimSpace(stringValue(values["model"]))
	if modelName == "" {
		return nil, orm.InvalidParamsf("model is required when trigger_type is model")
	}
	operation := strings.TrimSpace(stringValue(values["operation"]))
	switch operation {
	case WorkflowModelTriggerBeforeCreate, WorkflowModelTriggerAfterCreate,
		WorkflowModelTriggerBeforeWrite, WorkflowModelTriggerAfterWrite,
		WorkflowModelTriggerBeforeUnlink, WorkflowModelTriggerAfterUnlink:
	default:
		return nil, orm.InvalidParamsf("operation must be one of before_create, after_create, before_write, after_write, before_unlink, after_unlink")
	}

	config := map[string]any{
		"model":     modelName,
		"operation": operation,
	}
	// Structural validation only (no ModelMetadata available here) so a
	// malformed domain fails fast on save instead of silently never matching
	// at dispatch time.
	if rawTriggerDomain, exists := values["trigger_domain"]; exists && rawTriggerDomain != nil {
		if _, err := orm.ParseDomain(rawTriggerDomain); err != nil {
			return nil, orm.InvalidParamsf("trigger_domain is invalid: %v", err)
		}
		config["trigger_domain"] = rawTriggerDomain
	}
	if rawDomain, exists := values["domain"]; exists && rawDomain != nil {
		if _, err := orm.ParseDomain(rawDomain); err != nil {
			return nil, orm.InvalidParamsf("domain is invalid: %v", err)
		}
		config["domain"] = rawDomain
	}
	return config, nil
}

// Defaults for tcp trigger settings.
const (
	tcpTriggerDefaultReplyTimeoutMS = 30000
	tcpTriggerMaxReplyTimeoutMS     = 300000
)

// Webhook trigger config keys (plan T43). hmac_secret holds a credential reference only, like the
// action secrets of T40; max_body_bytes lowers or raises the /wh body cap within the server limit.
const (
	WebhookTriggerConfigHMACSecret   = "hmac_secret"
	WebhookTriggerConfigMaxBodyBytes = "max_body_bytes"
	// WebhookDefaultMaxBodyBytes is the /wh body cap when max_body_bytes is unset.
	WebhookDefaultMaxBodyBytes = 1 << 20
	// WebhookMaxBodyBytesLimit is fiber's default request BodyLimit, the hard upper bound.
	WebhookMaxBodyBytesLimit = 4 << 20
)

// NormalizeWebhookTriggerConfig keeps only the known webhook keys: hmac_secret must be empty or a
// @cred:/@syscred: reference (a literal secret would be stored, shown and exported in clear) and
// max_body_bytes a positive integer up to WebhookMaxBodyBytesLimit. Empty values are dropped.
func NormalizeWebhookTriggerConfig(values map[string]any) (map[string]any, error) {
	normalized := map[string]any{}
	if raw, ok := values[WebhookTriggerConfigHMACSecret]; ok && !isBlankConfigValue(raw) {
		ref, isString := raw.(string)
		if !isString || !IsWorkflowCredentialRef(ref) {
			return nil, orm.InvalidParamsf("webhook trigger hmac_secret must be a @cred:<name> or @syscred:<name> reference; store the secret as a workflow credential")
		}
		normalized[WebhookTriggerConfigHMACSecret] = strings.TrimSpace(ref)
	}
	if raw, ok := values[WebhookTriggerConfigMaxBodyBytes]; ok && !isBlankConfigValue(raw) {
		limit, isInt := int64FromAny(raw)
		if !isInt || limit <= 0 || limit > WebhookMaxBodyBytesLimit {
			return nil, orm.InvalidParamsf("webhook trigger max_body_bytes must be between 1 and %d", WebhookMaxBodyBytesLimit)
		}
		normalized[WebhookTriggerConfigMaxBodyBytes] = limit
	}
	return normalized, nil
}

// isBlankConfigValue is true for a missing value or an empty/whitespace string (form left blank).
func isBlankConfigValue(raw any) bool {
	if raw == nil {
		return true
	}
	s, ok := raw.(string)
	return ok && strings.TrimSpace(s) == ""
}

// normalizeTCPTriggerConfig validates transport settings only: port, framing, header counting, TLS
// mode and reply timeout. Nothing here knows about message contents.
func normalizeTCPTriggerConfig(values map[string]any) (map[string]any, error) {
	tcpCfg := config.Get().Tcp.WithDefaults()
	if err := tcpCfg.Validate(); err != nil {
		return nil, orm.InvalidParamsf("%v", err)
	}

	port := int64(tcpCfg.DefaultPort)
	if raw, ok := values["port"]; ok && !isBlankConfigValue(raw) {
		p, ok := int64FromAny(raw)
		if !ok {
			return nil, orm.InvalidParamsf("tcp trigger port must be an integer")
		}
		port = p
	}
	if port < int64(tcpCfg.PortMin) || port > int64(tcpCfg.PortMax) {
		return nil, orm.InvalidParamsf("tcp trigger port must be within %d-%d", tcpCfg.PortMin, tcpCfg.PortMax)
	}

	framing, ok := tcpserver.NormalizeFraming(strings.ToLower(strings.TrimSpace(stringValue(values["framing"]))))
	if !ok {
		return nil, orm.InvalidParamsf("tcp trigger framing must be one of len2_bcd, len2_bin, stx_etx, len4_ascii")
	}

	tlsMode := strings.ToLower(strings.TrimSpace(stringValue(values["tls_mode"])))
	switch tlsMode {
	case "":
		tlsMode = "none"
	case "none":
	case "tls", "mtls":
		if tcpCfg.TLSCertFile == "" || tcpCfg.TLSKeyFile == "" {
			return nil, orm.InvalidParamsf("tcp trigger tls_mode %s requires tcp.tlsCertFile and tcp.tlsKeyFile in the server config", tlsMode)
		}
		if tlsMode == "mtls" && tcpCfg.TLSClientCAFile == "" {
			return nil, orm.InvalidParamsf("tcp trigger tls_mode mtls requires tcp.tlsClientCaFile in the server config")
		}
	default:
		return nil, orm.InvalidParamsf("tcp trigger tls_mode must be none, tls, or mtls")
	}

	replyTimeout := int64(tcpTriggerDefaultReplyTimeoutMS)
	if raw, ok := values["reply_timeout_ms"]; ok && !isBlankConfigValue(raw) {
		v, ok := int64FromAny(raw)
		if !ok || v <= 0 || v > tcpTriggerMaxReplyTimeoutMS {
			return nil, orm.InvalidParamsf("tcp trigger reply_timeout_ms must be between 1 and %d", tcpTriggerMaxReplyTimeoutMS)
		}
		replyTimeout = v
	}

	return map[string]any{
		"port":                port,
		"framing":             framing,
		"len_includes_header": boolValue(values["len_includes_header"]),
		"tls_mode":            tlsMode,
		"reply_timeout_ms":    replyTimeout,
	}, nil
}

func workflowTriggerRecordFromRecord(record orm.RecordMap) WorkflowTriggerRecord {
	state := workflowTriggerStateFromRecord(record)
	return WorkflowTriggerRecord{
		BaseModel: orm.BaseModel{
			ID: int64Value(record["id"]),
		},
		WorkflowID:  state.WorkflowID,
		TriggerType: state.TriggerType,
		Topic:       state.Topic,
		IsActive:    state.IsActive,
		Config:      cloneActionParams(state.Config),
	}
}

func registerWorkflowTriggerRecord(ctx context.Context, trigger WorkflowTriggerRecord) error {
	if !trigger.IsActive || trigger.ID <= 0 {
		return nil
	}
	if workflowTriggerRuntimeHooks.RegisterTrigger == nil {
		return nil
	}
	return workflowTriggerRuntimeHooks.RegisterTrigger(ctx, trigger)
}

func unregisterWorkflowTriggerID(ctx context.Context, triggerID int64) error {
	if triggerID <= 0 {
		return nil
	}
	if workflowTriggerRuntimeHooks.UnregisterTrigger == nil {
		return nil
	}
	return workflowTriggerRuntimeHooks.UnregisterTrigger(ctx, triggerID)
}

// syncWorkflowTriggers asks the trigger registry to reload workflowID's triggers. Best effort:
// the registry logs failures and its periodic Sync converges anyway.
func syncWorkflowTriggers(ctx context.Context, workflowID int64) {
	if workflowID <= 0 || workflowTriggerRuntimeHooks.SyncWorkflowTriggers == nil {
		return
	}
	workflowTriggerRuntimeHooks.SyncWorkflowTriggers(ctx, workflowID)
}
