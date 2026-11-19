// Owl port of frontend-admin's gen-action-flow-dialog.tsx: "Generate Workflow
// by Script" — describe the action sequence as a markdown script, then
// replace_action_sequence rebuilds the backend rows from it.
//
// Controlled pure-Owl Dialog (props.open/props.onOpenChange) — same contract
// as every other workflow dialog (DagSettingsDialog.js is the closest
// template). The React source's MDXEditor split-pane (instructions preview +
// editor) is reproduced with this app's own ported MarkdownViewer/MarkdownEdit
// components (modules/knowledge/components/) instead of vendoring MDXEditor.
import { Component, loadTemplate, onWillUpdateProps, useState, xml } from "nodexui/owl";
import { Alert, Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "nodexui/component";
import { extractMarkdownHeadings } from "@/modules/knowledge/markdown-headings.js";
import { MarkdownViewer } from "@/modules/knowledge/components/MarkdownViewer.js";
import { MarkdownEdit } from "@/modules/knowledge/components/MarkdownEdit.js";

import { fetchWorkflowActionCatalog, fetchWorkflowActions, replaceWorkflowActionSequence } from "@/modules/workflow/api.js";

const template = await loadTemplate(new URL("./GenActionFlowDialog.xml", import.meta.url));

export class GenActionFlowDialog extends Component {
  static template = xml({ raw: [template] });
  static components = {
    Alert,
    Button,
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
    MarkdownViewer,
    MarkdownEdit,
  };

  setup() {
    this.state = useState({
      loading: false,
      generating: false,
      loadError: "",
      parseError: "",
      catalog: [],
      instructions: "",
      script: "",
    });
    if (this.props.open) void this.reload();
    onWillUpdateProps((nextProps) => {
      if (nextProps.open && !this.props.open) void this.reload();
    });
  }

  get instructionHeadings() {
    return extractMarkdownHeadings(this.state.instructions);
  }

  get workflowID() {
    return Number(this.props.workflowID ?? 0);
  }

  get supportedActionsLabel() {
    return this.state.catalog.map((item) => item.code).join(", ");
  }

  async reload() {
    const workflowID = this.workflowID;
    if (!workflowID || workflowID <= 0) return;
    this.state.loading = true;
    this.state.loadError = "";
    this.state.parseError = "";
    try {
      const [catalog, actions] = await Promise.all([fetchWorkflowActionCatalog(), fetchWorkflowActions(workflowID)]);
      this.state.catalog = catalog;
      this.state.instructions = buildWorkflowInstructions(this.props.workflowRecord, actions, catalog);
      this.state.script = buildWorkflowScript(actions);
    } catch (error) {
      this.state.loadError = error instanceof Error ? error.message : t("Failed to load workflow script data");
    } finally {
      this.state.loading = false;
    }
  }

  setScript(value) {
    this.state.script = value;
  }

  cancel() {
    this.props.onOpenChange(false);
  }

  async generate() {
    const workflowID = this.workflowID;
    if (!workflowID || workflowID <= 0) {
      this.state.parseError = t("This action requires a saved workflow record.");
      return;
    }
    this.state.generating = true;
    this.state.parseError = "";
    try {
      const parsedItems = parseWorkflowScript(this.state.script, this.state.catalog);
      await replaceWorkflowActionSequence(workflowID, parsedItems);
      await this.props.onGenerated?.();
      this.props.onOpenChange(false);
    } catch (error) {
      this.state.parseError = error instanceof Error ? error.message : t("Failed to generate workflow actions");
    } finally {
      this.state.generating = false;
    }
  }
}

// ---------------------------------------------------------------------------
// Pure helpers, verbatim ports of gen-action-flow-dialog.tsx's module-level
// functions (instructions/script builders + the markdown script parser).
// ---------------------------------------------------------------------------

function buildWorkflowInstructions(workflowRecord, actions, catalog) {
  const workflowName =
    typeof workflowRecord?.name === "string" && workflowRecord.name.trim() ? workflowRecord.name : "Current workflow";
  const topic = typeof workflowRecord?.topic === "string" && workflowRecord.topic.trim() ? workflowRecord.topic : "unknown";
  const version = typeof workflowRecord?.version === "number" ? workflowRecord.version : 0;
  const description = typeof workflowRecord?.description === "string" ? workflowRecord.description.trim() : "";

  const lines = [
    `# Generate Actions For \`${workflowName}\``,
    "",
    `- Topic: \`${topic}\``,
    `- Version: \`${version}\``,
    `- Existing action count: \`${actions.length}\``,
    "- Generate replaces the current workflow action sequence for this workflow.",
    "",
    "## How To Write The Script",
    "",
    "Create one `## Step <sequence>: <name>` section per workflow action.",
    "Inside each step, add `- key: value` lines.",
    `Use one of the supported action codes: ${catalog.map((item) => `\`${item.code}\``).join(", ")}.`,
    "",
    "## Common Keys",
    "",
    "- Required: `action`.",
    "- Optional scalars: `description`, `step_key`, `timeout_seconds`, `continue_on_error`, `model_name`, `target_ref`, `channel`, `output_field`, `hmac_key`, `aes_key`, `flatten_input`, `return_saved_record`, `include_raw`, `include_key`.",
    '- Optional inline JSON arrays/objects: `key_list`, `input_contract`, `output_contract`, `headers`, `template`, `extra_params`, `retry_policy` (single source of truth for retry behavior — `{"max_attempts":3,"retry_cool_down":15}` is the default when omitted).',
    "",
    "## Example",
    "",
    "```md",
    "## Step 10: Parse Payload",
    "- action: parse_payload",
    "",
    "## Step 20: Create Partner Bank",
    "- action: crud_model",
    "- model_name: tbl.partner.bank",
    '- extra_params: {"operation":"find_or_create","result_key":"bank_account"}',
    "```",
  ];

  if (description) lines.push("", "## Current Use Case", "", description);
  if (actions.length > 0) {
    lines.push("", "## Current Action Sequence", "");
    for (const action of actions) {
      lines.push(
        `- \`${action.sequence}\` \`${firstNonEmpty(action.name, action.step_key, "step")}\` via \`${firstNonEmpty(action.action_code, "parse_payload")}\``,
      );
    }
  }
  return lines.join("\n");
}

