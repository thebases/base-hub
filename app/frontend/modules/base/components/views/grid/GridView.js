// GridView — spreadsheet-style pivot table (row × col, e.g. employee×task by
// day) with a named-range switcher (this-week/this-month, per the arch's
// <range> children). No bar-chart footer, no inline row creation, no
// keyboard cell navigation in v1 (see the plan's Grid MVP cut list).
import { Component, loadTemplate, xml } from "nodexui/owl";
import { NoContent } from "@/modules/base/components/NoContent.js";
import { GridRow } from "./GridRow.js";
import { Button } from "nodexui/component";
import { gridColumnTotals, gridGrandTotal } from "@/modules/base/components/views/base-view/grid-utils.js";


const template = await loadTemplate(new URL("./GridView.xml", import.meta.url));

export class GridView extends Component {
  static template = xml({ raw: [template] });
  static components = { Button, GridRow, NoContent };

  get columnTotals() {
    return gridColumnTotals(this.props.matrix, this.props.rows, this.props.columns);
  }

  get grandTotal() {
    return gridGrandTotal(this.props.matrix, this.props.rows, this.props.columns);
  }

  setRange(rangeName) {
    this.props.onRangeChange(rangeName);
  }
}
