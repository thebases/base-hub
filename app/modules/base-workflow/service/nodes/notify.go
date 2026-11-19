package nodes

// File overview:
// - Responsibility: the notify@v1 node — fire-and-forget outbound HTTP JSON delivery via the
//   shared outbound delivery queue (see service/libs.OutboundDeliveryService), unlike
//   post_output_object which posts synchronously and persists its own audit row.
// - Key symbols: `Notify`.

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

// init registers this node's single definition (plan T93): canvas spec, catalog entry and
// param validation all derive from it.
func init() {
	registerNode(nodeDef{
		Code:        "notify",
		Version:     1,
		Sequence:    30,
		Label:       "Notify",
		Description: "Queue one notification-style outbound JSON delivery for a provider or channel endpoint.",
		Category:    "Delivery",
		Params: []paramDef{
			{Name: "channel", Label: "Channel", Type: "string", Default: "generic_webhook", Column: "channel"},
			{Name: "target_url", Label: "Target URL", Type: "string", Column: "target_ref"},
			{Name: "headers", Label: "Headers", Type: "json", Column: "headers"},
			authorizationParam,
		},
		SideEffect: effectOutbox,
	}, Notify)
}

func Notify(ctx context.Context, req WorkflowActionRequest) ([]byte, error) {
	if workflowActionRuntimeHooks.QueueHTTPJSON == nil {
		log.Errorf("notify: outbound queue hook is not initialized")
		return nil, fmt.Errorf("notify: outbound queue hook is not initialized")
	}
	rawURL := strings.TrimSpace(firstStringParam(req.Step.Params, "url", "target_ref", "callback_url"))
	if rawURL == "" {
		log.Errorf("notify: params.url is required")
		return nil, orm.InvalidParamsf("notify: params.url is required")
	}
	if _, err := url.ParseRequestURI(rawURL); err != nil {
		log.Errorf("notify: invalid url: %v", err)
		return nil, orm.InvalidParamsf("notify: invalid url: %v", err)
	}

	body, err := decodeJSONObject(req.Payload)
	if err != nil {
		log.Errorf("notify: %v", err)
		return nil, fmt.Errorf("notify: %w", err)
	}

	headers, err := actionOutboundHeaders(req.Step.Params)
	if err != nil {
		log.Errorf("notify: %v", err)
		return nil, orm.InvalidParamsf("notify: %v", err)
	}
	// One delivery row per step, named after the step idempotency key and reused by every attempt
	// (plan T53); the receiver gets the same key as Idempotency-Key on every retry (T56).
	idempotencyKey := stepIdempotencyKey(ctx, req)
	headers = withIdempotencyKeyHeader(headers, idempotencyKey)
	ctx = workflowmodels.ContextWithWorkflowStepIdempotencyKey(ctx, idempotencyKey)

	if err := workflowActionRuntimeHooks.QueueHTTPJSON(ctx, req.Topic, rawURL, headers, req.CredentialRefs["authorization"], body); err != nil {
		log.Errorf("notify: queue delivery: %v", err)
		return nil, fmt.Errorf("notify: queue delivery: %w", err)
	}
	return req.Payload, nil
}
