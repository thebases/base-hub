// ViewSwitcher — port of frontend-admin's control-panel/view-switcher.tsx.
// "form" is never shown as a switcher tab (a record is opened, not switched
// into); the group renders nothing when there is nothing to switch between.
import { Component, loadTemplate, xml } from "nodexui/owl";
import { Button } from "nodexui/component";


const template = await loadTemplate(new URL("./ViewSwitcher.xml", import.meta.url));

// Lucide icon per view mode; any mode not listed falls back to "square-menu".
const MODE_ICONS = {
  list: "list",
  kanban: "kanban-square",
  hierarchy: "folder-tree",
  gantt: "gantt-chart-square",
  cohort: "chart-column",
  calendar: "calendar",
  grid: "grid-3x3",
  map: "map-pin",
};

// Display label per view mode (aria-label of the icon-only switcher buttons); unknown modes fall back to the raw name.
const MODE_LABELS = {
  list: t("List"),
  kanban: t("Kanban"),
  hierarchy: t("Hierarchy"),
  gantt: t("Gantt"),
  cohort: t("Cohort"),
  calendar: t("Calendar"),
  grid: t("Grid"),
  map: t("Map"),
};

export class ViewSwitcher extends Component {
  static template = xml({ raw: [template] });
  static props = { modes: Array, active: String, onChange: Function };
  static components = { Button };

  get visibleModes() {
    return this.props.modes.filter((mode) => mode !== "form");
  }

  // Maps a view mode name to its icon name.
  iconFor(mode) {
    return MODE_ICONS[mode] ?? "square-menu";
  }

  labelFor(mode) {
    return MODE_LABELS[mode] ?? mode;
  }

  onClickMode(mode) {
    this.props.onChange(mode);
  }
}
