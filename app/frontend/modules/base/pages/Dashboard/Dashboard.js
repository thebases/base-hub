// Master dashboard for the authenticated shell (route "/web"): one tab per
// installed module that ships a ui.view row of type="dashboard", each tab
// body rendered by the generic DashboardView component. Replaces the earlier
// hardcoded pages/Home page — every actual workspace screen is still reached
// through the sidebar's ui.menu-driven links, which route to
// /web/:actionModel (ModelView); this page is just an at-a-glance summary
// shown before the user picks one.

import { Component, loadTemplate, onWillStart, useState, xml } from "nodexui/owl";

import { createModelRpc } from "@/services/api/jsonv2.js";
import { DashboardView } from "@/modules/base/components/views/dashboard/DashboardView.js";
import { TabContent, Tabs, TabsContainer, TabsList, TabTrigger } from "nodexui/component";

const template = await loadTemplate(new URL("./Dashboard.xml", import.meta.url));

export class Dashboard extends Component {
  static template = xml({ raw: [template] });
  static components = { DashboardView, TabContent, Tabs, TabsContainer, TabsList, TabTrigger };

  setup() {
    this.state = useState({
      dashboards: [],
      status: "loading",
    });

    onWillStart(() => this.loadDashboards());
  }

  // ui.view.list_installed_dashboards already scopes results to installed
  // modules server-side (see internal/models/ui_view.go) — no extra
  // client-side install-state filtering needed here.
  async loadDashboards() {
    try {
      const response = await createModelRpc("ui.view").call("list_installed_dashboards");
      this.state.dashboards = Array.isArray(response?.result) ? response.result : [];
      this.state.status = "ready";
    } catch {
      this.state.dashboards = [];
      this.state.status = "error";
    }
  }

  // Uncontrolled Tabs (see ArchNode.xml's identical activePageKey pattern):
  // this only seeds the initially active tab, Tabs/TabTrigger own the
  // active-tab state themselves afterward.
  get firstDashboardModule() {
    return this.state.dashboards[0]?.module ?? "";
  }
}
