package nodes

// File overview:
// - Responsibility: the aes_decrypt_action@v1 node — decrypts an AES-wrapped payload field
//   back into a JSON object via the runtime AESDecryptAction hook.
// - Key symbols: `AesDecryptAction`.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/services/log"
)

// init registers this node's single definition (plan T93): canvas spec, catalog entry and
// param validation all derive from it.
func init() {
	registerNode(nodeDef{
		Code:        "aes_decrypt_action",
		Version:     1,
		Sequence:    90,
		Label:       "AES Decrypt Action",
		Description: "Decrypt one AES-wrapped payload field and restore the original JSON object.",
		Category:    "Security",
		Params: []paramDef{
			{Name: "input_field", Label: "Input Field", Type: "string", Default: "data", ExtraKey: "input_field"},
			{Name: "key_field", Label: "Key Field", Type: "string", ExtraKey: "key_field"},
			{Name: "aes_key", Label: "AES Key", Type: "string", Column: "aes_key", Secret: secretCredentialRef},
		},
	}, AesDecryptAction)
}

// errAESDecryptFailed is the only decrypt failure callers see.
var errAESDecryptFailed = errors.New("aes_decrypt_action: decrypt failed")

// AesDecryptAction decrypts an AES-wrapped payload field back into a JSON object.
func AesDecryptAction(_ context.Context, req WorkflowActionRequest) ([]byte, error) {
	if workflowActionRuntimeHooks.AESDecryptAction == nil {
		log.Errorf("aes_decrypt_action: aes decrypt hook is not initialized")
		return nil, fmt.Errorf("aes_decrypt_action: aes decrypt hook is not initialized")
	}
	input, err := decodeJSONObject(req.Payload)
	if err != nil {
		log.Errorf("aes_decrypt_action: %v", err)
		return nil, fmt.Errorf("aes_decrypt_action: %w", err)
	}

	inputField := strings.TrimSpace(firstStringParam(req.Step.Params, "input_field"))
	if inputField == "" {
		inputField = "data"
	}
	rawCiphertext := strings.TrimSpace(stringValue(input[inputField]))
	if rawCiphertext == "" {
		log.Errorf("aes_decrypt_action: params.%s is required in payload", inputField)
		return nil, fmt.Errorf("aes_decrypt_action: params.%s is required in payload", inputField)
	}

	key := strings.TrimSpace(firstStringParam(req.Step.Params, "key", "aes_key"))
	if key == "" {
		keyField := strings.TrimSpace(firstStringParam(req.Step.Params, "key_field"))
		if keyField == "" {
			keyField = "_aes_key"
		}
		key = strings.TrimSpace(stringValue(input[keyField]))
	}
	if key == "" {
		log.Errorf("aes_decrypt_action: aes key is required")
		return nil, fmt.Errorf("aes_decrypt_action: aes key is required")
	}

	// One error text for a bad key, bad ciphertext/padding and non-JSON plaintext (WF-S-6): the
	// step error lands in run history and on webhook replies, so it must not tell them apart.
	// The detail goes to the server log only.
	plainText, err := workflowActionRuntimeHooks.AESDecryptAction(key, rawCiphertext)
	if err != nil {
		log.Errorf("aes_decrypt_action: %v", err)
		return nil, errAESDecryptFailed
	}

	output := map[string]any{}
	if err := json.Unmarshal([]byte(plainText), &output); err != nil {
		log.Errorf("aes_decrypt_action: decode decrypted json: %v", err)
		return nil, errAESDecryptFailed
	}
	if boolParamDefault(req.Step.Params, "include_key", false) {
		output["_aes_key"] = key
	}
	if boolParamDefault(req.Step.Params, "include_raw", false) {
		output["_aes_raw"] = plainText
	}
	return json.Marshal(output)
}
