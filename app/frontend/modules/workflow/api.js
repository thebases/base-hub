// Workflow-specific RPC layer. Faithful port of frontend-admin's
// modules/workflow/api.ts: node specs + action catalog, full action/trigger
// reads, replace_action_sequence, DAG edge CRUD, validate-graph, the
// sys.model/credential pickers the node param dialog uses, plus (7c)
// test-run/latest-run polling and export/import.
import {
  authenticatedHeaders,
  buildBackendURL,
  createModelRpc,
  emitInvalidAuthEvent,
  readResponsePayload,
  shouldInvalidateSession,
} from "@/services/api/jsonv2.js";

// DEFAULT_RETRY_POLICY mirrors the backend's workflowmodels.DefaultRetryPolicy().
// retry_policy is the single source of truth for retry behavior —
// timeout_ms/retry_count do not exist on base.workflow.action.
export const DEFAULT_RETRY_POLICY = { max_attempts: 3, retry_cool_down: 15 };

// stringFieldValue guards char-field mappers against the backend's "empty
// relation -> false" serialization: `String(false ?? "")` would produce the
// literal text "false" and corrupt any write that re-sends the row.
function stringFieldValue(value) {
  return typeof value === "string" ? value : "";
}

function tupleOrScalarID(value) {
  if (Array.isArray(value) && value.length > 0) return value[0];
  return value;
}

// normalizeStoredObject is the local stand-in for jsonv2.ts's
// normalizeStoredWindowActionContext (not exported by this app's jsonv2.js):
// coerce a stored JSON column into a plain object, tolerating a double-encoded
// JSON string and rejecting arrays/scalars.
function normalizeStoredObject(value) {
  if (typeof value === "string") {
    if (!value.trim()) return {};
    try {
      return normalizeStoredObject(JSON.parse(value));
    } catch {
      return {};
    }
  }
  if (!value || typeof value !== "object" || Array.isArray(value)) return {};
  return { ...value };
}

function normalizeStoredStringArray(value) {
  if (!Array.isArray(value)) return [];
  return value.map((item) => (typeof item === "string" ? item.trim() : "")).filter(Boolean);
}

// normalizeWorkflowRetryPolicy falls back to DEFAULT_RETRY_POLICY for rows
// created before retry_policy existed, and for any partial/malformed object.
function normalizeWorkflowRetryPolicy(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return { ...DEFAULT_RETRY_POLICY };
  }
  const maxAttempts = Number(value.max_attempts);
  const retryCoolDown = Number(value.retry_cool_down);
  return {
    max_attempts: Number.isFinite(maxAttempts) && maxAttempts > 0 ? maxAttempts : DEFAULT_RETRY_POLICY.max_attempts,
    retry_cool_down:
      Number.isFinite(retryCoolDown) && retryCoolDown > 0 ? retryCoolDown : DEFAULT_RETRY_POLICY.retry_cool_down,
  };
}

// requestJSON is the shared raw-fetch helper for the /wf/* REST routes (node
// specs, validate-graph, sys-models, credentials) — these are not JSON-RPC
// model calls, so they bypass createModelRpc but must still carry the shared
// Bearer headers and invalidate the session on 401/403.
async function requestJSON(path, { method = "GET", body } = {}) {
  const init = { method, cache: "no-store" };
  init.headers = body === undefined ? authenticatedHeaders() : authenticatedHeaders({ "Content-Type": "application/json" });
  if (body !== undefined) init.body = JSON.stringify(body);
  const response = await fetch(buildBackendURL(path), init);
  const payload = await readResponsePayload(response);
  return { response, payload };
}

export async function fetchWorkflowActionCatalog() {
  const response = await createModelRpc("base.workflow.action.catalog").search(
    [["is_active", "=", true]],
    ["id", "name", "code", "version", "description"],
    "id ASC",
    0,
    200,
    false,
  );
  const rows = Array.isArray(response.result) ? response.result : [];
  return rows.map((row) => ({
    id: Number(row.id ?? 0),
    name: stringFieldValue(row.name),
    code: stringFieldValue(row.code),
    version: Number(row.version ?? 1),
    description: stringFieldValue(row.description),
  }));
}

