// Port of the pure graph/draft helpers in frontend-admin's WorkflowCanvas.tsx:
// node id scheme, layout round-trip, linear edge chain, action/trigger drafts,
// the replace_action_sequence payload builder, and the trigger row sync.
//
// Node ids MUST stay identical to the React canvas's scheme (`action:<step_key
// || id>`, fixed `trigger:webhook`) — a persisted `layout` JSON written by
// either app has to resolve in the other.
import { createModelRpc } from "@/services/api/jsonv2.js";
import { resolveRelationValue } from "@/modules/base/components/views/form/form-field-normalizer.js";

import { DEFAULT_RETRY_POLICY } from "@/modules/workflow/api.js";
import { booleanDefault, normalizeParamSpecs, objectDefault, storedActionValue, stringDefault } from "@/modules/workflow/node-params.js";

export const TRIGGER_NODE_ID = "trigger:webhook";
// Older layouts (written before the React canvas's trigger id rename)
// persisted the trigger node under this id.
const LEGACY_TRIGGER_NODE_ID = "trigger:http";
const DEFAULT_VIEWPORT = { x: 0, y: 0, zoom: 1 };
export const DEFAULT_MAX_PARALLEL_STEPS = 4;
export const DEFAULT_DAG_FAILURE_POLICY = "fail_branch";

export const DAG_EDGE_TYPE_LABELS = {
  success: t("Success"),
  failure: t("Failure"),
  condition: t("Condition"),
  always: t("Always"),
  manual: t("Manual"),
};

function normalizeTriggerNodeID(nodeID) {
  return nodeID === LEGACY_TRIGGER_NODE_ID ? TRIGGER_NODE_ID : nodeID;
}

export function isTriggerNodeID(nodeID) {
  return nodeID === TRIGGER_NODE_ID || nodeID === LEGACY_TRIGGER_NODE_ID;
}

// Node id <-> step key. base.workflow.edge stores bare step keys; canvas node
// ids carry the `action:` prefix. The React canvas persisted whatever
// connection.source held (i.e. the prefixed node id), so reads tolerate both
// forms while writes always send the bare step key the backend scheduler
// actually matches against.
export function nodeIDForStepKey(stepKey) {
  const key = String(stepKey ?? "");
  return key.startsWith("action:") ? key : `action:${key}`;
}

export function stepKeyForNodeID(nodeID) {
  const id = String(nodeID ?? "");
  return id.startsWith("action:") ? id.slice("action:".length) : id;
}

function stringValue(value) {
  return typeof value === "string" ? value.trim() : "";
}

function numberValue(value) {
  if (typeof value === "number" && Number.isFinite(value)) return value;
  if (typeof value === "string" && value.trim() !== "") {
    const parsed = Number(value);
    return Number.isFinite(parsed) ? parsed : null;
  }
  return null;
}

export function normalizeLayout(raw) {
  const fallback = { nodes: [], edges: [], viewport: { ...DEFAULT_VIEWPORT } };
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return fallback;
  return {
    nodes: Array.isArray(raw.nodes)
      ? raw.nodes.flatMap((node) => {
          const id = normalizeTriggerNodeID(stringValue(node?.id));
          const x = numberValue(node?.position?.x);
          const y = numberValue(node?.position?.y);
          if (!id || x === null || y === null) return [];
          return [{ id, position: { x, y } }];
        })
      : [],
    edges: Array.isArray(raw.edges)
      ? raw.edges.flatMap((edge) => {
          const id = stringValue(edge?.id);
          const source = normalizeTriggerNodeID(stringValue(edge?.source));
          const target = stringValue(edge?.target);
          if (!id || !source || !target) return [];
          return [{ id, source, target }];
        })
      : [],
    viewport: {
      x: numberValue(raw.viewport?.x) ?? DEFAULT_VIEWPORT.x,
      y: numberValue(raw.viewport?.y) ?? DEFAULT_VIEWPORT.y,
      zoom: numberValue(raw.viewport?.zoom) ?? DEFAULT_VIEWPORT.zoom,
    },
  };
}

