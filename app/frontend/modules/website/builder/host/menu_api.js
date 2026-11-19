import { createModelRpc } from "@/services/api/jsonv2.js";
import { resolveRelationValue } from "@/modules/base/components/views/form/form-field-normalizer.js";

const MenuRpc = createModelRpc("tbl.website_menu");
const FIELDS = ["name", "parent_id", "page_id", "url", "sequence", "new_window", "is_visible", "site_id"];

function check(response, fallback) {
  if (response.error) throw new Error(response.error.message || fallback);
  return response.result;
}

// The site's menu as a flat list in display order (depth-first, siblings by sequence), each row
// with its depth, for an indented editor. Cycles cannot exist (the server rejects them) but are
// guarded anyway so a bad row never hangs the panel.
export async function fetchMenuTree(siteId) {
  const rows = check(await MenuRpc.search([["site_id", "=", siteId]], FIELDS, "sequence ASC, id ASC", undefined, 500), "Could not load the menu") || [];
  const items = rows.map((r) => ({
    id: r.id, name: r.name || "", url: r.url || "", sequence: Number(r.sequence) || 0,
    parent: Number(resolveRelationValue(r.parent_id)) || 0, page: Number(resolveRelationValue(r.page_id)) || 0,
    new_window: !!r.new_window, is_visible: r.is_visible !== false,
  }));
  const out = [];
  const seen = new Set();
  const walk = (parent, depth) => {
    for (const item of items.filter((i) => i.parent === parent)) {
      if (seen.has(item.id)) continue;
      seen.add(item.id);
      out.push({ ...item, depth });
      walk(item.id, depth + 1);
    }
  };
  walk(0, 0);
  return out;
}

export async function createMenuItem(siteId, { name, url, parent }) {
  return check(await MenuRpc.create({ site_id: siteId, name, url, parent_id: parent || false, sequence: 100 }), "Could not add the entry");
}

export async function updateMenuItem(id, values) {
  return check(await MenuRpc.write([id], values), "Could not save the entry");
}

export async function deleteMenuItem(id) {
  return check(await MenuRpc.delete([id]), "Could not delete the entry");
}

// Swaps the order of an entry with its previous/next sibling. Sequences are renumbered by 10 so a swap
// of equal values still produces a strict order.
export async function moveMenuItem(tree, id, direction) {
  const item = tree.find((i) => i.id === id);
  if (!item) return;
  const siblings = tree.filter((i) => i.parent === item.parent).sort((a, b) => a.sequence - b.sequence || a.id - b.id);
  const at = siblings.findIndex((i) => i.id === id);
  const to = at + direction;
  if (to < 0 || to >= siblings.length) return;
  [siblings[at], siblings[to]] = [siblings[to], siblings[at]];
  for (const [i, s] of siblings.entries()) {
    const sequence = (i + 1) * 10;
    if (s.sequence !== sequence) await updateMenuItem(s.id, { sequence });
  }
}