function buildWorkflowScript(actions) {
  if (actions.length === 0) {
    return [
      "## Step 10: Parse Payload",
      "- action: parse_payload",
      "- description: Convert the inbound request body into a JSON payload for the next steps.",
      "",
      "## Step 20: Persist Result",
      "- action: save_to_db",
      "- model_name: tbl.partner",
      "- description: Replace this sample model with the record you want to create.",
    ].join("\n");
  }

  const lines = [];
  for (const action of actions) {
    lines.push(`## Step ${action.sequence}: ${firstNonEmpty(action.name, action.step_key, action.action_code, "Step")}`);
    lines.push(`- action: ${firstNonEmpty(action.action_code, "parse_payload")}`);
    appendStringLine(lines, "description", action.description);
    appendStringLine(lines, "step_key", action.step_key);
    appendNumberLine(lines, "timeout_seconds", action.timeout_seconds);
    appendJSONLine(lines, "retry_policy", action.retry_policy);
    appendBooleanLine(lines, "continue_on_error", action.continue_on_error);
    appendStringLine(lines, "model_name", action.model_name);
    appendStringLine(lines, "target_ref", action.target_ref);
    appendStringLine(lines, "channel", action.channel);
    appendStringLine(lines, "output_field", action.output_field);
    appendStringLine(lines, "hmac_key", action.hmac_key);
    appendStringLine(lines, "aes_key", action.aes_key);
    appendBooleanLine(lines, "flatten_input", action.flatten_input);
    appendBooleanLine(lines, "return_saved_record", action.return_saved_record);
    appendBooleanLine(lines, "include_raw", action.include_raw);
    appendBooleanLine(lines, "include_key", action.include_key);
    appendJSONLine(lines, "key_list", action.key_list);
    appendJSONLine(lines, "input_contract", action.input_contract);
    appendJSONLine(lines, "output_contract", action.output_contract);
    appendJSONLine(lines, "headers", action.headers);
    appendJSONLine(lines, "template", action.template);
    appendJSONLine(lines, "extra_params", action.extra_params);
    lines.push("");
  }
  return lines.join("\n").trim();
}

function appendStringLine(lines, key, value) {
  if (!String(value ?? "").trim()) return;
  lines.push(`- ${key}: ${String(value).trim()}`);
}

function appendNumberLine(lines, key, value) {
  if (!Number.isFinite(value) || value <= 0) return;
  lines.push(`- ${key}: ${value}`);
}

function appendBooleanLine(lines, key, value) {
  if (!value) return;
  lines.push(`- ${key}: true`);
}

function appendJSONLine(lines, key, value) {
  if (Array.isArray(value) && value.length === 0) return;
  if (value && typeof value === "object" && !Array.isArray(value) && Object.keys(value).length === 0) return;
  if (!value || (typeof value !== "object" && !Array.isArray(value))) return;
  lines.push(`- ${key}: ${JSON.stringify(value)}`);
}