export function buildLayoutPayload(nodes, edges, viewport) {
  return {
    nodes: nodes.map((node) => ({ id: node.id, position: { x: Math.round(node.x), y: Math.round(node.y) } })),
    edges: edges.map((edge) => ({ id: edge.id, source: edge.source, target: edge.target })),
    viewport,
  };
}

// buildCanvasEdges is linear mode's whole edge concept: trigger -> first
// action -> ... -> last action, derived from actionOrder. There is no backend
// edge row involved (unlike DAG mode).
export function buildCanvasEdges(actionOrder) {
  const edges = [];
  if (actionOrder.length === 0) return edges;
  edges.push({ id: `${TRIGGER_NODE_ID}->${actionOrder[0]}`, source: TRIGGER_NODE_ID, target: actionOrder[0] });
  for (let index = 1; index < actionOrder.length; index += 1) {
    edges.push({
      id: `${actionOrder[index - 1]}->${actionOrder[index]}`,
      source: actionOrder[index - 1],
      target: actionOrder[index],
    });
  }
  return edges;
}

function categoryClassName(category) {
  switch (category) {
    case "Input":
      return "is-input";
    case "Persistence":
      return "is-persistence";
    case "Transform":
      return "is-transform";
    case "Delivery":
      return "is-delivery";
    case "Security":
      return "is-security";
    default:
      return "is-default";
  }
}

function triggerCategoryClassName(triggerType) {
  switch (triggerType) {
    case "kafka":
      return "is-persistence";
    case "mqtt":
      return "is-transform";
    default:
      return "is-trigger";
  }
}

function toActionDraft(action, actionSpecMap) {
  return {
    id: action.id,
    nodeId: `action:${action.step_key || action.id}`,
    step_key: action.step_key || `step_${action.id}`,
    action_code: action.action_code,
    catalog_id: action.catalog_id,
    name: action.name || action.action_code,
    description: action.description,
    // Disabled steps stay disabled through a canvas save (the save recreates every row).
    is_active: action.is_active !== false,
    category: actionSpecMap.get(action.action_code)?.category ?? "Action",
    branch_label: action.branch_label ?? "",
    condition_step_key: action.condition_step_key ?? "",
    model_name: action.model_name,
    target_ref: action.target_ref,
    channel: action.channel,
    output_field: action.output_field,
    hmac_key: action.hmac_key,
    aes_key: action.aes_key,
    timeout_seconds: action.timeout_seconds,
    retry_policy: action.retry_policy,
    continue_on_error: action.continue_on_error,
    flatten_input: action.flatten_input,
    return_saved_record: action.return_saved_record,
    include_raw: action.include_raw,
    include_key: action.include_key,
    key_list: action.key_list,
    input_contract: action.input_contract,
    output_contract: action.output_contract,
    headers: action.headers,
    authorization: action.authorization,
    template: action.template,
    extra_params: action.extra_params,
  };
}

// createDraftAction seeds a brand-new palette node entirely from its node
// spec — never from a hardcoded frontend default set. catalog_id is forced
// null when the spec's catalog_id is <= 0 (a static builtin with no real
// catalog row); sending 0 would 400 on the backend's int64 parse.
export function createDraftAction(spec) {
  const stepKey = `${spec.code}_${Date.now()}_${Math.random().toString(36).slice(2, 6)}`;
  const catalogID = Number(spec.catalog_id ?? 0) > 0 ? Number(spec.catalog_id) : null;
  const draft = {
    id: undefined,
    nodeId: `action:${stepKey}`,
    step_key: stepKey,
    action_code: spec.code,
    catalog_id: catalogID ? [catalogID, spec.label] : null,
    name: spec.label,
    description: "",
    is_active: true,
    category: spec.category,
    branch_label: "",
    condition_step_key: "",
    model_name: "",
    target_ref: "",
    channel: "",
    output_field: "",
    hmac_key: "",
    aes_key: "",
    timeout_seconds: 0,
    retry_policy: { ...DEFAULT_RETRY_POLICY },
    continue_on_error: false,
    flatten_input: false,
    return_saved_record: false,
    include_raw: false,
    include_key: false,
    key_list: [],
    input_contract: {},
    output_contract: {},
    headers: {},
    authorization: "",
    template: {},
    extra_params: {},
  };
  return applyActionDefaults(draft, spec.params);
}

