// gantt-utils.js — arch parsing for <gantt date_start="..." date_stop="..."
// default_group_by="..." string="..." color="..." default_scale="week"> plus
// the day/week/month/year date-to-pixel math the timeline needs. Hand-rolled
// (no library), mirroring calendar-utils.js's own hand-rolled date-grid math
// — day/week/month/year scaling is simple enough to not warrant one. No RPC
// calls here: fetching stays in jsonv2.js/ModelView.js, this module only
// shapes the arch + records it is handed.

import { addDays, resolveCalendarLabel, startOfDay, startOfMonth, startOfWeek } from "@/modules/base/components/views/base-view/calendar-utils.js";
import { groupKey } from "@/modules/base/components/views/base-view/kanban-utils.js";
import { paletteColorForIndex } from "@/modules/base/components/views/base-view/color-palette.js";

export const GANTT_SCALES = ["day", "week", "month", "year"];

// One pixel-per-day rate per scale drives both the header columns and the
// pill placement, so they can never drift out of sync with each other. Month
// and year columns use a flat 30/365-day approximation rather than real
// calendar-month/leap-year boundaries — an accepted v1 approximation, same
// spirit as calendar-utils.js's fixed 6-week month grid.
const SCALE_CONFIG = {
  day: { unitDays: 1, columnWidthPx: 40, spanDays: 60 },
  week: { unitDays: 7, columnWidthPx: 96, spanDays: 7 * 26 },
  month: { unitDays: 30, columnWidthPx: 120, spanDays: 30 * 18 },
  year: { unitDays: 365, columnWidthPx: 140, spanDays: 365 * 6 },
};

export function pxPerDayForScale(scale) {
  const config = SCALE_CONFIG[scale] ?? SCALE_CONFIG.week;
  return config.columnWidthPx / config.unitDays;
}

export function columnWidthPxForScale(scale) {
  return (SCALE_CONFIG[scale] ?? SCALE_CONFIG.week).columnWidthPx;
}

// navigationStepDays is how far Prev/Next shifts the visible window — one
// quarter of the window's span, so consecutive clicks overlap a little.
export function navigationStepDays(scale) {
  const config = SCALE_CONFIG[scale] ?? SCALE_CONFIG.week;
  return Math.max(config.unitDays, Math.round(config.spanDays / 4));
}

// parseGanttArchConfig reads the <gantt> root's attributes. date_start/
// date_stop are required by the arch contract; the rest are optional.
export function parseGanttArchConfig(arch) {
  if (!arch?.trim()) return {};
  const document = new DOMParser().parseFromString(arch, "text/xml");
  if (document.querySelector("parsererror")) return {};
  const root = document.documentElement;
  if (!root || root.tagName.toLowerCase() !== "gantt") return {};
  return {
    dateStartField: root.getAttribute("date_start")?.trim() || undefined,
    dateStopField: root.getAttribute("date_stop")?.trim() || undefined,
    groupByField: root.getAttribute("default_group_by")?.trim() || undefined,
    colorField: root.getAttribute("color")?.trim() || undefined,
    string: root.getAttribute("string")?.trim() || undefined,
    defaultScale: root.getAttribute("default_scale")?.trim() || undefined,
  };
}

function fieldKind(fields, name) {
  const meta = fields[name];
  if (!meta) return undefined;
  if (meta.kind === "date" || meta.backendType === "date") return "date";
  if (meta.kind === "datetime" || meta.backendType === "datetime") return "datetime";
  return undefined;
}

// resolveGanttViewData validates the arch's required date fields against
// fields_get metadata and resolves the scale/grouping/color config the view
// needs to render. Returns null when the model has no usable start/stop pair.
export function resolveGanttViewData(viewData) {
  const archConfig = parseGanttArchConfig(viewData?.arch);
  const fields = viewData?.fields ?? {};
  const dateStartKind = archConfig.dateStartField ? fieldKind(fields, archConfig.dateStartField) : undefined;
  const dateStopKind = archConfig.dateStopField ? fieldKind(fields, archConfig.dateStopField) : undefined;
  if (!dateStartKind || !dateStopKind) return null;
  return {
    archConfig,
    dateStartField: archConfig.dateStartField,
    dateStopField: archConfig.dateStopField,
    dateKind: dateStartKind,
    groupByField: archConfig.groupByField,
    colorField: archConfig.colorField,
    string: archConfig.string,
    scale: GANTT_SCALES.includes(archConfig.defaultScale) ? archConfig.defaultScale : "week",
  };
}

