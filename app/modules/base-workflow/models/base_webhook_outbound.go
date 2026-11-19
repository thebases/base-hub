package models

// File overview:
// - Responsibility: defines the `workflow` module model contract for base outbound delivery, including field metadata, runtime methods, and module-specific invariants.
// - Key symbols: `init`, `createOutboundDelivery`, `OutboundDeliveryRecord`, `outboundDeliveryState`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

const (
	OutboundDeliveryStatusQueued          = "queued"
	OutboundDeliveryStatusSending         = "sending"
	OutboundDeliveryStatusDelivered       = "delivered"
	OutboundDeliveryStatusRetryableFailed = "retryable_failed"
	OutboundDeliveryStatusPermanentFailed = "permanent_failed"
)

// Delivery types. Only http_json rows (notify) are sent by the outbox sweeper; http_json_sync rows
// are the audit trail of post_output_object, whose retries belong to the workflow run (plan T53).
const (
	OutboundDeliveryTypeHTTPJSON     = "http_json"
	OutboundDeliveryTypeHTTPJSONSync = "http_json_sync"
)

type OutboundDeliveryRecord struct {
	orm.BaseModel
	CompanyID *int64 `bun:"company_id" json:"company_id" meta:"kind:many2one,model:tbl.company,ondelete:SET NULL,index,label:Company ID,help:Inherits the company of the workflow that sent this delivery."`
	// WorkflowID is the workflow that queued the delivery: the outbox sweeper resolves
	// authorization_ref in that workflow's credential scope (plan T53). Engine-owned.
	WorkflowID   *int64                 `bun:"workflow_id" json:"workflow_id" meta:"index,readonly,label:Workflow ID,help:Workflow that queued this delivery. Its credentials are used when the delivery is retried."`
	Name         string                 `bun:"name,unique" json:"name" meta:"unique,label:Request ID,help:Stores the outbound delivery request ID for this record."`
	Topic        string                 `bun:"topic" json:"topic" meta:"index,filter,label:Topic,help:Stores the topic value for this record."`
	DeliveryType string                 `bun:"delivery_type" json:"delivery_type" meta:"index,filter,label:Delivery Type,help:Stores the delivery type value for this record."`
	TargetRef    string                 `bun:"target_ref" json:"target_ref" meta:"index,label:Target Ref,help:Stores the target ref value for this record."`
	Headers      map[string]interface{} `bun:"headers,type:jsonb,nullzero" json:"headers" meta:"kind:json,label:Headers,help:Stores the headers payload in JSON format."`
	// AuthorizationRef keeps the credential reference instead of the resolved header (plan T40).
	AuthorizationRef string                 `bun:"authorization_ref" json:"authorization_ref" meta:"label:Authorization Reference,help:Stores the @cred:<name> or @syscred:<name> reference the Authorization header was resolved from. The resolved header is never stored; a manual retry resolves the reference again."`
	RequestBody      map[string]interface{} `bun:"request_body,type:jsonb,nullzero" json:"request_body" meta:"kind:json,label:Request Body,help:Stores the request body payload in JSON format."`
	ResponseBody     string                 `bun:"response_body" json:"response_body" meta:"label:Response Body,help:Stores the raw response body returned by the target ref after the outbound request."`
	Status           string                 `bun:"status" json:"status" meta:"index,filter,label:Status,help:Stores the current status for this record."`
	AttemptCount     int                    `bun:"attempt_count" json:"attempt_count" meta:"label:Attempt Count,help:Stores the numeric value for attempt count."`
	MaxAttempts      int                    `bun:"max_attempts" json:"max_attempts" meta:"label:Max Attempts,help:Stores the numeric value for max attempts."`
	LastError        string                 `bun:"last_error" json:"last_error" meta:"label:Last Error,help:Stores the last error value for this record."`
	NextAttemptAt    int64                  `bun:"next_attempt_at" json:"next_attempt_at" meta:"index,label:Next Attempt At,help:Stores the numeric value for next attempt at."`
	LastAttemptAt    int64                  `bun:"last_attempt_at" json:"last_attempt_at" meta:"label:Last Attempt At,help:Stores the numeric value for last attempt at."`
}

