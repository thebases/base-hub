package models

// File overview:
// - Responsibility: defines the `workflow` module model contract for base workflow action, including field metadata, runtime methods, and module-specific invariants.
// - Key symbols: `string`, `int`, `Step`, `WorkflowActionRecord`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

type WorkflowActionRecord struct {
	orm.BaseModel
	WorkflowID        *int64                 `bun:"workflow_id" json:"workflow_id" meta:"kind:many2one,model:base.workflow,ondelete:CASCADE,index,label:Workflow,help:Links this action to its parent workflow definition."`
	CompanyID         *int64                 `bun:"company_id" json:"company_id" meta:"kind:many2one,model:tbl.company,ondelete:SET NULL,index,label:Company ID,help:Inherits the company scope from the parent workflow; not settable independently."`
	CatalogID         *int64                 `bun:"catalog_id" json:"catalog_id" meta:"kind:many2one,model:base.workflow.action.catalog,ondelete:RESTRICT,index,label:Action Catalog,help:Selects one built-in workflow action type from the fixed workflow action catalog."`
	Sequence          int                    `bun:"sequence" json:"sequence" meta:"index,label:Sequence,help:Controls execution order inside the workflow."`
	Name              string                 `bun:"name" json:"name" meta:"index,label:Name,help:Stores the human readable name for this configured workflow action step."`
	ActionCode        string                 `bun:"action_code" json:"action_code" meta:"related:catalog_id.code,stored,depends:catalog_id,index,label:Action Code,help:Stores the resolved built-in action code copied from the selected workflow action catalog entry."`
	ActionVersion     int                    `bun:"action_version" json:"action_version" meta:"related:catalog_id.version,stored,depends:catalog_id,label:Action Version,help:Stores the resolved built-in action version copied from the selected workflow action catalog entry."`
	IsActive          bool                   `bun:"is_active" json:"is_active" meta:"index,label:Is Active,help:Controls whether this configured action participates in workflow execution."`
	Description       string                 `bun:"description" json:"description" meta:"label:Description,help:Stores the descriptive notes for this configured workflow action step."`
	StepKey           string                 `bun:"step_key" json:"step_key" meta:"index,label:Step Key,help:Stores the stable step key used by workflow run audit and resume logic."`
	TimeoutSeconds    int                    `bun:"timeout_seconds,default:30" json:"timeout_seconds" meta:"label:Timeout Seconds,help:Stores the numeric value for timeout seconds."`
	RetryPolicy       RetryPolicy            `bun:"retry_policy,type:jsonb,nullzero" json:"retry_policy" meta:"kind:json,label:Retry Policy,help:Stores the retry policy for this configured workflow action step (max_attempts, retry_cool_down) — the single source of truth for retry behavior."`
	ContinueOnError   bool                   `bun:"continue_on_error" json:"continue_on_error" meta:"label:Continue On Error,help:Controls whether the workflow should continue after this action exhausts retries."`
	InputContract     map[string]interface{} `bun:"input_contract,type:jsonb,nullzero" json:"input_contract" meta:"kind:json,label:Input Contract,help:Stores the input contract payload in JSON format."`
	OutputContract    map[string]interface{} `bun:"output_contract,type:jsonb,nullzero" json:"output_contract" meta:"kind:json,label:Output Contract,help:Stores the output contract payload in JSON format."`
	ModelName         string                 `bun:"model_name" json:"model_name" meta:"label:Model Name,help:Stores the runtime model name used by crud_model actions or as the source JSON template for transform_data actions."`
	TargetRef         string                 `bun:"target_ref" json:"target_ref" meta:"label:Target Ref,help:Stores the outbound URL or channel target used by notify and post_output_object actions."`
	Channel           string                 `bun:"channel" json:"channel" meta:"label:Channel,help:Stores the delivery channel used by notify actions."`
	OutputField       string                 `bun:"output_field" json:"output_field" meta:"label:Output Field,help:Stores the output field name used by hmac_action or aes_action."`
	HmacKey           string                 `bun:"hmac_key" json:"hmac_key" meta:"label:HMAC Key,help:Stores the @cred:<name> or @syscred:<name> reference to the HMAC key used by hmac_action; a literal key is refused."`
	AesKey            string                 `bun:"aes_key" json:"aes_key" meta:"label:AES Key,help:Stores the @cred:<name> or @syscred:<name> reference to the AES key used by aes_action and aes_decrypt_action; a literal key is refused."`
	KeyList           []string               `bun:"key_list,type:jsonb,nullzero" json:"key_list" meta:"kind:json,label:Key List,help:Stores the ordered list of field names used by signing actions."`
	Headers           map[string]interface{} `bun:"headers,type:jsonb,nullzero" json:"headers" default:"{\"Content-Type\":\"application/json\"}" meta:"kind:json,label:Headers,help:Stores outbound transport headers in JSON format."`
	Authorization     string                 `bun:"authorization" json:"authorization" meta:"label:Authorization,help:Stores the Authorization header sent by notify and post_output_object actions. Expects an @cred:<name> or @syscred:<name> reference resolved at execution time; leave empty to send none."`
	Template          map[string]interface{} `bun:"template,type:jsonb,nullzero" json:"template" meta:"kind:json,label:Template,help:Stores the JSON template used by transform_data actions."`
	ExtraParams       map[string]interface{} `bun:"extra_params,type:jsonb,nullzero" json:"extra_params" meta:"kind:json,label:Extra Params,help:Stores optional advanced action parameters in JSON format."`
	FlattenInput      bool                   `bun:"flatten_input" json:"flatten_input" default:"true" meta:"label:Flatten Input,help:Controls whether template binding should flatten nested JSON before placeholder replacement."`
	ReturnSavedRecord bool                   `bun:"return_saved_record" json:"return_saved_record" meta:"label:Return Saved Record,help:Controls whether crud_model actions should expose the saved record in runtime params for downstream handlers."`
	IncludeRaw        bool                   `bun:"include_raw" json:"include_raw" meta:"label:Include Raw,help:Controls whether crypto actions append raw payload diagnostics into the output."`
	IncludeKey        bool                   `bun:"include_key" json:"include_key" meta:"label:Include Key,help:Controls whether crypto actions append the effective key into the output."`
	BranchLabel       string                 `bun:"branch_label" json:"branch_label" meta:"label:Branch Label,help:Stores the condition branch that activates this step. Empty runs always; 'yes' runs when the preceding condition result is true; 'no' runs when it is false."`
	ConditionStepKey  string                 `bun:"condition_step_key" json:"condition_step_key" meta:"label:Condition Step Key,help:Stores the step_key of the condition node that gates this step. When set, branch routing uses the condition-specific result key instead of the global _condition_result."`
}