// fetchWorkflowNodeSpecs drives the whole palette + param form: new nodes
// inherit their defaults from here, never from a hardcoded frontend list.
export async function fetchWorkflowNodeSpecs() {
  const { response, payload } = await requestJSON("/wf/node-specs");
  if (!response.ok) {
    if (shouldInvalidateSession(response)) emitInvalidAuthEvent();
    throw new Error(`Response error: ${response.status} - ${JSON.stringify(payload)}`);
  }
  if (!payload) throw new Error("Response error: empty response body");
  // Standard envelope is {result:{triggers,actions}}; fall back to the bare shape.
  const specs = payload.result ?? payload;
  return {
    triggers: Array.isArray(specs.triggers)
      ? specs.triggers.map((trigger) => ({ ...trigger, params: normalizeWorkflowNodeParams(trigger.params) }))
      : [],
    actions: Array.isArray(specs.actions)
      ? specs.actions.map((action) => ({ ...action, params: normalizeWorkflowNodeParams(action.params) }))
      : [],
  };
}

function normalizeWorkflowNodeParams(params) {
  if (!Array.isArray(params)) return [];
  return params.flatMap((param) => {
    if (!param || typeof param !== "object") return [];
    const name = typeof param.name === "string" ? param.name.trim() : "";
    if (!name) return [];
    const label = typeof param.label === "string" ? param.label.trim() : name;
    const type = typeof param.type === "string" ? param.type.trim() : "string";
    return [
      {
        name,
        label: label || name,
        type: type || "string",
        required: Boolean(param.required),
        default: param.default,
        help: typeof param.help === "string" ? param.help : undefined,
        // Storage and coercion hints the param dialog needs (node-params.js): without them a
        // spec-driven param has no storage and is silently never saved.
        column: typeof param.column === "string" ? param.column : undefined,
        extra_key: typeof param.extra_key === "string" ? param.extra_key : undefined,
        secret: typeof param.secret === "string" ? param.secret : undefined,
        value_type: typeof param.value_type === "string" ? param.value_type : undefined,
        options: Array.isArray(param.options)
          ? param.options.flatMap((option) => {
              if (!option || typeof option !== "object") return [];
              const value = typeof option.value === "string" ? option.value : String(option.value ?? "");
              if (!value) return [];
              const optionLabel = typeof option.label === "string" ? option.label : value;
              return [{ value, label: optionLabel || value }];
            })
          : [],
        selection: Array.isArray(param.selection)
          ? param.selection.flatMap((option) => {
              if (!option || typeof option !== "object") return [];
              const value = String(option.value ?? "");
              return value ? [{ value, label: typeof option.label === "string" && option.label ? option.label : value }] : [];
            })
          : undefined,
      },
    ];
  });
}