var OutboundDeliveryModelName = "base.workflow.outbound"

var OutboundDeliveryModelSpec = orm.ModelSpec{
	Name:        OutboundDeliveryModelName,
	Label:       "Outbound Delivery",
	Description: "Durable outbound delivery attempt record for HTTP or MQTT targets",
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "create", Public: true, ModelOnly: true, Call: createOutboundDelivery},
		{Name: "write", Public: true, UsesPathID: true, Call: writeOutboundDelivery},
		{Name: "manual_retry", Public: true, UsesPathID: true, Call: manualRetryOutboundDelivery},
		guardedOutboundFindOrCreate(),
	},
}.WithStorageName("base_outbound_delivery")

// OutboundDeliveryModel should be customized by composing small helpers and
// handler options around the base spec instead of centralizing all behavior in
// one customize function.
var OutboundDeliveryModel = orm.Define[OutboundDeliveryRecord](&Models, 120, OutboundDeliveryModelSpec)

type OutboundDeliveryManualRetryRequest struct {
	DeliveryID int64
	Topic      string
	TargetRef  string
	Headers    map[string]string
	// AuthorizationRef is resolved again by the retry (within CompanyID) and sent as the
	// Authorization header.
	AuthorizationRef string
	CompanyID        *int64
	// WorkflowID scopes the credential lookup to the workflow that queued the row (0 = company only).
	WorkflowID  int64
	RequestBody map[string]any
	MaxAttempts int
}

type outboundDeliveryManualRetryFunc func(context.Context, OutboundDeliveryManualRetryRequest) error

var runOutboundDeliveryManualRetry outboundDeliveryManualRetryFunc = func(_ context.Context, _ OutboundDeliveryManualRetryRequest) error {
	return fmt.Errorf("base.workflow.outbound manual retry is not registered")
}

// RegisterOutboundDeliveryManualRetry wires the runtime retry executor without
// forcing the models package to import the workflow service package.
func RegisterOutboundDeliveryManualRetry(fn outboundDeliveryManualRetryFunc) {
	if fn == nil {
		return
	}
	runOutboundDeliveryManualRetry = fn
}

func init() {
	orm.RegisterLegacyDataMigration("base-workflow: outbound sweeper index", ensureOutboundSweepIndex)
}

// createOutboundDelivery creates the outbound delivery handled by this package.
func createOutboundDelivery(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	valuesList, err := recordMapListFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if len(valuesList) == 0 {
		log.Errorf("base.workflow.outbound.create requires values")
		return nil, orm.InvalidParamsf("base.workflow.outbound.create requires values")
	}

	created := make([]orm.RecordMap, 0, len(valuesList))
	for _, values := range valuesList {
		if err := ensureOutboundCreateAllowed(ctx, values); err != nil {
			return nil, err
		}
		if err := ensureOutboundWorkflowIDAllowed(ctx, nil, values); err != nil {
			return nil, err
		}
		normalized, err := normalizeOutboundDeliveryValues(values, nil, true)
		if err != nil {
			return nil, err
		}
		record, err := orm.RuntimeRepositoryCreate(ctx, orm.RuntimeModelRef(OutboundDeliveryModelName, model), normalized)
		if err != nil {
			return nil, err
		}
		created = append(created, record)
	}
	if len(created) == 1 {
		return created[0], nil
	}
	return created, nil
}

