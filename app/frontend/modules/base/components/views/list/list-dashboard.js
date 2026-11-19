// List-view stat tiles: a list arch may carry a <dashboard> block of <stat>
// children rendered as count cards above the table, e.g.
//
//   <dashboard>
//     <stat string="Online" icon="activity" color="success" domain="[('status', '=', 'ONLINE')]"/>
//     <stat string="Battery Health Good" icon="battery-medium" color="warning"
//           domain="[...]" total_domain="[...]"/>
//   </dashboard>
//
// Each stat is a server-side search_count of its domain AND'ed with the
// list's active search domain, so tiles follow the current filter. With
// `total_domain` the tile renders "count/total" instead of a bare count.

import { createModelRpc } from "@/services/api/jsonv2.js";
import { evaluateArchDomain } from "@/modules/base/components/views/form/form-domain.js";

// Bootstrap theme colours a stat may use (bg-<color>-subtle text-<color>);
// anything else falls back to primary rather than emitting a dead class.
const STAT_COLORS = new Set(["primary", "secondary", "success", "danger", "warning", "info", "dark"]);

// parseStatDomain turns a Python-like arch domain string into a domain array;
// a missing or unparsable attribute yields null (no extra condition / no total).
function parseStatDomain(source) {
  if (!source?.trim()) return null;
  return evaluateArchDomain(source, {})?.domain ?? null;
}

// listDashboardFromView parses `list > dashboard > stat` into tile specs.
export function listDashboardFromView(view) {
  if (typeof DOMParser === "undefined" || !view?.arch) return [];
  const document = new DOMParser().parseFromString(view.arch, "text/xml");
  if (document.querySelector("parsererror")) return [];
  return Array.from(document.querySelectorAll("list > dashboard > stat")).map((node, index) => {
    const color = node.getAttribute("color") ?? "primary";
    return {
      key: `stat_${index}`,
      label: node.getAttribute("string") ?? "",
      icon: node.getAttribute("icon") ?? "hash",
      color: STAT_COLORS.has(color) ? color : "primary",
      domain: parseStatDomain(node.getAttribute("domain")) ?? [],
      totalDomain: parseStatDomain(node.getAttribute("total_domain")),
    };
  });
}

// countStatDomain runs one search_count of `domain` within the list's search
// domain (implicit AND of two concatenated domains).
async function countStatDomain(model, baseDomain, domain) {
  const response = await createModelRpc(model).call("search_count", [], { domain: [...baseDomain, ...domain] });
  return Number(response.result ?? 0);
}

// loadStat resolves one tile's display value; a failed count (bad domain,
// RBAC) renders an em dash for that tile instead of failing the whole list.
async function loadStat(model, baseDomain, stat) {
  try {
    const [count, total] = await Promise.all([
      countStatDomain(model, baseDomain, stat.domain),
      stat.totalDomain ? countStatDomain(model, baseDomain, stat.totalDomain) : null,
    ]);
    return { ...stat, value: total === null ? String(count) : `${count}/${total}` };
  } catch {
    return { ...stat, value: "—" };
  }
}

// fetchListDashboardStats loads every tile's value concurrently.
export function fetchListDashboardStats(model, stats, baseDomain = []) {
  return Promise.all(stats.map((stat) => loadStat(model, baseDomain, stat)));
}
