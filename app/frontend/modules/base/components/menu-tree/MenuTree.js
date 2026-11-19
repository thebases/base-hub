// Recursive ui.menu tree renderer, built on BaseUI's pure Owl Sidebar
// compound (SidebarGroupCollapsible/SidebarMenu/SidebarMenuItem/
// SidebarMenuButton/SidebarMenuSub) — see Shell.xml's sidebar-05-style nav.
// `xml()` (used by every page in this tree) registers exactly one flat
// template per call, so a `t-call` back into a second `t-name` in the same
// raw string doesn't resolve at runtime ("Missing template") — a genuine
// self-referencing component is the pattern Owl expects for recursive trees
// instead.

import { Component, loadTemplate, useState, xml } from "nodexui/owl";
import {
  Icon,
  SidebarGroupCollapsible,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarMenuSub,
} from "nodexui/component";

// Where a menu entry links to: its window action's model page, else its own url.
function menuHref(item) {
  if (item.windowAction) return `/web/${item.windowAction.res_model}`;
  return item.url && item.url !== "#" ? item.url : "#";
}

// True when the current page lives somewhere under this menu entry, so its group starts expanded.
function hasActiveDescendant(item, currentPath) {
  return (item.children ?? []).some(
    (child) => (menuHref(child) !== "#" && menuHref(child) === currentPath) || hasActiveDescendant(child, currentPath),
  );
}

const template = await loadTemplate(new URL("./MenuTree.xml", import.meta.url));

export class MenuTree extends Component {
  static template = xml({ raw: [template] });
  static components = {
    Icon,
    SidebarGroupCollapsible,
    SidebarMenu,
    SidebarMenuButton,
    SidebarMenuItem,
    SidebarMenuSub,
  };

  setup() {
    this.state = useState({ open: {} });
  }

  itemHref(item) {
    return menuHref(item);
  }

  // A nested group is open when the user opened it, when a search is active,
  // or (until the user toggles it) when it contains the page being viewed.
  isOpen(item) {
    if (this.props.expandAll) return true;
    return this.state.open[item.id] ?? hasActiveDescendant(item, this.props.currentPath);
  }

  // Flips a nested group open/closed without navigating or closing the mobile drawer.
  toggle(item) {
    this.state.open = { ...this.state.open, [item.id]: !this.isOpen(item) };
  }

  isActive(item) {
    const href = this.itemHref(item);
    return href !== "#" && this.props.currentPath === href;
  }

  onMenuClick(ev, item) {
    // Close the mobile drawer on navigation; main.js's document-level click
    // delegate already intercepts the <a href> itself for client-side routing.
    if (this.itemHref(item) === "#") {
      ev.preventDefault();
    }
    // MenuTree is always mounted inside Shell.xml's pure Owl <Sidebar>, which
    // provides this env setter to every descendant.
    this.env.setSidebarOpen?.(false);
  }
}

MenuTree.components = { ...MenuTree.components, MenuTree };
