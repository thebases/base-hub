package models

// File overview:
// - Responsibility: verifies workflow model state, metadata, and persistence behavior for base workflow action.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down workflow model semantics so request IDs, run state, and action definitions remain replay-safe and observable.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestNormalizeWorkflowActionValuesDefaultsFromCatalogCode(t *testing.T) {
	values, err := normalizeWorkflowActionValues(context.Background(), orm.RecordMap{
		"action_code": "parse_payload",
	}, nil, true)
	if err != nil {
		t.Fatalf("normalizeWorkflowActionValues() error = %v", err)
	}
	if got := values["action_code"]; got != "parse_payload" {
		t.Fatalf("action_code = %#v, want parse_payload", got)
	}
	if got := values["action_version"]; got != int64(1) {
		t.Fatalf("action_version = %#v, want 1", got)
	}
	if got := values["name"]; got != "Parse Payload" {
		t.Fatalf("name = %#v, want Parse Payload", got)
	}
	if got := values["is_active"]; got != true {
		t.Fatalf("is_active = %#v, want true", got)
	}
}

func TestNormalizeWorkflowActionValuesRejectsUnknownCatalogCode(t *testing.T) {
	_, err := normalizeWorkflowActionValues(context.Background(), orm.RecordMap{
		"action_code": "custom_partner_push",
	}, nil, true)
	if err == nil {
		t.Fatalf("expected unknown action code to fail")
	}
}

func TestNormalizeWorkflowActionValuesRequiresTransformTemplate(t *testing.T) {
	_, err := normalizeWorkflowActionValues(context.Background(), orm.RecordMap{
		"action_code": "transform_data",
	}, nil, true)
	if err == nil {
		t.Fatalf("expected transform_data without template to fail")
	}
}

func TestNormalizeWorkflowActionValuesAcceptsStringifiedJSONObjectFields(t *testing.T) {
	values, err := normalizeWorkflowActionValues(context.Background(), orm.RecordMap{
		"action_code": "notify",
		"target_ref":  "https://partner.local/callback",
		"headers":     `{"Authorization":"Bearer token"}`,
	}, nil, true)
	if err != nil {
		t.Fatalf("normalizeWorkflowActionValues() error = %v", err)
	}

	headers, ok := values["headers"].(map[string]any)
	if !ok {
		t.Fatalf("headers = %#v, want map[string]any", values["headers"])
	}
	if got := headers["Authorization"]; got != "Bearer token" {
		t.Fatalf("headers[Authorization] = %#v, want Bearer token", got)
	}
}

func TestNormalizeWorkflowActionValuesRequiresCRUDModelName(t *testing.T) {
	_, err := normalizeWorkflowActionValues(context.Background(), orm.RecordMap{
		"action_code": "crud_model",
	}, nil, true)
	if err == nil {
		t.Fatalf("expected crud_model without model_name to fail")
	}
}

func TestNormalizeWorkflowActionValuesAllowsGeneratedCryptoKeys(t *testing.T) {
	values, err := normalizeWorkflowActionValues(context.Background(), orm.RecordMap{
		"action_code": "hmac_action",
	}, nil, true)
	if err != nil {
		t.Fatalf("expected hmac_action without hmac_key to pass, got %v", err)
	}
	if got := values["action_code"]; got != "hmac_action" {
		t.Fatalf("action_code = %#v, want hmac_action", got)
	}

	values, err = normalizeWorkflowActionValues(context.Background(), orm.RecordMap{
		"action_code": "aes_action",
	}, nil, true)
	if err != nil {
		t.Fatalf("expected aes_action without aes_key to pass, got %v", err)
	}
	if got := values["action_code"]; got != "aes_action" {
		t.Fatalf("action_code = %#v, want aes_action", got)
	}
}

