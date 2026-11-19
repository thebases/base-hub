// Port of the pure half of frontend-admin's NodeParamPanel.tsx: maps a node
// spec's params onto the real base.workflow.action / base.workflow.trigger
// columns.
//
// Action params are mapped generically from the spec (plan T93): every action
// param names its storage (`column` = base.workflow.action field, `extra_key`
// = extra_params key), its `type` picks the coercion, `secret` the credential
// picker and `value_type: "int"` an integer id. A new node needs no change
// here. Trigger params keep their per-name mapping (topic/group_id columns,
// everything else in config).
import { DEFAULT_RETRY_POLICY } from "@/modules/workflow/api.js";

export const SYS_CREDENTIAL_REF_PREFIX = "@syscred:";

export function normalizeParamSpecs(params) {
  return Array.isArray(params) ? params : [];
}

function stringValue(value, fallback) {
  const candidate = typeof value === "string" ? value.trim() : "";
  if (candidate) return candidate;
  return String(fallback ?? "");
}

function booleanValue(value, fallback) {
  if (typeof value === "boolean") return value;
  if (typeof fallback === "boolean") return fallback;
  return false;
}

export function stringDefault(value) {
  return typeof value === "string" ? value.trim() : "";
}

export function booleanDefault(value) {
  return typeof value === "boolean" ? value : false;
}

export function objectDefault(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) return {};
  return { ...value };
}

// storedActionValue reads the raw value an action param is stored in, or
// undefined for a param that names no storage.
export function storedActionValue(action, param) {
  if (param.column) return action[param.column];
  if (param.extra_key) return (action.extra_params ?? {})[param.extra_key];
  return undefined;
}

function hasActionStorage(param) {
  return Boolean(param.column || param.extra_key);
}

// jsonParamText renders a JSON object param for its textarea. A required
// extra_params object that is unset or empty (condition expression) reads as
// "" so the required check catches it; every other object renders as JSON,
// "{}" when empty (template, headers, lua data).
function jsonParamText(value, blankWhenEmpty) {
  const isObject = Boolean(value) && typeof value === "object" && !Array.isArray(value);
  if (blankWhenEmpty && (!isObject || Object.keys(value).length === 0)) return "";
  return JSON.stringify(value ?? {}, null, 2);
}

export function paramValueFromAction(action, param) {
  if (!hasActionStorage(param)) return param.default ?? "";
  const stored = storedActionValue(action, param);
  if (param.type === "json") return jsonParamText(stored, param.required && !param.column);
  if (param.type === "boolean") return booleanValue(stored, param.default);
  if (param.column) return stored || String(param.default ?? "");
  // extra_params: an integer id (lua_script automation_action_id) reads as its
  // string form so the select matches its option; text falls back to the default.
  if (typeof stored === "number") return String(stored);
  return stringValue(stored, param.default);
}

function paramValueFromTrigger(trigger, param) {
  switch (param.name) {
    case "topic":
      return trigger.topic;
    case "group_id":
      return trigger.group_id ?? "";
    default:
      return trigger.config[param.name] ?? param.default ?? "";
  }
}

export function buildActionDraftValues(action, params) {
  const values = {};
  for (const param of normalizeParamSpecs(params)) values[param.name] = paramValueFromAction(action, param);
  return values;
}

export function buildTriggerDraftValues(trigger, params) {
  const values = {};
  for (const param of normalizeParamSpecs(params)) values[param.name] = paramValueFromTrigger(trigger, param);
  return values;
}

// draftValueForStorage coerces one dialog value into what the action stores.
function draftValueForStorage(param, value) {
  if (param.type === "json") return parseJSONObject(value, t(param.label || param.name));
  if (param.type === "boolean") return Boolean(value);
  return String(value ?? "").trim();
}

function writeActionValue(action, param, value) {
  if (param.column) action[param.column] = value;
  else action.extra_params[param.extra_key] = value;
}

export function applyDraftValuesToAction(action, params, values) {
  const nextAction = {
    ...action,
    extra_params: { ...action.extra_params },
    headers: { ...action.headers },
    template: { ...action.template },
  };
  for (const param of normalizeParamSpecs(params)) {
    if (!hasActionStorage(param)) continue;
    const value = values[param.name];
    if (param.value_type === "int") {
      // An integer id; anything else (the "—" option) unsets it.
      const id = Number(value);
      if (Number.isInteger(id) && id > 0) writeActionValue(nextAction, param, id);
      else if (param.column) nextAction[param.column] = null;
      else delete nextAction.extra_params[param.extra_key];
      continue;
    }
    writeActionValue(nextAction, param, draftValueForStorage(param, value));
  }
  return nextAction;
}

