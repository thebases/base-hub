// Keeps ids unique when markup is inserted or cloned. A block may use ids to wire its parts
// (accordion/tab/carousel targets); when an id already exists in the document it is renamed and every
// reference inside the new markup (href/data-bs-target "#id", for, aria-controls, aria-labelledby) follows.
const REF_ATTRS = ["href", "data-bs-target", "for", "aria-controls", "aria-labelledby"];

export function uniquifyIds(node, doc) {
  const used = new Set([...doc.querySelectorAll("[id]")].map((el) => el.id));
  const renames = new Map();
  for (const el of node.querySelectorAll("[id]")) {
    if (!used.has(el.id)) {
      used.add(el.id);
      continue;
    }
    let candidate;
    do candidate = `${el.id.replace(/-\d{4,}$/, "")}-${Math.floor(1000 + Math.random() * 9000)}`;
    while (used.has(candidate));
    renames.set(el.id, candidate);
    used.add(candidate);
    el.id = candidate;
  }
  if (!renames.size) return;
  for (const el of node.querySelectorAll("*")) {
    for (const attr of REF_ATTRS) {
      const value = el.getAttribute(attr);
      if (!value) continue;
      for (const [from, to] of renames) {
        if (value === `#${from}` || value === from) el.setAttribute(attr, value.startsWith("#") ? `#${to}` : to);
        else if (attr === "aria-controls" || attr === "aria-labelledby") el.setAttribute(attr, value.split(/\s+/).map((v) => (v === from ? to : v)).join(" "));
      }
    }
  }
}