func writeOutboundDelivery(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	if len(ids) == 0 {
		log.Errorf("base.workflow.outbound.write requires ids")
		return nil, orm.InvalidParamsf("base.workflow.outbound.write requires ids")
	}
	// The ACL checks manual_retry as "write" too, so the operator role passes it for this generic
	// write as well. Keep the generic write to authors (base.group_system): an operator may only
	// requeue a delivery through manual_retry, never rewrite its status, target or body. The engine
	// writes through OutboundEngineContext (sudo) and is exempt.
	if err := EnsureWorkflowRole(ctx, WorkflowRoleAuthor); err != nil {
		return nil, err
	}
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		log.Errorf("base.workflow.outbound.write requires values")
		return nil, orm.InvalidParamsf("base.workflow.outbound.write requires values")
	}

	beforeRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(OutboundDeliveryModelName, model), ids, nil)
	if err != nil {
		return nil, err
	}
	if len(beforeRows) == 0 {
		return false, nil
	}
	if err := ensureOutboundWriteAllowed(ctx, beforeRows, values); err != nil {
		return nil, err
	}
	if err := ensureOutboundWorkflowIDAllowed(ctx, beforeRows, values); err != nil {
		return nil, err
	}

	current := outboundDeliveryStateFromRecord(beforeRows[0])
	normalized, err := normalizeOutboundDeliveryValues(values, &current, false)
	if err != nil {
		return nil, err
	}
	return orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(OutboundDeliveryModelName, model), ids, normalized)
}

// manualRetryOutboundDelivery requeues an existing outbound delivery row so an
// operator can replay a stuck or failed delivery from the form view.
func manualRetryOutboundDelivery(ctx context.Context, model *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	if len(ids) == 0 {
		log.Errorf("base.workflow.outbound.manual_retry requires ids")
		return nil, orm.InvalidParamsf("base.workflow.outbound.manual_retry requires ids")
	}

	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(OutboundDeliveryModelName, model), ids[:1], nil)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return false, nil
	}

	record := rows[0]
	status := strings.TrimSpace(stringValue(record["status"]))
	if !isManualRetryableOutboundStatus(status) {
		log.Errorf("base.workflow.outbound.manual_retry does not allow status %q", status)
		return nil, orm.Conflictf("base.workflow.outbound.manual_retry does not allow status %q", status)
	}

	deliveryID := int64Value(record["id"])
	targetRef := strings.TrimSpace(stringValue(record["target_ref"]))
	headers, err := outboundDeliveryHeadersFromRecord(record["headers"])
	if err != nil {
		return nil, err
	}
	requestBody, err := outboundDeliveryRequestBodyFromRecord(record["request_body"])
	if err != nil {
		return nil, err
	}

	// Reuse the stored delivery payload so manual retries preserve the original
	// audit row and outbound request shape instead of creating a fresh record.
	if err := runOutboundDeliveryManualRetry(ctx, OutboundDeliveryManualRetryRequest{
		DeliveryID:       deliveryID,
		Topic:            strings.TrimSpace(stringValue(record["topic"])),
		TargetRef:        targetRef,
		Headers:          headers,
		AuthorizationRef: strings.TrimSpace(stringValue(record["authorization_ref"])),
		CompanyID:        int64PointerFromAny(record["company_id"]),
		WorkflowID:       int64Value(record["workflow_id"]),
		RequestBody:      requestBody,
		MaxAttempts:      int(currentOutboundDeliveryMaxAttempts(&outboundDeliveryState{MaxAttempts: int64Value(record["max_attempts"])})),
	}); err != nil {
		return nil, err
	}
	return true, nil
}

type outboundDeliveryState struct {
	Status       string
	AttemptCount int64
	MaxAttempts  int64
	NextAttempt  int64
}

func outboundDeliveryStateFromRecord(record orm.RecordMap) outboundDeliveryState {
	return outboundDeliveryState{
		Status:       strings.TrimSpace(stringValue(record["status"])),
		AttemptCount: int64Value(record["attempt_count"]),
		MaxAttempts:  int64Value(record["max_attempts"]),
		NextAttempt:  int64Value(record["next_attempt_at"]),
	}
}