export function applyDraftValuesToTrigger(trigger, params, values) {
  const nextTrigger = { ...trigger, topic: String(values.topic ?? "").trim(), config: {} };
  for (const param of normalizeParamSpecs(params)) {
    switch (param.name) {
      case "topic":
      case "group_id":
        break;
      default:
        nextTrigger.config[param.name] = values[param.name];
        break;
    }
  }
  return nextTrigger;
}

export function validateRequiredParams(action, params) {
  const missing = normalizeParamSpecs(params).flatMap((param) => {
    if (!param.required) return [];
    return isMissingRequiredValue(paramValueFromAction(action, param)) ? [param.label] : [];
  });
  if (missing.length > 0) throw new Error(t("Missing required fields: {fields}", { fields: missing.join(", ") }));
}

export function validateRequiredTriggerParams(trigger, params) {
  const missing = normalizeParamSpecs(params).flatMap((param) => {
    if (!param.required) return [];
    return isMissingRequiredValue(paramValueFromTrigger(trigger, param)) ? [param.label] : [];
  });
  if (missing.length > 0) throw new Error(t("Missing required fields: {fields}", { fields: missing.join(", ") }));
}

function isMissingRequiredValue(value) {
  if (value === null || value === undefined) return true;
  if (typeof value === "string") return value.trim() === "";
  if (Array.isArray(value)) return value.length === 0;
  if (typeof value === "object") return Object.keys(value).length === 0;
  return false;
}

function parseJSONObject(value, fieldName) {
  if (typeof value !== "string" || value.trim() === "") return {};
  let parsed;
  try {
    parsed = JSON.parse(value);
  } catch {
    throw new Error(t("{fieldName} must be valid JSON.", { fieldName }));
  }
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new Error(t("{fieldName} must be a JSON object.", { fieldName }));
  }
  return parsed;
}

// normalizedParamValue mirrors NodeParamPanel's read-side coercion: a
// select with no stored value falls back to its spec default, and an empty
// number renders as an empty input rather than NaN/0.
export function normalizedParamValue(param, value) {
  // Select options are string-valued, but persisted config can come back as a
  // JSON number (mqtt qos is stored as int) — stringify so the dialog's
  // strict option match still selects the saved value.
  if (param.type === "select" || param.type === "selection") {
    return value === undefined || value === null ? String(param.default ?? "") : String(value);
  }
  if (param.type === "number" && (value === "" || value === undefined || value === null)) return "";
  return value;
}

export function paramSelectionOptions(param) {
  if (Array.isArray(param.options) && param.options.length > 0) return param.options;
  return Array.isArray(param.selection) ? param.selection : [];
}

// paramWidgetKind collapses the spec's declared type plus its secret picker
// into the one widget the Owl dialog renders: a combobox is the sys.model
// picker, a sys_credential secret the Authorization credential select.
export function paramWidgetKind(param) {
  if (param.type === "combobox") return "model";
  if (param.secret === "sys_credential") return "authorization";
  if (param.type === "json") return "json";
  if (param.type === "domain") return "domain";
  switch (param.type) {
    case "boolean":
      return "boolean";
    case "number":
      return "number";
    case "select":
    case "selection":
      return "select";
    case "many2one":
      return "many2one";
    default:
      return "text";
  }
}

// relationModelForParam mirrors NodeParamPanel.tsx's per-name many2one
// relation mapping — only the webhook trigger's group_id param uses it today
// (an optional tbl.groups access gate). Falls back to plain text if a future
// many2one param has no mapping here.
export function relationModelForParam(paramName) {
  switch (paramName) {
    case "group_id":
      return "tbl.groups";
    default:
      return undefined;
  }
}

// isCredentialParam marks the secret params that get the @cred:<name>
// quick-pick row (sourced from /wf/credentials, a different model from the
// Authorization field's sys.credential list).
export function isCredentialParam(param) {
  return param.secret === "credential";
}

// credentialsForParam lists the workflow credentials whose type matches the
// param (hmac_key, aes_key) or is the catch-all "other".
export function credentialsForParam(credentials, paramName) {
  return credentials.filter((credential) => credential.credential_type === paramName || credential.credential_type === "other");
}