// applyActionDefaults writes each spec default into the param's storage
// (`column` or `extra_key`, see node-params.js); params without a default keep
// the blank draft value. applyTriggerDefaults is the trigger twin and keeps its
// per-name mapping.
function applyActionDefaults(action, params) {
  const nextAction = {
    ...action,
    headers: { ...action.headers },
    template: { ...action.template },
    extra_params: { ...action.extra_params },
  };
  for (const param of normalizeParamSpecs(params)) {
    if (param.default === undefined || param.default === null) continue;
    const value = actionDefaultValue(param);
    if (param.column) nextAction[param.column] = value;
    else if (param.extra_key) nextAction.extra_params[param.extra_key] = value;
  }
  return nextAction;
}

function actionDefaultValue(param) {
  if (param.type === "json") return objectDefault(param.default);
  if (param.type === "boolean") return booleanDefault(param.default);
  return stringDefault(param.default);
}

export function applyTriggerDefaults(trigger, params) {
  const nextTrigger = { ...trigger, config: { ...trigger.config } };
  for (const param of normalizeParamSpecs(params)) {
    switch (param.name) {
      case "topic":
        if (!nextTrigger.topic) nextTrigger.topic = stringDefault(param.default);
        break;
      case "consumer_group":
        if (!stringDefault(nextTrigger.config.consumer_group)) {
          nextTrigger.config.consumer_group = stringDefault(param.default);
        }
        break;
      case "use_dedupe":
        if (nextTrigger.config.use_dedupe === undefined) {
          nextTrigger.config.use_dedupe = booleanDefault(param.default);
        }
        break;
      // MQTT QoS defaults to 1 (at-least-once) when unset or blank; a saved
      // numeric value is normalized to the string form the select options use.
      case "qos": {
        const qos = nextTrigger.config.qos;
        nextTrigger.config.qos = qos === undefined || qos === null || qos === "" ? String(param.default ?? "1") : String(qos);
        break;
      }
      // tcp transport settings fall back to the spec defaults (same values the backend applies).
      case "port":
      case "framing":
      case "tls_mode":
      case "reply_timeout_ms":
      case "len_includes_header":
        if (nextTrigger.type === "tcp" && (nextTrigger.config[param.name] === undefined || nextTrigger.config[param.name] === null || nextTrigger.config[param.name] === "")) {
          nextTrigger.config[param.name] = param.default;
        }
        break;
      default:
        break;
    }
  }
  return nextTrigger;
}

// resolveTriggerDraft picks the trigger row the canvas node stands for: the first active
// non-webhook row, else the first inactive one (an inactive Kafka/MQTT/... trigger is still the
// workflow's transport and must not be mistaken for "webhook", which would delete it on save),
// else the implicit webhook.
export function resolveTriggerDraft(workflowTopic, groupID, triggers, specs) {
  const transportRows = triggers.filter((trigger) => TRANSPORT_TRIGGER_TYPES.includes(trigger.type) && trigger.type !== "webhook");
  const persisted = transportRows.find((trigger) => trigger.is_active) ?? transportRows[0];
  if (!persisted) {
    return { nodeId: TRIGGER_NODE_ID, type: "webhook", topic: workflowTopic, group_id: groupID, config: {}, is_active: true };
  }
  return applyTriggerDefaults(
    {
      id: persisted.id,
      nodeId: TRIGGER_NODE_ID,
      type: persisted.type,
      topic: persisted.topic,
      group_id: groupID,
      config: { ...persisted.config },
      is_active: persisted.is_active !== false,
    },
    specs.find((item) => item.type === persisted.type)?.params,
  );
}

