package nodes

// File overview:
// - Responsibility: the post_output_object@v1 node — posts the payload synchronously to a
//   target URL and persists its own base.workflow.outbound audit row via CRUDModel, unlike
//   notify which queues delivery asynchronously through the shared outbound worker.
// - Key symbols: `PostOutputObject`.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

// init registers this node's single definition (plan T93): canvas spec, catalog entry and
// param validation all derive from it.
func init() {
	registerNode(nodeDef{
		Code:        "post_output_object",
		Version:     1,
		Sequence:    60,
		Label:       "Post Output Object",
		Description: "Queue one outbound webhook delivery using the current JSON object payload.",
		Category:    "Delivery",
		Params: []paramDef{
			{Name: "target_url", Label: "Target URL", Type: "string", Required: true, Column: "target_ref"},
			{Name: "headers", Label: "Headers", Type: "json", Column: "headers"},
			authorizationParam,
		},
		SideEffect: effectHTTP,
	}, PostOutputObject)
}

// postOutputErrorBodyLimit caps how much of a non-2xx response body goes into the step error.
const postOutputErrorBodyLimit = 512

// PostOutputObject posts the payload to params.url and records the attempt on one
// base.workflow.outbound row per step (plan T53): the row is named after the step idempotency key
// (`<request id>:<step key>`) and reused by every attempt of the step, so a retry or a second
// post_output_object step in the same run never collides on the unique name. Only a 2xx response
// succeeds: any other status fails the step (the run's retry policy decides what happens next).
// The request carries `Idempotency-Key: <step key>` unless the author set that header.
func PostOutputObject(ctx context.Context, req WorkflowActionRequest) ([]byte, error) {
	if workflowActionRuntimeHooks.PostHTTPJSON == nil {
		log.Errorf("post_output_object: http post hook is not initialized")
		return nil, fmt.Errorf("post_output_object: http post hook is not initialized")
	}
	rawURL := strings.TrimSpace(firstStringParam(req.Step.Params, "url", "target_ref", "callback_url"))
	if rawURL == "" {
		log.Errorf("post_output_object: params.url is required")
		return nil, orm.InvalidParamsf("post_output_object: params.url is required")
	}
	if _, err := url.ParseRequestURI(rawURL); err != nil {
		log.Errorf("post_output_object: invalid url: %v", err)
		return nil, orm.InvalidParamsf("post_output_object: invalid url: %v", err)
	}

	body, err := decodeJSONObject(req.Payload)
	if err != nil {
		log.Errorf("post_output_object: %v", err)
		return nil, fmt.Errorf("post_output_object: %w", err)
	}

	headers, err := actionOutboundHeaders(req.Step.Params)
	if err != nil {
		log.Errorf("post_output_object: %v", err)
		return nil, orm.InvalidParamsf("post_output_object: %v", err)
	}
	idempotencyKey := stepIdempotencyKey(ctx, req)
	headers = withIdempotencyKeyHeader(headers, idempotencyKey)

	deliveryID, err := savePostOutputDeliveryRecord(ctx, req, idempotencyKey, rawURL, body, headers)
	if err != nil {
		return nil, err
	}

	statusCode, responseBody, postErr := workflowActionRuntimeHooks.PostHTTPJSON(ctx, rawURL, headers, body)
	status := outboundDeliveryStatusFromHTTP(statusCode, postErr)
	lastError := ""
	if postErr != nil {
		lastError = strings.TrimSpace(postErr.Error())
	} else if status != workflowmodels.OutboundDeliveryStatusDelivered {
		lastError = fmt.Sprintf("http status=%d", statusCode)
	}

	if updateErr := updatePostOutputDeliveryRecord(ctx, req, deliveryID, status, responseBody, lastError); updateErr != nil {
		return nil, updateErr
	}
	if postErr != nil {
		log.Errorf("post_output_object: post http json: %v", postErr)
		return nil, fmt.Errorf("post_output_object: post http json: %w", postErr)
	}
	if status != workflowmodels.OutboundDeliveryStatusDelivered {
		log.Errorf("post_output_object: %s answered http %d", rawURL, statusCode)
		return nil, fmt.Errorf("post_output_object: http status %d: %s", statusCode, truncateForError(responseBody, postOutputErrorBodyLimit))
	}
	return req.Payload, nil
}

// truncateForError keeps at most limit bytes of text (on a rune boundary) for an error message.
func truncateForError(text string, limit int) string {
	text = strings.TrimSpace(text)
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "...(truncated)"
}

