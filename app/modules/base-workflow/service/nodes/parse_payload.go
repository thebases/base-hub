package nodes

// File overview:
// - Responsibility: the parse_payload@v1 node — validates the raw payload is JSON and marks
//   it as parsed for downstream steps.
// - Key symbols: `ParsePayload`.

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
		Code:        "parse_payload",
		Version:     1,
		Sequence:    10,
		Label:       "Parse Payload",
		Description: "Parse inbound bytes into a JSON object payload for downstream workflow steps.",
		Category:    "Input",
	}, ParsePayload)
}

// ParsePayload parses the payload handled by this package.
func ParsePayload(_ context.Context, req WorkflowActionRequest) ([]byte, error) {
	var parsed map[string]interface{}
	if err := json.Unmarshal(req.Payload, &parsed); err != nil {
		log.Errorf("parse_payload: %v", err)
		return nil, fmt.Errorf("parse_payload: %w", err)
	}
	parsed["parsed"] = true
	return json.Marshal(parsed)
}
