// CohortView — read-only retention/churn heatmap: a button-group toolbar
// (timeline/interval/mode/measure) plus one row per cohort bucket, offset
// columns colored by retained/churned %, and a weighted-average footer row.
// Row click drills into the list view filtered to that bucket's date range
// (skipped when disable_linking is set) — see ModelView.js's openCohortBucket.
import { Component, loadTemplate, xml } from "nodexui/owl";
import { Button } from "nodexui/component";
import { NoContent } from "@/modules/base/components/NoContent.js";
import {
  COHORT_INTERVALS,
  COHORT_MODES,
  COHORT_TIMELINES,
  cohortAverageRow,
  cohortCellPercent,
  cohortHeatClass,
} from "@/modules/base/components/views/base-view/cohort-utils.js";


const template = await loadTemplate(new URL("./CohortView.xml", import.meta.url));

export class CohortView extends Component {
  static template = xml({ raw: [template] });
  static components = { Button, NoContent };

  setup() {
    this.intervals = COHORT_INTERVALS;
    this.timelines = COHORT_TIMELINES;
    this.modes = COHORT_MODES;
  }

  // optionLabel translates the interval/timeline/mode values, which the toolbar buttons show as lowercase words.
  optionLabel(value) {
    const labels = {
      day: t("day"), week: t("week"), month: t("month"), year: t("year"),
      forward: t("forward"), backward: t("backward"),
      retention: t("retention"), churn: t("churn"),
    };
    return labels[value] ?? value;
  }

  get offsets() {
    return Array.from({ length: this.props.columnCount }, (_, index) => index);
  }

  get averageRow() {
    return cohortAverageRow(this.props.rows, this.props.columnCount);
  }

  cellPercent(row, offset) {
    return cohortCellPercent(row.total, row.values[offset] ?? 0, this.props.mode);
  }

  cellClass(row, offset) {
    return cohortHeatClass(this.cellPercent(row, offset));
  }

  cellLabel(row, offset) {
    const percent = this.cellPercent(row, offset);
    return percent === null ? "" : `${Math.round(percent * 100)}%`;
  }

  onClickRow(row) {
    if (this.props.disableLinking) return;
    this.props.onOpenBucket(row);
  }
}
