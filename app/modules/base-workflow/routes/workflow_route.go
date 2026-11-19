package routes

// File overview:
// - Responsibility: implements the workflow module's Fiber transport handlers and request-bound orchestration for /wh/* endpoints.
// - Key symbols: `RegisterWorkflowRoutes`, `RegisterRoutes`, `HandleWorkflowRoute`.
// - Pattern: Keeps workflow-specific HTTP transport ownership inside the workflow module while reusing shared RPC auth and DB helpers from internal routes.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/modelruntime"
	internalmodels "git.thebase.vn/base_hub/internal/models"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/queries"
	internalroutes "git.thebase.vn/base_hub/internal/routes"
	"git.thebase.vn/base_hub/internal/services/etlflow"
	"git.thebase.vn/base_hub/internal/services/log"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
	"github.com/gofiber/fiber/v3"
)

var (
	workflowRouteModuleEnabled = func(ctx context.Context, dbName string) (bool, error) {
		pool := orm.ResolvePGXPool(dbName)
		if pool == nil {
			pool = orm.PGXPoolFromContext(ctx)
		}
		enabled, err := queries.ModuleInstalled(ctx, pool, "base-workflow")
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "does not exist") {
				return false, nil
			}
			return false, err
		}
		return enabled, nil
	}
	workflowRouteLookupFlow    = loadWorkflowRouteFlow
	workflowRouteServesWebhook = workflowWebhookSettingsFor
	workflowRouteExecute       = func(ctx context.Context, topic string, input []byte) error {
		return etlflow.NewService().ExecuteTopic(ctx, topic, input)
	}
	workflowResolveDBName        = internalroutes.ResolveRPCDBName
	workflowContextForDB         = internalroutes.RPCContextForDB
	workflowRouteCurrentIdentity = internalroutes.CurrentRPCIdentity
	workflowRequireIdentity      = internalroutes.RequireRPCIdentity
	// workflowRouteSearchRead is the read seam for the /wf helper routes; callers pass a context
	// carrying the caller's environment (workflowRouteContext) so ACL and record rules apply.
	workflowRouteSearchRead = func(ctx context.Context, model string, filter orm.RecordMap, fields []string) ([]orm.RecordMap, error) {
		return modelruntime.NewService().SearchRead(ctx, model, filter, fields)
	}
	workflowRouteModelRegistered = func() bool {
		_, ok := orm.LookupModel(internalmodels.FlowModelName)
		return ok
	}
	workflowEnsureRole = workflowmodels.EnsureWorkflowRole
	// workflowRouteRunIDByName reads the id of the run named name (nil when there is none).
	workflowRouteRunIDByName = func(ctx context.Context, name string) (any, error) {
		run, err := workflowRouteLookupRunByName(ctx, name)
		if err != nil || run == nil {
			return nil, err
		}
		return run["id"], nil
	}
)

// workflowRouteDBContext resolves the request's tenant DB name (via the
// shared RequestDBNameRaw/workflowResolveDBName seam) and binds a context to
// it — the two-line resolve+bind step every workflow HTTP handler needs
// before checking identity, extracted here so it isn't duplicated per-handler
// across this file and workflow_node_spec_route.go.
func workflowRouteDBContext(c fiber.Ctx) (context.Context, error) {
	dbName, err := workflowResolveDBName(internalroutes.RequestDBNameRaw(c))
	if err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	return workflowContextForDB(c.Context(), dbName), nil
}

// RegisterRoutes lets the workflow module satisfy internal/modules.HTTPRouteRegistrar.
func RegisterRoutes(router fiber.Router) {
	RegisterWorkflowRoutes(router)
}