export function buildTriggerNodeData(trigger, workflowTopic, specs) {
  const spec = specs.find((item) => item.type === trigger.type);
  const subtitle = trigger.type === "webhook" ? `/wh/${workflowTopic}` : trigger.topic || t("Trigger topic not configured");
  return {
    kind: "trigger",
    label: spec?.label ?? t("Trigger"),
    subtitle: trigger.is_active === false ? `${subtitle} · ${t("Inactive")}` : subtitle,
    category: t("Trigger"),
    colorClassName: triggerCategoryClassName(trigger.type),
    trigger,
  };
}

// isActionConfigured drives the node card's amber "needs configuration" dot:
// every required param must resolve to a non-empty value from its storage.
export function isActionConfigured(action, actionSpec) {
  for (const param of normalizeParamSpecs(actionSpec.params)) {
    if (!param.required) continue;
    const value = requiredValueForAction(action, param);
    if (value === null || value === undefined) return false;
    if (typeof value === "string" && value.trim() === "") return false;
    if (typeof value === "object" && !Array.isArray(value) && Object.keys(value).length === 0) return false;
  }
  return true;
}

// requiredValueForAction reads a required param from its spec storage: text
// falls back to the spec default (the backend applies it too), a JSON param
// must be an object, an integer id must be > 0. A param with no storage
// counts as configured rather than flagging every node of a new type.
function requiredValueForAction(action, param) {
  if (!param.column && !param.extra_key) return "configured";
  const stored = storedActionValue(action, param);
  if (param.value_type === "int") {
    const id = Number(stored ?? 0);
    return Number.isFinite(id) && id > 0 ? String(id) : null;
  }
  if (param.type === "json") return stored && typeof stored === "object" && !Array.isArray(stored) ? stored : null;
  if (stored === undefined || stored === null || typeof stored === "string") return stringDefault(stored) || stringDefault(param.default);
  return stored;
}

// buildCanvasNodes assembles the flat node list the Owl canvas renders: the
// fixed trigger node plus one node per visible action (parse_payload and
// code-less rows are hidden, matching the React canvas).
export function buildCanvasNodes({ topic, groupID, actions, triggers, specs, layout }) {
  const positions = new Map(layout.nodes.map((node) => [node.id, node.position]));
  const actionSpecMap = new Map(specs.actions.map((item) => [item.code, item]));
  const triggerDraft = resolveTriggerDraft(topic, groupID, triggers, specs.triggers);

  const nodes = [
    {
      id: TRIGGER_NODE_ID,
      ...buildTriggerNodeData(triggerDraft, topic, specs.triggers),
      ...(positions.get(TRIGGER_NODE_ID) ?? { x: 80, y: 180 }),
    },
  ];

  const visibleActions = actions.filter((action) => action.action_code !== "parse_payload" && action.action_code !== "");
  visibleActions.forEach((action, index) => {
    const draft = toActionDraft(action, actionSpecMap);
    const spec = actionSpecMap.get(draft.action_code);
    nodes.push({
      id: draft.nodeId,
      ...buildActionNodeData(draft, spec, spec ? isActionConfigured(draft, spec) : true),
      ...(positions.get(draft.nodeId) ?? { x: 340 + index * 260, y: 180 }),
    });
  });

  return nodes;
}

export function buildActionNodeData(action, spec, configured) {
  return {
    kind: "action",
    label: action.name,
    subtitle: `${spec?.category ?? action.category} · ${action.action_code}`,
    category: spec?.category ?? action.category,
    colorClassName: categoryClassName(spec?.category ?? action.category),
    configured,
    action,
  };
}