const WorkflowActionModelName = "base.workflow.action"

var WorkflowActionDefinition = orm.ModelDefinition(WorkflowActionModelName, (*WorkflowActionRecord)(nil))

var WorkflowActionModelSpec = orm.ModelSpec{
	Name:        WorkflowActionModelName,
	Label:       "Workflow Action",
	Description: "Configured workflow action step that selects one built-in action and stores its inputs",
	Definition:  WorkflowActionDefinition,
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "create", Public: true, ModelOnly: true, Call: createWorkflowAction},
		{Name: "write", Public: true, UsesPathID: true, Call: writeWorkflowAction},
		{Name: "unlink", Public: true, UsesPathID: true, Call: unlinkWorkflowAction},
	},
}

var WorkflowActionModel = orm.Define[WorkflowActionRecord](&Models, 100, WorkflowActionModelSpec)

var (
	workflowActionLookupModelTemplate = func(ctx context.Context, modelName string) (map[string]any, error) {
		modelName = strings.TrimSpace(modelName)
		if modelName == "" {
			return map[string]any{}, nil
		}
		env := orm.EnvironmentFromContext(ctx)
		if env == nil || env.DB() == nil {
			log.Errorf("base.workflow.action onchange requires initialized environment")
			return nil, fmt.Errorf("base.workflow.action onchange requires initialized environment")
		}
		type workflowSysModelTemplateRow struct {
			JSONTemplate map[string]any `bun:"json_template"`
		}
		row := new(workflowSysModelTemplateRow)
		err := env.DB().NewSelect().
			Model(row).
			ModelTableExpr("sys_model").
			ColumnExpr("COALESCE(json_dto, '{}'::jsonb) AS json_template").
			Where("name = ?", modelName).
			Limit(1).
			Scan(ctx)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return map[string]any{}, nil
			}
			return nil, err
		}
		if row.JSONTemplate == nil {
			return map[string]any{}, nil
		}
		return cloneActionParams(row.JSONTemplate), nil
	}
)

