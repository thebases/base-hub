import { Component, loadTemplate, onMounted, useState, xml } from "nodexui/owl";
import { Alert, Button, Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, Field, Input, FieldLabel, NativeSelect } from "nodexui/component";

import { createCalendarRecurrence } from "@/services/api/jsonv2.js";
import {
  addDays,
  calendarOccurrenceDate,
  calendarRangeFor,
  resolveCalendarLabel,
  sameDay,
  startOfDay,
  startOfMonth,
  startOfWeek,
} from "@/modules/base/components/views/base-view/calendar-utils.js";


const template = await loadTemplate(new URL("./CalendarView.xml", import.meta.url));

const MODE_OPTIONS = [
  { value: "day", label: t("Day") },
  { value: "week", label: t("Week") },
  { value: "month", label: t("Month") },
  { value: "year", label: t("Year") },
  { value: "schedule", label: t("Schedule") },
];
const WEEKDAY_LABELS = [t("Sun"), t("Mon"), t("Tue"), t("Wed"), t("Thu"), t("Fri"), t("Sat")];
const FREQUENCY_OPTIONS = [
  { value: "daily", label: t("Daily") },
  { value: "weekly", label: t("Weekly") },
  { value: "biweekly", label: t("Every 2 weeks") },
  { value: "monthly", label: t("Monthly") },
  { value: "yearly", label: t("Yearly") },
  { value: "weekday", label: t("Specific day(s) of week") },
  { value: "monthday", label: t("Specific day of month") },
];
const HOUR_ROW_HEIGHT_PX = 48;
const MONTH_CELL_VISIBLE_LIMIT = 3;

// CalendarView is a fully custom Owl event grid. BaseUI's `calendar` component
// is only a month date-picker and rewrites its host's innerHTML, which would
// fight Owl's patching — it is deliberately not used here; BaseUI contributes
// the pure-Owl `Dialog` (reschedule scope + recurrence) and form primitives.
export class CalendarView extends Component {
  static template = xml({ raw: [template] });
  static components = { Alert, Button, Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, Field, Input, FieldLabel, NativeSelect };

  setup() {
    this.modeOptions = MODE_OPTIONS;
    this.weekdayLabels = WEEKDAY_LABELS;
    this.frequencyOptions = FREQUENCY_OPTIONS;
    this.hourRowHeight = HOUR_ROW_HEIGHT_PX;
    this.hours = Array.from({ length: 24 }, (_, hour) => hour);
    this.dragged = null;
    this.pendingDrag = null;
    this.recurrenceTarget = null;

    this.state = useState({
      mode: "month",
      currentMs: startOfDay(new Date()).getTime(),
      recurrence: this.emptyRecurrenceForm(),
      scopeDialogOpen: false,
      recurrenceDialogOpen: false,
    });

    // The first range fetch must run in onMounted, not onWillStart: it writes
    // parent state, and a parent re-render while this child is still starting
    // makes Owl discard and re-create the child — an infinite fetch loop.
    onMounted(() => void this.emitRange());
  }

  emptyRecurrenceForm() {
    return { frequency: "weekly", interval: 1, weekdays: [], monthday: 1, startDate: "", endDate: "", error: "", submitting: false };
  }

  get currentDate() {
    return new Date(this.state.currentMs);
  }

  get range() {
    return calendarRangeFor(this.state.mode, this.currentDate);
  }

  get headerLabel() {
    const current = this.currentDate;
    if (this.state.mode === "year") return String(current.getFullYear());
    if (this.state.mode === "day") return current.toLocaleDateString(undefined, { dateStyle: "full" });
    return current.toLocaleDateString(undefined, { month: "long", year: "numeric" });
  }

  get isDraggable() {
    return !this.props.anchorField.isCreatedAtFallback;
  }

  emitRange() {
    const { from, to } = this.range;
    return this.props.onRangeChange(from, to, this.state.mode);
  }

  // NativeSelect's onChange passes the selected value directly (not a raw DOM event).
  setMode(value) {
    this.state.mode = value;
    void this.emitRange();
  }

  goToday() {
    this.state.currentMs = startOfDay(new Date()).getTime();
    void this.emitRange();
  }