// RegisterWorkflowRoutes reserves the /wh/* transport for topic-triggered workflow execution.
func RegisterWorkflowRoutes(router fiber.Router) {
	if router == nil {
		return
	}
	router.Get("/wf/node-specs", HandleWorkflowNodeSpecs)
	router.Get("/wf/runs/latest", HandleWorkflowRunLatest)
	router.Post("/wf/workflows/:id/test-run", HandleWorkflowTestRun)
	router.Post("/wf/workflows/:id/validate-graph", HandleWorkflowValidateGraph)
	router.Get("/wf/workflows/:id/export", HandleWorkflowExport)
	router.Post("/wf/workflows/import", HandleWorkflowImport)
	router.Get("/wf/credentials", HandleWorkflowCredentials)
	router.Get("/wf/sys-credentials", HandleWorkflowSysCredentials)
	router.Get("/wf/sys-models", HandleWorkflowSysModels)
	router.All("/wh", HandleWorkflowRoute)
	router.All("/wh/*", HandleWorkflowRoute)
}

// HandleWorkflowRunLatest returns the most recent workflow run for a given topic.
// The canvas live-run overlay polls this endpoint to colour-code nodes by step status.
func HandleWorkflowRunLatest(c fiber.Ctx) error {
	ctx, err := workflowRouteDBContext(c)
	if err != nil {
		return err
	}
	identity, err := workflowRequireIdentity(c, false)
	if err != nil {
		return err
	}
	// Runs carry trigger payloads: read them as the caller, never with the identity-less DB context.
	ctx = workflowRouteContext(ctx, identity)
	if err := workflowRouteRequireRole(ctx, workflowmodels.WorkflowRoleViewer); err != nil {
		return err
	}

	topic := strings.TrimSpace(c.Query("topic"))
	if topic == "" {
		return fiber.NewError(fiber.StatusBadRequest, "topic is required")
	}

	runs, err := workflowRouteSearchRead(ctx, workflowmodels.WorkflowRunModelName, orm.RecordMap{
		"topic":   topic,
		"__order": "id desc",
		"__limit": 1,
	}, []string{"id", "status", "step_runs", "current_step", "last_error"})
	if err != nil {
		log.Errorf("workflow: runs/latest topic=%s: %v", topic, err)
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	if len(runs) == 0 {
		return c.JSON(fiber.Map{"result": nil})
	}
	return c.JSON(fiber.Map{"result": runs[0]})
}

// workflowTestRunRequest is the body shape accepted by HandleWorkflowTestRun:
// the sample payload the canvas already collects for {{field}} suggestions.
type workflowTestRunRequest struct {
	Payload json.RawMessage `json:"payload"`
	// DryRun runs the workflow without side effects (plan T101): side-effect steps only record
	// their intent, no credential is resolved and the run row is marked dry_run.
	DryRun bool `json:"dry_run"`
}

// HandleWorkflowTestRun executes one manual "Run Now" test of a workflow
// through the same durable ExecuteTopic/step_runs[] audit path as a real
// trigger — no shortcut execution path that skips persistence — tagged with
// trigger_type "manual_test" so operators can tell test runs apart from real
// traffic. The run itself is still synchronous like the existing webhook
// route; the response reports whatever terminal (or in-flight) status the
// run reached.
func HandleWorkflowTestRun(c fiber.Ctx) error {
	ctx, err := workflowRouteDBContext(c)
	if err != nil {
		return err
	}

	identity, err := workflowRequireIdentity(c, false)
	if err != nil {
		return err
	}
	if err := workflowRouteRequireRole(workflowRouteContext(ctx, identity), workflowmodels.WorkflowRoleOperator); err != nil {
		return err
	}

	workflowID, err := strconv.ParseInt(strings.TrimSpace(c.Params("id")), 10, 64)
	if err != nil || workflowID <= 0 {
		return fiber.NewError(fiber.StatusBadRequest, "workflow id must be a positive integer")
	}

	flow, err := workflowRouteLookupFlowByID(ctx, workflowID)
	if err != nil {
		if errors.Is(err, etlflow.ErrFlowNotFound) {
			return fiber.NewError(fiber.StatusNotFound, "workflow not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	if !flow.IsActive {
		return fiber.NewError(fiber.StatusBadRequest, "workflow is inactive")
	}
	if flow.IsTemplate {
		return fiber.NewError(fiber.StatusBadRequest, "workflow is a template: instantiate it first")
	}
	if !workflowRouteAllows(identity, flow) {
		return fiber.NewError(fiber.StatusForbidden, "permission denied: workflow access denied")
	}

	var req workflowTestRunRequest
	if len(c.Body()) > 0 {
		if err := json.Unmarshal(c.Body(), &req); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "payload must be valid JSON")
		}
	}
	payload := []byte(req.Payload)
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	if !json.Valid(payload) {
		return fiber.NewError(fiber.StatusBadRequest, "payload must be valid JSON")
	}

	requestCtx := workflowRouteContext(ctx, identity)
	requestCtx = workflowmodels.ContextWithWorkflowTriggerType(requestCtx, workflowmodels.WorkflowTriggerTypeManualTest)
	requestCtx, requestID := workflowmodels.EnsureWorkflowRequestID(requestCtx)
	if req.DryRun {
		requestCtx = workflowmodels.ContextWithWorkflowDryRun(requestCtx)
	}

	// Best-effort: a step-level execution error still leaves a fully audited
	// run row (failed/dead_letter) — that is a valid outcome to report back,
	// not an HTTP-level failure of the test-run request itself.
	execErr := workflowRouteExecute(requestCtx, flow.Topic, payload)
	if execErr != nil {
		if errors.Is(execErr, etlflow.ErrFlowNotFound) {
			return fiber.NewError(fiber.StatusBadRequest, execErr.Error())
		}
		if errors.Is(execErr, workflowmodels.ErrWorkflowRunConcurrencyLimit) {
			// The refused run gave its request id back, so there is no run to report (plan T100).
			return fiber.NewError(fiber.StatusConflict, execErr.Error())
		}
	}

	run, err := workflowRouteLookupRunByName(ctx, requestID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	if run == nil {
		return fiber.NewError(fiber.StatusInternalServerError, "workflow test run was executed but its audit row could not be found")
	}

	result := fiber.Map{
		"run_id": run["id"],
		"status": run["status"],
	}
	// A run parked at wait_approval (plan T103): the tester gets the resume token once.
	var pending *workflowmodels.WorkflowApprovalPending
	if errors.As(execErr, &pending) {
		result["resume_token"] = pending.Token
		result["expires_at"] = pending.ExpiresAt
	}
	return c.JSON(fiber.Map{"result": result})
}

// HandleWorkflowValidateGraph runs Kahn's cycle check over the workflow's
// currently persisted base.workflow.edge rows and reports whether the DAG is
// acyclic. It never trusts save-time validation as final — a direct RPC
// write to base.workflow.edge could bypass replace_workflow_edges — so this
// endpoint (and executeRunDAG at run start) both re-run the same check.
func HandleWorkflowValidateGraph(c fiber.Ctx) error {
	ctx, err := workflowRouteDBContext(c)
	if err != nil {
		return err
	}

	identity, err := workflowRequireIdentity(c, false)
	if err != nil {
		return err
	}
	if err := workflowRouteRequireRole(workflowRouteContext(ctx, identity), workflowmodels.WorkflowRoleViewer); err != nil {
		return err
	}

	workflowID, err := strconv.ParseInt(strings.TrimSpace(c.Params("id")), 10, 64)
	if err != nil || workflowID <= 0 {
		return fiber.NewError(fiber.StatusBadRequest, "workflow id must be a positive integer")
	}

	flow, err := workflowRouteLookupFlowByID(ctx, workflowID)
	if err != nil {
		if errors.Is(err, etlflow.ErrFlowNotFound) {
			return fiber.NewError(fiber.StatusNotFound, "workflow not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	if !workflowRouteAllows(identity, flow) {
		return fiber.NewError(fiber.StatusForbidden, "permission denied: workflow access denied")
	}

	requestCtx := workflowRouteContext(ctx, identity)
	rows, err := modelruntime.NewService().SearchRead(requestCtx, workflowmodels.WorkflowEdgeModelName, orm.RecordMap{
		"workflow_id": workflowID,
		"__order":     "id asc",
	}, []string{"from_step_key", "to_step_key"})
	if err != nil {
		log.Errorf("workflow: validate-graph workflow_id=%d: %v", workflowID, err)
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}

	layers, validateErr := workflowmodels.ValidateWorkflowGraph(workflowmodels.WorkflowGraphEdgesFromRecords(rows))
	if validateErr != nil {
		var cycleErr *workflowmodels.WorkflowGraphCycleError
		if errors.As(validateErr, &cycleErr) {
			return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
				"result": fiber.Map{
					"acyclic": false,
					"cycle":   cycleErr.StepKeys,
				},
			})
		}
		return fiber.NewError(fiber.StatusInternalServerError, validateErr.Error())
	}

	order := make([]string, 0)
	for _, layer := range layers {
		order = append(order, layer...)
	}
	return c.JSON(fiber.Map{
		"result": fiber.Map{
			"acyclic": true,
			"order":   order,
			"layers":  layers,
		},
	})
}

// HandleWorkflowExport returns a version-tagged JSON snapshot of one
// workflow's actions/triggers/layout, gated by the same RBAC as the
// existing webhook trigger path.
func HandleWorkflowExport(c fiber.Ctx) error {
	ctx, err := workflowRouteDBContext(c)
	if err != nil {
		return err
	}

	identity, err := workflowRequireIdentity(c, false)
	if err != nil {
		return err
	}
	if err := workflowRouteRequireRole(workflowRouteContext(ctx, identity), workflowmodels.WorkflowRoleViewer); err != nil {
		return err
	}

	workflowID, err := strconv.ParseInt(strings.TrimSpace(c.Params("id")), 10, 64)
	if err != nil || workflowID <= 0 {
		return fiber.NewError(fiber.StatusBadRequest, "workflow id must be a positive integer")
	}

	flow, err := workflowRouteLookupFlowByID(ctx, workflowID)
	if err != nil {
		if errors.Is(err, etlflow.ErrFlowNotFound) {
			return fiber.NewError(fiber.StatusNotFound, "workflow not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	if !workflowRouteAllows(identity, flow) {
		return fiber.NewError(fiber.StatusForbidden, "permission denied: workflow access denied")
	}

	requestCtx := workflowRouteContext(ctx, identity)
	result, err := orm.ExecuteRuntimeMethod(
		requestCtx,
		workflowmodels.FlowModelName,
		"export",
		[]string{strconv.FormatInt(workflowID, 10)},
		nil,
		nil,
	)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}

	return c.JSON(fiber.Map{"result": result})
}

// HandleWorkflowImport recreates a workflow from the shape produced by
// HandleWorkflowExport. The new workflow always gets a fresh name/topic —
// see importFlow — so an import can never collide with a live webhook path.
func HandleWorkflowImport(c fiber.Ctx) error {
	ctx, err := workflowRouteDBContext(c)
	if err != nil {
		return err
	}

	identity, err := workflowRequireIdentity(c, false)
	if err != nil {
		return err
	}

	var payload map[string]any
	if err := json.Unmarshal(c.Body(), &payload); err != nil || payload == nil {
		return fiber.NewError(fiber.StatusBadRequest, "import payload must be a JSON object")
	}

	requestCtx := workflowRouteContext(ctx, identity)
	result, err := orm.ExecuteRuntimeMethod(
		requestCtx,
		workflowmodels.FlowModelName,
		"import",
		nil,
		[]any{payload},
		nil,
	)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	created, ok := result.(orm.RecordMap)
	if !ok {
		return fiber.NewError(fiber.StatusInternalServerError, "workflow import did not return a created record")
	}

	return c.JSON(fiber.Map{
		"result": fiber.Map{
			"workflow_id": created["workflow_id"],
		},
	})
}

// HandleWorkflowRoute executes a topic-driven workflow request. Hardening (T43, see
// workflow_webhook_guard.go): body cap, optional HMAC signature with a replay window,
// Idempotency-Key as the unique run name, generic error text for anonymous callers.
func HandleWorkflowRoute(c fiber.Ctx) error {
	topic := strings.Trim(strings.TrimPrefix(c.Path(), "/wh"), "/")
	if topic == "" {
		return workflowNoRoute(c)
	}

	dbName, err := workflowResolveDBName(internalroutes.RequestDBNameRaw(c))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	workflowCtx := workflowContextForDB(c.Context(), dbName)

	enabled, err := workflowRouteModuleEnabled(workflowCtx, dbName)
	if err != nil {
		log.Errorf("workflow route: module check db=%s: %v", dbName, err)
		return fiber.NewError(fiber.StatusServiceUnavailable, "workflow service unavailable")
	}
	if !enabled {
		return workflowNoRoute(c)
	}
	if !workflowRouteModelRegistered() {
		return workflowNoRoute(c)
	}

	flow, err := workflowRouteLookupFlow(workflowCtx, topic)
	if err != nil {
		if errors.Is(err, etlflow.ErrFlowNotFound) {
			return workflowNoRoute(c)
		}
		return fiber.NewError(fiber.StatusServiceUnavailable, "workflow service unavailable")
	}
	// A gallery template (plan T101) never runs, even if someone activates it.
	if flow == nil || flow.IsTemplate {
		return workflowNoRoute(c)
	}
	// /wh/<topic> serves webhook workflows only: an MQTT/Kafka/model/TCP workflow must not
	// be callable over HTTP just because its topic is known.
	settings, err := workflowRouteServesWebhook(workflowCtx, flow.ID)
	if err != nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "workflow trigger lookup failed")
	}
	if !settings.Serves {
		return workflowNoRoute(c)
	}

	var identity *internalroutes.RPCRequestIdentity
	if flow.GroupID == nil || *flow.GroupID <= 0 {
		identity, err = workflowRouteCurrentIdentity(c)
		if err != nil {
			return fiber.NewError(fiber.StatusUnauthorized, "authentication failed")
		}
	} else {
		identity, err = workflowRequireIdentity(c, false)
		if err != nil {
			return err
		}
	}
	if !workflowRouteAllows(identity, flow) {
		return fiber.NewError(fiber.StatusForbidden, "permission denied: workflow access denied")
	}

	body := c.Body()
	if int64(len(body)) > settings.bodyLimit() {
		return fiber.NewError(fiber.StatusRequestEntityTooLarge, "request body too large")
	}
	idempotencyKey := strings.TrimSpace(c.Get(webhookIdempotencyHeader))
	if idempotencyKey != "" && !webhookIdempotencyKeyPattern.MatchString(idempotencyKey) {
		return fiber.NewError(fiber.StatusBadRequest, "Idempotency-Key must be 1-128 characters of A-Z a-z 0-9 . _ : -")
	}

	// Signature before any run lookup, so an unsigned caller learns nothing about past runs.
	var signature []byte
	if settings.HMACSecretRef != "" {
		secret, err := workflowRouteResolveWebhookSecret(workflowCtx, flow, settings.HMACSecretRef)
		if err != nil || secret == "" {
			log.Errorf("workflow route: resolve hmac secret workflow_id=%d: %v", flow.ID, err)
			return fiber.NewError(fiber.StatusServiceUnavailable, "workflow service unavailable")
		}
		signature, err = verifyWebhookSignature([]byte(secret), c.Get(webhookTimestampHeader), c.Get(webhookSignatureHeader), idempotencyKey, body, webhookNow())
		if err != nil {
			return fiber.NewError(fiber.StatusUnauthorized, err.Error())
		}
	}

	var callerUID int64
	if identity != nil {
		callerUID = identity.UserID
	}
	requestCtx := workflowRouteContext(workflowCtx, identity)
	requestCtx = workflowmodels.ContextWithWorkflowTriggerType(requestCtx, workflowmodels.WorkflowTriggerTypeWebhook)
	requestCtx = workflowmodels.ContextWithWorkflowAuditMode(requestCtx, settings.AuditPayload)
	runName := webhookRunName(dbName, flow.ID, callerUID, idempotencyKey, signature)
	if runName != "" {
		exists, err := workflowRouteRunExists(workflowCtx, runName)
		if err != nil {
			return fiber.NewError(fiber.StatusServiceUnavailable, "workflow service unavailable")
		}
		if exists {
			return workflowWebhookDuplicate(workflowCtx, c, flow, runName)
		}
		// A concurrent request with the same name loses on the unique run name (answered as a
		// duplicate below); the workflow still runs once.
		requestCtx = workflowmodels.ContextWithWorkflowRequestID(requestCtx, runName)
	}
	requestCtx, requestID := workflowmodels.EnsureWorkflowRequestID(requestCtx)
	if err := workflowRouteExecute(requestCtx, flow.Topic, body); err != nil {
		if errors.Is(err, etlflow.ErrFlowNotFound) {
			return workflowNoRoute(c)
		}
		if runName != "" && isUniqueViolation(err) {
			return workflowWebhookDuplicate(workflowCtx, c, flow, runName)
		}
		if errors.Is(err, workflowmodels.ErrWorkflowRunConcurrencyLimit) {
			// max_concurrent_runs reached (plan T100): the caller retries later; the refused run gave
			// its request id back, so the same Idempotency-Key is not answered as a duplicate.
			return fiber.NewError(fiber.StatusConflict, "workflow concurrency limit reached")
		}
		var pending *workflowmodels.WorkflowApprovalPending
		if errors.As(err, &pending) {
			// The run parked at a wait_approval step (plan T103): accepted. The caller gets the
			// resume token once (only its hash is stored) to hand to the approver.
			return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
				"result": fiber.Map{"topic": flow.Topic, "workflow": flow.Name, "status": workflowmodels.WorkflowRunStatusWaitingApproval,
					"run_id": pending.RunID, "resume_token": pending.Token, "expires_at": pending.ExpiresAt},
			})
		}
		if errors.Is(err, etlflow.ErrRunRetryScheduled) {
			// A step failed but has attempts left: the run is stored and resume_due_runs retries it
			// (plan T48), so the delivery is accepted, not failed.
			return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
				"result": fiber.Map{"topic": flow.Topic, "workflow": flow.Name, "status": "retry_scheduled",
					"run_id": workflowRouteRunID(workflowCtx, requestID)},
			})
		}
		if identity == nil || identity.UserID <= 0 {
			log.Errorf("workflow route: anonymous run topic=%s: %v", flow.Topic, err)
			return fiber.NewError(fiber.StatusBadRequest, "workflow execution failed")
		}
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	return c.JSON(fiber.Map{
		"result": fiber.Map{
			"topic":    flow.Topic,
			"workflow": flow.Name,
			"status":   "ok",
			"run_id":   workflowRouteRunID(workflowCtx, requestID),
		},
	})
}