// buildReplaceActionSequenceItems produces the save payload. It no longer
// force-prepends a hidden parse_payload step — a trigger-only workflow saves
// zero actions.
export function buildReplaceActionSequenceItems(nodes, actionOrder) {
  const nodeMap = new Map(nodes.map((node) => [node.id, node]));
  return actionOrder.flatMap((nodeID, index) => {
    const node = nodeMap.get(nodeID);
    if (!node || node.kind !== "action") return [];
    const action = node.action;
    // catalog_id must reach the RPC as a plain scalar id — an [id,label]
    // tuple (what the draft carries for a freshly dragged node) is not
    // parseable by the backend's int64FromAny.
    const catalogIdValue = resolveRelationValue(action.catalog_id);
    return [
      {
        action_code: action.action_code,
        catalog_id: catalogIdValue === "" ? null : catalogIdValue,
        name: action.name,
        description: action.description,
        is_active: action.is_active !== false,
        step_key: action.step_key,
        branch_label: action.branch_label ?? "",
        condition_step_key: action.condition_step_key ?? "",
        sequence: (index + 1) * 10,
        model_name: action.model_name,
        target_ref: action.target_ref,
        channel: action.channel,
        output_field: action.output_field,
        hmac_key: action.hmac_key,
        aes_key: action.aes_key,
        timeout_seconds: action.timeout_seconds,
        retry_policy: action.retry_policy,
        continue_on_error: action.continue_on_error,
        flatten_input: action.flatten_input,
        return_saved_record: action.return_saved_record,
        include_raw: action.include_raw,
        include_key: action.include_key,
        key_list: action.key_list,
        input_contract: action.input_contract,
        output_contract: action.output_contract,
        headers: action.headers,
        authorization: action.authorization,
        template: action.template,
        extra_params: action.extra_params,
      },
    ];
  });
}

// buildTriggerConfigPayload is the config the canvas writes. audit_payload (full|hash|none) has no
// canvas field: it is set on the trigger row and carried over here so a canvas save keeps it.
function buildTriggerConfigPayload(trigger) {
  const payload = buildTransportConfigPayload(trigger);
  const auditPayload = trigger.config?.audit_payload;
  if (typeof auditPayload === "string" && auditPayload !== "") payload.audit_payload = auditPayload;
  // dedupe_key (T56, kafka/mqtt) is likewise set on the row, not in the canvas.
  const dedupeKey = trigger.config?.dedupe_key;
  if (typeof dedupeKey === "string" && dedupeKey !== "") payload.dedupe_key = dedupeKey;
  return payload;
}

function buildTransportConfigPayload(trigger) {
  switch (trigger.type) {
    case "kafka":
      return {
        consumer_group: typeof trigger.config.consumer_group === "string" ? trigger.config.consumer_group : "",
        use_dedupe: Boolean(trigger.config.use_dedupe),
      };
    case "mqtt":
      return {
        // Blank/missing QoS falls back to 1, matching the backend default.
        qos: (typeof trigger.config.qos === "string" && trigger.config.qos !== "") || typeof trigger.config.qos === "number" ? trigger.config.qos : "1",
      };
    case "tcp":
      // Transport settings only. Blank numbers are omitted so the backend applies its defaults.
      return {
        ...(trigger.config.port === undefined || trigger.config.port === "" ? {} : { port: Number(trigger.config.port) }),
        framing: typeof trigger.config.framing === "string" && trigger.config.framing !== "" ? trigger.config.framing : "len2_bcd",
        len_includes_header: Boolean(trigger.config.len_includes_header),
        tls_mode: typeof trigger.config.tls_mode === "string" && trigger.config.tls_mode !== "" ? trigger.config.tls_mode : "none",
        ...(trigger.config.reply_timeout_ms === undefined || trigger.config.reply_timeout_ms === ""
          ? {}
          : { reply_timeout_ms: Number(trigger.config.reply_timeout_ms) }),
      };
    case "model": {
      const payload = {
        model: typeof trigger.config.model === "string" ? trigger.config.model.trim() : "",
        operation: typeof trigger.config.operation === "string" ? trigger.config.operation : "",
      };
      const triggerDomain = parseTriggerDomainConfigValue(trigger.config.trigger_domain);
      if (triggerDomain !== undefined) payload.trigger_domain = triggerDomain;
      const domain = parseTriggerDomainConfigValue(trigger.config.domain);
      if (domain !== undefined) payload.domain = domain;
      return payload;
    }
    default:
      return {};
  }
}