  navigate(direction) {
    const prev = this.currentDate;
    const mode = this.state.mode;
    let next;
    if (mode === "day") next = addDays(prev, direction);
    else if (mode === "week") next = addDays(prev, 7 * direction);
    else if (mode === "year") next = new Date(prev.getFullYear() + direction, prev.getMonth(), 1);
    else if (mode === "schedule") next = addDays(prev, 30 * direction);
    else next = new Date(prev.getFullYear(), prev.getMonth() + direction, 1);
    this.state.currentMs = next.getTime();
    void this.emitRange();
  }

  openDay(dayMs) {
    this.state.currentMs = dayMs;
    this.state.mode = "day";
    void this.emitRange();
  }

  // eventLabel mirrors React's "#<id> · <label> · <amount>" chip caption.
  eventLabel(occurrence) {
    const label = resolveCalendarLabel(occurrence, this.props.fields);
    const amount = this.props.amountField ? occurrence[this.props.amountField] : undefined;
    return amount !== undefined && amount !== null && amount !== "" ? `#${occurrence.id} · ${label} · ${amount}` : `#${occurrence.id} · ${label}`;
  }

  occurrencesByDayKey() {
    const map = new Map();
    for (const occurrence of this.props.occurrences) {
      const date = calendarOccurrenceDate(occurrence, this.props.anchorField.name);
      if (!date) continue;
      const key = startOfDay(date).toDateString();
      const bucket = map.get(key) ?? [];
      bucket.push({ occurrence, date, label: this.eventLabel(occurrence) });
      map.set(key, bucket);
    }
    return map;
  }

  get monthCells() {
    const current = this.currentDate;
    const gridStart = startOfWeek(startOfMonth(current));
    const byDay = this.occurrencesByDayKey();
    const today = new Date();
    return Array.from({ length: 42 }, (_, index) => {
      const day = addDays(gridStart, index);
      const entries = byDay.get(day.toDateString()) ?? [];
      return {
        key: day.toISOString(),
        ms: day.getTime(),
        dayNumber: day.getDate(),
        otherMonth: day.getMonth() !== current.getMonth(),
        isToday: sameDay(day, today),
        visible: entries.slice(0, MONTH_CELL_VISIBLE_LIMIT),
        overflow: Math.max(0, entries.length - MONTH_CELL_VISIBLE_LIMIT),
      };
    });
  }

  get timeGridDays() {
    const current = this.currentDate;
    const days = this.state.mode === "day" ? [startOfDay(current)] : Array.from({ length: 7 }, (_, index) => addDays(startOfWeek(current), index));
    const byDay = this.occurrencesByDayKey();
    return days.map((day) => ({
      key: day.toISOString(),
      ms: day.getTime(),
      label: day.toLocaleDateString(undefined, { weekday: "short", day: "numeric" }),
      // A date-kind anchor has no clock time, so every chip pins to the top row.
      events: (byDay.get(day.toDateString()) ?? []).map((entry) => ({
        ...entry,
        top: this.props.anchorField.kind === "datetime" ? (entry.date.getHours() + entry.date.getMinutes() / 60) * HOUR_ROW_HEIGHT_PX : 0,
      })),
    }));
  }

  get yearMonths() {
    const year = this.currentDate.getFullYear();
    const counts = new Map();
    for (const occurrence of this.props.occurrences) {
      const date = calendarOccurrenceDate(occurrence, this.props.anchorField.name);
      if (!date) continue;
      const key = startOfDay(date).toDateString();
      counts.set(key, (counts.get(key) ?? 0) + 1);
    }
    return Array.from({ length: 12 }, (_, month) => {
      const monthStart = new Date(year, month, 1);
      const gridStart = startOfWeek(monthStart);
      return {
        key: `${year}-${month}`,
        label: monthStart.toLocaleDateString(undefined, { month: "long" }),
        days: Array.from({ length: 42 }, (_, index) => {
          const day = addDays(gridStart, index);
          return {
            key: day.toISOString(),
            ms: day.getTime(),
            dayNumber: day.getDate(),
            otherMonth: day.getMonth() !== month,
            count: counts.get(day.toDateString()) ?? 0,
          };
        }),
      };
    });
  }

