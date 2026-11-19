// GanttScaleSelector — day/week/month/year toggle, mirrors ViewSwitcher.js's
// simplicity (a small button group over a fixed option list).
import { Component, loadTemplate, xml } from "nodexui/owl";
import { Button } from "nodexui/component";
import { GANTT_SCALES } from "@/modules/base/components/views/base-view/gantt-utils.js";


const template = await loadTemplate(new URL("./GanttScaleSelector.xml", import.meta.url));

export class GanttScaleSelector extends Component {
  static template = xml({ raw: [template] });
  static props = { scale: String, onChange: Function };
  static components = { Button };

  setup() {
    this.scales = GANTT_SCALES;
  }

  // scaleLabel translates the scale values, which the buttons show as lowercase words.
  scaleLabel(scale) {
    const labels = { day: t("day"), week: t("week"), month: t("month"), year: t("year") };
    return labels[scale] ?? scale;
  }

  onClickScale(scale) {
    this.props.onChange(scale);
  }
}