function parseWorkflowScript(markdown, catalog) {
  const supportedActions = new Set(catalog.map((item) => item.code));
  const steps = [];
  let current = null;

  for (const rawLine of markdown.split("\n")) {
    const line = rawLine.trim();
    if (!line) continue;

    const heading = parseStepHeading(line);
    if (heading) {
      if (current) steps.push(normalizeParsedStep(current, supportedActions));
      current = { sequence: heading.sequence, name: heading.name };
      continue;
    }

    if (!current || !line.startsWith("- ")) continue;
    const content = line.slice(2).trim();
    const separatorIndex = content.indexOf(":");
    if (separatorIndex < 0) continue;
    const key = content.slice(0, separatorIndex).trim();
    const rawValue = content.slice(separatorIndex + 1).trim();
    assignParsedStepValue(current, key, rawValue);
  }

  if (current) steps.push(normalizeParsedStep(current, supportedActions));

  if (steps.length === 0) throw new Error(t("The markdown script must contain at least one `Step` section."));

  return steps.sort((left, right) => Number(left.sequence ?? 0) - Number(right.sequence ?? 0));
}

function parseStepHeading(line) {
  const match = line.match(/^#{2,3}\s+Step\s+(\d+)\s*:\s*(.+)$/i);
  if (!match) return null;
  return { sequence: Number(match[1] ?? 0), name: (match[2] ?? "").trim() };
}

function assignParsedStepValue(step, key, rawValue) {
  switch (key) {
    case "action":
    case "name":
    case "description":
    case "step_key":
    case "model_name":
    case "target_ref":
    case "channel":
    case "output_field":
    case "hmac_key":
    case "aes_key":
      step[key === "action" ? "action_code" : key] = rawValue;
      return;
    case "timeout_seconds":
      step[key] = parseNonNegativeInt(rawValue, key);
      return;
    case "continue_on_error":
    case "flatten_input":
    case "return_saved_record":
    case "include_raw":
    case "include_key":
      step[key] = parseBoolean(rawValue, key);
      return;
    case "key_list":
      step[key] = parseJSONArray(rawValue, key);
      return;
    case "input_contract":
    case "output_contract":
    case "headers":
    case "template":
    case "extra_params":
    case "retry_policy":
      step[key] = parseJSONObject(rawValue, key);
      return;
    default:
      return;
  }
}

function parseNonNegativeInt(rawValue, key) {
  const parsed = Number.parseInt(rawValue, 10);
  if (!Number.isFinite(parsed) || parsed < 0) throw new Error(t("Field `{key}` must be a non-negative integer.", { key }));
  return parsed;
}

function parseBoolean(rawValue, key) {
  if (rawValue === "true") return true;
  if (rawValue === "false") return false;
  throw new Error(t("Field `{key}` must be `true` or `false`.", { key }));
}

function parseJSONArray(rawValue, key) {
  const parsed = JSON.parse(rawValue);
  if (!Array.isArray(parsed)) throw new Error(t("Field `{key}` must be a JSON array.", { key }));
  return parsed;
}

function parseJSONObject(rawValue, key) {
  const parsed = JSON.parse(rawValue);
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) throw new Error(t("Field `{key}` must be a JSON object.", { key }));
  return parsed;
}

function normalizeParsedStep(step, supportedActions) {
  const sequence = Number(step.sequence ?? 0);
  if (!Number.isFinite(sequence) || sequence <= 0) throw new Error(t("Each `Step` heading must use a positive sequence number."));
  const actionCode = typeof step.action_code === "string" ? step.action_code.trim() : "";
  if (!actionCode) throw new Error(t("Step {sequence} is missing `action`.", { sequence }));
  if (!supportedActions.has(actionCode)) throw new Error(t("Step {sequence} uses unsupported action `{actionCode}`.", { sequence, actionCode }));
  const normalizedName = typeof step.name === "string" && step.name.trim() ? step.name.trim() : humanizeActionCode(actionCode);
  return { ...step, sequence, action_code: actionCode, name: normalizedName };
}

function humanizeActionCode(value) {
  return value
    .split(/[._-]+/)
    .filter(Boolean)
    .map((segment) => segment[0]?.toUpperCase() + segment.slice(1))
    .join(" ");
}

function firstNonEmpty(...values) {
  for (const value of values) {
    if (typeof value === "string" && value.trim()) return value.trim();
  }
  return "";
}
