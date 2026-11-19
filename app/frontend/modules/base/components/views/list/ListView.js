import { Component, onWillStart, onWillUpdateProps, useState, xml } from "nodexui/owl";

import { formatLocalDateTime } from "@/services/server-time.js";
import { NoContent } from "@/modules/base/components/NoContent.js";
import { loadI18nTerms, t as serverTerm } from "@/stores/i18n-service.js";
import { Badge, Button, Card, CardBody, DataTable, Field, FieldLabel, Icon, Input } from "nodexui/component";
import { evaluateArchBoolean } from "@/modules/base/components/views/form/form-arch-eval.js";
import { isFormWidget, resolveFormWidget } from "@/modules/base/components/views/form/form-widget-registry.js";

// formatCellValue renders one record field for the list table. Relation
// fields (many2one) arrive as [id, name] tuples from search_read; everything
// else is a plain scalar. Booleans/undefined render as an em dash so a real
// empty string vs. "no value" stay visually distinct.
function formatCellValue(value) {
  // Boolean fields must be checked before the null/undefined/empty em-dash
  // fallback below — `false` is a real, renderable value ("No"), not an
  // absent one, so testing `value === false` first would swallow it.
  if (typeof value === "boolean") return value ? t("Yes") : t("No");
  if (value === null || value === undefined || value === "") return "—";
  if (Array.isArray(value) && value.length === 2 && typeof value[1] !== "object") return String(value[1]);
  // many2many: a list of [id, name] tuples reads as its names, not raw JSON.
  if (isTupleList(value)) return value.map((item) => item[1]).join(", ");
  // json/map columns arrive as objects or arrays; String() would give "[object Object]".
  if (typeof value === "object") return stringifyJSON(value);
  return String(value);
}

// isTupleList reports a non-empty many2many value: an array of [id, name] tuples.
function isTupleList(value) {
  return Array.isArray(value) && value.length > 0 && value.every((item) => Array.isArray(item) && item.length === 2 && typeof item[1] !== "object");
}

// stringifyJSON serialises a json value (indent 0 = one line); unserialisable values fall back to String().
function stringifyJSON(value, indent = 0) {
  try {
    return JSON.stringify(value, null, indent || undefined) ?? String(value);
  } catch {
    return String(value);
  }
}

// Variants tried in this order, so a row matching several decoration-*
// expressions gets the most severe colour (BaseUI Badge variant names).
const DECORATION_ORDER = ["danger", "warning", "success", "info", "primary", "muted"];

// readCellValue returns the raw value a column shows: the field itself, or —
// with a `path` arch attribute — a dotted sub-key of a json field (e.g.
// metadata + path="batteryDetail.health"). json may arrive as an object or a
// JSON string; anything unparsable or missing resolves to undefined.
function readCellValue(row, column) {
  const value = row[column.name];
  if (!column.path) return value;
  let current = value;
  if (typeof current === "string") {
    try {
      current = JSON.parse(current);
    } catch {
      return undefined;
    }
  }
  for (const key of column.path.split(".")) {
    if (current === null || typeof current !== "object") return undefined;
    current = current[key];
  }
  return current;
}

// formatWidgetValue applies the column's display widget on top of the plain
// formatter; empty values always stay an em dash whatever the widget.
function formatWidgetValue(value, widget, fieldType) {
  if (typeof value !== "boolean" && (value === null || value === undefined || value === "")) return "—";
  // The backend sends `false` for an empty relation/date/text, which must not render as a "No" boolean.
  if (value === false && fieldType && fieldType !== "boolean") return "—";
  if (widget === "percentage" && Number.isFinite(Number(value))) return `${Number(value)}%`;
  if (widget === "datetime_short") return formatLocalDateTime(value, true);
  // Every datetime-typed column (and widget="datetime") shows the viewer's local time.
  if (widget === "datetime" || (fieldType === "datetime" && !widget)) return formatLocalDateTime(value);
  return formatCellValue(value);
}

// displayValue maps a selection field's stored key to its label (list
// behaviour), unless the column is a json `path` or opts out via raw_value.
function displayValue(raw, column) {
  if (column.path || column.rawValue || !Array.isArray(column.selection)) return raw;
  return column.selection.find((option) => option.value === raw)?.label ?? raw;
}