// ganttRangeFor starts the window at the anchor's own unit — day: that day,
// week: that week's first day, month: the 1st — then spans forward, sized by
// scale (a day-scale window is a couple of months, a year-scale window is
// several years). Year stays centered on the anchor.
export function ganttRangeFor(scale, anchor) {
  const config = SCALE_CONFIG[scale] ?? SCALE_CONFIG.week;
  let from;
  if (scale === "day") from = startOfDay(anchor);
  else if (scale === "week") from = startOfWeek(anchor);
  else if (scale === "month") from = startOfMonth(anchor);
  else from = addDays(startOfDay(anchor), -Math.floor(config.spanDays / 2));
  return { from, to: addDays(from, config.spanDays) };
}

// ganttColumns builds the header cells spanning `range` at the scale's unit
// step, for the ruler row above the pills.
export function ganttColumns(scale, range) {
  const config = SCALE_CONFIG[scale] ?? SCALE_CONFIG.week;
  const columns = [];
  for (let cursor = range.from; cursor.getTime() < range.to.getTime(); cursor = addDays(cursor, config.unitDays)) {
    columns.push({ key: cursor.toISOString(), ms: cursor.getTime(), label: ganttColumnLabel(scale, cursor) });
  }
  return columns;
}

function ganttColumnLabel(scale, date) {
  if (scale === "day") return date.toLocaleDateString(undefined, { weekday: "short", day: "numeric" });
  if (scale === "week") return date.toLocaleDateString(undefined, { month: "short", day: "numeric" });
  if (scale === "month") return date.toLocaleDateString(undefined, { month: "short", year: "numeric" });
  return String(date.getFullYear());
}

// dateToOffsetPx/offsetPxToDate convert between a date and its horizontal
// pixel position relative to the range start — the core drag-to-reschedule
// math (pointer x -> day offset -> new date_start/date_stop).
export function dateToOffsetPx(date, rangeFrom, scale) {
  const days = (startOfDay(date).getTime() - startOfDay(rangeFrom).getTime()) / 86400000;
  return days * pxPerDayForScale(scale);
}

export function offsetPxToDate(offsetPx, rangeFrom, scale) {
  const days = Math.round(offsetPx / pxPerDayForScale(scale));
  return addDays(startOfDay(rangeFrom), days);
}

function displayValue(value) {
  if (value === null || value === undefined || value === false) return "";
  if (Array.isArray(value) && value.length === 2) return String(value[1]);
  return String(value);
}

// buildGanttRows groups records by the arch's default_group_by field into one
// row per group (mirrors kanban's board-axis grouping); with no group field,
// each record gets its own row.
export function buildGanttRows(records, groupByField, fields) {
  if (!groupByField) {
    return records.map((record) => ({ key: String(record.id), label: resolveCalendarLabel(record, fields), records: [record] }));
  }
  const rows = new Map();
  for (const record of records) {
    const key = groupKey(record[groupByField]) || "undefined";
    const label = displayValue(record[groupByField]) || t("Undefined");
    if (!rows.has(key)) rows.set(key, { key, label, records: [] });
    rows.get(key).records.push(record);
  }
  return Array.from(rows.values());
}

// A small fixed palette keyed by the color field's value hash — the "kanban-
// style palette" the plan calls for, without pulling in a real color-index
// system (frontend has none to reuse yet).
const PILL_PALETTE = ["#4c6ef5", "#12b886", "#f59f00", "#e64980", "#7048e8", "#15aabf", "#fa5252", "#82c91e"];

export function ganttPillColor(record, colorField) {
  if (!colorField) return PILL_PALETTE[0];
  // An integer color index (kanban palette, e.g. a stage's color) wins over the hash fallback.
  const indexed = paletteColorForIndex(record[colorField]);
  if (indexed) return indexed;
  const key = groupKey(record[colorField]) || "undefined";
  let hash = 0;
  for (let index = 0; index < key.length; index++) hash = (hash * 31 + key.charCodeAt(index)) | 0;
  return PILL_PALETTE[Math.abs(hash) % PILL_PALETTE.length];
}
