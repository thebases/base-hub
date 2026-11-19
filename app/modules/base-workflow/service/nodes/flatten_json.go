package nodes

// File overview:
// - Responsibility: the flatten_json@v1 node — flattens nested JSON payload fields to a
//   single-level map via the runtime FlattenJSON hook.
// - Key symbols: `FlattenJSONAction`.

import (
	"context"
	"encoding/json"
	"fmt"

	"git.thebase.vn/base_hub/internal/services/log"
)

// init registers this node's single definition (plan T93): canvas spec, catalog entry and
// param validation all derive from it.
func init() {
	registerNode(nodeDef{
		Code:        "flatten_json",
		Version:     1,
		Sequence:    40,
		Label:       "Flatten JSON",
		Description: "Flatten nested JSON fields into one leaf-key map to simplify placeholder binding.",
		Category:    "Input",
	}, FlattenJSONAction)
}

// FlattenJSONAction flattens the json action handled by this package.
func FlattenJSONAction(_ context.Context, req WorkflowActionRequest) ([]byte, error) {
	if workflowActionRuntimeHooks.FlattenJSON == nil {
		log.Errorf("flatten_json: flatten hook is not initialized")
		return nil, fmt.Errorf("flatten_json: flatten hook is not initialized")
	}
	input, err := decodeJSONObject(req.Payload)
	if err != nil {
		log.Errorf("flatten_json: %v", err)
		return nil, fmt.Errorf("flatten_json: %w", err)
	}

	flattened := map[string]any{}
	workflowActionRuntimeHooks.FlattenJSON(input, flattened)
	return json.Marshal(flattened)
}
