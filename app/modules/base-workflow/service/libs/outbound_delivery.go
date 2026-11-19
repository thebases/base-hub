package libs

// File overview:
// - Responsibility: owns `workflow` module orchestration for outbound delivery, where long-running workflow logic and operational state transitions are coordinated.
// - Key symbols: `NewOutboundDeliveryService`, `httpDeliveryJob`, `OutboundDeliveryWorkerConfig`.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.
// - Pattern: Protects shared mutable state with explicit synchronization or worker coordination rather than relying on unsupervised goroutine side effects.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"git.thebase.vn/base_hub/internal/modelruntime"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"git.thebase.vn/base_hub/internal/services/safehttp"
	"git.thebase.vn/base_hub/internal/worker"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
	"github.com/google/uuid"
)

const (
	defaultHTTPMaxAttempts   = 3
	defaultWorkerCount       = 2
	defaultWorkerQueueSize   = 64
	contentTypeApplicationJS = "application/json"
)

// defaultRetryDelay pulls the outbound delivery retry cooldown from
// workflowmodels.DefaultRetryPolicy so RetryPolicy stays the single source of
// truth for retry timing across the workflow module instead of drifting from
// the workflow-action retry cooldown.
func defaultRetryDelay() time.Duration {
	return time.Duration(workflowmodels.DefaultRetryPolicy().RetryCoolDown) * time.Second
}

type httpDeliveryJob struct {
	DeliveryID  int64
	TargetRef   string
	Headers     map[string]string
	RequestBody map[string]interface{}
	MaxAttempts int
	// DBName is the database that holds the delivery row (plan T46): the worker runs detached
	// from the request, so it rebinds this database before writing the delivery result.
	DBName string
	// Attempt > 0 means the row was already claimed (moved to sending, attempt counted) by the
	// outbox sweeper; 0 means the worker claims it itself before sending (plan T53).
	Attempt int
}

type OutboundDeliveryWorkerConfig struct {
	WorkerCount  int
	JobQueueSize int
}

type ManualRetryRequest struct {
	DeliveryID int64
	Topic      string
	TargetRef  string
	Headers    map[string]string
	// AuthorizationRef is the stored @cred:/@syscred: reference; resolved again for the retry,
	// within the row's company (CompanyID).
	AuthorizationRef string
	CompanyID        *int64
	// WorkflowID scopes that lookup to the workflow that queued the row (0 = company only).
	WorkflowID  int64
	RequestBody map[string]interface{}
	MaxAttempts int
}

type submitWithTimeoutFunc interface {
	SubmitWithTimeout(httpDeliveryJob, time.Duration) error
}

type OutboundDeliveryService struct {
	createRecord func(context.Context, workflowmodels.OutboundDeliveryRecord) (*orm.InsertResult, error)
	updateRecord func(context.Context, orm.RecordMap, orm.RecordMap) (*orm.UpdateResult, error)
	// findByName returns the delivery row with this name (the step idempotency key), if any.
	findByName func(context.Context, string) (orm.RecordMap, bool, error)
	// claimOne moves one queued/retryable row to sending (fast path); ok=false: someone else owns it.
	claimOne func(context.Context, int64, time.Time) (workflowmodels.ClaimedOutboundDelivery, bool, error)
	// claimDue is the sweeper's SKIP LOCKED claim of due rows.
	claimDue   func(context.Context, workflowmodels.OutboundSweepOptions) ([]workflowmodels.ClaimedOutboundDelivery, error)
	submitJob  func(context.Context, httpDeliveryJob) error
	postHTTP   func(context.Context, string, map[string]string, map[string]interface{}) (*http.Response, error)
	now        func() time.Time
	retryDelay time.Duration
}

func init() {
	workflowmodels.RegisterOutboundDeliveryManualRetry(func(ctx context.Context, request workflowmodels.OutboundDeliveryManualRetryRequest) error {
		return NewOutboundDeliveryService().ManualRetry(ctx, ManualRetryRequest{
			DeliveryID:       request.DeliveryID,
			Topic:            request.Topic,
			TargetRef:        request.TargetRef,
			Headers:          request.Headers,
			AuthorizationRef: request.AuthorizationRef,
			CompanyID:        request.CompanyID,
			WorkflowID:       request.WorkflowID,
			RequestBody:      request.RequestBody,
			MaxAttempts:      request.MaxAttempts,
		})
	})
}

