package models

// File overview:
// - Responsibility: defines the `workflow` module model contract for named, encrypted workflow
//   credentials — field metadata, runtime methods, and the encrypt/decrypt hook wiring points.
//   Encryption wiring (key derived from app.encryptKey) lives in `workflow_credential_crypto.go`.
//   `encrypted_value` is never returned by a runtime method and never accepted from a client:
//   callers send the plaintext as `value`.
// - Key symbols: `WorkflowCredentialModel`, `createWorkflowCredential`, `WorkflowCredentialRecord`, `WorkflowCredentialEncryptHook`, `WorkflowCredentialDecryptHook`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"context"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"git.thebase.vn/base_hub/modules/base-credential/secretbox"
)

type WorkflowCredentialRecord struct {
	orm.BaseModel
	WorkflowID     *int64 `bun:"workflow_id" json:"workflow_id" meta:"kind:many2one,model:base.workflow,ondelete:CASCADE,index,label:Workflow,help:Scopes this credential to a specific workflow. Null means the credential is shared across all workflows in the tenant."`
	CompanyID      *int64 `bun:"company_id" json:"company_id" meta:"kind:many2one,model:tbl.company,ondelete:SET NULL,index,label:Company ID,help:Inherits the company scope from the parent workflow when set; otherwise pinned to the creating caller's company."`
	Name           string `bun:"name" json:"name" meta:"index,label:Name,help:Stores the unique reference name used in action params to look up this credential (e.g. my_hmac_key)."`
	CredentialType string `bun:"credential_type" json:"credential_type" meta:"index,label:Credential Type,help:Stores the type of secret stored. Values: hmac_key, aes_key, api_key, password, token, other."`
	Description    string `bun:"description" json:"description" meta:"label:Description,help:Stores human-readable notes about the credential's purpose and usage."`
	EncryptedValue string `bun:"encrypted_value" json:"encrypted_value" meta:"secret,label:Encrypted Value,help:Stores the AES-GCM encrypted secret value. Never returned in API responses."`
}

const WorkflowCredentialModelName = "base.workflow.credential"

var WorkflowCredentialDefinition = orm.ModelDefinition(WorkflowCredentialModelName, (*WorkflowCredentialRecord)(nil))

var WorkflowCredentialModelSpec = orm.ModelSpec{
	Name:        WorkflowCredentialModelName,
	Label:       "Workflow Credential",
	Description: "Named, encrypted credential values referenced by action params",
	Definition:  WorkflowCredentialDefinition,
	// read, search_read, search_fetch, read_group, copy_data and copy never return encrypted_value.
	RuntimeMethods: append([]orm.RuntimeMethod{
		{Name: "create", Public: true, ModelOnly: true, Call: createWorkflowCredential},
		{Name: "write", Public: true, UsesPathID: true, Call: writeWorkflowCredential},
		{Name: "unlink", Public: true, UsesPathID: true, Call: unlinkWorkflowCredential},
	}, secretbox.RedactedReadMethods(workflowCredentialSecretFields...)...),
}

var WorkflowCredentialModel = orm.Define[WorkflowCredentialRecord](&Models, 135, WorkflowCredentialModelSpec)

// WorkflowCredentialEncryptHook and WorkflowCredentialDecryptHook encrypt values under a key
// derived from app.encryptKey (secretbox). They are variables only so tests can swap them.
var (
	WorkflowCredentialEncryptHook func(plaintext string) (string, error)  = sealWorkflowCredentialValue
	WorkflowCredentialDecryptHook func(ciphertext string) (string, error) = openWorkflowCredentialValue
)

type workflowCredentialState struct {
	WorkflowID     *int64
	Name           string
	CredentialType string
	Description    string
	EncryptedValue string
}

func workflowCredentialStateFromRecord(record orm.RecordMap) workflowCredentialState {
	return workflowCredentialState{
		WorkflowID:     int64PointerFromAny(record["workflow_id"]),
		Name:           strings.TrimSpace(stringValue(record["name"])),
		CredentialType: strings.TrimSpace(stringValue(record["credential_type"])),
		Description:    strings.TrimSpace(stringValue(record["description"])),
		EncryptedValue: strings.TrimSpace(stringValue(record["encrypted_value"])),
	}
}