  get scheduleGroups() {
    const withDates = this.props.occurrences
      .map((occurrence) => ({ occurrence, date: calendarOccurrenceDate(occurrence, this.props.anchorField.name) }))
      .filter((entry) => entry.date !== null)
      .sort((a, b) => a.date.getTime() - b.date.getTime());
    const map = new Map();
    for (const { occurrence, date } of withDates) {
      const dayStart = startOfDay(date);
      const key = dayStart.toDateString();
      const bucket = map.get(key);
      const entry = { occurrence, date, label: this.eventLabel(occurrence) };
      if (bucket) bucket.entries.push(entry);
      else map.set(key, { key, label: dayStart.toLocaleDateString(undefined, { weekday: "short", month: "short", day: "numeric" }), entries: [entry] });
    }
    return Array.from(map.values());
  }

  onChipDragStart(occurrence, ev) {
    this.dragged = occurrence;
    ev.dataTransfer?.setData("text/plain", String(occurrence.id));
  }

  dropOnDay(dayMs) {
    this.applyDrop(new Date(dayMs));
  }

  dropOnHour(dayMs, hour) {
    const target = new Date(dayMs);
    target.setHours(hour, 0, 0, 0);
    this.applyDrop(target);
  }

  // A recurring occurrence must ask which occurrences the move applies to
  // before anything is written (SRS-F-005 step 3).
  applyDrop(newDate) {
    const occurrence = this.dragged;
    this.dragged = null;
    if (!occurrence || !this.isDraggable) return;
    if (occurrence.is_recurring) {
      this.pendingDrag = { occurrence, newDate };
      this.state.scopeDialogOpen = true;
      return;
    }
    void this.props.onReschedule(occurrence, newDate, undefined);
  }

  chooseScope(scope) {
    const pending = this.pendingDrag;
    this.pendingDrag = null;
    this.state.scopeDialogOpen = false;
    if (!pending) return;
    void this.props.onReschedule(pending.occurrence, pending.newDate, scope);
  }

  openRecurrence(occurrence) {
    this.recurrenceTarget = occurrence;
    const anchor = calendarOccurrenceDate(occurrence, this.props.anchorField.name) ?? new Date();
    this.state.recurrence = { ...this.emptyRecurrenceForm(), startDate: anchor.toISOString().slice(0, 10) };
    this.state.recurrenceDialogOpen = true;
  }

  get requiresWeekday() {
    const frequency = this.state.recurrence.frequency;
    return frequency === "weekly" || frequency === "biweekly" || frequency === "weekday";
  }

  // Owl templates cannot call String(), so the numeric inputs get their text values here.
  get recurrenceIntervalText() {
    return String(this.state.recurrence.interval ?? "");
  }

  get recurrenceMonthdayText() {
    return String(this.state.recurrence.monthday ?? "");
  }

  get requiresMonthday() {
    return this.state.recurrence.frequency === "monthday";
  }

  toggleWeekday(day) {
    const weekdays = this.state.recurrence.weekdays;
    this.state.recurrence.weekdays = weekdays.includes(day) ? weekdays.filter((item) => item !== day) : [...weekdays, day];
  }

  setRecurrenceField(name, value) {
    this.state.recurrence[name] = value;
  }

  async submitRecurrence() {
    const form = this.state.recurrence;
    if (this.requiresWeekday && form.weekdays.length === 0) {
      form.error = t("Select at least one day of the week.");
      return;
    }
    form.submitting = true;
    form.error = "";
    try {
      await createCalendarRecurrence({
        sys_model: this.props.model,
        record_id: Number(this.recurrenceTarget?.master_record_id ?? this.recurrenceTarget?.id),
        frequency: form.frequency,
        interval: Number(form.interval) || 1,
        by_weekday: this.requiresWeekday ? form.weekdays : undefined,
        by_monthday: this.requiresMonthday ? Number(form.monthday) : undefined,
        start_date: form.startDate,
        end_date: form.endDate || null,
      });
      this.state.recurrenceDialogOpen = false;
      this.recurrenceTarget = null;
      await this.props.onCreated();
    } catch (error) {
      form.error = error instanceof Error ? error.message : t("Failed to create recurrence");
    } finally {
      form.submitting = false;
    }
  }
}