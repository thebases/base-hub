// grid-utils.js — arch parsing for <grid><field type="row"/section pair><field
// type="col"><range .../></field><field type="measure"/></grid>, plus pivoting
// a flat read_group response into a {rowKey: {colKey: sum}} matrix and the
// row/column totals a spreadsheet-style grid needs. No RPC calls here: the
// fetch stays in jsonv2.js/ModelView.js, this module only shapes the arch +
// read_group rows it is handed (see backend-context.md's read_group contract:
// a flat list of {...groupby field values, __count, "<field>:sum": value}).

import { addDays, startOfDay } from "@/modules/base/components/views/base-view/calendar-utils.js";
import { groupKey } from "@/modules/base/components/views/base-view/kanban-utils.js";

// parseGridArchConfig reads the <grid> root's row/section field(s), the col
// field + its named ranges, and the measure field/operator.
function parseGridArchConfig(arch) {
  if (!arch?.trim()) return {};
  const document = new DOMParser().parseFromString(arch, "text/xml");
  if (document.querySelector("parsererror")) return {};
  const root = document.documentElement;
  if (!root || root.tagName.toLowerCase() !== "grid") return {};

  const rowFieldNames = Array.from(root.querySelectorAll('field[type="row"]'))
    .map((field) => field.getAttribute("name")?.trim() || "")
    .filter((name) => name.length > 0);
  // A second row field acts as the section grouping (e.g. project -> task);
  // the last one is always the leaf row.
  const sectionField = rowFieldNames.length === 2 ? rowFieldNames[0] : undefined;
  const rowField = rowFieldNames[rowFieldNames.length - 1];

  const colFieldEl = root.querySelector('field[type="col"]');
  const colField = colFieldEl?.getAttribute("name")?.trim() || undefined;
  const ranges = colFieldEl
    ? Array.from(colFieldEl.querySelectorAll("range")).map((range) => ({
        name: range.getAttribute("name")?.trim() || "",
        span: Number(range.getAttribute("span")) || 7,
        step: range.getAttribute("step")?.trim() || "day",
        isDefault: range.getAttribute("default") === "1",
      }))
    : [];

  const measureEl = root.querySelector('field[type="measure"]');
  const measureField = measureEl?.getAttribute("name")?.trim() || undefined;
  const measureOperator = measureEl?.getAttribute("operator")?.trim() || "sum";

  return { rowField, sectionField, colField, ranges, measureField, measureOperator };
}

// resolveGridViewData validates the arch's row/col/measure fields exist on
// the model (fields_get) and resolves the default range. Returns null when
// the arch is missing a required field.
export function resolveGridViewData(viewData) {
  const archConfig = parseGridArchConfig(viewData?.arch);
  const fields = viewData?.fields ?? {};
  if (!archConfig.rowField || !archConfig.colField || !archConfig.measureField) return null;
  if (!fields[archConfig.rowField] || !fields[archConfig.colField] || !fields[archConfig.measureField]) return null;
  const ranges = archConfig.ranges.length > 0 ? archConfig.ranges : [{ name: "week", span: 7, step: "day", isDefault: true }];
  const defaultRange = ranges.find((range) => range.isDefault)?.name ?? ranges[0].name;
  const rowFields = archConfig.sectionField ? [archConfig.sectionField, archConfig.rowField] : [archConfig.rowField];
  return { ...archConfig, rowFields, ranges, defaultRange };
}

// gridRangeFor turns a named range (e.g. "week"/"month") into a concrete day
// window anchored on `anchor`, plus its day columns — v1 only has day-stepped
// ranges (weekly/monthly timesheets), so `step` beyond "day" is not needed.
export function gridRangeFor(ranges, rangeName, anchor) {
  const range = ranges.find((candidate) => candidate.name === rangeName) ?? ranges[0];
  const from = startOfDay(anchor);
  const columns = Array.from({ length: range.span }, (_, index) => {
    const date = addDays(from, index);
    return { key: date.toISOString().slice(0, 10), label: date.toLocaleDateString(undefined, { weekday: "short", day: "numeric" }) };
  });
  return { from, to: addDays(from, range.span), columns };
}

// displayValue mirrors the other views' many2one-tuple-to-label helper.
function displayValue(value) {
  if (value === null || value === undefined || value === false) return "";
  if (Array.isArray(value) && value.length === 2) return String(value[1]);
  return String(value);
}

// buildGridMatrix pivots the flat read_group response (one entry per present
// row-combo × col combination) into a {rowKey: {colKey: sum}} matrix plus the
// ordered row list the view renders. rowKey is the joined groupKey of every
// rowFields value (section+row when a section field is configured), so a
// section change and a row change under it are distinct rows.
export function buildGridMatrix(groupRows, rowFields, colField, measureField) {
  const matrix = {};
  const rowsByKey = new Map();
  for (const entry of groupRows) {
    const ids = rowFields.map((field) => groupKey(entry[field]) || "undefined");
    const rowKey = ids.join("|");
    const colKey = groupKey(entry[colField]) || "undefined";
    const value = Number(entry[`${measureField}:sum`] ?? 0);
    matrix[rowKey] = matrix[rowKey] ?? {};
    matrix[rowKey][colKey] = value;
    if (!rowsByKey.has(rowKey)) {
      const labels = rowFields.map((field) => displayValue(entry[field]) || t("Undefined"));
      rowsByKey.set(rowKey, { key: rowKey, ids, labels });
    }
  }
  return { matrix, rows: Array.from(rowsByKey.values()) };
}

// gridRowTotal/gridColumnTotals/gridGrandTotal sum the pivoted matrix along
// each axis for the row/column/footer totals.
export function gridRowTotal(matrix, rowKey, columns) {
  const cells = matrix[rowKey] ?? {};
  return columns.reduce((sum, column) => sum + (cells[column.key] ?? 0), 0);
}

export function gridColumnTotals(matrix, rows, columns) {
  return Object.fromEntries(columns.map((column) => [column.key, rows.reduce((sum, row) => sum + (matrix[row.key]?.[column.key] ?? 0), 0)]));
}

export function gridGrandTotal(matrix, rows, columns) {
  return rows.reduce((sum, row) => sum + gridRowTotal(matrix, row.key, columns), 0);
}