// parseTriggerDomainConfigValue keeps a model trigger's domain as a real token
// array. The Owl dialog edits domains as raw JSON text (there is no ported
// domain rule-builder widget yet), so unparsable text fails the save instead
// of silently becoming "match everything".
function parseTriggerDomainConfigValue(value) {
  if (Array.isArray(value)) return value.length > 0 ? value : undefined;
  if (typeof value !== "string" || value.trim() === "" || value.trim() === "[]") return undefined;
  let parsed;
  try {
    parsed = JSON.parse(value);
  } catch {
    throw new Error(t("Domain must be a valid JSON array of domain tokens."));
  }
  if (!Array.isArray(parsed)) throw new Error(t("Domain must be a valid JSON array of domain tokens."));
  return parsed.length > 0 ? parsed : undefined;
}

const TRANSPORT_TRIGGER_TYPES = ["webhook", "kafka", "mqtt", "model", "tcp"];

// triggerDraftChanged reports whether the trigger node differs from the row it stands for. Only
// that primary row is compared: other trigger rows of the workflow (several Kafka/MQTT/model
// triggers are allowed) are not the canvas's to rewrite.
export function triggerDraftChanged(trigger, existingTriggers) {
  const persisted = existingTriggers.filter((item) => TRANSPORT_TRIGGER_TYPES.includes(item.type));
  if (trigger.type === "webhook") return persisted.some((item) => item.type !== "webhook");
  const transportRows = persisted.filter((item) => item.type !== "webhook");
  // An active webhook row next to other transports is an explicit choice made in the trigger list
  // (Phase 6 follow-up), not a pending switch: it does not make the node dirty.
  const primary = transportRows.find((item) => item.id === trigger.id) ?? transportRows[0];
  if (!primary) return true;
  return (
    primary.type !== trigger.type ||
    primary.topic !== trigger.topic ||
    primary.is_active !== (trigger.is_active !== false) ||
    JSON.stringify(primary.config) !== JSON.stringify(buildTriggerConfigPayload(trigger))
  );
}

// syncWorkflowTriggerState reconciles the workflow's trigger rows with the canvas draft.
// - webhook: the operator switched the node to webhook; /wh refuses a workflow that still has a
//   non-webhook row, so those rows are unlinked (a webhook row is kept: it carries the
//   hmac_secret/max_body_bytes config the canvas does not edit).
// - any other type: writes (or creates) only the primary row the node stands for, keeping its
//   is_active. Other transport rows are left alone: the canvas shows one trigger, it must not
//   delete the others (or an inactive one) on save. An active webhook row is deactivated only when
//   this save switches the workflow away from webhook (no other transport row existed yet); next to
//   existing transports it is an explicit trigger-list choice (Phase 6 follow-up) and is kept.
export async function syncWorkflowTriggerState(workflowID, trigger, existingTriggers) {
  const rpc = createModelRpc("base.workflow.trigger");
  const persisted = existingTriggers.filter((item) => TRANSPORT_TRIGGER_TYPES.includes(item.type));
  if (trigger.type === "webhook") {
    for (const existing of persisted) {
      if (existing.type !== "webhook") await rpc.callById("unlink", existing.id);
    }
    return;
  }

  // Never turn a webhook row into another transport: it carries the hmac_secret config.
  const transportRows = persisted.filter((item) => item.type !== "webhook");
  const primaryID = transportRows.some((item) => item.id === trigger.id) ? trigger.id : transportRows[0]?.id;
  const values = {
    workflow_id: workflowID,
    trigger_type: trigger.type,
    topic: trigger.topic,
    is_active: trigger.is_active !== false,
    config: buildTriggerConfigPayload(trigger),
  };
  if (primaryID && primaryID > 0) {
    await rpc.callById("write", primaryID, [values]);
  } else {
    await rpc.create(values);
  }
  if (transportRows.length === 0) await deactivateWebhookRows(rpc, persisted);
}