func TestNormalizeWorkflowActionValuesRequiresDecryptKeySource(t *testing.T) {
	_, err := normalizeWorkflowActionValues(context.Background(), orm.RecordMap{
		"action_code": "aes_decrypt_action",
	}, nil, true)
	if err == nil {
		t.Fatalf("expected aes_decrypt_action without aes_key or extra_params.key_field to fail")
	}

	values, err := normalizeWorkflowActionValues(context.Background(), orm.RecordMap{
		"action_code": "aes_decrypt_action",
		"extra_params": map[string]any{
			"key_field": "_aes_key",
		},
	}, nil, true)
	if err != nil {
		t.Fatalf("expected aes_decrypt_action with extra_params.key_field to pass, got %v", err)
	}
	if got := values["action_code"]; got != "aes_decrypt_action" {
		t.Fatalf("action_code = %#v, want aes_decrypt_action", got)
	}
}

func TestWorkflowActionModelNameOnchangePopulatesTemplateForTransformData(t *testing.T) {
	originalLookup := workflowActionLookupModelTemplate
	t.Cleanup(func() {
		workflowActionLookupModelTemplate = originalLookup
	})

	workflowActionLookupModelTemplate = func(_ context.Context, modelName string) (map[string]any, error) {
		if modelName != "tbl.partner" {
			t.Fatalf("modelName = %q, want tbl.partner", modelName)
		}
		return map[string]any{
			"name":  "string",
			"email": "string",
		}, nil
	}

	update, err := workflowActionModelNameOnchange(context.Background(), orm.RecordSet{}, orm.RecordMap{
		"action_code": "transform_data",
		"model_name":  "tbl.partner",
	}, []string{"model_name"}, nil)
	if err != nil {
		t.Fatalf("workflowActionModelNameOnchange() error = %v", err)
	}
	if update == nil {
		t.Fatalf("expected onchange update")
	}
	template, ok := update["template"].(map[string]any)
	if !ok {
		t.Fatalf("template = %#v, want map", update["template"])
	}
	if got := template["name"]; got != "string" {
		t.Fatalf("template[name] = %#v, want string", got)
	}
}

func TestWorkflowActionModelNameOnchangeSkipsNonTransformActions(t *testing.T) {
	update, err := workflowActionModelNameOnchange(context.Background(), orm.RecordSet{}, orm.RecordMap{
		"action_code": "crud_model",
		"model_name":  "tbl.partner",
	}, []string{"model_name"}, nil)
	if err != nil {
		t.Fatalf("workflowActionModelNameOnchange() error = %v", err)
	}
	if update != nil {
		t.Fatalf("expected nil update for non-transform action, got %#v", update)
	}
}

func TestResolvedStepMergesCatalogDefaultsAndActionOverrides(t *testing.T) {
	step := WorkflowActionRecord{
		Name:        "Notify Partner",
		ActionCode:  "notify",
		TargetRef:   "https://partner.local/callback",
		ExtraParams: map[string]any{"channel": "vip_webhook"},
	}.ResolvedStep()

	if got := step.Params["url"]; got != "https://partner.local/callback" {
		t.Fatalf("url = %#v, want target_ref value", got)
	}
	if got := step.Params["channel"]; got != "vip_webhook" {
		t.Fatalf("channel = %#v, want override from action record", got)
	}
}

func TestResolvedStepIncludesReturnSavedRecordParam(t *testing.T) {
	step := WorkflowActionRecord{
		ActionCode:        "crud_model",
		ModelName:         "tbl.partner",
		ReturnSavedRecord: true,
	}.ResolvedStep()

	if got := step.Params["return_saved_record"]; got != true {
		t.Fatalf("return_saved_record = %#v, want true", got)
	}
	if got := step.Params["model"]; got != "tbl.partner" {
		t.Fatalf("model = %#v, want tbl.partner", got)
	}
}

func TestMergeETLActionParamsPrefersStepOverrides(t *testing.T) {
	merged := MergeETLActionParams(
		map[string]any{"model": "base.workflow.outbound", "tenant": "default"},
		map[string]any{"tenant": "vip"},
	)
	if got := merged["model"]; got != "base.workflow.outbound" {
		t.Fatalf("model = %#v, want base.workflow.outbound", got)
	}
	if got := merged["tenant"]; got != "vip" {
		t.Fatalf("tenant = %#v, want vip", got)
	}
}

