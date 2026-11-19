// view-type-value.js — pure helpers for the ui_view_type / ui_view_types form
// widgets. Both widgets store a plain string: one view type name, or a
// comma-separated list ("form,list,kanban"). The list keeps its stored order
// and appends new picks, because order can be meaningful (e.g. a window
// action's view_mode lists its default mode first).

// parseViewTypes splits a stored value into trimmed, lower-case, de-duplicated names.
export function parseViewTypes(raw) {
  if (typeof raw !== "string") return [];
  const names = raw.split(",").map((item) => item.trim().toLowerCase()).filter(Boolean);
  return [...new Set(names)];
}

// toggleViewType adds (at the end) or removes `name` and returns the new stored string.
export function toggleViewType(raw, name, checked) {
  const current = parseViewTypes(raw).filter((item) => item !== name);
  return (checked ? [...current, name] : current).join(",");
}