// fetchWorkflowActions lists a workflow's ordered action rows — the canvas's
// action nodes. Reads the full column set because every one of them round
// trips back through replace_action_sequence on save.
export async function fetchWorkflowActions(workflowID) {
  const response = await createModelRpc("base.workflow.action").search(
    [["workflow_id", "=", workflowID]],
    [
      "id",
      "sequence",
      "name",
      "action_code",
      "catalog_id",
      "description",
      "is_active",
      "step_key",
      "branch_label",
      "condition_step_key",
      "model_name",
      "target_ref",
      "channel",
      "output_field",
      "hmac_key",
      "aes_key",
      "timeout_seconds",
      "retry_policy",
      "continue_on_error",
      "flatten_input",
      "return_saved_record",
      "include_raw",
      "include_key",
      "key_list",
      "input_contract",
      "output_contract",
      "headers",
      "authorization",
      "template",
      "extra_params",
    ],
    "sequence ASC, id ASC",
    0,
    500,
    false,
  );
  const rows = Array.isArray(response.result) ? response.result : [];
  return rows.map((row) => ({
    id: Number(row.id ?? 0),
    sequence: Number(row.sequence ?? 0),
    name: stringFieldValue(row.name),
    action_code: stringFieldValue(row.action_code),
    catalog_id: row.catalog_id,
    description: stringFieldValue(row.description),
    is_active: row.is_active !== false,
    step_key: stringFieldValue(row.step_key),
    branch_label: stringFieldValue(row.branch_label),
    condition_step_key: stringFieldValue(row.condition_step_key),
    model_name: stringFieldValue(row.model_name),
    target_ref: stringFieldValue(row.target_ref),
    channel: stringFieldValue(row.channel),
    output_field: stringFieldValue(row.output_field),
    hmac_key: stringFieldValue(row.hmac_key),
    aes_key: stringFieldValue(row.aes_key),
    timeout_seconds: Number(row.timeout_seconds ?? 0),
    retry_policy: normalizeWorkflowRetryPolicy(row.retry_policy),
    continue_on_error: Boolean(row.continue_on_error ?? false),
    flatten_input: Boolean(row.flatten_input ?? false),
    return_saved_record: Boolean(row.return_saved_record ?? false),
    include_raw: Boolean(row.include_raw ?? false),
    include_key: Boolean(row.include_key ?? false),
    key_list: normalizeStoredStringArray(row.key_list),
    input_contract: normalizeStoredObject(row.input_contract),
    output_contract: normalizeStoredObject(row.output_contract),
    headers: normalizeStoredObject(row.headers),
    authorization: stringFieldValue(row.authorization),
    template: normalizeStoredObject(row.template),
    extra_params: normalizeStoredObject(row.extra_params),
  }));
}

// fetchWorkflowTriggers lists a workflow's trigger rows — rendered as the
// fixed entry node ahead of the first action.
export async function fetchWorkflowTriggers(workflowID) {
  const response = await createModelRpc("base.workflow.trigger").search(
    [["workflow_id", "=", workflowID]],
    ["id", "workflow_id", "trigger_type", "topic", "is_active", "config"],
    "id ASC",
    0,
    50,
    false,
  );
  const rows = Array.isArray(response.result) ? response.result : [];
  return rows.map((row) => ({
    id: Number(row.id ?? 0),
    workflow_id: Number(tupleOrScalarID(row.workflow_id) ?? 0),
    type: stringFieldValue(row.trigger_type),
    topic: stringFieldValue(row.topic),
    is_active: Boolean(row.is_active ?? false),
    config: normalizeStoredObject(row.config),
  }));
}

// replaceWorkflowActionSequence saves the whole ordered sequence (and, when given, the canvas
// layout) in one server transaction. expectedRevision (the revision the canvas loaded) makes the
// server refuse the save with a Conflict when someone saved the workflow in between. Resolves to
// {ok, revision}.
export async function replaceWorkflowActionSequence(workflowID, items, { expectedRevision, layout } = {}) {
  const kwargs = { workflow_id: workflowID, items };
  if (Number.isInteger(expectedRevision) && expectedRevision >= 0) kwargs.expected_revision = expectedRevision;
  if (layout !== undefined) kwargs.layout = layout;
  const response = await createModelRpc("base.workflow").call("replace_action_sequence", [], kwargs);
  return response.result;
}

export async function unlinkWorkflowAction(actionID) {
  const response = await createModelRpc("base.workflow.action").callById("unlink", actionID);
  return response.result;
}

