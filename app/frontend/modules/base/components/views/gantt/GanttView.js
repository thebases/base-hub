// GanttView — hand-rolled day/week/month/year timeline. No dependency
// connectors, resize, progress bars, consolidation rows or multi-select in
// v1 (see the plan's Gantt MVP cut list); clicking a pill asks the host to
// open the record's form in a popup (ModelView#openRecordDialog).
import { serverTimeToDate } from "@/services/server-time.js";
import { Component, loadTemplate, onMounted, useState, xml } from "nodexui/owl";
import { NoContent } from "@/modules/base/components/NoContent.js";
import { GanttRow } from "./GanttRow.js";
import { GanttScaleSelector } from "./GanttScaleSelector.js";
import { Button } from "nodexui/component";
import { addDays, startOfDay } from "@/modules/base/components/views/base-view/calendar-utils.js";
import { columnWidthPxForScale, dateToOffsetPx, ganttColumns, ganttRangeFor, navigationStepDays, offsetPxToDate } from "@/modules/base/components/views/base-view/gantt-utils.js";


const template = await loadTemplate(new URL("./GanttView.xml", import.meta.url));

export class GanttView extends Component {
  static template = xml({ raw: [template] });
  static components = { Button, GanttRow, GanttScaleSelector, NoContent };

  setup() {
    this.dragged = null;
    this.state = useState({ scale: this.props.defaultScale, currentMs: startOfDay(new Date()).getTime() });

    // Same reasoning as CalendarView's onMounted range emit: the first fetch
    // must not run in onWillStart, or a parent re-render while this child is
    // still starting causes Owl to discard and re-create it.
    onMounted(() => void this.emitRange());
  }

  get currentDate() {
    return new Date(this.state.currentMs);
  }

  get range() {
    return ganttRangeFor(this.state.scale, this.currentDate);
  }

  get columns() {
    return ganttColumns(this.state.scale, this.range);
  }

  get columnWidthPx() {
    return columnWidthPxForScale(this.state.scale);
  }

  // todayColumnIndex is the header column whose span contains today (-1 when
  // today is outside the window); the header cell and every row band use it.
  get todayColumnIndex() {
    const today = startOfDay(new Date()).getTime();
    const columns = this.columns;
    return columns.findIndex((col, index) => today >= col.ms && (index + 1 === columns.length || today < columns[index + 1].ms));
  }

  get timelineWidthPx() {
    return dateToOffsetPx(this.range.to, this.range.from, this.state.scale);
  }

  emitRange() {
    return this.props.onRangeChange(this.range.from, this.range.to, this.state.scale);
  }

  setScale(scale) {
    this.state.scale = scale;
    void this.emitRange();
  }

  goToday() {
    this.state.currentMs = startOfDay(new Date()).getTime();
    void this.emitRange();
  }

  navigate(direction) {
    this.state.currentMs = addDays(this.currentDate, navigationStepDays(this.state.scale) * direction).getTime();
    void this.emitRange();
  }

  setDragged(record) {
    this.dragged = record;
  }

  // Move-only reschedule (no resize in v1): the dropped x position becomes
  // the new date_start, and the record's original duration is preserved.
  onRowDrop(row, offsetX) {
    const record = this.dragged;
    this.dragged = null;
    if (!record) return;
    const originalStart = serverTimeToDate(record[this.props.dateStartField]) ?? new Date(NaN);
    const originalStop = serverTimeToDate(record[this.props.dateStopField]) ?? new Date(NaN);
    const durationDays = Math.round((originalStop.getTime() - originalStart.getTime()) / 86400000);
    const newStart = offsetPxToDate(offsetX, this.range.from, this.state.scale);
    const newStop = addDays(newStart, durationDays);
    void this.props.onReschedule(record, newStart, newStop);
  }

  // A one-day default span for a newly created record, pre-filled from the
  // clicked timeline position.
  onRowEmptyClick(row, offsetX) {
    const dateStart = offsetPxToDate(offsetX, this.range.from, this.state.scale);
    void this.props.onCreateRecord(row, dateStart, addDays(dateStart, 1));
  }
}