func normalizeOutboundDeliveryValues(values orm.RecordMap, current *outboundDeliveryState, creating bool) (orm.RecordMap, error) {
	normalized := cloneRecordMap(values)
	workflowRequestID, _ := stringFromAny(normalized["workflow_request_id"])
	name, _ := stringFromAny(normalized["name"])
	workflowRequestID = strings.TrimSpace(workflowRequestID)
	name = strings.TrimSpace(name)
	delete(normalized, "workflow_request_id")

	if creating {
		// An explicit name wins: the engine names a delivery after its step idempotency key
		// (`<request id>:<step key>`, plan T53). The bare request ID is only a fallback: the name
		// is unique, so using it for every row made the second delivery of a run fail.
		if name != "" {
			normalized["name"] = name
		} else if workflowRequestID != "" {
			normalized["name"] = workflowRequestID
		} else {
			normalized["name"] = uuid4String()
		}
	} else if name != "" {
		normalized["name"] = name
	}

	if topic, ok := stringFromAny(normalized["topic"]); ok {
		normalized["topic"] = strings.TrimSpace(topic)
	}
	if deliveryType, ok := stringFromAny(normalized["delivery_type"]); ok {
		deliveryType = strings.TrimSpace(deliveryType)
		if deliveryType == "" {
			log.Errorf("base.workflow.outbound delivery_type is required")
			return nil, orm.InvalidParamsf("base.workflow.outbound delivery_type is required")
		}
		normalized["delivery_type"] = deliveryType
	} else if creating {
		log.Errorf("base.workflow.outbound delivery_type is required")
		return nil, orm.InvalidParamsf("base.workflow.outbound delivery_type is required")
	}
	if targetRef, ok := stringFromAny(normalized["target_ref"]); ok {
		targetRef = strings.TrimSpace(targetRef)
		if targetRef == "" {
			log.Errorf("base.workflow.outbound target_ref is required")
			return nil, orm.InvalidParamsf("base.workflow.outbound target_ref is required")
		}
		normalized["target_ref"] = targetRef
	} else if creating {
		log.Errorf("base.workflow.outbound target_ref is required")
		return nil, orm.InvalidParamsf("base.workflow.outbound target_ref is required")
	}

	if rawHeaders, exists := normalized["headers"]; exists {
		headers, err := normalizeUIActionContext(rawHeaders)
		if err != nil {
			log.Errorf("base.workflow.outbound headers must be an object")
			return nil, orm.InvalidParamsf("base.workflow.outbound headers must be an object")
		}
		normalized["headers"] = headers
	} else if creating {
		normalized["headers"] = map[string]any{}
	}
	if rawBody, exists := normalized["request_body"]; exists {
		body, err := normalizeUIActionContext(rawBody)
		if err != nil {
			log.Errorf("base.workflow.outbound request_body must be an object")
			return nil, orm.InvalidParamsf("base.workflow.outbound request_body must be an object")
		}
		normalized["request_body"] = body
	} else if creating {
		normalized["request_body"] = map[string]any{}
	}
	if responseBody, ok := stringFromAny(normalized["response_body"]); ok {
		normalized["response_body"] = strings.TrimSpace(responseBody)
	} else if creating {
		normalized["response_body"] = ""
	}

	status := strings.TrimSpace(currentOutboundDeliveryStatus(current))
	if rawStatus, exists := normalized["status"]; exists {
		statusValue, ok := stringFromAny(rawStatus)
		if !ok {
			log.Errorf("base.workflow.outbound status must be a string")
			return nil, orm.InvalidParamsf("base.workflow.outbound status must be a string")
		}
		status = strings.TrimSpace(statusValue)
	}
	if status == "" {
		if creating {
			status = OutboundDeliveryStatusQueued
		}
	}
	if status != "" {
		if !isValidOutboundDeliveryStatus(status) {
			log.Errorf("base.workflow.outbound unsupported status %q", status)
			return nil, orm.InvalidParamsf("base.workflow.outbound unsupported status %q", status)
		}
		normalized["status"] = status
	}

	attemptCount := currentOutboundDeliveryAttemptCount(current)
	if rawAttempts, exists := normalized["attempt_count"]; exists {
		value, ok := int64FromNonNegativeAny(rawAttempts)
		if !ok {
			log.Errorf("base.workflow.outbound attempt_count must be a non-negative integer")
			return nil, orm.InvalidParamsf("base.workflow.outbound attempt_count must be a non-negative integer")
		}
		attemptCount = value
		normalized["attempt_count"] = value
	} else if creating {
		normalized["attempt_count"] = int64(0)
	}

	maxAttempts := currentOutboundDeliveryMaxAttempts(current)
	if rawMax, exists := normalized["max_attempts"]; exists {
		value, ok := int64FromAny(rawMax)
		if !ok {
			log.Errorf("base.workflow.outbound max_attempts must be a positive integer")
			return nil, orm.InvalidParamsf("base.workflow.outbound max_attempts must be a positive integer")
		}
		maxAttempts = value
		normalized["max_attempts"] = value
	} else if creating {
		maxAttempts = 3
		normalized["max_attempts"] = int64(3)
	}
	if maxAttempts > 0 && attemptCount > maxAttempts {
		log.Errorf("base.workflow.outbound attempt_count cannot exceed max_attempts")
		return nil, fmt.Errorf("base.workflow.outbound attempt_count cannot exceed max_attempts")
	}

	if lastError, ok := stringFromAny(normalized["last_error"]); ok {
		normalized["last_error"] = strings.TrimSpace(lastError)
	} else if creating {
		normalized["last_error"] = ""
	}

	nextAttempt := currentOutboundDeliveryNextAttempt(current)
	if rawNext, exists := normalized["next_attempt_at"]; exists {
		value, ok := int64FromNonNegativeAny(rawNext)
		if !ok {
			log.Errorf("base.workflow.outbound next_attempt_at must be a non-negative integer")
			return nil, orm.InvalidParamsf("base.workflow.outbound next_attempt_at must be a non-negative integer")
		}
		nextAttempt = value
		normalized["next_attempt_at"] = value
	} else if creating {
		normalized["next_attempt_at"] = int64(0)
	}
	if rawLastAttempt, exists := normalized["last_attempt_at"]; exists {
		value, ok := int64FromNonNegativeAny(rawLastAttempt)
		if !ok {
			log.Errorf("base.workflow.outbound last_attempt_at must be a non-negative integer")
			return nil, orm.InvalidParamsf("base.workflow.outbound last_attempt_at must be a non-negative integer")
		}
		normalized["last_attempt_at"] = value
	}

	if status == OutboundDeliveryStatusRetryableFailed && nextAttempt <= 0 {
		log.Errorf("base.workflow.outbound retryable_failed rows require next_attempt_at")
		return nil, fmt.Errorf("base.workflow.outbound retryable_failed rows require next_attempt_at")
	}
	if status == OutboundDeliveryStatusDelivered || status == OutboundDeliveryStatusPermanentFailed {
		normalized["next_attempt_at"] = int64(0)
	}
	return normalized, nil
}

