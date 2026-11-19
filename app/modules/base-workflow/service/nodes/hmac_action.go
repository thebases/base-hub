package nodes

// File overview:
// - Responsibility: the hmac_action@v1 node — computes an HMAC-SHA256 signature over the
//   payload via the runtime HMACAction hook.
// - Key symbols: `HmacAction`.

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
		Code:        "hmac_action",
		Version:     1,
		Sequence:    70,
		Label:       "HMAC Action",
		Description: "Compute an HMAC signature from the current JSON payload and append it into the output object.",
		Category:    "Security",
		Params: []paramDef{
			{Name: "hmac_key", Label: "HMAC Key", Type: "string", Column: "hmac_key", Secret: secretCredentialRef},
			{Name: "output_field", Label: "Output Field", Type: "string", Default: "signature", Column: "output_field"},
			{Name: "include_key", Label: "Include Key", Type: "boolean", Column: "include_key"},
		},
	}, HmacAction)
}

func HmacAction(_ context.Context, req WorkflowActionRequest) ([]byte, error) {
	if workflowActionRuntimeHooks.HMACAction == nil {
		log.Errorf("hmac_action: hmac hook is not initialized")
		return nil, fmt.Errorf("hmac_action: hmac hook is not initialized")
	}
	input, err := decodeJSONObject(req.Payload)
	if err != nil {
		log.Errorf("hmac_action: %v", err)
		return nil, fmt.Errorf("hmac_action: %w", err)
	}

	output := cloneJSONObject(input)
	key := strings.TrimSpace(firstStringParam(req.Step.Params, "key", "hmac_key"))
	keyList := stringSliceParam(req.Step.Params, "key_list", "keys")
	outputField := strings.TrimSpace(firstStringParam(req.Step.Params, "output_field"))
	if outputField == "" {
		outputField = "signature"
	}

	generatedKey, rawData, signature, err := workflowActionRuntimeHooks.HMACAction(key, output, keyList)
	if err != nil {
		log.Errorf("hmac_action: %v", err)
		return nil, fmt.Errorf("hmac_action: %w", err)
	}

	output[outputField] = signature
	if boolParamDefault(req.Step.Params, "include_key", false) {
		output["_hmac_key"] = generatedKey
	}
	if boolParamDefault(req.Step.Params, "include_raw", false) {
		output["_hmac_raw"] = rawData
	}
	return json.Marshal(output)
}