// NewOutboundDeliveryService constructs and returns the outbound delivery service handled by this package.
func NewOutboundDeliveryService() *OutboundDeliveryService {
	runtime := modelruntime.NewService()
	return &OutboundDeliveryService{
		// Delivery rows are engine-owned: written in the caller's database with a sudo env
		// (OutboundEngineContext), never on the default database a bare context falls back to.
		createRecord: func(ctx context.Context, record workflowmodels.OutboundDeliveryRecord) (*orm.InsertResult, error) {
			created, err := runtime.Create(workflowmodels.OutboundEngineContext(ctx), workflowmodels.OutboundDeliveryModelName, recordToMap(record))
			if err != nil {
				return nil, err
			}
			return &orm.InsertResult{InsertedID: created["id"], Acknowledged: true}, nil
		},
		updateRecord: func(ctx context.Context, filter orm.RecordMap, values orm.RecordMap) (*orm.UpdateResult, error) {
			id, ok := insertedIDAsString(filter["id"])
			if !ok {
				log.Errorf("outbounddelivery: update requires id filter")
				return nil, fmt.Errorf("outbounddelivery: update requires id filter")
			}
			updated, err := runtime.Write(workflowmodels.OutboundEngineContext(ctx), workflowmodels.OutboundDeliveryModelName, []string{id}, values)
			if err != nil {
				return nil, err
			}
			modified := int64(0)
			if updated {
				modified = 1
			}
			return &orm.UpdateResult{Acknowledged: true, MatchedCount: modified, ModifiedCount: modified}, nil
		},
		findByName: func(ctx context.Context, name string) (orm.RecordMap, bool, error) {
			rows, err := runtime.SearchRead(workflowmodels.OutboundEngineContext(ctx), workflowmodels.OutboundDeliveryModelName, orm.RecordMap{"name": name}, []string{"id", "status"})
			if err != nil || len(rows) == 0 {
				return nil, false, err
			}
			return rows[0], true, nil
		},
		claimOne: workflowmodels.ClaimOutboundDelivery,
		claimDue: workflowmodels.ClaimDueOutboundDeliveries,
		// Keep submission cancellable at the enqueue edge while the worker
		// itself owns the slow HTTP side effect after the job is accepted. We
		// still cap enqueue wait time so a saturated queue cannot hold the
		// webhook route open for the full client timeout.
		submitJob: func(ctx context.Context, job httpDeliveryJob) error {
			return submitHTTPDeliveryJob(ctx, ensurePool(), job)
		},
		postHTTP:   postJSONRequest,
		now:        func() time.Time { return time.Now().UTC() },
		retryDelay: defaultRetryDelay(),
	}
}

func recordToMap(record any) orm.RecordMap {
	values := orm.RecordMap{}
	raw, err := json.Marshal(record)
	if err != nil {
		return values
	}
	_ = json.Unmarshal(raw, &values)
	return values
}

// insertedIDAsInt64 is a local copy of the engine package's helper of the same
// name (see nodes' stringValue comment for why small helpers like this are
// duplicated across the engine/nodes/libs split rather than exported).
func insertedIDAsInt64(result *orm.InsertResult) (int64, error) {
	if result == nil {
		return 0, fmt.Errorf("missing insert result")
	}
	switch typed := result.InsertedID.(type) {
	case int64:
		return typed, nil
	case int:
		return int64(typed), nil
	case uint64:
		return int64(typed), nil
	case float64:
		return int64(typed), nil
	default:
		return 0, fmt.Errorf("unsupported inserted id type %T", result.InsertedID)
	}
}

func insertedIDAsString(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, typed != ""
	case int64:
		return fmt.Sprint(typed), true
	case int:
		return fmt.Sprint(typed), true
	default:
		return "", false
	}
}

// workflowHTTPClient sends every user-authored workflow request (notify deliveries,
// post_output_object, manual retries): public addresses only unless
// server.outboundAllowCidrs opens them, at most 3 redirects, no env proxy (T42).
var workflowHTTPClient = safehttp.NewClient(safehttp.Options{Timeout: 30 * time.Second})