func TestCreateWorkflowActionRegeneratesParentFlowMap(t *testing.T) {
	var wroteFlowMap string
	created := false
	// Stub the shared ORM runtime repository seam for both models involved so
	// the flow-map regeneration path runs without executors or a database.
	restoreAction := orm.StubRuntimeRepository(WorkflowActionModelName, orm.RuntimeRepositoryStub{
		Create: func(_ context.Context, values orm.RecordMap) (orm.RecordMap, error) {
			created = true
			return orm.RecordMap{
				"id":             int64(55),
				"workflow_id":    values["workflow_id"],
				"name":           values["name"],
				"sequence":       values["sequence"],
				"action_code":    values["action_code"],
				"action_version": values["action_version"],
				"step_key":       values["step_key"],
			}, nil
		},
		SearchRead: func(_ context.Context, filter orm.RecordMap, fields []string) ([]orm.RecordMap, error) {
			if int64Value(filter["workflow_id"]) != 11 {
				t.Fatalf("workflow_id filter = %#v, want 11", filter["workflow_id"])
			}
			_ = fields
			// The step_key uniqueness check reads the workflow's steps before the row exists.
			if !created {
				return nil, nil
			}
			return []orm.RecordMap{{
				"id":             int64(55),
				"workflow_id":    int64(11),
				"name":           "Parse Payload",
				"sequence":       int64(10),
				"action_code":    "parse_payload",
				"action_version": int64(1),
				"step_key":       "parse_payload",
			}}, nil
		},
	})
	defer restoreAction()
	restoreFlow := orm.StubRuntimeRepository(FlowModelName, orm.RuntimeRepositoryStub{
		Read: func(_ context.Context, ids []string, fields []string) ([]orm.RecordMap, error) {
			if len(ids) != 1 || ids[0] != "11" {
				t.Fatalf("flow ids = %#v, want [11]", ids)
			}
			_ = fields
			return []orm.RecordMap{{
				"id":      int64(11),
				"name":    "bank_account_import_v1",
				"topic":   "bank",
				"version": int64(1),
			}}, nil
		},
		Write: func(_ context.Context, ids []string, values orm.RecordMap) (bool, error) {
			if len(ids) != 1 || ids[0] != "11" {
				t.Fatalf("flow write ids = %#v, want [11]", ids)
			}
			// The same create also marks the workflow draft (plan T97), a separate write.
			if flowMap, ok := values["flow_map"]; ok {
				wroteFlowMap = stringValue(flowMap)
			}
			return true, nil
		},
	})
	defer restoreFlow()

	createdAny, err := createWorkflowAction(context.Background(), nil, nil, []any{orm.RecordMap{
		"workflow_id": int64(11),
		"action_code": "parse_payload",
		"name":        "Parse Payload",
		"sequence":    int64(10),
	}}, nil)
	if err != nil {
		t.Fatalf("createWorkflowAction() error = %v", err)
	}

	record, ok := createdAny.(orm.RecordMap)
	if !ok || int64Value(record["id"]) != 55 {
		t.Fatalf("created = %#v, want created action record", createdAny)
	}
	if wroteFlowMap == "" {
		t.Fatalf("expected flow_map to be regenerated")
	}
	if !strings.Contains(wroteFlowMap, "```mermaid") {
		t.Fatalf("expected mermaid markdown, got %q", wroteFlowMap)
	}
	if !strings.Contains(wroteFlowMap, "trigger --> parse_payload") {
		t.Fatalf("expected workflow edge, got %q", wroteFlowMap)
	}
}

func TestFlowRecordDecodesManyToOneCompanyPair(t *testing.T) {
	for raw, want := range map[string]int64{`[1,"Base Company"]`: 1, `7`: 7} {
		var flow FlowRecord
		if err := json.Unmarshal([]byte(`{"id":3,"company_id":`+raw+`}`), &flow); err != nil {
			t.Fatalf("company_id %s: %v", raw, err)
		}
		if flow.CompanyID == nil || *flow.CompanyID != want {
			t.Fatalf("company_id %s decoded as %v, want %d", raw, flow.CompanyID, want)
		}
	}
	var flow FlowRecord
	if err := json.Unmarshal([]byte(`{"id":3,"company_id":false}`), &flow); err != nil || flow.CompanyID != nil {
		t.Fatalf("false company_id: err=%v id=%v", err, flow.CompanyID)
	}
}