func isValidOutboundDeliveryStatus(status string) bool {
	switch status {
	case OutboundDeliveryStatusQueued,
		OutboundDeliveryStatusSending,
		OutboundDeliveryStatusDelivered,
		OutboundDeliveryStatusRetryableFailed,
		OutboundDeliveryStatusPermanentFailed:
		return true
	default:
		return false
	}
}

// isManualRetryableOutboundStatus limits operator-triggered replays to rows
// that are not already in-flight or terminally successful.
func isManualRetryableOutboundStatus(status string) bool {
	switch status {
	case OutboundDeliveryStatusQueued,
		OutboundDeliveryStatusRetryableFailed,
		OutboundDeliveryStatusPermanentFailed:
		return true
	default:
		return false
	}
}

func currentOutboundDeliveryStatus(current *outboundDeliveryState) string {
	if current == nil {
		return ""
	}
	return current.Status
}

func currentOutboundDeliveryAttemptCount(current *outboundDeliveryState) int64 {
	if current == nil {
		return 0
	}
	return current.AttemptCount
}

func currentOutboundDeliveryMaxAttempts(current *outboundDeliveryState) int64 {
	if current == nil || current.MaxAttempts <= 0 {
		return 3
	}
	return current.MaxAttempts
}

func currentOutboundDeliveryNextAttempt(current *outboundDeliveryState) int64 {
	if current == nil {
		return 0
	}
	return current.NextAttempt
}