// postJSONRequest sends a POST with a JSON body and returns the response with
// an open body for the caller to read and close.
func postJSONRequest(ctx context.Context, targetURL string, headers map[string]string, body map[string]interface{}) (*http.Response, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		log.Errorf("marshal request body: %v", err)
		return nil, fmt.Errorf("marshal request body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return safehttp.Do(ctx, workflowHTTPClient, req)
}

// PostWorkflowHTTPJSON is the synchronous post_output_object send: same client as the
// delivery worker, response body capped at safehttp.MaxResponseBytes.
func PostWorkflowHTTPJSON(ctx context.Context, targetURL string, headers map[string]string, body map[string]any) (int, string, error) {
	resp, err := postJSONRequest(ctx, targetURL, headers, body)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	responseBody, err := safehttp.ReadBody(resp.Body, 0)
	if err != nil {
		return resp.StatusCode, "", err
	}
	return resp.StatusCode, strings.TrimSpace(string(responseBody)), nil
}

var deliveryPoolState = struct {
	mu   sync.RWMutex
	pool *worker.Pool[httpDeliveryJob, struct{}]
}{}

// InitOutboundDeliveryWorker initializes the outbound delivery worker handled by this package.
func InitOutboundDeliveryWorker(cfg OutboundDeliveryWorkerConfig) {
	workerCount := cfg.WorkerCount
	if workerCount <= 0 {
		workerCount = defaultWorkerCount
	}

	queueSize := cfg.JobQueueSize
	if queueSize <= 0 {
		queueSize = defaultWorkerQueueSize
	}

	deliveryPoolState.mu.Lock()
	defer deliveryPoolState.mu.Unlock()

	if deliveryPoolState.pool != nil {
		deliveryPoolState.pool.Shutdown()
	}

	service := NewOutboundDeliveryService()
	deliveryPoolState.pool = worker.NewPool(service.processHTTPDelivery, worker.WithWorkers(workerCount), worker.WithQueue(queueSize))
	log.Infof("initialized outbound delivery worker: workers=%d queue=%d", workerCount, queueSize)
}

// ShutdownOutboundDeliveryWorker shuts down the outbound delivery worker handled by this package.
func ShutdownOutboundDeliveryWorker() {
	deliveryPoolState.mu.Lock()
	defer deliveryPoolState.mu.Unlock()

	if deliveryPoolState.pool != nil {
		deliveryPoolState.pool.Shutdown()
		deliveryPoolState.pool = nil
	}
}

// outboundFastPathGrace is how long a freshly queued row is left to the fast path (the job handed
// to the worker right away) before the sweeper may claim it.
const outboundFastPathGrace = 30 * time.Second

// QueueHTTPJSON is the notify outbox (plan T53): the delivery row is persisted first, then handed
// to the delivery worker as a fast path. The row, not the in-memory job, is the source of truth:
// if the job cannot be queued, or the process dies before sending, the outbox sweeper
// (SweepDueDeliveries) claims the row and sends it. Caller-supplied headers are merged over the
// application/json default so a caller can still override Content-Type.
//
// The row is named after the step idempotency key (`<request id>:<step key>`), so every attempt of
// one notify step reuses one row: a step retry re-queues it, a delivered or in-flight row is left
// alone (the step counts as done), and two notify steps of one run never collide on the unique name.
//
// authorizationRef is the @cred:/@syscred: reference the Authorization header in headers was
// resolved from, or "". When set, the row stores the reference and not the resolved header (plan
// T40); the fast-path job still sends the header, the sweeper resolves the reference again.
func (s *OutboundDeliveryService) QueueHTTPJSON(ctx context.Context, topic, targetURL string, headers map[string]string, authorizationRef string, body map[string]interface{}) error {
	targetURL = strings.TrimSpace(targetURL)
	if targetURL == "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	mergedHeaders := map[string]string{"Content-Type": contentTypeApplicationJS}
	for key, value := range headers {
		mergedHeaders[key] = value
	}

	now := s.now()
	record := workflowmodels.OutboundDeliveryRecord{
		CompanyID:        workflowScopeCompanyID(ctx),
		WorkflowID:       workflowScopeWorkflowID(ctx),
		Name:             outboundDeliveryName(ctx),
		Topic:            strings.TrimSpace(topic),
		DeliveryType:     workflowmodels.OutboundDeliveryTypeHTTPJSON,
		TargetRef:        targetURL,
		Headers:          headerMapToAny(storedOutboundHeaders(mergedHeaders, authorizationRef)),
		AuthorizationRef: strings.TrimSpace(authorizationRef),
		RequestBody:      cloneBody(body),
		Status:           workflowmodels.OutboundDeliveryStatusQueued,
		AttemptCount:     0,
		MaxAttempts:      defaultHTTPMaxAttempts,
		NextAttemptAt:    now.Add(outboundFastPathGrace).Unix(),
	}

	deliveryID, send, err := s.persistQueuedDelivery(ctx, record)
	if err != nil {
		return err
	}
	if !send {
		return nil
	}

	job := httpDeliveryJob{
		DeliveryID:  deliveryID,
		TargetRef:   targetURL,
		Headers:     mergedHeaders,
		RequestBody: cloneBody(body),
		MaxAttempts: defaultHTTPMaxAttempts,
		DBName:      outboundDBName(ctx),
	}
	if err := s.submitJob(ctx, job); err != nil {
		// The row is stored as queued: the sweeper sends it once the fast-path grace is over.
		log.Warnf("outbound delivery %d: worker queue unavailable, left to the sweeper: %v", deliveryID, err)
	}
	return nil
}

// persistQueuedDelivery writes record as a queued row, reusing the row that already carries its
// name. send=false means that row is delivered or in flight and must not be sent again.
func (s *OutboundDeliveryService) persistQueuedDelivery(ctx context.Context, record workflowmodels.OutboundDeliveryRecord) (int64, bool, error) {
	if record.Name != "" && s.findByName != nil {
		existing, found, err := s.findByName(ctx, record.Name)
		if err != nil {
			log.Errorf("look up outbound delivery %q: %v", record.Name, err)
			return 0, false, fmt.Errorf("look up outbound delivery %q: %w", record.Name, err)
		}
		if found {
			return s.requeueExistingDelivery(ctx, existing, record)
		}
	}
	inserted, err := s.createRecord(ctx, record)
	if err != nil {
		// Two attempts of the same step racing on the unique name: the loser reuses the winner's row.
		if record.Name != "" && s.findByName != nil {
			if existing, found, findErr := s.findByName(ctx, record.Name); findErr == nil && found {
				return s.requeueExistingDelivery(ctx, existing, record)
			}
		}
		log.Errorf("create outbound delivery record: %v", err)
		return 0, false, fmt.Errorf("create outbound delivery record: %w", err)
	}
	deliveryID, err := insertedIDAsInt64(inserted)
	if err != nil {
		log.Errorf("queue outbound delivery: %v", err)
		return 0, false, fmt.Errorf("queue outbound delivery: %w", err)
	}
	return deliveryID, true, nil
}

// requeueExistingDelivery points the step's existing row at this attempt's request and queues it
// again with a fresh attempt budget, unless it is delivered (done) or sending (in flight; the
// sweeper takes it over if that send died).
func (s *OutboundDeliveryService) requeueExistingDelivery(ctx context.Context, existing orm.RecordMap, record workflowmodels.OutboundDeliveryRecord) (int64, bool, error) {
	id, err := insertedIDAsInt64(&orm.InsertResult{InsertedID: existing["id"]})
	if err != nil || id <= 0 {
		log.Errorf("outbound delivery %q: existing row has no usable id", record.Name)
		return 0, false, fmt.Errorf("outbound delivery %q: existing row has no usable id", record.Name)
	}
	switch status := strings.TrimSpace(fmt.Sprint(existing["status"])); status {
	case workflowmodels.OutboundDeliveryStatusDelivered, workflowmodels.OutboundDeliveryStatusSending:
		log.Infof("outbound delivery %d (%s) is %s; not sent again", id, record.Name, status)
		return id, false, nil
	}
	values := orm.RecordMap{
		"topic":             record.Topic,
		"delivery_type":     record.DeliveryType,
		"target_ref":        record.TargetRef,
		"headers":           record.Headers,
		"authorization_ref": record.AuthorizationRef,
		"request_body":      record.RequestBody,
		"status":            workflowmodels.OutboundDeliveryStatusQueued,
		"attempt_count":     0,
		"max_attempts":      record.MaxAttempts,
		"last_error":        "",
		"response_body":     "",
		"next_attempt_at":   record.NextAttemptAt,
	}
	if _, err := s.updateRecord(ctx, orm.RecordMap{"id": id}, values); err != nil {
		log.Errorf("requeue outbound delivery %d: %v", id, err)
		return 0, false, fmt.Errorf("requeue outbound delivery %d: %w", id, err)
	}
	return id, true, nil
}

// outboundDeliveryName is the row name for a notify delivery: the step idempotency key bound by
// the engine, else `<request id>:<random>` (no step key: unique, still correlated to the run),
// else "" (the model generates one).
func outboundDeliveryName(ctx context.Context) string {
	if key := workflowmodels.WorkflowStepIdempotencyKeyFromContext(ctx); key != "" {
		return key
	}
	if requestID := workflowmodels.WorkflowRequestIDFromContext(ctx); requestID != "" {
		return requestID + ":" + uuid.NewString()
	}
	return ""
}

// ManualRetry requeues an existing outbound delivery row so operators can
// replay queued or failed deliveries without creating a duplicate audit record.
func (s *OutboundDeliveryService) ManualRetry(ctx context.Context, request ManualRetryRequest) error {
	targetURL := strings.TrimSpace(request.TargetRef)
	if request.DeliveryID <= 0 {
		log.Errorf("manual retry outbound delivery: delivery id is required")
		return fmt.Errorf("manual retry outbound delivery: delivery id is required")
	}
	if targetURL == "" {
		log.Errorf("manual retry outbound delivery: target ref is required")
		return fmt.Errorf("manual retry outbound delivery: target ref is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	maxAttempts := request.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultHTTPMaxAttempts
	}

	// Re-mark the existing audit row as queued before enqueueing so the UI and
	// persisted state reflect that an operator explicitly requested a replay.
	// attempt_count restarts so the worker's claim (attempt_count < max_attempts) accepts the row.
	if _, err := s.updateRecord(ctx, orm.RecordMap{"id": request.DeliveryID}, orm.RecordMap{
		"status":          workflowmodels.OutboundDeliveryStatusQueued,
		"attempt_count":   0,
		"max_attempts":    maxAttempts,
		"last_error":      "",
		"response_body":   "",
		"next_attempt_at": s.now().Unix(),
	}); err != nil {
		log.Errorf("manual retry outbound delivery: mark queued: %v", err)
		return fmt.Errorf("manual retry outbound delivery: mark queued: %w", err)
	}

	headers := mergeOutboundHeaders(request.Headers)
	if ref := strings.TrimSpace(request.AuthorizationRef); ref != "" {
		authorization, err := credentialRefLookup(workflowmodels.ContextWithWorkflowScope(ctx, request.WorkflowID, request.CompanyID), "authorization", ref)
		if err != nil {
			log.Errorf("manual retry outbound delivery: resolve authorization: %v", err)
			return fmt.Errorf("manual retry outbound delivery: resolve authorization: %w", err)
		}
		headers["Authorization"] = authorization
	}

	job := httpDeliveryJob{
		DeliveryID:  request.DeliveryID,
		TargetRef:   targetURL,
		Headers:     headers,
		RequestBody: cloneBody(request.RequestBody),
		MaxAttempts: maxAttempts,
		DBName:      outboundDBName(ctx),
	}
	if err := s.submitJob(ctx, job); err != nil {
		// The row stays queued (due now), so the sweeper sends it on its next pass.
		log.Errorf("manual retry outbound delivery: submit job: %v", err)
		return fmt.Errorf("manual retry outbound delivery: submit job (the sweeper will send it): %w", err)
	}

	return nil
}

// ensurePool ensures the pool handled by this package.
func ensurePool() *worker.Pool[httpDeliveryJob, struct{}] {
	deliveryPoolState.mu.RLock()
	pool := deliveryPoolState.pool
	deliveryPoolState.mu.RUnlock()
	if pool != nil {
		return pool
	}

	deliveryPoolState.mu.Lock()
	defer deliveryPoolState.mu.Unlock()
	if deliveryPoolState.pool == nil {
		// Lazily initialize with defaults so non-server tests and one-off callers
		// still get a dedicated delivery worker even without explicit bootstrap.
		service := NewOutboundDeliveryService()
		deliveryPoolState.pool = worker.NewPool(service.processHTTPDelivery, worker.WithWorkers(defaultWorkerCount), worker.WithQueue(defaultWorkerQueueSize))
		log.Infof("initialized outbound delivery worker: workers=%d queue=%d", defaultWorkerCount, defaultWorkerQueueSize)
	}
	return deliveryPoolState.pool
}

// submitHTTPDeliveryJob submits the http delivery job handled by this package.
func submitHTTPDeliveryJob(ctx context.Context, submitter submitWithTimeoutFunc, job httpDeliveryJob) error {
	if submitter == nil {
		log.Errorf("submit outbound delivery job: worker pool is not initialized")
		return fmt.Errorf("submit outbound delivery job: worker pool is not initialized")
	}
	if ctx != nil {
		if deadline, ok := ctx.Deadline(); ok {
			remaining := time.Until(deadline)
			if remaining > 0 && remaining < worker.DefaultSubmitTimeout {
				return submitter.SubmitWithTimeout(job, remaining)
			}
		}
	}
	return submitter.SubmitWithTimeout(job, worker.DefaultSubmitTimeout)
}

func (s *OutboundDeliveryService) processHTTPDelivery(ctx context.Context, job httpDeliveryJob) (struct{}, error) {
	if job.MaxAttempts <= 0 {
		job.MaxAttempts = defaultHTTPMaxAttempts
	}
	// Without its database the result could not be recorded (or would land on the default
	// database), so the request is not sent; the row stays queued for the sweeper.
	ctx, err := outboundDeliveryDBContext(ctx, job.DBName)
	if err != nil {
		log.Errorf("outbound delivery %d: bind database %q: %v", job.DeliveryID, job.DBName, err)
		return struct{}{}, fmt.Errorf("outbound delivery %d: bind database %q: %w", job.DeliveryID, job.DBName, err)
	}

	attemptNumber, owned, err := s.claimForSend(ctx, &job)
	if err != nil {
		return struct{}{}, err
	}
	if !owned {
		// The sweeper (or a manual retry's worker) claimed the row first: one sender per attempt.
		log.Debugf("outbound delivery %d already claimed; skipped", job.DeliveryID)
		return struct{}{}, nil
	}

	resp, err := s.postHTTP(ctx, job.TargetRef, job.Headers, job.RequestBody)
	if err != nil {
		if updateErr := s.markAttemptFailed(ctx, job, attemptNumber, err.Error(), ""); updateErr != nil {
			log.Errorf("post outbound delivery: %v; mark failure: %v", err, updateErr)
			return struct{}{}, fmt.Errorf("post outbound delivery: %v; mark failure: %w", err, updateErr)
		}
		return struct{}{}, err
	}
	defer resp.Body.Close()

	// An oversized body is kept truncated for the audit row; the status decides the outcome.
	responseBody, _ := safehttp.ReadBody(resp.Body, 0)
	responseText := strings.TrimSpace(string(responseBody))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if _, err := s.updateRecord(ctx, orm.RecordMap{"id": job.DeliveryID}, orm.RecordMap{
			"status":          workflowmodels.OutboundDeliveryStatusDelivered,
			"attempt_count":   attemptNumber,
			"last_attempt_at": s.now().Unix(),
			"last_error":      "",
			"response_body":   responseText,
			"next_attempt_at": int64(0),
		}); err != nil {
			log.Errorf("mark outbound delivery delivered: %v", err)
			return struct{}{}, fmt.Errorf("mark outbound delivery delivered: %w", err)
		}
		return struct{}{}, nil
	}

	failureMessage := fmt.Sprintf("http status=%d body=%s", resp.StatusCode, responseText)
	if isPermanentHTTPStatus(resp.StatusCode) {
		if err := s.markPermanentFailure(ctx, job.DeliveryID, attemptNumber, failureMessage, responseText); err != nil {
			log.Errorf("mark outbound delivery permanent failure: %v", err)
			return struct{}{}, fmt.Errorf("mark outbound delivery permanent failure: %w", err)
		}
		log.Errorf("permanent outbound delivery failure: %s", failureMessage)
		return struct{}{}, fmt.Errorf("permanent outbound delivery failure: %s", failureMessage)
	}

	if err := s.markAttemptFailed(ctx, job, attemptNumber, failureMessage, responseText); err != nil {
		log.Errorf("mark outbound delivery retryable failure: %v", err)
		return struct{}{}, fmt.Errorf("mark outbound delivery retryable failure: %w", err)
	}
	log.Errorf("retryable outbound delivery failure: %s", failureMessage)
	return struct{}{}, fmt.Errorf("retryable outbound delivery failure: %s", failureMessage)
}

// claimForSend makes this worker the row's only sender for the attempt. A sweeper job arrives
// already claimed (job.Attempt > 0). A fast-path or manual-retry job claims the row with a
// conditional UPDATE; without a claim function (unit doubles) the row is marked sending directly.
func (s *OutboundDeliveryService) claimForSend(ctx context.Context, job *httpDeliveryJob) (int, bool, error) {
	if job.Attempt > 0 {
		return job.Attempt, true, nil
	}
	if s.claimOne != nil {
		claimed, ok, err := s.claimOne(ctx, job.DeliveryID, s.now())
		if err != nil || !ok {
			return 0, ok, err
		}
		if claimed.MaxAttempts > 0 {
			job.MaxAttempts = claimed.MaxAttempts
		}
		return claimed.Attempt, true, nil
	}
	if _, err := s.updateRecord(ctx, orm.RecordMap{"id": job.DeliveryID}, orm.RecordMap{
		"status":          workflowmodels.OutboundDeliveryStatusSending,
		"attempt_count":   1,
		"last_attempt_at": s.now().Unix(),
		"last_error":      "",
		"response_body":   "",
		"next_attempt_at": int64(0),
	}); err != nil {
		log.Errorf("mark outbound delivery sending: %v", err)
		return 0, false, fmt.Errorf("mark outbound delivery sending: %w", err)
	}
	return 1, true, nil
}

// markAttemptFailed records a transport error or retryable status: retryable_failed with an
// exponential backoff while attempts are left, permanent_failed on the last one.
func (s *OutboundDeliveryService) markAttemptFailed(ctx context.Context, job httpDeliveryJob, attemptNumber int, failureMessage string, responseBody string) error {
	if attemptNumber >= job.MaxAttempts {
		return s.markPermanentFailure(ctx, job.DeliveryID, attemptNumber, fmt.Sprintf("%s (attempt %d of %d, none left)", failureMessage, attemptNumber, job.MaxAttempts), responseBody)
	}
	return s.markRetryableFailure(ctx, job.DeliveryID, attemptNumber, failureMessage, responseBody)
}

func (s *OutboundDeliveryService) markPermanentFailure(ctx context.Context, deliveryID int64, attemptCount int, failureMessage string, responseBody string) error {
	_, err := s.updateRecord(ctx, orm.RecordMap{"id": deliveryID}, orm.RecordMap{
		"status":          workflowmodels.OutboundDeliveryStatusPermanentFailed,
		"attempt_count":   attemptCount,
		"last_attempt_at": s.now().Unix(),
		"last_error":      failureMessage,
		"response_body":   strings.TrimSpace(responseBody),
		"next_attempt_at": int64(0),
	})
	return err
}

// maxOutboundRetryDelay caps the exponential backoff between delivery attempts.
const maxOutboundRetryDelay = time.Hour

// outboundRetryBackoff is retryDelay * 2^(attempt-1), capped at maxOutboundRetryDelay: attempt 1
// failed -> base delay, attempt 2 failed -> twice that, and so on.
func outboundRetryBackoff(base time.Duration, attempt int) time.Duration {
	if base <= 0 {
		base = defaultRetryDelay()
	}
	delay := base
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay >= maxOutboundRetryDelay {
			return maxOutboundRetryDelay
		}
	}
	if delay > maxOutboundRetryDelay {
		return maxOutboundRetryDelay
	}
	return delay
}