// parseLuaParams turns an extra_params value into a plain object. A string is
// what the generic json widget commits while the user types, so it is parsed
// here too. Returns null when the value is not a JSON object.
export function parseLuaParams(value) {
  if (value === null || value === undefined || value === false || value === "") return {};
  if (typeof value === "string") {
    try {
      return parseLuaParams(JSON.parse(value));
    } catch {
      return null;
    }
  }
  return typeof value === "object" && !Array.isArray(value) ? value : null;
}

// formatDetailValue renders one param value for the read-only detail list.
// Objects are pretty JSON (flagged multiline for a <pre>), booleans Yes/No.
function formatDetailValue(value) {
  if (typeof value === "boolean") return { text: value ? t("Yes") : t("No"), multiline: false };
  if (value && typeof value === "object") return { text: JSON.stringify(value, null, 2), multiline: true };
  const text = String(value ?? "").trim();
  return { text, multiline: text.includes("\n") };
}

// describeActionConfig lists what an action node is configured to do, for the
// canvas "View details" sheet: every spec param with a non-empty value plus
// the routing/retry columns that live outside the spec. Secrets (HMAC/AES
// keys, Authorization) are never echoed — only whether one is set..
export function describeActionConfig(action, params) {
  const rows = [];
  for (const param of normalizeParamSpecs(params)) {
    const value = paramValueFromAction(action, param);
    if (isMissingRequiredValue(value) || value === "{}") continue;
    const label = param.label || param.name;
    if (param.secret) {
      rows.push({ key: param.name, label, text: t("Configured (hidden)"), multiline: false });
      continue;
    }
    // A select shows its option label (e.g. the custom action's name), not the stored id.
    const option = paramSelectionOptions(param).find((item) => String(item.value) === String(value));
    rows.push({ key: param.name, label, ...formatDetailValue(option?.label ?? value) });
  }
  if (action.condition_step_key) rows.push({ key: "condition_step_key", label: t("Runs when"), text: `${action.condition_step_key} → ${action.branch_label || "—"}`, multiline: false });
  if (Number(action.timeout_seconds) > 0) rows.push({ key: "timeout_seconds", label: t("Timeout"), text: `${action.timeout_seconds}s`, multiline: false });
  if (action.continue_on_error) rows.push({ key: "continue_on_error", label: t("On error"), text: t("Continue"), multiline: false });
  return rows;
}

// relationDisplayName renders a many2one value (form records return
// [id, name], {id, display_name|name}, or a bare id) as a readable label.
function relationDisplayName(value) {
  if (Array.isArray(value)) return String(value[1] ?? value[0] ?? "");
  if (value && typeof value === "object") return String(value.display_name ?? value.name ?? value.id ?? "");
  return value === false || value === null || value === undefined ? "" : `#${value}`;
}

// webhookTriggerRows mirrors HandleWorkflowRoute: /wh/<topic> accepts any HTTP
// method; with no workflow group_id the endpoint is public, otherwise the
// caller must be authenticated and a member of that group (or sudo).
function webhookTriggerRows(trigger) {
  const group = relationDisplayName(trigger.group_id);
  return [
    { key: "http_method", label: t("HTTP method"), text: t("Any (GET, POST, PUT, …)"), multiline: false },
    { key: "access", label: t("Access"), text: group ? t("Authenticated members of {group}", { group }) : t("Public — no access group"), multiline: false },
  ];
}

// describeTriggerConfig lists the trigger node's configured params for the
// canvas "View details" sheet — every spec param except topic (already shown
// in the header list) and group_id (folded into webhook "Access"). Select
// values render by option label; a blank param shows its spec default tagged
// "(default)", since that is what the backend registry falls back to (e.g.
// kafka consumer_group → ipn-consumer-group, mqtt qos → 1).
export function describeTriggerConfig(trigger, params) {
  const rows = trigger.type === "webhook" ? webhookTriggerRows(trigger) : [];
  for (const param of normalizeParamSpecs(params)) {
    if (param.name === "topic" || param.name === "group_id") continue;
    let value = normalizedParamValue(param, paramValueFromTrigger(trigger, param));
    let suffix = "";
    if (isMissingRequiredValue(value) && !isMissingRequiredValue(param.default)) {
      value = param.default;
      suffix = " " + t("(default)");
    }
    if (isMissingRequiredValue(value) || value === "{}") continue;
    const option = paramSelectionOptions(param).find((item) => String(item.value) === String(value));
    const formatted = option ? { text: String(option.label), multiline: false } : formatDetailValue(value);
    rows.push({ key: param.name, label: param.label || param.name, ...formatted, text: formatted.text + suffix });
  }
  return rows;
}

export { DEFAULT_RETRY_POLICY };