// outboundDeliveryHeadersFromRecord normalizes stored JSON headers back into
// the worker's string-header map without mutating the repository payload.
func outboundDeliveryHeadersFromRecord(raw any) (map[string]string, error) {
	normalized, err := normalizeUIActionContext(raw)
	if err != nil {
		log.Errorf("base.workflow.outbound headers must be an object")
		return nil, orm.InvalidParamsf("base.workflow.outbound headers must be an object")
	}
	headers := make(map[string]string, len(normalized))
	for key, value := range normalized {
		if strings.TrimSpace(key) == "" || value == nil {
			continue
		}
		headers[key] = strings.TrimSpace(fmt.Sprint(value))
	}
	return headers, nil
}

// outboundDeliveryRequestBodyFromRecord clones the stored JSON body into the
// worker-facing shape used by manual retry submission.
func outboundDeliveryRequestBodyFromRecord(raw any) (map[string]any, error) {
	normalized, err := normalizeUIActionContext(raw)
	if err != nil {
		log.Errorf("base.workflow.outbound request_body must be an object")
		return nil, orm.InvalidParamsf("base.workflow.outbound request_body must be an object")
	}
	return normalized, nil
}

// ensureOutboundWorkflowIDAllowed keeps workflow_id engine-owned: the sweeper resolves a row's
// authorization_ref in that workflow's credential scope, so a non-sudo RPC caller must not be able
// to point a row at another workflow's credentials.
func ensureOutboundWorkflowIDAllowed(ctx context.Context, rows []orm.RecordMap, values orm.RecordMap) error {
	raw, exists := values["workflow_id"]
	if !exists || !outboundRestrictedCaller(ctx) {
		return nil
	}
	if rows == nil {
		if int64Value(raw) == 0 {
			return nil
		}
		return orm.Forbiddenf("permission denied: workflow_id on %s is set by the workflow engine only", OutboundDeliveryModelName)
	}
	for _, row := range rows {
		if int64Value(row["workflow_id"]) != int64Value(raw) {
			return orm.Forbiddenf("permission denied: workflow_id on %s is set by the workflow engine only", OutboundDeliveryModelName)
		}
	}
	return nil
}

// WorkflowStepIdempotencyKey is the step key format: "<request id>:<step key>", or "" when either
// part is missing (the caller then falls back to a random name).
func WorkflowStepIdempotencyKey(requestID, stepKey string) string {
	requestID, stepKey = strings.TrimSpace(requestID), strings.TrimSpace(stepKey)
	if requestID == "" || stepKey == "" {
		return ""
	}
	return requestID + ":" + stepKey
}

// outboundSweepIndexName backs the sweeper's (status, next_attempt_at) scan (plan T53).
const outboundSweepIndexName = "base_outbound_delivery_status_next_attempt_idx"

// ensureOutboundSweepIndex adds the composite sweeper index on databases that have the outbound
// table. The model metadata only declares single-column indexes, hence this data migration.
// Not CONCURRENTLY: legacy migrations run inside a transaction; the table is small.
func ensureOutboundSweepIndex(ctx context.Context, db bun.IDB) error {
	table := OutboundDeliveryModelSpec.ResolveStorageName()
	var regclass sql.NullString
	if err := db.NewRaw(`SELECT to_regclass(?)::text`, table).Scan(ctx, &regclass); err != nil {
		return fmt.Errorf("outbound sweeper index: %w", err)
	}
	if !regclass.Valid || regclass.String == "" {
		return nil
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`CREATE INDEX IF NOT EXISTS %s ON %s (status, next_attempt_at)`, outboundSweepIndexName, table)); err != nil {
		return fmt.Errorf("outbound sweeper index: %w", err)
	}
	return nil
}

