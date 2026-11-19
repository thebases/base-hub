// JS port of frontend-admin's views/base-view/kanban-utils.ts — reads the
// kanban arch (<kanban default_group_by="..."><templates><t t-name="card">)
// for the board axis and the card layout, and holds the grouped-record
// bookkeeping the board needs.

import { inferKanbanGroupByField } from "@/services/api/jsonv2.js";

const FALLBACK_KANBAN_LAYOUT = { titleField: "name", subFields: [] };

export function resolveKanbanCardLayout(viewData) {
  return parseKanbanArchConfig(viewData?.arch).layout ?? FALLBACK_KANBAN_LAYOUT;
}

export function resolveKanbanGroupByField(viewData) {
  return parseKanbanArchConfig(viewData?.arch).groupByField ?? inferKanbanGroupByField(viewData?.fields ?? {});
}

export function parseKanbanArchConfig(arch) {
  if (!arch?.trim()) return {};
  const document = new DOMParser().parseFromString(arch, "text/xml");
  if (document.querySelector("parsererror")) return {};

  const root = document.documentElement;
  if (!root || root.tagName.toLowerCase() !== "kanban") return {};
  const groupByField = root.getAttribute("default_group_by")?.trim() || undefined;

  const cardTemplate = Array.from(root.querySelectorAll("templates > t")).find((template) => template.getAttribute("t-name") === "card");
  const colorField = root.getAttribute("color")?.trim() || undefined;
  if (!cardTemplate) return { groupByField };

  const templateFields = Array.from(cardTemplate.querySelectorAll("field"))
    .map((field) => ({
      name: field.getAttribute("name")?.trim() || "",
      widget: field.getAttribute("widget")?.trim() || "",
      className: field.getAttribute("class")?.trim() || field.getAttribute("className")?.trim() || "",
    }))
    .filter((field) => field.name.length > 0);
  if (templateFields.length === 0) return { groupByField };

  const avatarField = templateFields.find(
    (field) => field.widget === "image" || field.name === "avatar_128" || field.name === "image_128" || field.name === "image_1920"
  )?.name;
  const badgeField = templateFields.find((field) => /\bbadge\b/i.test(field.className))?.name;
  const titleField =
    templateFields.find((field) => /\b(fw-bold|font-semibold|font-bold|o_kanban_record_title)\b/i.test(field.className))?.name ??
    templateFields.find((field) => field.name !== avatarField && field.name !== badgeField)?.name ??
    templateFields[0]?.name;
  const subFields = templateFields
    .map((field) => field.name)
    .filter((fieldName) => fieldName !== avatarField && fieldName !== badgeField && fieldName !== titleField);

  return {
    groupByField,
    layout: titleField ? { titleField, subFields: subFields.length > 0 ? subFields : [], badgeField, avatarField, colorField } : undefined,
  };
}

// groupKey collapses a grouped value to its column id. React's callers do a
// bare String(value), which turns a many2one [id, name] tuple into
// "1,Base Co" and never matches a read_group column id — collapsing the tuple
// is a deliberate deviation so relation-grouped boards actually bucket.
export function groupKey(value) {
  if (value === null || value === undefined || value === false || value === "") return "";
  if (Array.isArray(value) && value.length >= 1) return String(value[0]);
  return String(value);
}

export function countRecordsByGroup(records, groupByField) {
  if (!groupByField) return {};
  return records.reduce((counts, record) => {
    const key = groupKey(record[groupByField]) || "undefined";
    counts[key] = (counts[key] ?? 0) + 1;
    return counts;
  }, {});
}

export function remainingCountsByColumn(groupTotals, loadedCounts) {
  return Object.fromEntries(Object.entries(groupTotals).map(([columnId, total]) => [columnId, Math.max(0, total - (loadedCounts[columnId] ?? 0))]));
}

export function appendUniqueRecords(records, nextRecords) {
  const seen = new Set(records.map((record) => String(record.id)));
  const appended = nextRecords.filter((record) => {
    const id = String(record.id);
    if (seen.has(id)) return false;
    seen.add(id);
    return true;
  });
  return [...records, ...appended];
}

// moveKanbanRecordSnapshot applies the optimistic client-side move before the
// backend write lands: the record adopts the target column's group value and
// is re-inserted either before the hovered card or at the end of that group.
export function moveKanbanRecordSnapshot(records, record, nextValues, meta, groupByField) {
  const moving = records.find((item) => String(item.id) === String(record.id));
  if (!moving) return records;

  const nextGroupValue = groupByField ? (nextValues[groupByField] ?? meta.targetColumn.id) : meta.targetColumn.id;
  const nextRecord = { ...moving, ...nextValues, ...(groupByField ? { [groupByField]: nextGroupValue } : {}) };
  const withoutMoving = records.filter((item) => String(item.id) !== String(record.id));
  const beforeIndex = meta.beforeRecord ? withoutMoving.findIndex((item) => String(item.id) === String(meta.beforeRecord?.id)) : -1;
  const insertIndex =
    beforeIndex >= 0
      ? beforeIndex
      : withoutMoving.reduce((lastIndex, item, index) => (groupByField && groupKey(item[groupByField]) === String(nextGroupValue) ? index + 1 : lastIndex), withoutMoving.length);

  return [...withoutMoving.slice(0, insertIndex), nextRecord, ...withoutMoving.slice(insertIndex)];
}
