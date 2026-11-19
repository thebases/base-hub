// Recursive folder/leaf tree for the sidebar's "KnowLedge" section, built on
// BaseUI's pure Owl Sidebar compound (same sidebar-05-style nav as
// ../../base/components/menu-tree/MenuTree.js) — folders render as
// collapsible groups, leaves as SidebarMenuButtons. Same self-referencing-
// component pattern as MenuTree.js (xml() registers one flat template per
// call, so a component can't t-call back into itself by template name).

import { Component, loadTemplate, xml } from "nodexui/owl";
import {
  SidebarGroupCollapsible,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
} from "nodexui/component";

const template = await loadTemplate(new URL("./KnowledgeTree.xml", import.meta.url));

export class KnowledgeTree extends Component {
  static template = xml({ raw: [template] });
  static components = {
    SidebarGroupCollapsible,
    SidebarMenu,
    SidebarMenuButton,
    SidebarMenuItem,
  };

  isActive(item) {
    // Leaf hrefs come from the backend as "/web/markdown?path=..." (see
    // markdown_route.go's RoutePath), but props.currentPath (Shell.js/main.js)
    // is the router's prefix-free *pathname only* — it never carries the
    // query string. Comparing it straight against item.href (as React's
    // nav-documents.tsx does against `${pathname}${search}`) would never
    // match, so the active class would never apply. Reconstruct the
    // pathname+search actually shown in the address bar instead.
    const currentPathWithQuery = `${this.props.currentPath ?? ""}${typeof window !== "undefined" ? window.location.search : ""}`;
    return item.kind === "leaf" && (item.href === currentPathWithQuery || item.href === this.props.currentPath);
  }

  onLeafClick() {
    // KnowledgeTree is always mounted inside Shell.xml's pure Owl <Sidebar>,
    // which provides this env setter to every descendant.
    this.env.setSidebarOpen?.(false);
  }
}

KnowledgeTree.components = { ...KnowledgeTree.components, KnowledgeTree };