func init() {
	orm.RegisterOnchange(WorkflowActionModelName, []string{"model_name"}, workflowActionModelNameOnchange)
	orm.RegisterOnchange(WorkflowActionModelName, []string{"catalog_id"}, workflowActionCatalogIDOnchange)
}

// workflowActionCatalogIDOnchange previews the action_code/action_version values that
// catalog_id resolves to via its `related:` tag. Related fields only resolve through
// relational reads on save/reload, not live in an unsaved onchange preview, so a new or
// edited row otherwise keeps showing a stale/blank action_code and action_version until
// the record is saved and re-read.
func workflowActionCatalogIDOnchange(ctx context.Context, _ orm.RecordSet, values orm.RecordMap, _ []string, _ map[string]any) (orm.RecordMap, error) {
	record, err := loadWorkflowActionCatalogRecord(ctx, values["catalog_id"])
	if err != nil {
		log.Errorf("base.workflow.action onchange catalog_id: %v", err)
		return nil, fmt.Errorf("base.workflow.action onchange catalog_id: %w", err)
	}
	if record == nil {
		return orm.RecordMap{"action_code": "", "action_version": 0}, nil
	}
	return orm.RecordMap{
		"action_code":    record.ResolvedCode(),
		"action_version": record.ResolvedVersion(),
	}, nil
}

func workflowActionModelNameOnchange(ctx context.Context, _ orm.RecordSet, values orm.RecordMap, _ []string, _ map[string]any) (orm.RecordMap, error) {
	if strings.TrimSpace(stringValue(values["action_code"])) != "transform_data" {
		return nil, nil
	}
	template, err := workflowActionLookupModelTemplate(ctx, stringValue(values["model_name"]))
	if err != nil {
		log.Errorf("base.workflow.action onchange model_name: %v", err)
		return nil, fmt.Errorf("base.workflow.action onchange model_name: %w", err)
	}
	return orm.RecordMap{
		"template": template,
	}, nil
}

// createWorkflowAction creates the workflow action handled by this package.
func createWorkflowAction(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	valuesList, err := recordMapListFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if len(valuesList) == 0 {
		log.Errorf("base.workflow.action.create requires values")
		return nil, orm.InvalidParamsf("base.workflow.action.create requires values")
	}

	created := make([]orm.RecordMap, 0, len(valuesList))
	for _, values := range valuesList {
		normalized, err := normalizeWorkflowActionValues(ctx, values, nil, true)
		if err != nil {
			return nil, err
		}
		// A workflow action's company must always match its parent workflow's,
		// never the caller's own session company, so inherit rather than pin.
		if err := inheritWorkflowChildCompanyID(ctx, model, normalized); err != nil {
			return nil, err
		}
		// step_key is unique per workflow (plan T62).
		if err := guardWorkflowActionStepKeyUnique(ctx, normalized, 0); err != nil {
			return nil, err
		}
		record, err := orm.CreateRecord(ctx, orm.RuntimeModelRef(WorkflowActionModelName, model), normalized)
		if err != nil {
			return nil, err
		}
		if workflowID := workflowIDFromAny(record["workflow_id"]); workflowID > 0 {
			if err := writeWorkflowFlowMap(ctx, workflowID); err != nil {
				return nil, err
			}
			// Steps are part of the published snapshot (plan T97).
			if err := markWorkflowDraft(ctx, workflowID); err != nil {
				return nil, err
			}
		}
		created = append(created, record)
	}
	if len(created) == 1 {
		return created[0], nil
	}
	return created, nil
}