// markRetryableFailure stores retryable_failed with next_attempt_at = last_attempt_at + backoff,
// which is when the sweeper claims the row again.
func (s *OutboundDeliveryService) markRetryableFailure(ctx context.Context, deliveryID int64, attemptCount int, failureMessage string, responseBody string) error {
	lastAttemptAt := s.now()
	_, err := s.updateRecord(ctx, orm.RecordMap{"id": deliveryID}, orm.RecordMap{
		"status":          workflowmodels.OutboundDeliveryStatusRetryableFailed,
		"attempt_count":   attemptCount,
		"last_attempt_at": lastAttemptAt.Unix(),
		"last_error":      failureMessage,
		"response_body":   strings.TrimSpace(responseBody),
		"next_attempt_at": lastAttemptAt.Add(outboundRetryBackoff(s.retryDelay, attemptCount)).Unix(),
	})
	return err
}

// SweepDueDeliveries is the outbox sweeper (plan T53), run per database by the
// `workflow.sweep_outbound` go_handler: it claims due notify rows (see
// workflowmodels.ClaimDueOutboundDeliveries) and hands each to the delivery worker. A row's
// Authorization header is never stored, so it is resolved again from authorization_ref in the
// scope of the workflow and company that queued it. Returns the number of rows handed over.
func (s *OutboundDeliveryService) SweepDueDeliveries(ctx context.Context, limit int) (int, error) {
	if s.claimDue == nil {
		return 0, nil
	}
	claimed, err := s.claimDue(ctx, workflowmodels.OutboundSweepOptions{Now: s.now(), Limit: limit})
	if err != nil {
		return 0, err
	}
	dbName := outboundDBName(ctx)
	submitted := 0
	for _, row := range claimed {
		headers := mergeOutboundHeaders(stringHeaders(row.Headers))
		maxAttempts := row.MaxAttempts
		if maxAttempts <= 0 {
			maxAttempts = defaultHTTPMaxAttempts
		}
		job := httpDeliveryJob{
			DeliveryID:  row.ID,
			TargetRef:   strings.TrimSpace(row.TargetRef),
			Headers:     headers,
			RequestBody: cloneBody(row.RequestBody),
			MaxAttempts: maxAttempts,
			DBName:      dbName,
			Attempt:     row.Attempt,
		}
		if ref := strings.TrimSpace(row.AuthorizationRef); ref != "" {
			workflowID := int64(0)
			if row.WorkflowID != nil {
				workflowID = *row.WorkflowID
			}
			authorization, err := credentialRefLookup(workflowmodels.ContextWithWorkflowScope(ctx, workflowID, row.CompanyID), "authorization", ref)
			if err != nil {
				log.Errorf("outbound delivery %d: resolve authorization: %v", row.ID, err)
				if markErr := s.markAttemptFailed(ctx, job, row.Attempt, "resolve authorization: "+err.Error(), ""); markErr != nil {
					log.Errorf("outbound delivery %d: mark failure: %v", row.ID, markErr)
				}
				continue
			}
			job.Headers["Authorization"] = authorization
		}
		if err := s.submitJob(ctx, job); err != nil {
			// The worker queue is full: give the attempt back and let a later pass retry it.
			log.Warnf("outbound delivery %d: worker queue unavailable: %v", row.ID, err)
			if _, markErr := s.updateRecord(ctx, orm.RecordMap{"id": row.ID}, orm.RecordMap{
				"status":          workflowmodels.OutboundDeliveryStatusRetryableFailed,
				"attempt_count":   row.Attempt - 1,
				"last_error":      "delivery worker queue unavailable",
				"next_attempt_at": s.now().Add(outboundFastPathGrace).Unix(),
			}); markErr != nil {
				log.Errorf("outbound delivery %d: release claim: %v", row.ID, markErr)
			}
			continue
		}
		submitted++
	}
	return submitted, nil
}