// savePostOutputDeliveryRecord creates the step's audit row, or, when an earlier attempt of the
// same step already created it (same name), points that row at this attempt.
func savePostOutputDeliveryRecord(ctx context.Context, req WorkflowActionRequest, name string, rawURL string, body map[string]any, headers map[string]string) (string, error) {
	if name != "" {
		existing, attempts, found, err := findPostOutputDeliveryRecord(ctx, name)
		if err != nil {
			return "", err
		}
		if found {
			return existing, reusePostOutputDeliveryRecord(ctx, req, existing, attempts, rawURL, body, headers)
		}
	} else {
		name = uniqueOutboundDeliveryName(ctx)
	}
	id, err := createPostOutputDeliveryRecord(ctx, req, name, rawURL, body, headers)
	if err == nil {
		return id, nil
	}
	// Lost a create race on the unique name to another attempt of this step: reuse its row.
	if existing, attempts, found, findErr := findPostOutputDeliveryRecord(ctx, name); findErr == nil && found {
		return existing, reusePostOutputDeliveryRecord(ctx, req, existing, attempts, rawURL, body, headers)
	}
	return "", err
}

// findPostOutputDeliveryRecord returns the id and attempt count of the outbound row named name.
func findPostOutputDeliveryRecord(ctx context.Context, name string) (string, int64, bool, error) {
	if workflowActionRuntimeHooks.SearchModelRecords == nil {
		return "", 0, false, nil
	}
	rows, err := workflowActionRuntimeHooks.SearchModelRecords(workflowmodels.OutboundEngineContext(ctx), workflowmodels.OutboundDeliveryModelName, map[string]any{"name": name}, []string{"id", "attempt_count"})
	if err != nil {
		log.Errorf("post_output_object: look up outbound delivery %q: %v", name, err)
		return "", 0, false, fmt.Errorf("post_output_object: look up outbound delivery %q: %w", name, err)
	}
	if len(rows) == 0 {
		return "", 0, false, nil
	}
	id := stringIDFromAny(rows[0]["id"])
	return id, int64FromAny(rows[0]["attempt_count"]), id != "", nil
}

// postOutputRowValues are the fields an attempt (first or later) writes on the step's row. The row
// keeps the credential reference, never the resolved Authorization header (plan T40), and the
// running workflow's company and id (T41/T53). Those are engine-owned values a run under the
// caller's env could not set, so the row is written as sudo (OutboundEngineContext).
func postOutputRowValues(ctx context.Context, req WorkflowActionRequest, rawURL string, body map[string]any, headers map[string]string) map[string]any {
	authorizationRef := strings.TrimSpace(req.CredentialRefs["authorization"])
	storedHeaders := headers
	if authorizationRef != "" {
		storedHeaders = headersWithoutAuthorization(headers)
	}
	var companyID, workflowID any
	if scope, ok := workflowmodels.WorkflowScopeFromContext(ctx); ok {
		if scope.CompanyID != nil {
			companyID = *scope.CompanyID
		}
		if scope.WorkflowID > 0 {
			workflowID = scope.WorkflowID
		}
	}
	return map[string]any{
		"topic":             strings.TrimSpace(req.Topic),
		"delivery_type":     workflowmodels.OutboundDeliveryTypeHTTPJSONSync,
		"target_ref":        rawURL,
		"headers":           headersToAny(storedHeaders),
		"authorization_ref": authorizationRef,
		"company_id":        companyID,
		"workflow_id":       workflowID,
		"request_body":      cloneJSONObject(body),
		"response_body":     "",
		"last_error":        "",
		"status":            workflowmodels.OutboundDeliveryStatusQueued,
	}
}

// reusePostOutputDeliveryRecord resets the step's existing row for this attempt. attempt_count
// and max_attempts move together so the row is never claimable by the outbox sweeper.
func reusePostOutputDeliveryRecord(ctx context.Context, req WorkflowActionRequest, deliveryID string, previousAttempts int64, rawURL string, body map[string]any, headers map[string]string) error {
	values := postOutputRowValues(ctx, req, rawURL, body, headers)
	attempt := previousAttempts + 1
	values["attempt_count"] = attempt
	values["max_attempts"] = attempt
	values["next_attempt_at"] = int64(0)
	if workflowActionRuntimeHooks.WriteModelRecords == nil {
		return fmt.Errorf("post_output_object: write hook is not initialized")
	}
	if _, err := workflowActionRuntimeHooks.WriteModelRecords(workflowmodels.OutboundEngineContext(ctx), workflowmodels.OutboundDeliveryModelName, []string{deliveryID}, values); err != nil {
		log.Errorf("post_output_object: reuse outbound delivery %s: %v", deliveryID, err)
		return fmt.Errorf("post_output_object: reuse outbound delivery %s: %w", deliveryID, err)
	}
	return nil
}

