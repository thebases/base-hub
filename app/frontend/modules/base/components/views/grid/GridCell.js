// GridCell — one editable pivot cell (a numeric input, decimal only, no
// HH:MM widget per the plan's v1 cut list). Writes back on blur/Enter through
// props.onCommit, which ModelView.writeGridCell resolves into a find-or-create
// against the underlying record for this exact row×column combination.
import { Component, loadTemplate, onWillUpdateProps, useState, xml } from "nodexui/owl";

const template = await loadTemplate(new URL("./GridCell.xml", import.meta.url));

// formatCellValue shows an empty input for a zero/unset cell rather than a
// literal "0", so an untouched cell reads as empty (matches a blank
// timesheet cell) instead of a pre-filled zero.
function formatCellValue(value) {
  return value ? String(value) : "";
}

export class GridCell extends Component {
  static template = xml({ raw: [template] });

  setup() {
    this.state = useState({ draft: formatCellValue(this.props.value), saving: false });
    // Re-sync the draft when the pivoted value changes from elsewhere (e.g.
    // after this cell's own commit triggers a full grid refresh) — skipped
    // mid-save so the in-flight edit isn't clobbered by a stale prop.
    onWillUpdateProps((nextProps) => {
      if (!this.state.saving) this.state.draft = formatCellValue(nextProps.value);
    });
  }

  onInput(ev) {
    this.state.draft = ev.target.value;
  }

  async onCommit() {
    const nextValue = this.state.draft === "" ? 0 : Number(this.state.draft);
    if (Number.isNaN(nextValue) || nextValue === (this.props.value ?? 0)) return;
    this.state.saving = true;
    try {
      await this.props.onCommit(nextValue);
    } finally {
      this.state.saving = false;
    }
  }
}