// workflowWebhookDuplicate answers a request whose run name already ran, with that run's id (the
// name embeds the caller or the signature digest, so only the original caller can reach it).
func workflowWebhookDuplicate(ctx context.Context, c fiber.Ctx, flow *internalmodels.FlowRecord, runName string) error {
	return c.JSON(fiber.Map{
		"result": fiber.Map{
			"topic":    flow.Topic,
			"workflow": flow.Name,
			"status":   "duplicate",
			"run_id":   workflowRouteRunID(ctx, runName),
		},
	})
}

// workflowRouteRunID is the id of run `name` for the /wh answer (plan T95), or nil when it cannot
// be read: the run already happened, so a failed lookup never turns the answer into an error.
func workflowRouteRunID(ctx context.Context, name string) any {
	if strings.TrimSpace(name) == "" {
		return nil
	}
	id, err := workflowRouteRunIDByName(ctx, name)
	if err != nil {
		log.Warnf("workflow route: run id of %s: %v", name, err)
		return nil
	}
	return id
}

// workflowNoRoute answers every "nothing to run here" case (module off, unknown topic,
// non-webhook workflow) with the same 404 envelope, so callers cannot tell them apart.
func workflowNoRoute(_ fiber.Ctx) error {
	return fiber.NewError(fiber.StatusNotFound, "no workflow")
}