func normalizeWorkflowCredentialValues(_ context.Context, values orm.RecordMap, _ *workflowCredentialState, creating bool) (orm.RecordMap, error) {
	// Only this function writes ciphertext: a client-supplied encrypted_value could store a value
	// encrypted under a key the caller chose.
	if _, exists := values["encrypted_value"]; exists {
		return nil, orm.InvalidParamsf("base.workflow.credential: encrypted_value is not writable; send the secret as value")
	}
	normalized := cloneRecordMap(values)

	if wfID, ok := int64PointerValue(normalized["workflow_id"]); ok {
		normalized["workflow_id"] = wfID
	} else if creating {
		normalized["workflow_id"] = nil
	}

	for _, field := range []string{"name", "credential_type", "description"} {
		if value, ok := stringFromAny(normalized[field]); ok {
			normalized[field] = strings.TrimSpace(value)
		} else if creating {
			normalized[field] = ""
		}
	}

	if creating {
		name := strings.TrimSpace(stringValue(normalized["name"]))
		if name == "" {
			log.Errorf("base.workflow.credential name is required")
			return nil, orm.InvalidParamsf("base.workflow.credential name is required")
		}
		credType := strings.TrimSpace(stringValue(normalized["credential_type"]))
		if credType == "" {
			normalized["credential_type"] = "other"
		}
	}

	// Encrypt the plaintext value when a raw "value" key is supplied.
	// The plaintext is never persisted; only the encrypted form is stored.
	if rawValue, exists := normalized["value"]; exists {
		plaintext := strings.TrimSpace(stringValue(rawValue))
		delete(normalized, "value")
		if plaintext != "" {
			if WorkflowCredentialEncryptHook == nil {
				log.Errorf("base.workflow.credential encrypt hook is not initialized")
				return nil, fmt.Errorf("base.workflow.credential encrypt hook is not initialized")
			}
			encrypted, err := WorkflowCredentialEncryptHook(plaintext)
			if err != nil {
				log.Errorf("base.workflow.credential encrypt: %v", err)
				return nil, fmt.Errorf("base.workflow.credential encrypt: %w", err)
			}
			normalized["encrypted_value"] = encrypted
		}
	}
	return normalized, nil
}

func createWorkflowCredential(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	valsList, err := recordMapListFromArgs(args, kwargs)
	if err != nil {
		log.Errorf("base.workflow.credential create: %v", err)
		return nil, fmt.Errorf("base.workflow.credential create: %w", err)
	}
	results := make([]orm.RecordMap, 0, len(valsList))
	for _, values := range valsList {
		normalized, err := normalizeWorkflowCredentialValues(ctx, values, nil, true)
		if err != nil {
			return nil, err
		}
		// Tenant-shared credentials (workflow_id == nil) get the caller's own
		// session company pinned; workflow-scoped credentials inherit the
		// parent workflow's company instead so the two never diverge.
		if err := inheritWorkflowChildCompanyID(ctx, model, normalized); err != nil {
			return nil, err
		}
		workflowID, _ := int64PointerValue(normalized["workflow_id"])
		companyID, _ := int64PointerValue(normalized["company_id"])
		if err := ensureWorkflowCredentialNameFree(ctx, model, stringValue(normalized["name"]), workflowID, companyID, 0); err != nil {
			return nil, err
		}
		created, err := orm.CreateRecord(ctx, orm.RuntimeModelRef(WorkflowCredentialModelName, model), normalized)
		if err != nil {
			log.Errorf("base.workflow.credential create: %v", err)
			return nil, fmt.Errorf("base.workflow.credential create: %w", err)
		}
		delete(created, "encrypted_value")
		results = append(results, created)
	}
	if len(results) == 1 {
		return results[0], nil
	}
	return results, nil
}