// stringHeaders narrows stored JSON headers to the request's string map.
func stringHeaders(raw map[string]any) map[string]string {
	out := make(map[string]string, len(raw))
	for key, value := range raw {
		if strings.TrimSpace(key) == "" || value == nil {
			continue
		}
		out[key] = strings.TrimSpace(fmt.Sprint(value))
	}
	return out
}

func isPermanentHTTPStatus(statusCode int) bool {
	return statusCode >= 400 && statusCode < 500 && statusCode != http.StatusTooManyRequests
}

// outboundDBName is the database bound on ctx, or "" (default database) when none is.
func outboundDBName(ctx context.Context) string {
	return strings.TrimSpace(orm.EnvironmentFromContext(ctx).DBName())
}

// outboundDeliveryDBContext binds a sudo env on dbName for the detached delivery worker. An empty
// name keeps parent (jobs queued before T46, or callers without a database env). Swappable in tests.
var outboundDeliveryDBContext = func(parent context.Context, dbName string) (context.Context, error) {
	if dbName == "" {
		return parent, nil
	}
	registered, err := orm.EnsureNamedBunDBNoCreate(dbName)
	if err != nil {
		return nil, err
	}
	if registered == nil || registered.DB == nil {
		return nil, fmt.Errorf("database %q is not available", dbName)
	}
	return orm.ContextWithEnvironment(parent, orm.NewEnvironmentWithDBName(registered.DB, dbName, 0, nil, true)), nil
}