// ClaimedOutboundDelivery is one row the caller now owns at status sending: Attempt is the attempt
// number this send counts as (attempt_count after the claim).
type ClaimedOutboundDelivery struct {
	ID               int64          `bun:"id"`
	Attempt          int            `bun:"attempt_count"`
	MaxAttempts      int            `bun:"max_attempts"`
	TargetRef        string         `bun:"target_ref"`
	Headers          map[string]any `bun:"headers,type:jsonb"`
	AuthorizationRef string         `bun:"authorization_ref"`
	RequestBody      map[string]any `bun:"request_body,type:jsonb"`
	CompanyID        *int64         `bun:"company_id"`
	WorkflowID       *int64         `bun:"workflow_id"`
}

// OutboundSweepOptions bounds one sweeper pass. Horizon keeps the sweeper away from rows that fell
// due long ago (a backlog left by a long outage, or rows that predate the outbox): those stay for
// an operator's manual_retry instead of being sent hours late.
type OutboundSweepOptions struct {
	Now          time.Time
	Limit        int
	StaleSending time.Duration
	Horizon      time.Duration
}

// outboundClaimSet is the SET clause shared by both claims: one more attempt, now in flight.
const outboundClaimSet = `status = ?, attempt_count = attempt_count + 1, last_attempt_at = ?, next_attempt_at = 0, last_error = '', updated_at = ?`

// ClaimOutboundDelivery moves one queued or retryable row to sending and counts the attempt. It is
// the fast path's claim (a job handed to the worker right after the row was written): ok=false
// means the sweeper or another worker owns the row already, so the caller must not send.
func ClaimOutboundDelivery(ctx context.Context, id int64, now time.Time) (ClaimedOutboundDelivery, bool, error) {
	table := OutboundDeliveryModelSpec.ResolveStorageName()
	claimed := make([]ClaimedOutboundDelivery, 0, 1)
	err := workflowRunWithRequestTx(ctx, func(ctx context.Context, db bun.IDB) error {
		query := fmt.Sprintf(
			`UPDATE %s
			    SET `+outboundClaimSet+`
			  WHERE id = ?
			    AND status IN (?, ?)
			    AND attempt_count < GREATEST(max_attempts, 1)
			RETURNING id, attempt_count, max_attempts, target_ref, headers, authorization_ref, request_body, company_id, workflow_id`,
			table,
		)
		return db.NewRaw(query,
			OutboundDeliveryStatusSending, now.Unix(), now,
			id, OutboundDeliveryStatusQueued, OutboundDeliveryStatusRetryableFailed,
		).Scan(ctx, &claimed)
	})
	if err != nil {
		log.Errorf("base.workflow.outbound claim %d: %v", id, err)
		return ClaimedOutboundDelivery{}, false, fmt.Errorf("base.workflow.outbound claim %d: %w", id, err)
	}
	if len(claimed) == 0 {
		return ClaimedOutboundDelivery{}, false, nil
	}
	return claimed[0], true, nil
}

