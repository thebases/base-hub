package models

// Workflow action secrets at rest (plan T40).
//
// hmac_key, aes_key and authorization on base.workflow.action, and the same keys inside
// extra_params (RuntimeParams merges extra_params into the step params, and the crypto nodes read
// params.key), hold only a credential reference: "@cred:<name>" (base.workflow.credential, sealed)
// or "@syscred:<name>" (sys.credential). The engine resolves the reference when the step runs, so
// the plaintext is never stored on the action, shown in its form, exported or copied. Rows stored
// before this check are moved by the boot migration in workflow_action_secret_migration.go.

import (
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
)

const (
	workflowCredentialRefPrefix    = "@cred:"
	workflowSysCredentialRefPrefix = "@syscred:"
)

// workflowActionSecretColumns are the action columns that must hold a credential reference.
var workflowActionSecretColumns = []string{"hmac_key", "aes_key", "authorization"}

// workflowActionSecretExtraParamKeys are the extra_params keys that reach the same node params.
var workflowActionSecretExtraParamKeys = []string{"key", "hmac_key", "aes_key", "authorization"}

// IsWorkflowCredentialRef reports whether value is "@cred:<name>" or "@syscred:<name>" with a
// non-empty name.
func IsWorkflowCredentialRef(value string) bool {
	trimmed := strings.TrimSpace(value)
	for _, prefix := range []string{workflowCredentialRefPrefix, workflowSysCredentialRefPrefix} {
		if strings.HasPrefix(trimmed, prefix) && strings.TrimSpace(strings.TrimPrefix(trimmed, prefix)) != "" {
			return true
		}
	}
	return false
}

// validateWorkflowActionSecretRefs refuses a literal secret in the normalized action values. Only
// fields present in values are checked, so a write that does not touch them passes.
func validateWorkflowActionSecretRefs(values orm.RecordMap) error {
	for _, field := range workflowActionSecretColumns {
		if raw, exists := values[field]; exists && !isEmptyOrCredentialRef(raw) {
			return workflowActionLiteralSecretError(field)
		}
	}
	extra, _ := values["extra_params"].(map[string]any)
	for _, key := range workflowActionSecretExtraParamKeys {
		if raw, exists := extra[key]; exists && !isEmptyOrCredentialRef(raw) {
			return workflowActionLiteralSecretError("extra_params." + key)
		}
	}
	return nil
}

func workflowActionLiteralSecretError(field string) error {
	return orm.InvalidParamsf("base.workflow.action %s must be a @cred:<name> or @syscred:<name> reference; store the secret as a workflow credential", field)
}

func isEmptyOrCredentialRef(raw any) bool {
	switch typed := raw.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == "" || IsWorkflowCredentialRef(typed)
	}
	return false
}

// redactWorkflowActionLiteralSecrets replaces any literal secret left on an exported action row
// (a database the boot migration could not convert, e.g. no app.encryptKey) with a placeholder
// reference, so the export never carries the plaintext. Importing it creates an action whose
// reference does not resolve until a credential with that name is created.
func redactWorkflowActionLiteralSecrets(row orm.RecordMap) {
	stepKey := strings.TrimSpace(stringValue(row["step_key"]))
	if stepKey == "" {
		stepKey = "action"
	}
	for _, field := range workflowActionSecretColumns {
		if raw, exists := row[field]; exists && !isEmptyOrCredentialRef(raw) {
			row[field] = workflowCredentialRefPrefix + "redacted_" + stepKey + "_" + field
		}
	}
	extra, _ := row["extra_params"].(map[string]any)
	if len(extra) == 0 {
		return
	}
	var cleaned map[string]any
	for _, key := range workflowActionSecretExtraParamKeys {
		if raw, exists := extra[key]; exists && !isEmptyOrCredentialRef(raw) {
			if cleaned == nil {
				cleaned = cloneActionParams(extra)
			}
			cleaned[key] = workflowCredentialRefPrefix + "redacted_" + stepKey + "_extra_" + key
		}
	}
	if cleaned != nil {
		row["extra_params"] = cleaned
	}
}
