// DashboardView — generic renderer for one module's <dashboard> arch fragment
// (ui.view.arch, type="dashboard"), a near-mechanical port of the retired
// pages/Home/Home.js: same Card/CardBody/CardHeader/Icon/Skeleton markup,
// same readableModelsFromSidebar ACL gating, same per-card search_count fetch
// pattern (try/catch per metric so one bad card can't blank the whole tab).
// Mounted once per tab by pages/Dashboard/Dashboard.js, one instance per
// installed module's dashboard.

import { Component, loadTemplate, onWillStart, useState, xml } from "nodexui/owl";

import { createModelRpc } from "@/services/api/jsonv2.js";
import { readableModelsFromSidebar } from "@/modules/base/api/menu.js";
import { Card, CardBody, CardHeader, Icon, Skeleton } from "nodexui/component";


const template = await loadTemplate(new URL("./DashboardView.xml", import.meta.url));

// parseDashboardArch reads the <dashboard><cards><card .../></cards>
// <quicklinks><link .../></quicklinks></dashboard> fragment into the same
// {key, label, model, icon, href, domain} shape Home.js's hardcoded
// METRICS/QUICK_LINKS arrays used, so loadDashboard() below needs no
// arch-specific branching beyond parsing. Mirrors the DOMParser-based
// arch-parsing convention already used by base-view/map-utils.js and its
// siblings (kanban/gantt/grid/hierarchy/cohort-utils.js).
function parseDashboardArch(arch) {
  if (!arch?.trim()) return { cards: [], quicklinks: [] };
  const document = new DOMParser().parseFromString(arch, "text/xml");
  if (document.querySelector("parsererror")) return { cards: [], quicklinks: [] };
  const root = document.documentElement;
  if (!root || root.tagName.toLowerCase() !== "dashboard") return { cards: [], quicklinks: [] };

  // key includes the arch position: several cards may share one model with
  // different domains (e.g. helpdesk's Open/Unassigned/Urgent tickets), and a
  // model-only key would collide in the template's t-foreach.
  const cards = Array.from(root.querySelectorAll("cards > card")).map((node, index) => ({
    key: `${index}:${node.getAttribute("model")?.trim() || ""}`,
    model: node.getAttribute("model")?.trim() || "",
    icon: node.getAttribute("icon")?.trim() || "file",
    label: node.getAttribute("label")?.trim() || "",
    href: node.getAttribute("href")?.trim() || "",
    domain: parseDashboardDomain(node.getAttribute("domain")),
  }));

  const quicklinks = Array.from(root.querySelectorAll("quicklinks > link")).map((node) => ({
    model: node.getAttribute("model")?.trim() || "",
    icon: node.getAttribute("icon")?.trim() || "file",
    label: node.getAttribute("label")?.trim() || "",
    href: node.getAttribute("href")?.trim() || "",
  }));

  return { cards, quicklinks };
}

// parseDashboardDomain accepts the arch's JSON-array-string domain attribute,
// defaulting to "no filter" on anything missing/malformed rather than failing
// the whole card — a bad domain string should degrade to an unfiltered count,
// not blank the tab.
function parseDashboardDomain(raw) {
  const trimmed = raw?.trim();
  if (!trimmed) return [];
  try {
    const parsed = JSON.parse(trimmed);
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

export class DashboardView extends Component {
  static template = xml({ raw: [template] });
  static components = { Card, CardBody, CardHeader, Icon, Skeleton };
  static props = { arch: { type: String, optional: true } };

  setup() {
    this.state = useState({
      metrics: [],
      quickLinks: [],
    });

    onWillStart(() => this.loadDashboard());
  }

  // A rejection from onWillStart escapes to AppRoot's error boundary and
  // blanks the whole shell, so any arch/ACL failure degrades to an empty
  // tab instead (per-card failures are already isolated in loadMetrics).
  async loadDashboard() {
    try {
      const { cards, quicklinks } = parseDashboardArch(this.props.arch);
      const readableModels = await readableModelsFromSidebar();

      this.state.metrics = cards
        .filter((card) => readableModels.has(card.model))
        .map((card) => ({ ...card, loading: true, value: null, error: false }));
      // key by position: two links may share a label across arch edits.
      this.state.quickLinks = quicklinks
        .filter((link) => readableModels.has(link.model))
        .map((link, index) => ({ ...link, key: `${index}:${link.model}` }));
    } catch (error) {
      console.error("[DashboardView] failed to load dashboard", error);
      this.state.metrics = [];
      this.state.quickLinks = [];
      return;
    }

    await this.loadMetrics();
  }

  async loadMetrics() {
    await Promise.all(
      this.state.metrics.map(async (metric) => {
        try {
          const response = await createModelRpc(metric.model).call("search_count", [], { domain: metric.domain });
          metric.value = Number(response?.result ?? 0);
        } catch {
          metric.error = true;
        } finally {
          metric.loading = false;
        }
      })
    );
  }
}