func writeWorkflowAction(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	if len(ids) == 0 {
		log.Errorf("base.workflow.action.write requires ids")
		return nil, orm.InvalidParamsf("base.workflow.action.write requires ids")
	}
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		log.Errorf("base.workflow.action.write requires values")
		return nil, orm.InvalidParamsf("base.workflow.action.write requires values")
	}

	beforeRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(WorkflowActionModelName, model), ids, nil)
	if err != nil {
		return nil, err
	}
	if len(beforeRows) == 0 {
		return false, nil
	}

	current := workflowActionStateFromRecord(beforeRows[0])
	normalized, err := normalizeWorkflowActionValues(ctx, values, &current, false)
	if err != nil {
		return nil, err
	}
	// company_id follows the parent workflow (plan T41).
	if err := applyWorkflowChildCompanyOnWrite(ctx, normalized); err != nil {
		return nil, err
	}
	// step_key stays unique per workflow (plan T62). One key cannot go on several rows at once.
	_, stepKeySent := values["step_key"]
	_, workflowSent := values["workflow_id"]
	if stepKeySent && len(ids) > 1 {
		log.Errorf("base.workflow.action.write step_key can only be set on one action at a time")
		return nil, orm.InvalidParamsf("base.workflow.action.write step_key can only be set on one action at a time")
	}
	if stepKeySent || workflowSent {
		if err := guardWorkflowActionStepKeyUnique(ctx, normalized, int64Value(beforeRows[0]["id"])); err != nil {
			return nil, err
		}
	}
	ok, err := orm.WriteRecords(ctx, orm.RuntimeModelRef(WorkflowActionModelName, model), ids, normalized)
	if err != nil {
		return nil, err
	}
	if !ok {
		return ok, nil
	}
	// A renamed step keeps its edges (plan T62): rewrite from/to_step_key in the same workflow.
	if stepKeySent && len(beforeRows) == 1 && !workflowSent {
		newKey := strings.TrimSpace(stringValue(normalized["step_key"]))
		if err := renameWorkflowEdgeStepKey(ctx, workflowIDFromAny(beforeRows[0]["workflow_id"]), current.StepKey, newKey); err != nil {
			return nil, err
		}
	}

	afterRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(WorkflowActionModelName, model), ids, nil)
	if err != nil {
		return nil, err
	}
	for workflowID := range workflowIDsFromActionRows(append(beforeRows, afterRows...)) {
		if err := writeWorkflowFlowMap(ctx, workflowID); err != nil {
			return nil, err
		}
		if err := markWorkflowDraft(ctx, workflowID); err != nil {
			return nil, err
		}
	}
	return ok, nil
}

func unlinkWorkflowAction(ctx context.Context, model *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	if len(ids) == 0 {
		log.Errorf("base.workflow.action.unlink requires ids")
		return nil, orm.InvalidParamsf("base.workflow.action.unlink requires ids")
	}

	beforeRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(WorkflowActionModelName, model), ids, []string{"id", "workflow_id"})
	if err != nil {
		return nil, err
	}

	ok, err := orm.RuntimeRepositoryUnlink(ctx, orm.RuntimeModelRef(WorkflowActionModelName, model), ids)
	if err != nil {
		return nil, err
	}
	if !ok {
		return ok, nil
	}

	for workflowID := range workflowIDsFromActionRows(beforeRows) {
		// The deleted steps' edges go with them (plan T62).
		if err := deleteEdgesOfMissingSteps(ctx, workflowID); err != nil {
			return nil, err
		}
		if err := writeWorkflowFlowMap(ctx, workflowID); err != nil {
			return nil, err
		}
		if err := markWorkflowDraft(ctx, workflowID); err != nil {
			return nil, err
		}
	}
	return ok, nil
}

// deleteEdgesOfMissingSteps drops the edges of a workflow whose steps no longer exist.
func deleteEdgesOfMissingSteps(ctx context.Context, workflowID int64) error {
	keys, err := workflowStepKeyIDs(ctx, workflowID)
	if err != nil {
		return err
	}
	keep := make(map[string]struct{}, len(keys))
	for key := range keys {
		keep[key] = struct{}{}
	}
	return deleteWorkflowEdgesNotIn(ctx, workflowID, keep)
}

func workflowIDsFromActionRows(rows []orm.RecordMap) map[int64]struct{} {
	workflowIDs := make(map[int64]struct{})
	for _, row := range rows {
		workflowID := workflowIDFromAny(row["workflow_id"])
		if workflowID <= 0 {
			continue
		}
		workflowIDs[workflowID] = struct{}{}
	}
	return workflowIDs
}

func workflowIDFromAny(raw any) int64 {
	value, ok := int64PointerValue(raw)
	if !ok || value == nil {
		return 0
	}
	return *value
}

