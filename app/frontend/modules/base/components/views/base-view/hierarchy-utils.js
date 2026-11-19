// hierarchy-utils.js — arch parsing (mirrors kanban-utils.js's
// parseKanbanArchConfig) for <hierarchy> and client-side tree assembly from a
// flat search_read result. No RPC calls here: the fetch stays in jsonv2.js /
// ModelView.js, this module only shapes the arch + records it is handed.

const FALLBACK_HIERARCHY_LAYOUT = { titleField: "name", subFields: [] };

export function resolveHierarchyCardLayout(viewData) {
  return parseHierarchyArchConfig(viewData?.arch).layout ?? FALLBACK_HIERARCHY_LAYOUT;
}

export function resolveHierarchyParentField(viewData) {
  return parseHierarchyArchConfig(viewData?.arch).parentField ?? "parent_id";
}

// parseHierarchyArchConfig reads <hierarchy parent_field="..." child_field="..."
// draggable="0|1" icon="..."><templates><t t-name="card">...</t> — same
// card-layout inference kanban-utils.js uses (title/subfields by field order),
// since v1 hardcodes a small card template instead of a full QWeb compiler.
function parseHierarchyArchConfig(arch) {
  if (!arch?.trim()) return {};
  const document = new DOMParser().parseFromString(arch, "text/xml");
  if (document.querySelector("parsererror")) return {};

  const root = document.documentElement;
  if (!root || root.tagName.toLowerCase() !== "hierarchy") return {};

  const parentField = root.getAttribute("parent_field")?.trim() || "parent_id";
  const childField = root.getAttribute("child_field")?.trim() || undefined;
  const draggable = root.getAttribute("draggable") === "1";
  const icon = root.getAttribute("icon")?.trim() || undefined;

  const cardTemplate = Array.from(root.querySelectorAll("templates > t")).find((template) => template.getAttribute("t-name") === "card");
  if (!cardTemplate) return { parentField, childField, draggable, icon };

  const templateFields = Array.from(cardTemplate.querySelectorAll("field"))
    .map((field) => field.getAttribute("name")?.trim() || "")
    .filter((name) => name.length > 0);
  if (templateFields.length === 0) return { parentField, childField, draggable, icon };

  const [titleField, ...subFields] = templateFields;
  return { parentField, childField, draggable, icon, layout: { titleField, subFields } };
}

// buildHierarchyTree groups the flat record list by parentField into a forest
// of {record, children} nodes — roots are records whose parent value is
// empty or points outside the fetched set (e.g. filtered out by a domain).
export function buildHierarchyTree(records, parentField) {
  const byParentKey = new Map();
  for (const record of records) {
    const key = parentKey(record[parentField]);
    if (!byParentKey.has(key)) byParentKey.set(key, []);
    byParentKey.get(key).push(record);
  }
  const knownIds = new Set(records.map((record) => String(record.id)));

  function buildNode(record) {
    const children = (byParentKey.get(String(record.id)) ?? []).map(buildNode);
    return { record, children };
  }

  const roots = records.filter((record) => {
    const key = parentKey(record[parentField]);
    return key === "" || !knownIds.has(key);
  });
  return roots.map(buildNode);
}

// parentKey collapses a many2one value (id, [id, label], or false) to the
// plain id string used to match against a candidate parent's own id.
function parentKey(value) {
  if (value === null || value === undefined || value === false || value === "") return "";
  if (Array.isArray(value) && value.length >= 1) return String(value[0]);
  return String(value);
}
