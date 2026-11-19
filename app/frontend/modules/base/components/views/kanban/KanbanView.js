import { Component, loadTemplate, onMounted, onPatched, useRef, useState, xml } from "nodexui/owl";

import { groupKey } from "@/modules/base/components/views/base-view/kanban-utils.js";
import { KanbanRecord } from "./KanbanRecord.js";
import { NoContent } from "@/modules/base/components/NoContent.js";
import { Badge, Button } from "nodexui/component";


const template = await loadTemplate(new URL("./KanbanView.xml", import.meta.url));

// Only these grouped-field types can be rewritten by a card drop — same
// whitelist as React's MOVABLE_RECORD_TYPES (legacy DynamicGroupList).
const MOVABLE_RECORD_TYPES = new Set(["char", "boolean", "integer", "selection", "many2one"]);

export class KanbanView extends Component {
  static template = xml({ raw: [template] });

  setup() {
    this.root = useRef("root");
    this.state = useState({ folded: {} });
    this.dragging = null;
    this.draggingColumn = null;
    onMounted(() => window.thebase?.mountAll(this.root.el));
    onPatched(() => window.thebase?.mountAll(this.root.el));
  }

  get fieldMap() {
    return Object.fromEntries(this.props.fields.map((field) => [field.name, field]));
  }

  get groupByFieldDef() {
    return this.props.fields.find((field) => field.name === this.props.groupByField);
  }

  get canMoveRecords() {
    const def = this.groupByFieldDef;
    return Boolean(def && !def.readonly && MOVABLE_RECORD_TYPES.has(def.type) && this.props.onMoveRecord);
  }

  // Columns come from three sources in priority order — the backend read_group
  // rows the shell passes in, the grouped field's own selection options, then
  // any leftover value seen on a loaded record.
  get columns() {
    const groupByField = this.props.groupByField;
    if (!groupByField) return [];
    const byId = new Map();
    for (const column of this.props.columns ?? []) byId.set(String(column.id), column);
    for (const option of this.groupByFieldDef?.options ?? []) {
      if (!byId.has(String(option.value))) byId.set(String(option.value), { id: option.value, label: option.label });
    }
    for (const record of this.props.records) {
      const id = groupKey(record[groupByField]);
      if (!id || byId.has(id)) continue;
      byId.set(id, { id, label: this.recordGroupLabel(record) });
    }
    return Array.from(byId.values());
  }

  recordGroupLabel(record) {
    const raw = record[this.props.groupByField];
    if (Array.isArray(raw) && raw.length === 2) return String(raw[1]);
    return groupKey(raw);
  }

  get isGrouped() {
    return Boolean(this.props.groupByField) && this.columns.length > 0;
  }

  recordsInColumn(column) {
    return this.props.records.filter((record) => groupKey(record[this.props.groupByField]) === String(column.id));
  }

  remainingIn(column) {
    return this.props.columnRemainingCounts?.[String(column.id)] ?? 0;
  }

  isFolded(column) {
    return this.state.folded[String(column.id)] ?? Boolean(column.folded);
  }

  // Replace the whole map rather than adding a key in place: Owl's reactivity
  // does not re-render on the *first* write to a previously-absent key.
  toggleFold(column) {
    this.state.folded = { ...this.state.folded, [String(column.id)]: !this.isFolded(column) };
  }

  onDragStart(record) {
    this.dragging = record;
  }

  // Dropping on empty column space appends to that group; dropping on a card
  // re-inserts before it, matching React's dnd-kit collision handling.
  onColumnDragStart(column) {
    this.draggingColumn = column;
  }

  // A column header drag reorders the board: dropping on another column puts
  // the dragged one before it. Card drags and column drags share the column's
  // drop target, so the active column drag is checked first.
  dropColumnOn(target) {
    const column = this.draggingColumn;
    this.draggingColumn = null;
    if (!column || String(column.id) === String(target.id)) return;
    void this.props.onMoveColumn(column, target);
  }

  dropOnColumn(column) {
    if (this.draggingColumn) return this.dropColumnOn(column);
    const record = this.dragging;
    this.dragging = null;
    if (!record || !this.canMoveRecords) return;
    void this.props.onMoveRecord(record, column, undefined);
  }

  dropOnRecord(column, beforeRecord) {
    const record = this.dragging;
    this.dragging = null;
    if (!record || !this.canMoveRecords) return;
    if (String(beforeRecord.id) === String(record.id)) return;
    void this.props.onMoveRecord(record, column, beforeRecord);
  }
}

KanbanView.components = { KanbanRecord, NoContent, Badge, Button };