type workflowActionState struct {
	WorkflowID        *int64
	CatalogID         *int64
	Sequence          int64
	Name              string
	ActionCode        string
	ActionVersion     int
	IsActive          bool
	Description       string
	StepKey           string
	TimeoutSeconds    int64
	RetryPolicy       RetryPolicy
	ContinueOnError   bool
	InputContract     map[string]any
	OutputContract    map[string]any
	ModelName         string
	TargetRef         string
	Channel           string
	OutputField       string
	HmacKey           string
	AesKey            string
	KeyList           []string
	Headers           map[string]any
	Authorization     string
	Template          map[string]any
	ExtraParams       map[string]any
	FlattenInput      bool
	ReturnSavedRecord bool
	IncludeRaw        bool
	IncludeKey        bool
	BranchLabel       string
	ConditionStepKey  string
}

func workflowActionStateFromRecord(record orm.RecordMap) workflowActionState {
	inputContract, _ := normalizeUIActionContext(record["input_contract"])
	outputContract, _ := normalizeUIActionContext(record["output_contract"])
	headers, _ := normalizeUIActionContext(record["headers"])
	template, _ := normalizeUIActionContext(record["template"])
	extraParams, _ := normalizeUIActionContext(record["extra_params"])
	return workflowActionState{
		WorkflowID:        int64PointerFromAny(record["workflow_id"]),
		CatalogID:         int64PointerFromAny(record["catalog_id"]),
		Sequence:          int64Value(record["sequence"]),
		Name:              strings.TrimSpace(stringValue(record["name"])),
		ActionCode:        strings.TrimSpace(stringValue(record["action_code"])),
		ActionVersion:     int(int64Value(record["action_version"])),
		IsActive:          boolValue(record["is_active"]),
		Description:       strings.TrimSpace(stringValue(record["description"])),
		StepKey:           strings.TrimSpace(stringValue(record["step_key"])),
		TimeoutSeconds:    int64Value(record["timeout_seconds"]),
		RetryPolicy:       retryPolicyFromAny(record["retry_policy"]),
		ContinueOnError:   boolValue(record["continue_on_error"]),
		InputContract:     inputContract,
		OutputContract:    outputContract,
		ModelName:         strings.TrimSpace(stringValue(record["model_name"])),
		TargetRef:         strings.TrimSpace(stringValue(record["target_ref"])),
		Channel:           strings.TrimSpace(stringValue(record["channel"])),
		OutputField:       strings.TrimSpace(stringValue(record["output_field"])),
		HmacKey:           strings.TrimSpace(stringValue(record["hmac_key"])),
		AesKey:            strings.TrimSpace(stringValue(record["aes_key"])),
		KeyList:           stringSliceFromAny(record["key_list"]),
		Headers:           headers,
		Authorization:     strings.TrimSpace(stringValue(record["authorization"])),
		Template:          template,
		ExtraParams:       extraParams,
		FlattenInput:      boolValue(record["flatten_input"]),
		ReturnSavedRecord: boolValue(record["return_saved_record"]),
		IncludeRaw:        boolValue(record["include_raw"]),
		IncludeKey:        boolValue(record["include_key"]),
		BranchLabel:       strings.TrimSpace(stringValue(record["branch_label"])),
		ConditionStepKey:  strings.TrimSpace(stringValue(record["condition_step_key"])),
	}
}