func loadWorkflowRouteFlow(ctx context.Context, topic string) (*internalmodels.FlowRecord, error) {
	flows, err := modelruntime.NewService().SearchRead(ctx, internalmodels.FlowModelName, orm.RecordMap{
		"topic":     strings.TrimSpace(topic),
		"is_active": true,
		"__order":   "version desc, id desc",
		"__limit":   1,
	}, nil)
	if err != nil {
		log.Errorf("workflow route: load flow topic=%s: %v", topic, err)
		return nil, fmt.Errorf("workflow route: load flow topic=%s: %w", topic, err)
	}
	if len(flows) == 0 {
		log.Errorf("%v: topic=%s", etlflow.ErrFlowNotFound, strings.TrimSpace(topic))
		return nil, fmt.Errorf("%w: topic=%s", etlflow.ErrFlowNotFound, strings.TrimSpace(topic))
	}

	delete(flows[0], "action_ids")

	raw, err := json.Marshal(flows[0])
	if err != nil {
		return nil, err
	}
	var flow internalmodels.FlowRecord
	if err := json.Unmarshal(raw, &flow); err != nil {
		return nil, err
	}
	return &flow, nil
}

func workflowRouteLookupFlowByID(ctx context.Context, workflowID int64) (*internalmodels.FlowRecord, error) {
	flows, err := modelruntime.NewService().SearchRead(ctx, internalmodels.FlowModelName, orm.RecordMap{
		"id":      workflowID,
		"__limit": 1,
	}, nil)
	if err != nil {
		log.Errorf("workflow route: load flow id=%d: %v", workflowID, err)
		return nil, fmt.Errorf("workflow route: load flow id=%d: %w", workflowID, err)
	}
	if len(flows) == 0 {
		return nil, fmt.Errorf("%w: id=%d", etlflow.ErrFlowNotFound, workflowID)
	}

	delete(flows[0], "action_ids")

	raw, err := json.Marshal(flows[0])
	if err != nil {
		return nil, err
	}
	var flow internalmodels.FlowRecord
	if err := json.Unmarshal(raw, &flow); err != nil {
		return nil, err
	}
	return &flow, nil
}