// badgeVariant evaluates the column's decoration-* expressions against the
// row (with `value` bound to the cell value, so path-based columns can test
// it directly) and returns the first matching variant, else neutral grey.
function badgeVariant(decorations, record, value) {
  for (const variant of DECORATION_ORDER) {
    const expression = decorations?.[variant];
    if (expression && evaluateArchBoolean(expression, { record, context: { value } }, false)) return variant;
  }
  return "secondary";
}

// RecordCell renders one column's value and opens the record on click,
// standing in for the old `<tr t-on-click="...openRecord">` since DataTable
// owns row markup and only exposes per-column custom cell renderers.
class RecordCell extends Component {
  static template = xml`
    <span class="basehub-modelview-row-cell" t-on-click="() => props.onOpenRecord(props.record)">
      <t t-esc="props.value"/>
    </span>
  `;
  static props = { record: Object, value: { type: String, optional: true }, onOpenRecord: Function };
}

// BadgeCell is RecordCell for widget="badge" columns: same click-to-open
// behaviour, value shown as a BaseUI Badge; an empty value stays a plain
// em dash so "never reported" doesn't look like a real state.
class BadgeCell extends Component {
  static template = xml`
    <span class="basehub-modelview-row-cell" t-on-click="() => props.onOpenRecord(props.record)">
      <t t-if="props.value === '—'"><t t-esc="props.value"/></t>
      <Badge t-else="" variant="props.variant" label="props.value"/>
    </span>
  `;
  static components = { Badge };
  static props = {
    record: Object,
    value: { type: String, optional: true },
    variant: { type: String, optional: true },
    onOpenRecord: Function,
  };
}

// TagsCell is RecordCell for many2many columns: one BaseUI Badge per related
// record (names only), wrapping inside the cell; an empty relation is an em dash.
class TagsCell extends Component {
  static template = xml`
    <span class="basehub-modelview-row-cell d-inline-flex flex-wrap gap-1" t-on-click="() => props.onOpenRecord(props.record)">
      <t t-if="props.names.length === 0">—</t>
      <Badge t-foreach="props.names" t-as="name" t-key="name_index" variant="'secondary'" label="name"/>
    </span>
  `;
  static components = { Badge };
  static props = { record: Object, names: Array, onOpenRecord: Function };
}

// JsonCell is RecordCell for widget="json" columns: the full payload,
// pretty-printed in a monospace block (bounded height, scrolls when long).
// A JSON string value is parsed first so stored text is indented too.
class JsonCell extends Component {
  static template = xml`
    <span class="basehub-modelview-row-cell" t-on-click="() => props.onOpenRecord(props.record)">
      <t t-if="props.value === '—'"><t t-esc="props.value"/></t>
      <pre t-else="" class="mb-0 small" style="white-space: pre-wrap; word-break: break-all; max-height: 16rem; overflow: auto;"><t t-esc="props.value"/></pre>
    </span>
  `;
  static props = { record: Object, value: { type: String, optional: true }, onOpenRecord: Function };
}

// WidgetCell mounts a registered widget (form-widget-registry.js) as a list
// cell, read-only on the row, keeping the click-to-open behaviour. The import
// is lazy, so the cell stays empty until the widget's module has loaded.
class WidgetCell extends Component {
  static template = xml`
    <span class="basehub-modelview-row-cell" t-on-click="() => props.onOpenRecord(props.record)">
      <t t-if="state.component">
        <t t-component="state.component" record="props.record" node="props.node" readonly="true"
           ctx="{ values: props.record, isReadonly: true, isCreateMode: false, isListCell: true }"/>
      </t>
    </span>
  `;
  static props = { record: Object, node: Object, onOpenRecord: Function };

  setup() {
    this.state = useState({ component: null });
    onWillStart(async () => {
      this.state.component = await resolveFormWidget(this.props.node.name);
    });
  }
}

// RowActionsCell mirrors the previous inline-button row actions: each
// action's `invisible` arch expression is evaluated per-record (not just
// parsed once at the view level), matching list-row-actions.tsx. An action
// with an `icon` attribute renders icon-only, its label kept as tooltip and
// aria-label, and its arch `class` (e.g. text-danger) tints the icon.
// Keyed by position: type="open" actions may share no `name`.
class RowActionsCell extends Component {
  static template = xml`
    <t t-foreach="visibleActions" t-as="action" t-key="action_index">
      <Button t-if="action.icon" variant="'ghost'" size="'icon-sm'" icon="action.icon"
              className="action.className or ''" ariaLabel="action.label" title="action.label"
              onClick="() => props.onRowAction(action, props.record)"/>
      <Button t-else="" variant="'ghost'" size="'sm'" onClick="() => props.onRowAction(action, props.record)">
        <t t-esc="action.label"/>
      </Button>
    </t>
  `;
  static components = { Button };
  static props = { record: Object, actions: Array, onRowAction: Function };

