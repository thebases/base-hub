// cohort-utils.js — arch parsing for <cohort date_start date_stop mode
// interval timeline measure string disable_linking>, plus the heatmap
// percentage/heat-color math and the row-click drill-in domain builder. No
// RPC calls here: the cohort_data fetch stays in ModelView.js, this module
// only shapes the arch + the RPC's {bucket, total, values} rows it is handed
// (see backend-context.md's cohort_data contract).

import { addDays } from "@/modules/base/components/views/base-view/calendar-utils.js";

export const COHORT_INTERVALS = ["day", "week", "month", "year"];
export const COHORT_MODES = ["retention", "churn"];
export const COHORT_TIMELINES = ["forward", "backward"];

// parseCohortArchConfig reads the <cohort> root's attributes, defaulting the
// optional ones exactly like the cohort_data RPC itself does server-side.
function parseCohortArchConfig(arch) {
  if (!arch?.trim()) return {};
  const document = new DOMParser().parseFromString(arch, "text/xml");
  if (document.querySelector("parsererror")) return {};
  const root = document.documentElement;
  if (!root || root.tagName.toLowerCase() !== "cohort") return {};
  const mode = root.getAttribute("mode")?.trim();
  const timeline = root.getAttribute("timeline")?.trim();
  const interval = root.getAttribute("interval")?.trim();
  return {
    dateStartField: root.getAttribute("date_start")?.trim() || undefined,
    dateStopField: root.getAttribute("date_stop")?.trim() || undefined,
    mode: COHORT_MODES.includes(mode) ? mode : "retention",
    timeline: COHORT_TIMELINES.includes(timeline) ? timeline : "forward",
    interval: COHORT_INTERVALS.includes(interval) ? interval : "day",
    measure: root.getAttribute("measure")?.trim() || "__count",
    string: root.getAttribute("string")?.trim() || undefined,
    disableLinking: root.getAttribute("disable_linking") === "1" || root.getAttribute("disable_linking") === "true",
  };
}

function fieldIsDate(fields, name) {
  const meta = fields[name];
  if (!meta) return false;
  return meta.kind === "date" || meta.kind === "datetime" || meta.backendType === "date" || meta.backendType === "datetime";
}

function fieldIsNumeric(fields, name) {
  const meta = fields[name];
  if (!meta) return false;
  return meta.kind === "float" || meta.kind === "integer" || meta.backendType === "float" || meta.backendType === "integer";
}

// resolveCohortViewData validates the arch's required date_start/date_stop
// fields (and measure, if not __count) against fields_get. Returns null when
// the model has no usable field pair — cohort never gets a synthesized arch
// (see view-registry.js), so a missing/invalid pair means genuinely
// misconfigured metadata, not a fallback case.
export function resolveCohortViewData(viewData) {
  const archConfig = parseCohortArchConfig(viewData?.arch);
  const fields = viewData?.fields ?? {};
  if (!archConfig.dateStartField || !archConfig.dateStopField) return null;
  if (!fieldIsDate(fields, archConfig.dateStartField) || !fieldIsDate(fields, archConfig.dateStopField)) return null;
  if (archConfig.measure !== "__count" && !fieldIsNumeric(fields, archConfig.measure)) return null;
  return archConfig;
}

// cohortMeasureOptions lists the switchable measures for the toolbar: the
// count plus every numeric field on the model (mirrors grid's single
// arch-declared measure field, but cohort's arch only names one, so the
// switcher offers the full numeric field set fields_get already has).
export function cohortMeasureOptions(fields) {
  const numeric = Object.entries(fields || {})
    .filter(([name]) => fieldIsNumeric(fields, name))
    .map(([name, meta]) => ({ value: name, label: meta.string || name }));
  return [{ value: "__count", label: t("Count") }, ...numeric];
}

// addCohortInterval advances `date` by `count` cohort intervals — the same
// day/week/month/year step the backend buckets by — used here only to turn a
// bucket's start back into a [start, end) range for the drill-in domain.
function addCohortInterval(date, interval, count) {
  if (interval === "day") return addDays(date, count);
  if (interval === "week") return addDays(date, count * 7);
  if (interval === "month") return new Date(date.getFullYear(), date.getMonth() + count, date.getDate());
  return new Date(date.getFullYear() + count, date.getMonth(), date.getDate());
}

// parseCohortBucketDate turns the RPC's bucket label ("2026-01-02" for
// day/week — the Monday bucket start, "2026-01" for month, "2026" for year)
// back into the local Date it was truncated from.
function parseCohortBucketDate(bucket, interval) {
  if (interval === "year") return new Date(Number(bucket), 0, 1);
  const parts = bucket.split("-").map(Number);
  if (interval === "month") return new Date(parts[0], parts[1] - 1, 1);
  return new Date(parts[0], parts[1] - 1, parts[2]);
}

// cohortCellPercent is the single formula every heatmap cell (a data row or
// the average row) uses: retention mode is the retained fraction, churn mode
// is its complement — both null (renders as an empty cell) when the bucket
// has no records to divide by, never a divide-by-zero 0%.
export function cohortCellPercent(total, value, mode) {
  if (!total) return null;
  const retained = value / total;
  return mode === "churn" ? 1 - retained : retained;
}

// cohortHeatClass bands a 0..1 fraction into one of 5 heat-intensity classes
// for CSS coloring; no-data cells get their own distinct class.
export function cohortHeatClass(percent) {
  if (percent === null || percent === undefined) return "basehub-cohort-heat-none";
  const band = Math.min(4, Math.floor(percent * 5));
  return `basehub-cohort-heat-${band}`;
}

// cohortAverageRow sums every data row's total/values so the average row's
// own percent (computed by the same cohortCellPercent formula) is the
// dataset-wide weighted average, not a naive mean of per-row percentages.
export function cohortAverageRow(rows, columnCount) {
  const values = new Array(columnCount).fill(0);
  let total = 0;
  for (const row of rows) {
    total += row.total;
    for (let index = 0; index < columnCount; index++) values[index] += row.values[index] ?? 0;
  }
  return { bucket: t("Average"), total, values };
}

// cohortBucketDomain builds the row-click drill-in domain: the bucket's own
// [start, end) range on whichever field it was keyed by (date_start for
// forward timeline, date_stop for backward) — the RPC returns no per-record
// ids, so the boundary is reproduced client-side with the same interval
// math the backend used to bucket it. Returns null when linking is disabled.
export function cohortBucketDomain(cohortData, bucket) {
  if (cohortData.disableLinking) return null;
  const keyField = cohortData.timeline === "backward" ? cohortData.dateStopField : cohortData.dateStartField;
  const start = parseCohortBucketDate(bucket.bucket, cohortData.interval);
  const end = addCohortInterval(start, cohortData.interval, 1);
  return [
    [keyField, ">=", start.toISOString()],
    [keyField, "<", end.toISOString()],
  ];
}