func normalizeWorkflowActionValues(ctx context.Context, values orm.RecordMap, current *workflowActionState, creating bool) (orm.RecordMap, error) {
	normalized := cloneRecordMap(values)

	catalogRecord, entry, err := resolveWorkflowActionCatalogSelection(ctx, normalized, current)
	if err != nil {
		return nil, err
	}
	if catalogRecord != nil && catalogRecord.ID > 0 {
		normalized["catalog_id"] = catalogRecord.ID
	}
	normalized["action_code"] = entry.Name
	normalized["action_version"] = int64(entry.Version)

	if rawWorkflowID, exists := normalized["workflow_id"]; exists {
		ptr, ok := int64PointerValue(rawWorkflowID)
		if !ok {
			log.Errorf("base.workflow.action workflow_id must be a workflow id")
			return nil, orm.InvalidParamsf("base.workflow.action workflow_id must be a workflow id")
		}
		normalized["workflow_id"] = ptr
	} else if current != nil && current.WorkflowID != nil {
		normalized["workflow_id"] = *current.WorkflowID
	}

	if rawSequence, exists := normalized["sequence"]; exists {
		number, ok := int64FromAny(rawSequence)
		if !ok || number <= 0 {
			log.Errorf("base.workflow.action sequence must be a positive integer")
			return nil, orm.InvalidParamsf("base.workflow.action sequence must be a positive integer")
		}
		normalized["sequence"] = number
	} else if creating {
		normalized["sequence"] = int64(10)
	}

	if name, ok := stringFromAny(normalized["name"]); ok {
		name = strings.TrimSpace(name)
		if name == "" {
			log.Errorf("base.workflow.action name is required")
			return nil, orm.InvalidParamsf("base.workflow.action name is required")
		}
		normalized["name"] = name
	} else if creating {
		normalized["name"] = entry.Label
	}

	if _, exists := normalized["is_active"]; !exists && creating {
		normalized["is_active"] = true
	}
	if description, ok := stringFromAny(normalized["description"]); ok {
		normalized["description"] = strings.TrimSpace(description)
	} else if creating {
		normalized["description"] = entry.Description
	}

	if stepKey, ok := stringFromAny(normalized["step_key"]); ok {
		stepKey = strings.TrimSpace(stepKey)
		if stepKey == "" {
			log.Errorf("base.workflow.action step_key is required")
			return nil, orm.InvalidParamsf("base.workflow.action step_key is required")
		}
		normalized["step_key"] = stepKey
	} else if creating {
		normalized["step_key"] = defaultWorkflowActionStepKey(entry.Name, stringValue(normalized["name"]))
	}

	if err := normalizeNonNegativeIntField(normalized, "timeout_seconds"); err != nil {
		log.Errorf("base.workflow.action %v", err)
		return nil, fmt.Errorf("base.workflow.action %w", err)
	}
	if rawPolicy, exists := normalized["retry_policy"]; exists {
		policy, err := normalizeRetryPolicy(rawPolicy)
		if err != nil {
			log.Errorf("base.workflow.action retry_policy %v", err)
			return nil, fmt.Errorf("base.workflow.action retry_policy %w", err)
		}
		normalized["retry_policy"] = policy
	} else if creating {
		// RetryPolicy is the single source of truth for retry behavior; a newly
		// created action always gets the full default object persisted (rather
		// than an empty struct) so the row is self-describing without relying on
		// zero-value fallbacks at read time.
		normalized["retry_policy"] = DefaultRetryPolicy()
	}

	// Walk each string{"input contract", "output contract", "headers", "template", "extra params" entry in order so validation and side effects stay scoped to the current element.
	for _, field := range []string{"input_contract", "output_contract", "headers", "template", "extra_params"} {
		if raw, exists := normalized[field]; exists {
			object, err := normalizeUIActionContext(raw)
			if err != nil {
				log.Errorf("base.workflow.action %s must be an object", field)
				return nil, orm.InvalidParamsf("base.workflow.action %s must be an object", field)
			}
			normalized[field] = object
		} else if creating {
			normalized[field] = map[string]any{}
		}
	}

	if rawKeys, exists := normalized["key_list"]; exists {
		normalized["key_list"] = stringSliceFromAny(rawKeys)
	} else if creating {
		normalized["key_list"] = []string{}
	}

	// Walk each string{"model name", "target ref", "channel", "output field", "hmac key", "aes key", "authorization", "branch label" entry in order so validation and side effects stay scoped to the current element.
	for _, field := range []string{"model_name", "target_ref", "channel", "output_field", "hmac_key", "aes_key", "authorization", "branch_label", "condition_step_key"} {
		if value, ok := stringFromAny(normalized[field]); ok {
			normalized[field] = strings.TrimSpace(value)
		} else if creating {
			normalized[field] = ""
		}
	}
	// Secrets are stored as credential references only (plan T40).
	if err := validateWorkflowActionSecretRefs(normalized); err != nil {
		return nil, err
	}

	// Required inputs are checked on the merged row: a partial write (e.g. a step_key rename)
	// keeps the stored values for the fields it does not send.
	merged := mergedWorkflowActionValues(normalized, current)
	switch entry.Name {
	case "crud_model":
		if strings.TrimSpace(stringValue(merged["model_name"])) == "" {
			log.Errorf("base.workflow.action %s requires model_name", entry.Name)
			return nil, orm.InvalidParamsf("base.workflow.action %s requires model_name", entry.Name)
		}
	case "notify", "post_output_object":
		if strings.TrimSpace(stringValue(merged["target_ref"])) == "" {
			log.Errorf("base.workflow.action %s requires target_ref", entry.Name)
			return nil, orm.InvalidParamsf("base.workflow.action %s requires target_ref", entry.Name)
		}
	case "transform_data":
		template, _ := normalizeUIActionContext(merged["template"])
		if len(template) == 0 {
			log.Errorf("base.workflow.action transform_data requires template")
			return nil, orm.InvalidParamsf("base.workflow.action transform_data requires template")
		}
	case "hmac_action":
		// hmac_action can omit hmac_key because the runtime generates one when absent.
	case "aes_action":
		// aes_action can omit aes_key because the runtime generates one when absent.
	case "aes_decrypt_action":
		if strings.TrimSpace(stringValue(merged["aes_key"])) == "" {
			extraParams, _ := normalizeUIActionContext(merged["extra_params"])
			if strings.TrimSpace(stringValue(extraParams["key_field"])) == "" {
				log.Errorf("base.workflow.action aes_decrypt_action requires aes_key or extra_params.key_field")
				return nil, orm.InvalidParamsf("base.workflow.action aes_decrypt_action requires aes_key or extra_params.key_field")
			}
		}
	}
	return normalized, nil
}

