import { serverTimeToDate } from "@/services/server-time.js";
// JS port of frontend-admin's views/base-view/calendar-utils.ts — resolves the
// calendar anchor field, label and amount field from the `<calendar>` arch plus
// fields_get metadata (SRS 2026-08-03-frontend-admin-calendar-view-srs.md).

// SRS-F-001: the calendar view is never offered for a sys.* model, even if a
// <calendar> arch row somehow exists for one.
export function isCalendarExcludedModel(model) {
  return model.startsWith("sys.");
}

function parseCalendarArchConfig(arch) {
  if (!arch?.trim()) return {};
  const document = new DOMParser().parseFromString(arch, "text/xml");
  if (document.querySelector("parsererror")) return {};
  const root = document.documentElement;
  if (!root || root.tagName.toLowerCase() !== "calendar") return {};
  return {
    dateStartField: root.getAttribute("date_start")?.trim() || undefined,
    dateStopField: root.getAttribute("date_stop")?.trim() || undefined,
    amountField: root.getAttribute("amount")?.trim() || undefined,
    titleField: root.getAttribute("title")?.trim() || undefined,
  };
}

// SRS-F-002 fallback chain: arch override → schedule_date → transaction_date →
// created_at. Landing on created_at flags the view non-draggable (SRS-F-005).
function resolveCalendarAnchorField(archConfig, fields) {
  const fieldKind = (name) => {
    const meta = fields[name];
    if (!meta) return undefined;
    if (meta.kind === "date" || meta.backendType === "date") return "date";
    if (meta.kind === "datetime" || meta.backendType === "datetime") return "datetime";
    return undefined;
  };

  if (archConfig.dateStartField) {
    const kind = fieldKind(archConfig.dateStartField);
    // An unresolvable explicit field falls through to the auto chain rather
    // than failing the whole view.
    if (kind) return { name: archConfig.dateStartField, kind, isCreatedAtFallback: false };
  }
  if ("schedule_date" in fields) {
    const kind = fieldKind("schedule_date");
    if (kind) return { name: "schedule_date", kind, isCreatedAtFallback: false };
  }
  if ("transaction_date" in fields) {
    const kind = fieldKind("transaction_date");
    if (kind) return { name: "transaction_date", kind, isCreatedAtFallback: false };
  }
  if ("created_at" in fields) {
    return { name: "created_at", kind: fieldKind("created_at") ?? "datetime", isCreatedAtFallback: true };
  }
  return null;
}

// SRS-F-003 label precedence: name → description → short_desc → "Record #<id>".
export function resolveCalendarLabel(record, fields) {
  for (const candidate of ["name", "description", "short_desc"]) {
    if (!(candidate in fields)) continue;
    const value = record[candidate];
    if (value !== undefined && value !== null && String(value).trim() !== "") return String(value);
  }
  return t("Record #{id}", { id: record.id });
}

// SRS-F-003 amount precedence: arch override → a field literally named "amount".
function resolveCalendarAmountField(archConfig, fields) {
  if (archConfig.amountField && archConfig.amountField in fields) return archConfig.amountField;
  if ("amount" in fields) return "amount";
  return undefined;
}

// calendarOccurrenceDate resolves the effective date for a row — expanded
// recurring occurrences carry their own occurrence_date.
export function calendarOccurrenceDate(occurrence, anchorField) {
  const raw = occurrence.is_recurring ? occurrence.occurrence_date : occurrence[anchorField];
  if (!raw) return null;
  return serverTimeToDate(raw);
}

export function resolveCalendarViewData(viewData) {
  const archConfig = parseCalendarArchConfig(viewData?.arch);
  return {
    archConfig,
    anchorField: resolveCalendarAnchorField(archConfig, viewData?.fields ?? {}),
    amountField: resolveCalendarAmountField(archConfig, viewData?.fields ?? {}),
  };
}

export function startOfDay(date) {
  const next = new Date(date);
  next.setHours(0, 0, 0, 0);
  return next;
}

export function startOfWeek(date) {
  const next = startOfDay(date);
  next.setDate(next.getDate() - next.getDay());
  return next;
}

export function startOfMonth(date) {
  return new Date(date.getFullYear(), date.getMonth(), 1);
}

export function addDays(date, days) {
  const next = new Date(date);
  next.setDate(next.getDate() + days);
  return next;
}

export function sameDay(a, b) {
  return a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
}

// calendarRangeFor resolves [from, to) for the active mode (SRS-F-004); month
// fetches the whole 6-week grid, not just the calendar month.
export function calendarRangeFor(mode, current) {
  if (mode === "day") {
    const from = startOfDay(current);
    return { from, to: addDays(from, 1) };
  }
  if (mode === "week") {
    const from = startOfWeek(current);
    return { from, to: addDays(from, 7) };
  }
  if (mode === "year") {
    return { from: new Date(current.getFullYear(), 0, 1), to: new Date(current.getFullYear() + 1, 0, 1) };
  }
  if (mode === "schedule") {
    const from = startOfDay(current);
    return { from, to: addDays(from, 60) };
  }
  const gridStart = startOfWeek(startOfMonth(current));
  return { from: gridStart, to: addDays(gridStart, 42) };
}