// fetchWorkflowEdges lists the persisted base.workflow.edge rows for one
// workflow — only meaningful in DAG mode.
export async function fetchWorkflowEdges(workflowID) {
  const response = await createModelRpc("base.workflow.edge").search(
    [["workflow_id", "=", workflowID]],
    ["id", "workflow_id", "from_step_key", "to_step_key", "edge_type", "condition_expr"],
    "id ASC",
    0,
    500,
    false,
  );
  const rows = Array.isArray(response.result) ? response.result : [];
  return rows.map((row) => ({
    id: Number(row.id ?? 0),
    workflow_id: Number(tupleOrScalarID(row.workflow_id) ?? 0),
    from_step_key: stringFieldValue(row.from_step_key),
    to_step_key: stringFieldValue(row.to_step_key),
    edge_type: stringFieldValue(row.edge_type) || "success",
    condition_expr: stringFieldValue(row.condition_expr),
  }));
}

export async function createWorkflowEdge(workflowID, fromStepKey, toStepKey) {
  const response = await createModelRpc("base.workflow.edge").create({
    workflow_id: workflowID,
    from_step_key: fromStepKey,
    to_step_key: toStepKey,
  });
  const record = response.result ?? {};
  return Number(record.id ?? 0);
}

export async function deleteWorkflowEdge(edgeID) {
  // The RPC client's delete method is named `delete`, not `unlink` — the
  // model-level unlink method only exists on the callById path.
  const response = await createModelRpc("base.workflow.edge").delete([edgeID]);
  return response.result;
}

// updateWorkflowEdge writes edge_type/condition_expr on one persisted edge in
// place — the per-edge counterpart of the bulk replace, used when only the
// routing metadata changes and from/to_step_key stay the same.
export async function updateWorkflowEdge(edgeID, values) {
  const response = await createModelRpc("base.workflow.edge").write([edgeID], values);
  return response.result;
}

// replaceWorkflowEdges unlink-then-recreates a workflow's whole DAG edge set
// in one call. Kept for parity with the React API surface; the canvas itself
// uses the per-edge create/delete calls above (edges are persisted
// immediately, not deferred to Save).
async function replaceWorkflowEdges(workflowID, items, { expectedRevision } = {}) {
  const kwargs = { workflow_id: workflowID, items };
  if (Number.isInteger(expectedRevision) && expectedRevision >= 0) kwargs.expected_revision = expectedRevision;
  const response = await createModelRpc("base.workflow").call("replace_workflow_edges", [], kwargs);
  return response.result;
}

// fetchLatestWorkflowID returns the most recently created workflow id, or 0 when there is none —
// what a bare /web/base.workflow/canvas opens.
export async function fetchLatestWorkflowID() {
  const response = await createModelRpc("base.workflow").search([], ["id"], "id DESC", 0, 1, false);
  const rows = Array.isArray(response.result) ? response.result : [];
  const id = Number(rows[0]?.id ?? 0);
  return Number.isFinite(id) && id > 0 ? id : 0;
}

// validateWorkflowGraph re-runs the server-side Kahn cycle check over the
// workflow's persisted edges. A 422 here is an expected, meaningful response
// (a rejected cyclic graph), not a transport failure — so it resolves to the
// {acyclic:false, cycle} payload instead of throwing.
export async function validateWorkflowGraph(workflowID) {
  const { response, payload } = await requestJSON(`/wf/workflows/${workflowID}/validate-graph`, { method: "POST", body: {} });
  if (response.status === 422 && payload?.result) return payload.result;
  if (!response.ok) {
    if (shouldInvalidateSession(response)) emitInvalidAuthEvent();
    throw new Error(`Response error: ${response.status} - ${JSON.stringify(payload)}`);
  }
  if (!payload?.result) throw new Error("workflow validate-graph response was missing a result");
  return payload.result;
}

// fetchWorkflowLatestRun polls the most recent run summary for a workflow's
// trigger topic — powers the canvas's live run-status overlay.
export async function fetchWorkflowLatestRun(topic) {
  const { response, payload } = await requestJSON(`/wf/runs/latest?topic=${encodeURIComponent(topic)}`);
  if (!response.ok) {
    if (shouldInvalidateSession(response)) emitInvalidAuthEvent();
    throw new Error(`Response error: ${response.status} - ${JSON.stringify(payload)}`);
  }
  if (!payload) return null;
  const run = payload.result;
  if (!run || typeof run !== "object") return null;
  return {
    id: run.id,
    status: run.status,
    current_step: run.current_step,
    last_error: run.last_error,
    step_runs: run.step_runs,
  };
}

