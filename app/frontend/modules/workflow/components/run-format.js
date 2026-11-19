// Shared display helpers for workflow runs (run inspector T95/T100, dry-run test result T101):
// status badge variants and labels, durations, pretty JSON, and the readable list of what a dry
// run would have done.
import { formatLocalDateTime } from "@/services/server-time.js";

// Run statuses that mean "not finished yet".
export const ACTIVE_RUN_STATUSES = ["pending", "running", "waiting_retry", "waiting_approval"];

// isRunActive says whether a run can still change (and so is worth polling or cancelling).
export function isRunActive(status) {
  return ACTIVE_RUN_STATUSES.includes(status);
}

// runStatusVariant picks the NodexUI Badge colour for a run or step status.
export function runStatusVariant(status) {
  switch (status) {
    case "succeeded":
      return "success";
    case "failed":
    case "dead_letter":
      return "danger";
    case "running":
    case "pending":
      return "info";
    case "waiting_retry":
    case "waiting_approval":
      return "warning";
    default:
      return "secondary";
  }
}

// runStatusLabel translates a run or step status for display.
export function runStatusLabel(status) {
  switch (status) {
    case "pending":
      return t("Pending");
    case "running":
      return t("Running");
    case "waiting_retry":
      return t("Waiting for retry");
    case "waiting_approval":
      return t("Waiting for approval");
    case "succeeded":
      return t("Succeeded");
    case "failed":
      return t("Failed");
    case "dead_letter":
      return t("Dead letter");
    case "cancelled":
      return t("Cancelled");
    case "skipped":
      return t("Skipped");
    default:
      return status || "—";
  }
}

// formatRunTime shows a server time (UTC Unix seconds) in the user's zone, "—" when unset.
// 0 counts as unset (a step not started yet): formatLocalDateTime(0) would show 01-01-1970.
export function formatRunTime(value) {
  if (!value || value === "0") return "—";
  return formatLocalDateTime(value) || "—";
}

// formatDuration turns milliseconds into a short "850 ms" / "2.4 s" / "3 min 5 s" text.
export function formatDuration(ms) {
  const value = Number(ms);
  if (!Number.isFinite(value) || value <= 0) return "—";
  if (value < 1000) return t("{value} ms", { value: Math.round(value) });
  if (value < 60000) return t("{value} s", { value: (value / 1000).toFixed(1) });
  return t("{minutes} min {seconds} s", { minutes: Math.floor(value / 60000), seconds: Math.round((value % 60000) / 1000) });
}

// prettyJSON formats a stored payload for a <pre> block; redaction markers stay exactly as stored.
export function prettyJSON(value) {
  if (value === undefined || value === null) return "—";
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

// effectLabel names what kind of outside effect a dry-run step skipped.
function effectLabel(effect) {
  switch (effect) {
    case "http":
      return t("HTTP call");
    case "outbox":
      return t("Outbound message");
    case "write":
      return t("Record write");
    case "script":
      return t("Script");
    default:
      return t("Side effect");
  }
}

// reasonLabel explains why the dry run skipped a step.
function reasonLabel(reason) {
  switch (reason) {
    case "side_effect":
      return t("has outside effects");
    case "credential":
      return t("needs a credential");
    case "unknown_node":
      return t("unknown node type");
    default:
      return reason || "";
  }
}

// intentTarget describes where the skipped effect would have gone (URL, model operation, action).
function intentTarget(entry) {
  if (entry.url) return [entry.method, entry.url].filter(Boolean).join(" ");
  if (entry.model) return [entry.model, entry.operation].filter(Boolean).join(" · ");
  if (entry.automation_action_id) return t("Automation action #{id}", { id: entry.automation_action_id });
  return "";
}

// collectDryRunIntents gathers every `_dry_run_intended` entry from a run's step outputs into
// display rows: effect, step/action, target and reason.
export function collectDryRunIntents(stepRuns) {
  const rows = [];
  for (const step of Array.isArray(stepRuns) ? stepRuns : []) {
    const intended = step?.output_snapshot?._dry_run_intended;
    if (!Array.isArray(intended)) continue;
    intended.forEach((entry, index) => {
      if (!entry || typeof entry !== "object") return;
      rows.push({
        key: `${step.step_key || step.step_name}:${step.attempt}:${index}`,
        effect: effectLabel(entry.effect),
        step: entry.step || step.step_key || step.step_name || "",
        action: entry.action || step.action || "",
        target: intentTarget(entry),
        reason: reasonLabel(entry.reason),
      });
    });
  }
  return rows;
}
