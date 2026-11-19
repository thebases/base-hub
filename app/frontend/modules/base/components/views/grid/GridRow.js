// GridRow — one pivot row: its label(s) (section+row, or plain row), its
// editable cells (one per column), and its row total.
import { Component, loadTemplate, xml } from "nodexui/owl";
import { GridCell } from "./GridCell.js";
import { gridRowTotal } from "@/modules/base/components/views/base-view/grid-utils.js";

const template = await loadTemplate(new URL("./GridRow.xml", import.meta.url));

export class GridRow extends Component {
  static template = xml({ raw: [template] });
  static components = { GridCell };

  get total() {
    return gridRowTotal(this.props.matrix, this.props.row.key, this.props.columns);
  }

  cellValue(columnKey) {
    return this.props.matrix[this.props.row.key]?.[columnKey] ?? 0;
  }

  onCommitCell(columnKey, nextValue) {
    return this.props.onCommitCell(this.props.row, columnKey, nextValue);
  }
}
