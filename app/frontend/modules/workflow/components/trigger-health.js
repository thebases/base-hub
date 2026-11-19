// Trigger health display (T99): turns one `base.workflow.trigger/health` row into a dot colour and
// a tooltip text. Health is per server process (`instance`), so the text names it.
import { formatLocalDateTime } from "@/services/server-time.js";

// healthByTrigger indexes health rows by trigger id.
export function healthByTrigger(rows) {
  const map = {};
  for (const row of Array.isArray(rows) ? rows : []) {
    if (row && row.trigger_id) map[row.trigger_id] = row;
  }
  return map;
}

// healthTone is the dot colour: "" (no dot: unknown or not applicable, e.g. webhook), "ok"
// (listening, no recent error), "warn" (listening but the last event failed), "off" (not listening).
export function healthTone(health) {
  if (!health || health.applicable === false) return "";
  if (!health.registered) return "off";
  return health.last_error ? "warn" : "ok";
}

// healthTime formats a health timestamp (Unix seconds). The backend sends 0 for "never happened",
// which formatLocalDateTime would show as 01-01-1970, so 0/false/null give "".
function healthTime(value) {
  return Number(value) > 0 ? formatLocalDateTime(value) : "";
}

// healthText is the tooltip: listener state, last event, last error, counters and instance.
// A disabled or unsubscribed transport reports registered:false with the reason in last_error
// (often without last_error_at), so the error line drops the time when there is none.
export function healthText(health) {
  if (!health) return "";
  const lines = [health.registered ? t("Listening") : t("Not listening")];
  lines.push(t("Last event: {time}", { time: healthTime(health.last_event_at) || t("never") }));
  if (health.last_error) {
    const errorTime = healthTime(health.last_error_at);
    lines.push(errorTime ? t("Last error ({time}): {error}", { time: errorTime, error: health.last_error }) : t("Last error: {error}", { error: health.last_error }));
  }
  lines.push(t("Events: {events}, failures: {failures}", { events: health.events ?? 0, failures: health.failures ?? 0 }));
  if (health.instance) lines.push(t("Server: {instance}", { instance: health.instance }));
  return lines.join(" · ");
}