func createPostOutputDeliveryRecord(ctx context.Context, req WorkflowActionRequest, name string, rawURL string, body map[string]any, headers map[string]string) (string, error) {
	values := postOutputRowValues(ctx, req, rawURL, body, headers)
	values["workflow_request_id"] = workflowmodels.WorkflowRequestIDFromContext(ctx)
	values["name"] = name
	values["attempt_count"] = 1
	values["max_attempts"] = 1
	createCtx := workflowmodels.OutboundEngineContext(ctx)
	crudOutput, err := CRUDModel(createCtx, WorkflowActionRequest{
		Topic: req.Topic,
		Step: workflowmodels.Step{Params: map[string]any{
			"model":        workflowmodels.OutboundDeliveryModelName,
			"operation":    "create",
			"merge_result": false,
			"values":       values,
		}},
		Payload: req.Payload,
	})
	if err != nil {
		log.Errorf("post_output_object: create outbound delivery: %v", err)
		return "", fmt.Errorf("post_output_object: create outbound delivery: %w", err)
	}

	record, err := decodeJSONObject(crudOutput)
	if err != nil {
		log.Errorf("post_output_object: decode created outbound delivery: %v", err)
		return "", fmt.Errorf("post_output_object: decode created outbound delivery: %w", err)
	}
	deliveryID := stringIDFromAny(record["id"])
	if deliveryID == "" {
		log.Errorf("post_output_object: created outbound delivery is missing id")
		return "", fmt.Errorf("post_output_object: created outbound delivery is missing id")
	}
	return deliveryID, nil
}

func updatePostOutputDeliveryRecord(ctx context.Context, req WorkflowActionRequest, deliveryID string, status string, responseBody string, lastError string) error {
	values := map[string]any{
		"status":        status,
		"response_body": strings.TrimSpace(responseBody),
		"last_error":    strings.TrimSpace(lastError),
	}
	// Keep retryable_failed rows aligned with the outbound model invariant:
	// the next attempt timestamp must be present whenever the status says the
	// failure is retryable, even if the workflow engine decides the actual
	// retry behavior at a higher level.
	if status == workflowmodels.OutboundDeliveryStatusRetryableFailed {
		values["next_attempt_at"] = retryableFailureNextAttemptAt()
	}
	_, err := CRUDModel(ctx, WorkflowActionRequest{
		Topic: req.Topic,
		Step: workflowmodels.Step{Params: map[string]any{
			"model":        workflowmodels.OutboundDeliveryModelName,
			"operation":    "write",
			"merge_result": false,
			"ids":          []string{deliveryID},
			"values":       values,
		}},
		Payload: req.Payload,
	})
	if err != nil {
		log.Errorf("post_output_object: update outbound delivery %s: %v", deliveryID, err)
		return fmt.Errorf("post_output_object: update outbound delivery %s: %w", deliveryID, err)
	}
	return nil
}

func outboundDeliveryStatusFromHTTP(statusCode int, postErr error) string {
	if postErr != nil {
		return workflowmodels.OutboundDeliveryStatusRetryableFailed
	}
	if statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices {
		return workflowmodels.OutboundDeliveryStatusDelivered
	}
	if statusCode >= http.StatusBadRequest && statusCode < http.StatusInternalServerError && statusCode != http.StatusTooManyRequests {
		return workflowmodels.OutboundDeliveryStatusPermanentFailed
	}
	return workflowmodels.OutboundDeliveryStatusRetryableFailed
}

// uniqueOutboundDeliveryName is the fallback row name when the step has no idempotency key.
func uniqueOutboundDeliveryName(ctx context.Context) string {
	requestID := workflowmodels.WorkflowRequestIDFromContext(ctx)
	if requestID == "" {
		return uuid4String()
	}
	return fmt.Sprintf("%s-%s", requestID, uuid4String())
}

func retryableFailureNextAttemptAt() int64 {
	return time.Now().UTC().Add(time.Duration(workflowmodels.DefaultRetryPolicy().RetryCoolDown) * time.Second).Unix()
}

// headersWithoutAuthorization returns headers minus the Authorization header (any casing).
func headersWithoutAuthorization(headers map[string]string) map[string]string {
	out := make(map[string]string, len(headers))
	for key, value := range headers {
		if strings.EqualFold(key, "Authorization") {
			continue
		}
		out[key] = value
	}
	return out
}