func writeWorkflowCredential(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	if len(ids) == 0 {
		log.Errorf("base.workflow.credential write requires ids")
		return false, orm.InvalidParamsf("base.workflow.credential write requires ids")
	}
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		log.Errorf("base.workflow.credential write: %v", err)
		return false, fmt.Errorf("base.workflow.credential write: %w", err)
	}
	if values == nil {
		values = orm.RecordMap{}
	}
	existing, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(WorkflowCredentialModelName, model), ids, nil)
	if err != nil {
		return false, err
	}
	var current *workflowCredentialState
	if len(existing) > 0 {
		state := workflowCredentialStateFromRecord(existing[0])
		current = &state
	}
	normalized, err := normalizeWorkflowCredentialValues(ctx, values, current, false)
	if err != nil {
		return false, err
	}
	if err := applyWorkflowCredentialCompanyOnWrite(ctx, model, existing, normalized); err != nil {
		return false, err
	}
	updated, err := orm.WriteRecords(ctx, orm.RuntimeModelRef(WorkflowCredentialModelName, model), ids, normalized)
	if err != nil {
		log.Errorf("base.workflow.credential write: %v", err)
		return false, fmt.Errorf("base.workflow.credential write: %w", err)
	}
	return secretbox.Redact(updated, workflowCredentialSecretFields...), nil
}

func unlinkWorkflowCredential(ctx context.Context, model *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	if len(ids) == 0 {
		log.Errorf("base.workflow.credential unlink requires ids")
		return false, orm.InvalidParamsf("base.workflow.credential unlink requires ids")
	}
	deleted, err := orm.RuntimeRepositoryUnlink(ctx, orm.RuntimeModelRef(WorkflowCredentialModelName, model), ids)
	if err != nil {
		log.Errorf("base.workflow.credential unlink: %v", err)
		return false, fmt.Errorf("base.workflow.credential unlink: %w", err)
	}
	return deleted, nil
}

// LookupWorkflowCredential fetches and decrypts the plaintext value of a named credential.
// It is called by the execution engine to resolve @cred:<name> references in action params.
// The workflow scope bound to ctx (ContextWithWorkflowScope) limits the candidates to that
// workflow's credentials and the shared ones of its company (plan T41); without a scope only
// tenant-wide credentials (no workflow, no company) resolve.
func LookupWorkflowCredential(ctx context.Context, name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", orm.InvalidParamsf("base.workflow.credential: name is required")
	}
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WorkflowCredentialModelName, nil), orm.RecordMap{
		"name": strings.TrimSpace(name),
	}, []string{"id", "name", "workflow_id", "company_id", "encrypted_value"})
	if err != nil {
		return "", fmt.Errorf("base.workflow.credential lookup %q: %w", name, err)
	}
	scope, scoped := WorkflowScopeFromContext(ctx)
	row := pickScopedWorkflowCredential(rows, scope, scoped)
	if row == nil {
		return "", orm.NotFoundf("base.workflow.credential %q not found", name)
	}
	encrypted := strings.TrimSpace(stringValue(row["encrypted_value"]))
	if encrypted == "" {
		return "", nil
	}
	if WorkflowCredentialDecryptHook == nil {
		return "", fmt.Errorf("base.workflow.credential decrypt hook is not initialized")
	}
	return WorkflowCredentialDecryptHook(encrypted)
}

// applyWorkflowCredentialCompanyOnWrite keeps a workflow-scoped credential in its workflow's
// company (a sent company_id is dropped, a workflow_id change re-inherits; a shared credential
// keeps an explicit company_id) and refuses a name already used in the resulting scope (T41).
func applyWorkflowCredentialCompanyOnWrite(ctx context.Context, model *orm.ModelMetadata, existing []orm.RecordMap, normalized orm.RecordMap) error {
	for _, row := range existing {
		workflowID := int64PointerFromAny(row["workflow_id"])
		if raw, exists := normalized["workflow_id"]; exists {
			workflowID, _ = int64PointerValue(raw)
		}
		companyID := int64PointerFromAny(row["company_id"])
		if workflowID != nil {
			inherited, err := workflowCompanyID(ctx, *workflowID)
			if err != nil {
				return err
			}
			if _, sent := normalized["company_id"]; sent || !sameInt64Pointer(inherited, companyID) {
				normalized["company_id"] = companyIDValue(inherited)
			}
			companyID = inherited
		} else if raw, exists := normalized["company_id"]; exists {
			companyID, _ = int64PointerValue(raw)
		}
		name := stringValue(row["name"])
		if raw, exists := normalized["name"]; exists {
			name = stringValue(raw)
		}
		if err := ensureWorkflowCredentialNameFree(ctx, model, name, workflowID, companyID, int64Value(row["id"])); err != nil {
			return err
		}
	}
	return nil
}