// runWorkflowTestRun triggers one manual "Run Now" test execution of a saved
// workflow through the same durable, audited path as a real webhook trigger.
// dryRun asks the server to skip every step with outside effects and only record what it would do.
export async function runWorkflowTestRun(workflowID, payload, { dryRun = false } = {}) {
  const { response, payload: responsePayload } = await requestJSON(`/wf/workflows/${workflowID}/test-run`, {
    method: "POST",
    body: { payload: payload ?? {}, dry_run: dryRun },
  });
  if (!response.ok) {
    if (shouldInvalidateSession(response)) emitInvalidAuthEvent();
    throw routeError(response, responsePayload);
  }
  if (!responsePayload?.result) throw new Error("workflow test-run response was missing a result");
  return responsePayload.result;
}

// fetchWorkflowExport downloads a portable, version-tagged JSON snapshot of one workflow
// (settings, actions, DAG edges, triggers, layout; schema v2, see workflow_import_export.go).
export async function fetchWorkflowExport(workflowID) {
  const { response, payload } = await requestJSON(`/wf/workflows/${workflowID}/export`);
  if (!response.ok) {
    if (shouldInvalidateSession(response)) emitInvalidAuthEvent();
    throw new Error(`Response error: ${response.status} - ${JSON.stringify(payload)}`);
  }
  if (!payload?.result) throw new Error("workflow export response was missing a result");
  return payload.result;
}

// importWorkflowExport recreates a workflow from the document produced by
// fetchWorkflowExport. The new workflow always gets a fresh name/topic on the
// backend.
export async function importWorkflowExport(document) {
  const { response, payload } = await requestJSON("/wf/workflows/import", { method: "POST", body: document });
  if (!response.ok) {
    if (shouldInvalidateSession(response)) emitInvalidAuthEvent();
    throw new Error(`Response error: ${response.status} - ${JSON.stringify(payload)}`);
  }
  if (!payload?.result) throw new Error("workflow import response was missing a result");
  return payload.result;
}

export async function fetchWorkflowModelOptions(query = "", limit = 25) {
  const trimmed = query.trim();
  const search = new URLSearchParams();
  if (trimmed) search.set("q", trimmed);
  search.set("limit", String(limit));
  const { response, payload } = await requestJSON(`/wf/sys-models?${search.toString()}`);
  if (!response.ok) {
    if (shouldInvalidateSession(response)) emitInvalidAuthEvent();
    return [];
  }
  if (!Array.isArray(payload?.result)) return [];
  return payload.result.map((item) => ({
    name: String(item?.name ?? "").trim(),
    label: String(item?.label ?? "").trim(),
  }));
}

export async function fetchWorkflowCredentials() {
  const { response, payload } = await requestJSON("/wf/credentials");
  if (!response.ok) {
    if (shouldInvalidateSession(response)) emitInvalidAuthEvent();
    return [];
  }
  if (!Array.isArray(payload?.result)) return [];
  return payload.result.map(normalizeCredentialSummary);
}

// fetchWorkflowSysCredentials lists sys.credential rows for the canvas's
// Authorization combobox. Only name/type/description come back — the access
// token is resolved server-side at step-execution time from the stored
// @syscred:<name> ref and never reaches the browser.
export async function fetchWorkflowSysCredentials() {
  const { response, payload } = await requestJSON("/wf/sys-credentials");
  if (!response.ok) {
    if (shouldInvalidateSession(response)) emitInvalidAuthEvent();
    return [];
  }
  if (!Array.isArray(payload?.result)) return [];
  return payload.result.map(normalizeCredentialSummary);
}

