package models

// File overview:
// - Responsibility: encryption wiring for `base.workflow.credential`. Values are sealed by
//   base-credential's `secretbox` under a key derived from app.encryptKey with this model's label;
//   legacy zero-key values are re-encrypted on every migrated database and the table takes part in
//   `rotate-encrypt-key` (registered in init below).
// - Key symbols: `workflowCredentialSecretLabel`, `sealWorkflowCredentialValue`,
//   `openWorkflowCredentialValue`.

import "git.thebase.vn/base_hub/modules/base-credential/secretbox"

// workflowCredentialSecretLabel scopes the key derived from app.encryptKey to this model.
const workflowCredentialSecretLabel = WorkflowCredentialModelName

// workflowCredentialSecretFields are stored encrypted and never returned by a runtime method.
var workflowCredentialSecretFields = []string{"encrypted_value"}

func sealWorkflowCredentialValue(plaintext string) (string, error) {
	return secretbox.Seal(secretbox.AppKey(), workflowCredentialSecretLabel, plaintext)
}

func openWorkflowCredentialValue(ciphertext string) (string, error) {
	return secretbox.Open(secretbox.AppKey(), workflowCredentialSecretLabel, ciphertext)
}

func init() {
	secretbox.RegisterSecretTable("base-workflow", WorkflowCredentialModelSpec.ResolveStorageName(), workflowCredentialSecretLabel, workflowCredentialSecretFields...)
}