  get visibleActions() {
    return this.props.actions.filter(
      (action) => !evaluateArchBoolean(action.invisible, { record: this.props.record }, false),
    );
  }
}

// ListDashboard renders the arch <dashboard> stat tiles (see list-dashboard.js)
// above the table in a responsive grid (1/2/3/4 cols, auto-wrap): a tinted icon square, the value, and its label.
class ListDashboard extends Component {
  static template = xml`
    <div id="list-dash" class="row row-cols-1 row-cols-sm-2 row-cols-md-3 row-cols-lg-4 g-3 mb-3">
      <div class="col" t-foreach="props.stats" t-as="stat" t-key="stat.key">
        <Card className="'h-100'">
          <CardBody className="'d-flex align-items-center gap-3 px-4 py-4'">
            <span t-attf-class="d-inline-flex flex-shrink-0 align-items-center justify-content-center rounded-3 bg-{{stat.color}}-subtle text-{{stat.color}}"
                  style="width: 3.5rem; height: 3.5rem;">
              <Icon name="stat.icon"/>
            </span>
            <div style="min-width: 0;">
              <div class="fs-3 fw-bold lh-1 mb-1" t-esc="stat.value"/>
              <div class="text-body-secondary text-truncate" t-esc="stat.label"/>
            </div>
          </CardBody>
        </Card>
      </div>
    </div>
  `;
  static components = { Card, CardBody, Icon };
  static props = { stats: Array };
}

const CARD_MAX_FIELDS = 5;

// Same breakpoint as ModelView.css's phone card layout.
const PHONE_QUERY = "(max-width: 767.98px)";

export class ListView extends Component {
  static template = xml`
    <main>
      <ListDashboard t-if="props.dashboard?.length" stats="props.dashboard"/>
      <t t-if="props.records.length === 0">
        <NoContent label="t('No records found.')"/>
      </t>
      <t t-else="">
        <div class="basehub-list-cards">
          <div t-foreach="mobileRecords" t-as="record" t-key="record_index" class="basehub-list-card"
               t-on-click="(ev) => this.onCardClick(ev, record)">
            <t t-foreach="cardColumns(record)" t-as="column" t-key="column.key">
              <div t-if="column.key === cardTitleKey(record)" class="basehub-list-card-title" t-esc="cardValue(column, record)"/>
              <Field t-elif="column.plainText" className="'basehub-list-card-field basehub-form-field-readonly'">
                <Input label="column.label" readonly="true" value="cardValue(column, record)"/>
              </Field>
              <Field t-else="" orientation="'horizontal'" className="'basehub-list-card-field basehub-list-card-field-inline'">
                <FieldLabel t-if="column.key !== '__actions__'" t-esc="column.label"/>
                <t t-component="column.component" t-props="column.componentProps(record)"/>
              </Field>
            </t>
          </div>
        </div>
        <div class="basehub-list-table">
          <DataTable columns="tableColumns"
                     rows="props.records"
                     searchable="false"
                     sortable="false"
                     pagination="true"
                     pageSize="80"
                     selectable="false"
                     columnToggle="false"
                     emptyLabel="t('No records found.')"/>
        </div>
      </t>
    </main>
  `;
  static components = { ListDashboard, NoContent, DataTable, Field, FieldLabel, Input };

  // Phone widths show these Base UI Field cards instead of DataTable's own card layout (see ModelView.css).
  setup() {
    // Pre-fetch translations for the phone cards' Input values before they render.
    onWillStart(() => this.loadCardTerms(this.props));
    onWillUpdateProps((next) => this.loadCardTerms(next));
  }

  async loadCardTerms(props) {
    // The cards only exist below 768px; wider screens use DataTable, whose text the DOM translator handles.
    if (!window.matchMedia(PHONE_QUERY).matches) return;
    const terms = new Set();
    for (const record of props.records.slice(0, 80)) {
      for (const column of this.tableColumns) {
        if (column.plainText) terms.add(column.componentProps(record).value ?? "");
      }
    }
    terms.delete("");
    if (terms.size) await loadI18nTerms([...terms]);
  }