function normalizeCredentialSummary(item) {
  return {
    id: Number(item?.id ?? 0),
    name: String(item?.name ?? ""),
    credential_type: String(item?.credential_type ?? ""),
    description: String(item?.description ?? ""),
  };
}

// getMqttMetrics reads the server's live MQTT snapshot (base.workflow.trigger.mqtt_metrics):
// client connection state/churn, subscription resync counters and per-topic traffic, queue depth
// and run times. Counters are process-lifetime totals; callers derive rates from deltas.
export async function getMqttMetrics() {
  const response = await createModelRpc("base.workflow.trigger").call("mqtt_metrics", [], {});
  return response.result ?? {};
}

// ------------------------------------------------------------------ Phase 6 (T95-T101)

// routeError turns a failed /wf/* answer into an Error carrying the HTTP status, so callers can tell a
// permission denial (403, shown as a toast) from a conflict or a validation failure.
function routeError(response, payload) {
  const message = payload?.error?.message ?? payload?.message ?? `Response error: ${response.status} - ${JSON.stringify(payload)}`;
  const error = new Error(message);
  error.status = response.status;
  if (typeof payload?.error?.code === "number") error.code = payload.error.code;
  if (response.status === 403) error.permissionDenied = true;
  return error;
}

// RPC error codes the canvas branches on (internal/orm/error_registry.go).
export const RPC_INVALID_PARAMS = -32602;
export const RPC_FORBIDDEN = -32003;
export const RPC_CONFLICT = -32009;

// workflowErrorInfo reads the code, the server message and "permission denied" out of any error
// the RPC client or the /wf routes threw. jsonv2 sets `code`, `status` and `body` on a non-2xx RPC
// answer; the JSON body is parsed back out of the message text ("Response error: 409 - {...}")
// only when those fields are missing (an error built elsewhere).
export function workflowErrorInfo(error) {
  let code = typeof error?.code === "number" ? error.code : 0;
  let message = error instanceof Error ? error.message : String(error ?? "");
  const bodyMessage = error?.body?.error?.message;
  if (typeof bodyMessage === "string" && bodyMessage) {
    message = bodyMessage;
  } else if (!code && message.indexOf("{") >= 0) {
    try {
      const body = JSON.parse(message.slice(message.indexOf("{")));
      if (typeof body?.error?.code === "number") code = body.error.code;
      if (typeof body?.error?.message === "string" && body.error.message) message = body.error.message;
    } catch {
      // Not a JSON body: keep the raw message.
    }
  }
  const permission = Boolean(error?.permissionDenied) || error?.status === 403 || code === RPC_FORBIDDEN;
  return { code, message, permission, status: Number(error?.status ?? 0) };
}

// fetchWorkflowVersionState reads the publication fields (T97) the toolbar badge shows.
export async function fetchWorkflowVersionState(workflowID) {
  const response = await createModelRpc("base.workflow").search(
    [["id", "=", workflowID]],
    ["id", "state", "published_version", "published_at", "revision"],
    "id ASC",
    0,
    1,
    false,
  );
  const row = Array.isArray(response.result) ? response.result[0] : null;
  if (!row) return null;
  return {
    state: row.state === "published" ? "published" : "draft",
    published_version: Number(row.published_version ?? 0) || 0,
    published_at: row.published_at || 0,
    revision: Number.isInteger(row.revision) ? row.revision : null,
  };
}

// publishWorkflow freezes the saved definition as the next published version. expectedRevision
// makes the server refuse it (Conflict) when someone saved the workflow after this canvas loaded.
export async function publishWorkflow(workflowID, expectedRevision) {
  const kwargs = {};
  if (Number.isInteger(expectedRevision) && expectedRevision >= 0) kwargs.expected_revision = expectedRevision;
  const response = await createModelRpc("base.workflow").callById("publish", workflowID, [], kwargs);
  return response.result ?? {};
}