// mergedWorkflowActionValues overlays normalized on the stored values of the fields that carry
// required inputs (create: current is nil and normalized is returned as is).
func mergedWorkflowActionValues(normalized orm.RecordMap, current *workflowActionState) orm.RecordMap {
	if current == nil {
		return normalized
	}
	merged := orm.RecordMap{
		"model_name":   current.ModelName,
		"target_ref":   current.TargetRef,
		"template":     current.Template,
		"aes_key":      current.AesKey,
		"extra_params": current.ExtraParams,
	}
	for key := range merged {
		if value, exists := normalized[key]; exists {
			merged[key] = value
		}
	}
	return merged
}

func currentWorkflowActionCode(current *workflowActionState) string {
	if current == nil {
		return ""
	}
	return current.ActionCode
}

func resolvedWorkflowActionVersion(values orm.RecordMap, current *workflowActionState) int {
	if raw, exists := values["action_version"]; exists {
		version, ok := int64FromAny(raw)
		if ok && version > 0 {
			return int(version)
		}
	}
	if current != nil && current.ActionVersion > 0 {
		return current.ActionVersion
	}
	return 1
}

// defaultWorkflowActionStepKey selects the workflow action step key handled by this package.
func defaultWorkflowActionStepKey(actionCode, name string) string {
	key := strings.TrimSpace(name)
	if key == "" {
		key = strings.TrimSpace(actionCode)
	}
	key = strings.ToLower(strings.ReplaceAll(key, " ", "_"))
	key = strings.ReplaceAll(key, "-", "_")
	return key
}

func normalizeNonNegativeIntField(values orm.RecordMap, key string) error {
	raw, exists := values[key]
	if !exists {
		return nil
	}
	number, ok := int64FromNonNegativeAny(raw)
	if !ok {
		log.Errorf("%s must be a non-negative integer", key)
		return orm.InvalidParamsf("%s must be a non-negative integer", key)
	}
	values[key] = number
	return nil
}

func normalizeRetryPolicy(raw any) (RetryPolicy, error) {
	switch typed := raw.(type) {
	case RetryPolicy:
		if typed.MaxAttempts < 0 || typed.RetryCoolDown < 0 {
			log.Errorf("must contain non-negative values")
			return RetryPolicy{}, orm.InvalidParamsf("must contain non-negative values")
		}
		return typed, nil
	case map[string]any:
		data, err := json.Marshal(typed)
		if err != nil {
			return RetryPolicy{}, err
		}
		var policy RetryPolicy
		if err := json.Unmarshal(data, &policy); err != nil {
			log.Errorf("must be an object")
			return RetryPolicy{}, orm.InvalidParamsf("must be an object")
		}
		if policy.MaxAttempts < 0 || policy.RetryCoolDown < 0 {
			log.Errorf("must contain non-negative values")
			return RetryPolicy{}, orm.InvalidParamsf("must contain non-negative values")
		}
		return policy, nil
	default:
		log.Errorf("must be an object")
		return RetryPolicy{}, orm.InvalidParamsf("must be an object")
	}
}

func retryPolicyFromAny(raw any) RetryPolicy {
	policy, err := normalizeRetryPolicy(raw)
	if err != nil {
		return RetryPolicy{}
	}
	return policy
}