// ClaimDueOutboundDeliveries is the outbox sweeper's claim (plan T53). In one transaction it closes
// stale sending rows that have no attempt left (permanent_failed), then locks up to Limit due notify
// rows with FOR UPDATE SKIP LOCKED (so two instances never claim the same row) and moves them to
// sending with one more attempt counted:
//   - queued rows whose next_attempt_at is due (the fast path never picked them up),
//   - retryable_failed rows whose backoff elapsed,
//   - sending rows whose last attempt started more than StaleSending ago (worker died mid-send).
//
// Only delivery_type http_json is swept; post_output_object rows are retried by their run.
func ClaimDueOutboundDeliveries(ctx context.Context, opts OutboundSweepOptions) ([]ClaimedOutboundDelivery, error) {
	opts = normalizeOutboundSweepOptions(opts)
	table := OutboundDeliveryModelSpec.ResolveStorageName()
	now := opts.Now.Unix()
	staleBefore := opts.Now.Add(-opts.StaleSending).Unix()
	notBefore := opts.Now.Add(-opts.Horizon).Unix()
	claimed := make([]ClaimedOutboundDelivery, 0, opts.Limit)
	err := workflowRunWithRequestTx(ctx, func(ctx context.Context, db bun.IDB) error {
		exhausted := fmt.Sprintf(
			`UPDATE %s
			    SET status = ?, next_attempt_at = 0, last_error = ?, updated_at = ?
			  WHERE delivery_type = ?
			    AND status = ?
			    AND last_attempt_at <= ?
			    AND attempt_count >= GREATEST(max_attempts, 1)`,
			table,
		)
		if _, err := db.ExecContext(ctx, exhausted,
			OutboundDeliveryStatusPermanentFailed, "delivery attempt interrupted and no attempt left", opts.Now,
			OutboundDeliveryTypeHTTPJSON, OutboundDeliveryStatusSending, staleBefore,
		); err != nil {
			return err
		}
		var ids []int64
		selectDue := fmt.Sprintf(
			`SELECT id
			   FROM %s
			  WHERE delivery_type = ?
			    AND COALESCE(active, true)
			    AND attempt_count < GREATEST(max_attempts, 1)
			    AND (
			         (status IN (?, ?) AND next_attempt_at <= ? AND next_attempt_at >= ?)
			      OR (status = ? AND last_attempt_at <= ? AND last_attempt_at >= ?)
			    )
			  ORDER BY next_attempt_at ASC, id ASC
			  FOR UPDATE SKIP LOCKED
			  LIMIT ?`,
			table,
		)
		if err := db.NewRaw(selectDue,
			OutboundDeliveryTypeHTTPJSON,
			OutboundDeliveryStatusQueued, OutboundDeliveryStatusRetryableFailed, now, notBefore,
			OutboundDeliveryStatusSending, staleBefore, notBefore,
			opts.Limit,
		).Scan(ctx, &ids); err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		claim := fmt.Sprintf(
			`UPDATE %s
			    SET `+outboundClaimSet+`
			  WHERE id IN (?)
			RETURNING id, attempt_count, max_attempts, target_ref, headers, authorization_ref, request_body, company_id, workflow_id`,
			table,
		)
		return db.NewRaw(claim, OutboundDeliveryStatusSending, now, opts.Now, bun.In(ids)).Scan(ctx, &claimed)
	})
	if err != nil {
		log.Errorf("base.workflow.outbound sweep claim: %v", err)
		return nil, fmt.Errorf("base.workflow.outbound sweep claim: %w", err)
	}
	return claimed, nil
}

func normalizeOutboundSweepOptions(opts OutboundSweepOptions) OutboundSweepOptions {
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	}
	if opts.Limit <= 0 {
		opts.Limit = 20
	}
	if opts.Limit > 200 {
		opts.Limit = 200
	}
	if opts.StaleSending <= 0 {
		opts.StaleSending = 5 * time.Minute
	}
	if opts.Horizon <= 0 {
		opts.Horizon = 24 * time.Hour
	}
	return opts
}

// OutboundDeliveryClaimable is the sweeper's selection rule in Go (mirrors the WHERE clause of
// ClaimDueOutboundDeliveries), so the rule can be unit-tested without a database.
func OutboundDeliveryClaimable(row OutboundDeliveryRecord, opts OutboundSweepOptions) bool {
	opts = normalizeOutboundSweepOptions(opts)
	now := opts.Now.Unix()
	notBefore := opts.Now.Add(-opts.Horizon).Unix()
	maxAttempts := row.MaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	if row.DeliveryType != OutboundDeliveryTypeHTTPJSON || row.AttemptCount >= maxAttempts {
		return false
	}
	switch row.Status {
	case OutboundDeliveryStatusQueued, OutboundDeliveryStatusRetryableFailed:
		return row.NextAttemptAt <= now && row.NextAttemptAt >= notBefore
	case OutboundDeliveryStatusSending:
		staleBefore := opts.Now.Add(-opts.StaleSending).Unix()
		return row.LastAttemptAt <= staleBefore && row.LastAttemptAt >= notBefore
	default:
		return false
	}
}
