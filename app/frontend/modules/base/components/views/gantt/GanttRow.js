// GanttRow — one timeline row (a record, or a group of records under
// default_group_by) and its positioned pills. Mirrors KanbanRecord.js's "one
// card" split, but a row can render several pills instead of one card.
import { serverTimeToDate } from "@/services/server-time.js";
import { Component, loadTemplate, useRef, xml } from "nodexui/owl";
import { resolveCalendarLabel } from "@/modules/base/components/views/base-view/calendar-utils.js";
import { dateToOffsetPx, ganttPillColor, pxPerDayForScale } from "@/modules/base/components/views/base-view/gantt-utils.js";

const template = await loadTemplate(new URL("./GanttRow.xml", import.meta.url));

export class GanttRow extends Component {
  static template = xml({ raw: [template] });

  setup() {
    this.timelineRef = useRef("timeline");
  }

  // pills positions every record in this row along the timeline; width is
  // floored to one day so a same-day start/stop record is still visible.
  get pills() {
    const { range, scale, dateStartField, dateStopField, colorField, fields } = this.props;
    return this.props.row.records.map((record) => {
      const start = serverTimeToDate(record[dateStartField]) ?? new Date(NaN);
      const stop = serverTimeToDate(record[dateStopField]) ?? new Date(NaN);
      const left = dateToOffsetPx(start, range.from, scale);
      const width = Math.max(pxPerDayForScale(scale), dateToOffsetPx(stop, range.from, scale) - left);
      return { record, left, width, label: resolveCalendarLabel(record, fields), color: ganttPillColor(record, colorField) };
    });
  }

  onPillDragStart(record, ev) {
    ev.dataTransfer?.setData("text/plain", String(record.id));
    this.props.onDragStart(record);
  }

  // A drop anywhere on the row's timeline reschedules the currently dragged
  // pill (tracked by the parent) to the dropped x position.
  onTimelineDrop(ev) {
    ev.preventDefault();
    const offsetX = ev.clientX - this.timelineRef.el.getBoundingClientRect().left;
    this.props.onDrop(this.props.row, offsetX);
  }

  // Clicking empty timeline space (not a pill) creates a record pre-filled
  // with the clicked date; clicks on a pill open that record instead (see
  // the .stop click handler on the pill itself in the template).
  onTimelineClick(ev) {
    const offsetX = ev.clientX - this.timelineRef.el.getBoundingClientRect().left;
    this.props.onEmptyClick(this.props.row, offsetX);
  }
}