// headerMapToAny widens a string header map to the interface{}-valued shape
// stored on OutboundDeliveryRecord.Headers.
func headerMapToAny(headers map[string]string) map[string]interface{} {
	out := make(map[string]interface{}, len(headers))
	for key, value := range headers {
		out[key] = value
	}
	return out
}

// workflowScopeWorkflowID returns the running workflow's id, or nil.
func workflowScopeWorkflowID(ctx context.Context) *int64 {
	scope, ok := workflowmodels.WorkflowScopeFromContext(ctx)
	if !ok || scope.WorkflowID <= 0 {
		return nil
	}
	id := scope.WorkflowID
	return &id
}

// workflowScopeCompanyID returns the company of the running workflow, or nil.
func workflowScopeCompanyID(ctx context.Context) *int64 {
	scope, ok := workflowmodels.WorkflowScopeFromContext(ctx)
	if !ok {
		return nil
	}
	return scope.CompanyID
}

// storedOutboundHeaders drops the resolved Authorization header (any casing)
// when it came from a credential reference, so the row never holds the secret.
func storedOutboundHeaders(headers map[string]string, authorizationRef string) map[string]string {
	if strings.TrimSpace(authorizationRef) == "" {
		return headers
	}
	stored := make(map[string]string, len(headers))
	for key, value := range headers {
		if strings.EqualFold(key, "Authorization") {
			continue
		}
		stored[key] = value
	}
	return stored
}

// mergeOutboundHeaders reapplies the default JSON content type so manual
// retries behave the same way as the initial queue path.
func mergeOutboundHeaders(headers map[string]string) map[string]string {
	merged := map[string]string{"Content-Type": contentTypeApplicationJS}
	for key, value := range headers {
		merged[key] = value
	}
	return merged
}

// cloneBody clones the body handled by this package.
func cloneBody(body map[string]interface{}) map[string]interface{} {
	if body == nil {
		return nil
	}
	cloned := make(map[string]interface{}, len(body))
	for key, value := range body {
		cloned[key] = value
	}
	return cloned
}