// validateWorkflow runs the server-side definition check (T98): {ok, issues:[...]}.
export async function validateWorkflow(workflowID) {
  const response = await createModelRpc("base.workflow").callById("validate", workflowID, [], {});
  const result = response.result ?? {};
  return { ok: Boolean(result.ok), issues: Array.isArray(result.issues) ? result.issues : [] };
}

// fetchTriggerHealth reads each trigger's listener state on the answering server process (T99).
export async function fetchTriggerHealth(workflowID) {
  const response = await createModelRpc("base.workflow.trigger").call("health", [], { workflow_id: workflowID });
  return Array.isArray(response.result) ? response.result : [];
}

const RUN_LIST_FIELDS = [
  "id",
  "name",
  "status",
  "trigger_type",
  "created_at",
  "updated_at",
  "attempt_count",
  "current_step",
  "last_error",
  "parent_run_id",
  "cancel_requested",
  "flow_version",
  "dry_run",
];

// fetchWorkflowRuns lists one page of a workflow's runs, newest first, plus the total count.
export async function fetchWorkflowRuns(workflowID, { limit = 20, offset = 0 } = {}) {
  const rpc = createModelRpc("base.workflow.run");
  const domain = [["workflow_id", "=", workflowID]];
  const [rows, count] = await Promise.all([
    rpc.search(domain, RUN_LIST_FIELDS, "id desc", offset, limit, false),
    rpc.call("search_count", [], { domain }),
  ]);
  return {
    rows: Array.isArray(rows.result) ? rows.result : [],
    total: Number(count.result ?? 0) || 0,
  };
}

// fetchWorkflowRun reads one run with its step timeline and (already redacted) payloads.
export async function fetchWorkflowRun(runID) {
  const response = await createModelRpc("base.workflow.run").read(
    [runID],
    [...RUN_LIST_FIELDS, "workflow_id", "step_runs", "input_payload", "output_payload", "audit_mode", "approval_expires_at", "approval_group_id"],
  );
  const rows = Array.isArray(response.result) ? response.result : [];
  return rows[0] ?? null;
}

// replayWorkflowRun starts a new run from a finished one (whole run, or from one linear step).
export async function replayWorkflowRun(runID, fromStepKey) {
  const kwargs = fromStepKey ? { from_step_key: fromStepKey } : {};
  const response = await createModelRpc("base.workflow.run").callById("replay_run", runID, [], kwargs);
  return response.result ?? {};
}

// decideWorkflowApproval approves or rejects a run waiting at a wait_approval step (T103). The
// token is optional for base.group_system. Answers {id, status, last_error}.
export async function decideWorkflowApproval(runID, approve, { token = "", comment = "" } = {}) {
  const kwargs = {};
  if (token) kwargs.token = token;
  if (comment) kwargs.comment = comment;
  const response = await createModelRpc("base.workflow.run").callById(approve ? "approve" : "reject", runID, [], kwargs);
  return response.result ?? {};
}

// cancelWorkflowRun asks the engine to stop a run at its next step boundary.
export async function cancelWorkflowRun(runID) {
  const response = await createModelRpc("base.workflow.run").callById("cancel_run", runID, [], {});
  return response.result ?? {};
}

// fetchWorkflowTemplates lists the template workflows (T101).
export async function fetchWorkflowTemplates() {
  const response = await createModelRpc("base.workflow").call("list_templates", [], {});
  return Array.isArray(response.result) ? response.result : [];
}

// instantiateWorkflowTemplate copies a template into a new workflow and returns the new id.
export async function instantiateWorkflowTemplate(templateID, name) {
  const kwargs = name ? { name } : {};
  const response = await createModelRpc("base.workflow").callById("instantiate_template", templateID, [], kwargs);
  const result = response.result;
  const id = typeof result === "number" ? result : Number(result?.id ?? 0);
  return Number.isFinite(id) ? id : 0;
}
