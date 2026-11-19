import { serverTimeToDate } from "@/services/server-time.js";
// Small formatting/derivation helpers shared across the chatter component
// tree. activityState is computed client-side from date_deadline only —
// discuss.activity intentionally has no persisted State column (see
// discuss-module-context.md), matching the read-time-only compute.
function startOfDay(date) {
  const clone = new Date(date);
  clone.setHours(0, 0, 0, 0);
  return clone;
}

export function activityState(dateDeadline) {
  if (!dateDeadline) return "planned";
  const deadline = startOfDay(serverTimeToDate(dateDeadline) ?? new Date(dateDeadline));
  const today = startOfDay(new Date());
  if (deadline.getTime() < today.getTime()) return "overdue";
  if (deadline.getTime() === today.getTime()) return "today";
  return "planned";
}

export function activityStateLabel(state) {
  if (state === "overdue") return t("Overdue");
  if (state === "today") return t("Today");
  return t("Planned");
}

export function formatChatterDate(value) {
  if (!value) return "";
  const date = serverTimeToDate(value);
  if (!date) return "";
  return date.toLocaleString();
}

export function initialsFromName(name) {
  const trimmed = (name ?? "").trim();
  if (!trimmed) return "?";
  const parts = trimmed.split(/\s+/).slice(0, 2);
  return parts.map((part) => part[0]?.toUpperCase() ?? "").join("") || "?";
}

// idFromMany2One unwraps the [id, displayName] tuple search_read/read expand
// many2one fields into, or passes a bare id through unchanged.
export function idFromMany2One(value) {
  if (Array.isArray(value)) return value[0] ?? null;
  return value ?? null;
}

export function labelFromMany2One(value, fallback = "") {
  if (Array.isArray(value)) return value[1] ?? fallback;
  return fallback;
}
