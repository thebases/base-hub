package nodes

// File overview:
// - Responsibility: the step idempotency key as nodes see it (plan T56): `<run name>:<step key>`,
//   the same for every attempt of one step in one run. post_output_object and notify send it as
//   the Idempotency-Key header and name their outbound row after it; lua_script exposes it to the
//   script as the `idempotency_key` global.

import (
	"context"
	"encoding/json"
	"net/textproto"
	"strconv"
	"strings"

	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

// idempotencyKeyHeader is the header outbound nodes send the step key in.
const idempotencyKeyHeader = "Idempotency-Key"

// stepIdempotencyKey is the key the engine bound for the running step, else the same format built
// from the request id and the step key (handlers called outside the engine, tests), else "".
func stepIdempotencyKey(ctx context.Context, req WorkflowActionRequest) string {
	if key := workflowmodels.WorkflowStepIdempotencyKeyFromContext(ctx); key != "" {
		return key
	}
	stepKey := strings.TrimSpace(req.Step.Key)
	if stepKey == "" {
		stepKey = strings.TrimSpace(req.Action.StepKey)
	}
	return workflowmodels.WorkflowStepIdempotencyKey(workflowmodels.WorkflowRequestIDFromContext(ctx), stepKey)
}

// withIdempotencyKeyHeader adds Idempotency-Key unless key is empty or the author already set the
// header (any casing). headers is modified in place and returned.
func withIdempotencyKeyHeader(headers map[string]string, key string) map[string]string {
	if key == "" {
		return headers
	}
	if headers == nil {
		headers = map[string]string{}
	}
	for name := range headers {
		if strings.EqualFold(name, idempotencyKeyHeader) {
			return headers
		}
	}
	headers[textproto.CanonicalMIMEHeaderKey(idempotencyKeyHeader)] = key
	return headers
}

// int64FromAny reads an integer from the shapes a runtime read returns (ints, floats, json.Number,
// numeric strings); anything else is 0.
func int64FromAny(raw any) int64 {
	switch typed := raw.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	case json.Number:
		value, _ := typed.Int64()
		return value
	case string:
		value, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return value
	default:
		return 0
	}
}