// deactivateWebhookRows switches off active webhook rows when the canvas node switches the workflow
// from webhook to another transport. Deactivated, not deleted: /wh closes, and the signature config
// is back in force if the workflow is switched back to webhook.
async function deactivateWebhookRows(rpc, rows) {
  for (const existing of rows) {
    if (existing.type === "webhook" && existing.is_active) await rpc.callById("write", existing.id, [{ is_active: false }]);
  }
}

// upsertWorkflowTriggerRow saves one non-webhook trigger row from the trigger list (multi-trigger
// editing, T104): writes the row it was opened from, or creates a new one. Webhook rows go through
// saveWorkflowWebhookRow; an explicit webhook row is left as it is (Phase 6 follow-up: it may serve next to
// other transports while active).
export async function upsertWorkflowTriggerRow(workflowID, trigger) {
  if (trigger.type === "webhook") throw new Error(t("Webhook rows are saved from the webhook settings in the trigger list."));
  const rpc = createModelRpc("base.workflow.trigger");
  const values = {
    workflow_id: workflowID,
    trigger_type: trigger.type,
    topic: trigger.topic,
    is_active: trigger.is_active !== false,
    config: buildTriggerConfigPayload(trigger),
  };
  if (Number(trigger.id) > 0) {
    await rpc.callById("write", trigger.id, [values]);
  } else {
    await rpc.create(values);
  }
}

// saveWorkflowWebhookRow creates the explicit webhook row (active) or rewrites the config of an
// existing one (Phase 6 follow-up). The row's type and active flag are never changed here; topic is the
// workflow topic (/wh/<topic> is matched on the workflow, the row topic is informational).
// config is rebuilt from the form: a blank secret or body limit removes it; audit_payload is kept.
export async function saveWorkflowWebhookRow(workflowID, workflowTopic, row, { hmacSecret, maxBodyBytes }) {
  const config = {};
  if (hmacSecret) config.hmac_secret = hmacSecret;
  if (maxBodyBytes !== "" && maxBodyBytes !== null && maxBodyBytes !== undefined) config.max_body_bytes = Number(maxBodyBytes);
  const auditPayload = row?.config?.audit_payload;
  if (typeof auditPayload === "string" && auditPayload !== "") config.audit_payload = auditPayload;
  const rpc = createModelRpc("base.workflow.trigger");
  if (Number(row?.id) > 0) {
    if (row.type !== "webhook") throw new Error(t("Only a webhook row can hold webhook settings."));
    await rpc.callById("write", row.id, [{ topic: workflowTopic, config }]);
    return;
  }
  await rpc.create({ workflow_id: workflowID, trigger_type: "webhook", topic: workflowTopic, is_active: true, config });
}

// setWorkflowTriggerRowActive toggles one trigger row on or off (a webhook row included: switching
// it off is how its /wh serving stops next to other transports; it is never deleted).
export async function setWorkflowTriggerRowActive(row, active) {
  await createModelRpc("base.workflow.trigger").callById("write", row.id, [{ is_active: active }]);
}

// removeWorkflowTriggerRow deletes one non-webhook trigger row. Once none is left the workflow is
// served by its webhook again (/wh/<topic>). A webhook row is never deleted (it carries the HMAC
// config): switch it off instead.
export async function removeWorkflowTriggerRow(row) {
  if (row.type === "webhook") throw new Error(t("A webhook row cannot be removed: switch it off instead."));
  await createModelRpc("base.workflow.trigger").callById("unlink", row.id);
}

// isWorkflowRevisionConflict recognizes the Conflict a graph save gets when the workflow was saved
// by someone else since it was loaded (backend workflow_tx.go: JSON-RPC code -32009 / HTTP 409).
// jsonv2 sets `code`/`status` on the error; the message text is only a fallback.
export function isWorkflowRevisionConflict(error) {
  if (!error) return false;
  if (typeof error.code === "number") return error.code === -32009;
  if (error.status === 409) return true;
  const message = String(error.message ?? "");
  return message.includes("-32009") || message.includes("revision conflict");
}