// workflowRouteLookupRunByName resolves the audit row created by this request
// via its unique `name` (the workflow request ID), so the response can report
// the run's actual persisted id/status instead of guessing at the latest row
// for the topic — which would race against any other run in flight.
func workflowRouteLookupRunByName(ctx context.Context, requestID string) (orm.RecordMap, error) {
	runs, err := modelruntime.NewService().SearchRead(ctx, workflowmodels.WorkflowRunModelName, orm.RecordMap{
		"name":    strings.TrimSpace(requestID),
		"__limit": 1,
	}, []string{"id", "status"})
	if err != nil {
		log.Errorf("workflow route: load run name=%s: %v", requestID, err)
		return nil, fmt.Errorf("workflow route: load run name=%s: %w", requestID, err)
	}
	if len(runs) == 0 {
		return nil, nil
	}
	return runs[0], nil
}

// workflowRouteRequireRole answers 403 when the caller lacks role. The /wf/* helper routes call
// runtime methods directly, past the RPC dispatcher's model ACL (and so past workflowRoleAccess),
// so each one checks its workflow role here (plan T94).
func workflowRouteRequireRole(ctx context.Context, role workflowmodels.WorkflowRole) error {
	if err := workflowEnsureRole(ctx, role); err != nil {
		return fiber.NewError(fiber.StatusForbidden, err.Error())
	}
	return nil
}

func workflowRouteAllows(identity *internalroutes.RPCRequestIdentity, flow *internalmodels.FlowRecord) bool {
	if flow == nil {
		return false
	}
	if flow.GroupID == nil || *flow.GroupID <= 0 {
		return true
	}
	if identity == nil {
		return false
	}
	if identity.Sudo {
		return true
	}
	return slices.Contains(identity.GroupIDs, *flow.GroupID)
}

func workflowRouteContext(ctx context.Context, identity *internalroutes.RPCRequestIdentity) context.Context {
	if identity == nil {
		return ctx
	}
	return orm.ContextWithEnvironment(ctx, orm.EnvironmentForIdentity(&orm.RBACIdentity{
		UserID:       identity.UserID,
		DBName:       identity.DBName,
		Login:        identity.Username,
		RoleID:       identity.RoleID,
		RoleName:     identity.RoleName,
		GroupIDs:     append([]int64(nil), identity.GroupIDs...),
		CompanyID:    identity.CompanyID,
		CompanyIDs:   append([]int64(nil), identity.CompanyIDs...),
		AuthMode:     identity.AuthMode,
		IsSuperadmin: identity.Sudo,
	}))
}

// fiber:context-methods migrated
