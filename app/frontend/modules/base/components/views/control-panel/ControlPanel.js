// ControlPanel — Owl port of frontend-admin's
// modules/base/components/views/control-panel/control-panel.tsx. Composes
// Breadcrumbs, SearchBar, ViewSwitcher and Pager into the single toolbar
// every list/kanban/calendar mode of ModelView renders instead of the ad hoc
// header markup that used to live in ModelView.xml.
import { Component, loadTemplate, xml } from "nodexui/owl";

import { Button, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger, Icon } from "nodexui/component";

import { Breadcrumbs } from "./Breadcrumbs.js";
import { SearchBar } from "./SearchBar.js";
import { ViewSwitcher } from "./ViewSwitcher.js";
import { Pager } from "./Pager.js";


const template = await loadTemplate(new URL("./ControlPanel.xml", import.meta.url));

export class ControlPanel extends Component {
  static template = xml({ raw: [template] });
  static components = {
    Breadcrumbs,
    SearchBar,
    ViewSwitcher,
    Pager,
    Button,
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuSeparator,
    DropdownMenuTrigger,
    Icon,
  };
  static props = {
    breadcrumbs: { type: Array, optional: true },
    viewModes: { type: Array, optional: true },
    activeViewMode: { type: String, optional: true },
    onViewModeChange: { type: Function, optional: true },
    pager: { type: Object, optional: true },
    onPagerUpdate: { type: Function, optional: true },
    canCreate: { type: Boolean, optional: true },
    onClickCreate: { type: Function, optional: true },
    toolbarActions: { type: Array, optional: true },
    onToolbarAction: { type: Function, optional: true },
    onRefresh: { type: Function, optional: true },
    // Actions menu (any mode): fixed items come from fixedMenuItems (list:
    // Import/Export); menuActions ({name, label, icon?}) are appended and
    // routed through onToolbarAction.
    onImport: { type: Function, optional: true },
    onExport: { type: Function, optional: true },
    onViewInactive: { type: Function, optional: true },
    showingInactive: { type: Boolean, optional: true },
    menuActions: { type: Array, optional: true },
    searchPlaceholder: { type: String, optional: true },
    searchFields: { type: Array, optional: true },
    searchFacets: { type: Array, optional: true },
    activeSearchField: { type: String, optional: true },
    onSearchFieldChange: { type: Function, optional: true },
    onSearch: { type: Function, optional: true },
    onRemoveFacet: { type: Function, optional: true },
    onClearSearch: { type: Function, optional: true },
  };
  static defaultProps = {
    breadcrumbs: [],
    viewModes: [],
    activeViewMode: "list",
    canCreate: false,
    toolbarActions: [],
    menuActions: [],
    searchFields: [],
    searchFacets: [],
  };

  get showSearchBar() {
    // Form mode has no record list to search, so SearchBar hides entirely
    // there regardless of the props below — ModelView.js unconditionally
    // wires those props for every mode, so the gate has to live here.
    if (this.props.activeViewMode === "form") {
      return false;
    }
    // Mirrors control-panel.tsx's showSearchBar — gated on placeholder/handler
    // presence (all unconditionally passed by ModelView.js) or existing
    // facets, NOT on whether searchFields happens to be non-empty (e.g.
    // calendar mode has no columns to derive fields from, but still wires a
    // working free-text search against the default "name" field).
    return (
      Boolean(this.props.searchPlaceholder) ||
      Boolean(this.props.onSearch) ||
      Boolean(this.props.onRemoveFacet) ||
      Boolean(this.props.onClearSearch) ||
      this.props.searchFacets.length > 0
    );
  }

  // fixedMenuItems are the mode's built-in Actions entries. Import/Export act
  // on the list's model and current search domain, so they exist in list mode
  // only, and each one only when its handler is wired.
  get fixedMenuItems() {
    if (this.props.activeViewMode !== "list") {
      return [];
    }
    const items = [];
    if (this.props.onImport) {
      items.push({ name: "import", label: t("Import"), icon: "upload", onSelect: () => this.props.onImport() });
    }
    if (this.props.onExport) {
      items.push({ name: "export", label: t("Export"), icon: "download", onSelect: () => this.props.onExport() });
    }
    if (this.props.onViewInactive) {
      items.push({
        name: "view_inactive",
        label: this.props.showingInactive ? t("View active") : t("View inactive"),
        icon: this.props.showingInactive ? "eye" : "eye-off",
        onSelect: () => this.props.onViewInactive(),
      });
    }
    return items;
  }

  // showActionsMenu: the dropdown renders in any view mode, but only when it
  // has at least one option (fixed items + bound menuActions).
  get showActionsMenu() {
    return this.fixedMenuItems.length + this.props.menuActions.length > 0;
  }

  onClickCreate() {
    this.props.onClickCreate?.();
  }

  onClickToolbarAction(action) {
    this.props.onToolbarAction?.(action);
  }

  onClickRefresh() {
    this.props.onRefresh?.();
  }
}
