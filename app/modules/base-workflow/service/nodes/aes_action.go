package nodes

// File overview:
// - Responsibility: the aes_action@v1 node — encrypts the payload with AES-CBC via the
//   runtime AESAction hook.
// - Key symbols: `AesAction`.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/services/log"
)

// init registers this node's single definition (plan T93): canvas spec, catalog entry and
// param validation all derive from it.
func init() {
	registerNode(nodeDef{
		Code:        "aes_action",
		Version:     1,
		Sequence:    80,
		Label:       "AES Action",
		Description: "Encrypt the current JSON payload and wrap it into a transport-ready JSON object.",
		Category:    "Security",
		Params: []paramDef{
			{Name: "aes_key", Label: "AES Key", Type: "string", Column: "aes_key", Secret: secretCredentialRef},
			{Name: "output_field", Label: "Output Field", Type: "string", Default: "data", Column: "output_field"},
			{Name: "include_key", Label: "Include Key", Type: "boolean", Column: "include_key"},
		},
	}, AesAction)
}

func AesAction(_ context.Context, req WorkflowActionRequest) ([]byte, error) {
	if workflowActionRuntimeHooks.AESAction == nil {
		log.Errorf("aes_action: aes hook is not initialized")
		return nil, fmt.Errorf("aes_action: aes hook is not initialized")
	}
	input, err := decodeJSONObject(req.Payload)
	if err != nil {
		log.Errorf("aes_action: %v", err)
		return nil, fmt.Errorf("aes_action: %w", err)
	}

	key := strings.TrimSpace(firstStringParam(req.Step.Params, "key", "aes_key"))
	outputField := strings.TrimSpace(firstStringParam(req.Step.Params, "output_field"))
	if outputField == "" {
		outputField = "data"
	}

	generatedKey, minifiedJSON, encryptedData, err := workflowActionRuntimeHooks.AESAction(key, input)
	if err != nil {
		log.Errorf("aes_action: %v", err)
		return nil, fmt.Errorf("aes_action: %w", err)
	}

	output := map[string]any{
		outputField: encryptedData,
	}
	if boolParamDefault(req.Step.Params, "include_key", false) {
		output["_aes_key"] = generatedKey
	}
	if boolParamDefault(req.Step.Params, "include_raw", false) {
		output["_aes_raw"] = minifiedJSON
	}
	return json.Marshal(output)
}