  // Input values are not reached by the DOM translator (text nodes only), so translate here.
  cardValue(column, record) {
    return serverTerm(column.componentProps(record).value ?? "");
  }

  // Row actions and per-cell click targets handle their own clicks; the rest of the card opens the record.
  onCardClick(ev, record) {
    if (ev.target.closest("button, .basehub-modelview-row-cell")) return;
    this.props.onOpenRecord(record);
  }

  // Phone card fields for one record: the first column is the card title (rendered without a label),
  // empty plain values are dropped, and at most CARD_MAX_FIELDS others are shown so a card stays scannable.
  cardTitleKey(record) {
    return this.tableColumns.find((column) => column.plainText && column.componentProps(record).value !== "—")?.key;
  }

  cardColumns(record) {
    const titleKey = this.cardTitleKey(record);
    const title = this.tableColumns.filter((column) => column.key === titleKey);
    const rest = this.tableColumns.filter((column) => column.key !== titleKey);
    let shown = 0;
    const kept = rest.filter((column) => {
      if (column.key === "__actions__") return true;
      if (column.plainText && column.componentProps(record).value === "—") return false;
      shown += 1;
      return shown <= CARD_MAX_FIELDS;
    });
    return [...title, ...kept];
  }

  get mobileRecords() {
    return this.props.records.slice(0, 80);
  }

  get tableColumns() {
    const columns = this.props.columns.map((column) => this.tableColumn(column));
    if (this.props.rowActions.length > 0) {
      // The <action> block's `before` field places the column; unknown or
      // absent falls back to the end of the row.
      const settings = this.props.rowActionsColumn ?? {};
      const index = columns.findIndex((column) => column.key === settings.before);
      columns.splice(index < 0 ? columns.length : index, 0, {
        key: "__actions__",
        label: settings.label || t("Actions"),
        hideable: false,
        component: RowActionsCell,
        componentProps: (row) => ({
          record: row,
          actions: this.props.rowActions,
          onRowAction: this.props.onRowAction,
        }),
      });
    }
    return columns;
  }

  // tableColumn maps one arch column to a DataTable column. A `path` column
  // gets a path-qualified key, since several columns may read sub-keys of
  // the same json field; widget="badge" swaps in BadgeCell, and a widget
  // registered in form-widget-registry.js renders through WidgetCell.
  tableColumn(column) {
    const key = column.path ? `${column.name}.${column.path}` : column.name;
    if (isFormWidget(column.widget)) {
      return {
        key,
        label: column.label,
        component: WidgetCell,
        componentProps: (row) => ({
          record: row,
          node: { name: column.widget, attrs: { ...column.attrs, field: column.name } },
          onOpenRecord: this.props.onOpenRecord,
        }),
      };
    }
    const isBadge = column.widget === "badge";
    if (column.type === "many2many" && !column.widget && !column.path) {
      return {
        key,
        label: column.label,
        plainText: false,
        component: TagsCell,
        componentProps: (row) => {
          const raw = row[column.name];
          return { record: row, names: isTupleList(raw) ? raw.map((item) => String(item[1])) : [], onOpenRecord: this.props.onOpenRecord };
        },
      };
    }
    if (column.widget === "json") {
      return {
        key,
        label: column.label,
        plainText: false,
        component: JsonCell,
        componentProps: (row) => {
          let raw = readCellValue(row, column);
          if (typeof raw === "string") {
            try {
              raw = JSON.parse(raw);
            } catch {
              // plain text stays as-is
            }
          }
          const empty = raw === null || raw === undefined || raw === "" || raw === false;
          const value = empty ? "—" : typeof raw === "object" ? stringifyJSON(raw, 2) : String(raw);
          return { record: row, value, onOpenRecord: this.props.onOpenRecord };
        },
      };
    }
    return {
      key,
      label: column.label,
      plainText: !isBadge,
      component: isBadge ? BadgeCell : RecordCell,
      componentProps: (row) => {
        const raw = readCellValue(row, column);
        const props = { record: row, value: formatWidgetValue(displayValue(raw, column), column.widget, column.type), onOpenRecord: this.props.onOpenRecord };
        if (isBadge) props.variant = badgeVariant(column.decorations, row, raw);
        return props;
      },
    };
  }
}
