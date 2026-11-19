import { Component, loadTemplate, onMounted, onPatched, onWillStart, onWillUnmount, onWillUpdateProps, useRef, useState, xml } from "nodexui/owl";

import { busService } from "@/modules/base-bus/index.js";
import {
  createModelRecord,
  createModelRpc,
  createRemoteOnchangeHandler,
  deleteModelRecords,
  duplicateModelRecord,
  executeModelMethod,
  executeObjectButtonMethod,
  executeLongRunningObjectMethod,
  executeObjectMethodWithArgs,
  extractArchButtonXMLID,
  fetchCalendarOccurrences,
  fetchBoundModelActions,
  fetchClientActionByXMLID,
  fetchModelFields,
  fetchModelFormRecord,
  fetchKanbanColumnOrder,
  fetchModelKanbanRecords,
  fetchModelListRecords,
  fieldDefsFromFields,
  fetchViewMetadataForType,
  fetchWindowAction,
  fetchWindowActionByXMLID,
  listFieldDefsFromView,
  listRowActionsColumnFromView,
  listRowActionsFromView,
  listCreateWidgetFromView,
  listToolbarActionsFromView,
  replaceActionContextPlaceholders,
  rescheduleCalendarOccurrence,
  viewModesFromWindowAction,
  writeModelRecord,
} from "@/services/api/jsonv2.js";
import {
  appendUniqueRecords,
  countRecordsByGroup,
  groupKey,
  moveKanbanRecordSnapshot,
  remainingCountsByColumn,
  resolveKanbanCardLayout,
  resolveKanbanGroupByField,
} from "@/modules/base/components/views/base-view/kanban-utils.js";
import { resolveFormWidget } from "@/modules/base/components/views/form/form-widget-registry.js";
import { navigate, toFullPath } from "@/router/navigation.js";
import { showToast } from "@/modules/base-bus/bus-root.js";
import { VIEW_TYPES } from "@/modules/base/components/views/view-registry.js";
import { VIEW_MOUNT_REGISTRY, resolveViewComponent } from "@/modules/base/components/views/view-mount-registry.js";
import { ControlPanel } from "@/modules/base/components/views/control-panel/ControlPanel.js";
import { fetchListDashboardStats, listDashboardFromView } from "@/modules/base/components/views/list/list-dashboard.js";
import {
  Alert,
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
  Spinner,
} from "nodexui/component";

const template = await loadTemplate(new URL("./ModelView.xml", import.meta.url));

const LIST_PAGE_SIZE = 80;

// CLOSED_WIDGET_HOST is the idle state of the widget host (list type="widget"
// row action and ui.actions.client `open_widget` with a dialog/sheet target).
const CLOSED_RECORD_DIALOG = { open: false, recordId: null, viewData: null, fields: [], record: null };
const CLOSED_WIDGET_HOST = { open: false, mode: "dialog", size: "", side: "right", heading: "", title: "", description: "", component: null, record: null, node: null, ctx: null };

// clientActionPresentation maps a ui.actions.client `target` (backend
// UIActionTarget* constants in ui_action_client.go) to how this SPA presents
// it: "new" is the alias of "dialog"; current/main/fullscreen navigate.
function clientActionPresentation(target) {
  switch (target) {
    case "dialog":
    case "new":
      return "dialog";
    case "sheet":
      return "sheet";
    case "window":
      return "window";
    default:
      return "navigate";
  }
}

// widgetHostPanelClass sizes the dialog panel from the action's params.size.
function widgetHostPanelClass(size) {
  return size ? `basehub-widget-dialog__panel basehub-widget-dialog__panel--${size}` : "basehub-widget-dialog__panel";
}

// SHEET_WIDTHS mirrors the dialog size presets for target="sheet"; BaseUI's
// SheetContent only forwards `style` to the .offcanvas root, so the width is
// set through Bootstrap's --bs-offcanvas-width variable.
const SHEET_WIDTHS = { sm: "24rem", md: "32rem", lg: "48rem", xl: "64rem" };
// Mirrors frontend-admin's "/web/<model>/<view>" forced-view route convention;
// any other trailing segment is a record id (or the literal "new"). Sourced
// from view-registry.js so a newly registered view type is automatically a
// valid forced-view segment.
const VIEW_SEGMENTS = VIEW_TYPES;

// resolveRoute turns the /web/:actionModel/:recordId? params into the view
// mode + record id the shell should render.
// buildDuplicateDatabaseNameSuggestion derives a starter value for the
// duplicate-database dialog from the current source database name — ported
// verbatim from base-view.tsx's helper of the same name.
function buildDuplicateDatabaseNameSuggestion(sourceName) {
  const trimmed = sourceName.trim();
  if (!trimmed) return "";
  if (trimmed.endsWith("-copy")) return `${trimmed}-2`;
  return `${trimmed}-copy`;
}

// parseDefaultQueryParams reads "default_<field>=<value>" query
// params (params.query is a URLSearchParams set by AppRoot.loadRoute) into a
// plain {field: value} object, or null when there are none.
function parseDefaultQueryParams(query) {
  if (!query) return null;
  const defaults = {};
  for (const [key, value] of query.entries()) {
    if (key.startsWith("default_")) defaults[key.slice("default_".length)] = value;
  }
  return Object.keys(defaults).length > 0 ? defaults : null;
}

function resolveRoute(params) {
  const segment = params.recordId;
  if (!segment) return { mode: "list", recordId: null, forced: false };
  if (VIEW_SEGMENTS.includes(segment)) return { mode: segment, recordId: null, forced: true };
  if (segment === "new") return { mode: "form", recordId: null, forced: true, isNew: true, defaults: parseDefaultQueryParams(params.query) };
  const id = Number(segment);
  return { mode: "form", recordId: Number.isFinite(id) && id > 0 ? id : null, forced: true };
}

// VIEW_UTILS lists the per-view helper modules; each is downloaded only when its view type (or the
// OAuth reveal action) is first used, instead of all of them on every /web page.
const VIEW_UTILS = {
  calendar: () => import("@/modules/base/components/views/base-view/calendar-utils.js"),
  hierarchy: () => import("@/modules/base/components/views/base-view/hierarchy-utils.js"),
  gantt: () => import("@/modules/base/components/views/base-view/gantt-utils.js"),
  map: () => import("@/modules/base/components/views/base-view/map-utils.js"),
  grid: () => import("@/modules/base/components/views/base-view/grid-utils.js"),
  cohort: () => import("@/modules/base/components/views/base-view/cohort-utils.js"),
  oauth: () => import("@/modules/base/components/views/base-view/oauth-client-reveal-result.js"),
};
const viewUtils = {};

// loadViewUtils imports (once) a view's helper module and keeps it on viewUtils for later synchronous use.
async function loadViewUtils(name) {
  viewUtils[name] ??= await VIEW_UTILS[name]();
  return viewUtils[name];
}

// registerViewComponent downloads the Owl class for a view mode (once) and exposes it to the template,
// so a page only ships the view types it actually renders; unknown modes render the list fallback.
async function registerViewComponent(mode) {
  const key = VIEW_MOUNT_REGISTRY.has(mode) ? mode : "list";
  ModelView.components[VIEW_MOUNT_REGISTRY.get(key).componentName] = await resolveViewComponent(key);
}

// loadModelDialogs fetches the rarely used dialogs (import/export, secrets, ...) once the page is idle,
// keeping them off the first-render path; their template block appears when state.dialogsReady flips.
async function loadModelDialogs(modelView) {
  await new Promise((resolve) => (window.requestIdleCallback ? window.requestIdleCallback(resolve, { timeout: 2000 }) : setTimeout(resolve, 200)));
  const { MODEL_DIALOGS } = await import("@/modules/base/components/views/base-view/model-dialogs.js");
  Object.assign(ModelView.components, MODEL_DIALOGS);
  modelView.state.dialogsReady = true;
}

export class ModelView extends Component {
  static template = xml({ raw: [template] });

  setup() {
    this.state = useState({
      actionModel: this.props.params.actionModel,
      dialogsReady: false,
      route: resolveRoute(this.props.params),
      title: "",
      loading: true,
      error: "",
      viewModes: [],
      columns: [],
      records: [],
      rowActions: [],
      rowActionsColumn: { label: t("Actions") },
      toolbarActions: [],
      createWidget: null,
      // sys.model.action entries bound to this model whose view_type covers
      // the active mode, rendered in the ControlPanel Actions dropdown.
      menuActions: [],
      listDashboard: [],
      offset: 0,
      limit: LIST_PAGE_SIZE,
      total: 0,
      domain: [],
      // showInactive switches the list to records whose `active` is false
      // (ControlPanel Actions > View inactive); hasActiveField gates that entry.
      showInactive: false,
      hasActiveField: false,
      search: { facets: [], activeField: undefined },
      formView: null,
      formFields: [],
      formRecord: null,
      kanbanFields: [],
      kanbanRecords: [],
      kanbanLayout: { titleField: "name", subFields: [] },
      kanbanGroupBy: "",
      kanbanColumns: [],
      kanbanColumnModel: "",
      kanbanRemaining: {},
      calendarFields: {},
      calendarAnchor: null,
      calendarAmountField: "",
      calendarOccurrences: [],
      hierarchyLayout: { titleField: "name", subFields: [] },
      hierarchyRoots: [],
      ganttFields: {},
      ganttDateStartField: "",
      ganttDateStopField: "",
      ganttDateKind: "datetime",
      ganttColorField: "",
      ganttDefaultScale: "week",
      ganttTitle: "",
      ganttRows: [],
      mapMarkers: [],
      mapPanelTitle: "",
      mapHideName: false,
      mapHideAddress: false,
      mapPopupFields: [],
      cohortTitle: "",
      cohortDateStartField: "",
      cohortDateStopField: "",
      cohortInterval: "day",
      cohortTimeline: "forward",
      cohortMode: "retention",
      cohortMeasure: "__count",
      cohortMeasureOptions: [],
      cohortDisableLinking: false,
      cohortColumnCount: 0,
      cohortRows: [],
      confirmMessage: "",
      // widgetDialog backs a list row type="widget" action and a
      // ui.actions.client `open_widget` action: the resolved form-widget
      // component rendered read-only over the current view, hosted in a
      // Dialog or a Sheet per `mode` (see clientActionPresentation).
      widgetDialog: { ...CLOSED_WIDGET_HOST },
      // recordDialog hosts the record's form view in a popup over a gantt
      // pill click (instead of navigating away from the timeline).
      recordDialog: { ...CLOSED_RECORD_DIALOG },
      // canDelete mirrors base-view.tsx's check_access_rights("unlink") probe
      // — the delete button is hidden client-side for users without unlink
      // rights instead of only failing server-side after a click.
      canDelete: false,
      // Phase 6 dialogs/x2many/PWA pass: the four base-view app-specific
      // dialogs (database duplicate/module-selection, user-security
      // password/OAuth-secret-reveal) — ported from base-view.tsx's
      // per-model button-name dispatch inside runArchButton below. State
      // shape matches each dialog component's `props.state` contract 1:1.
      databaseDuplicateDialog: { open: false, sourceID: null, sourceName: "", suggestedName: "" },
      databaseModuleSelectionDialog: { open: false, recordID: null, databaseName: "", enabledModules: [], availableModules: [], loadingModules: false },
      userSecurityPasswordDialog: { open: false, recordID: null, userName: "" },
      oauthClientSecretDialog: { open: false, subjectName: "", result: null },
      userApiKeyDialog: { open: false, subjectName: "", apiKey: "" },
      // busyOverlay is a non-dismissable waiting modal for long-running row
      // actions (module reinit); it is a plain overlay, not a BaseUI Dialog,
      // so Escape/backdrop clicks cannot close it before the RPC finishes.
      busyOverlay: { open: false, message: "" },
      // modelActionParamsDialog collects a sys.model.action "model" entry's
      // declared method params; `bound` is the entry Submit runs.
      modelActionParamsDialog: { open: false, title: "", description: "", params: [], bound: null },
      // ControlPanel Actions > Import/Export dialogs; imexFields is the
      // fields_get map both dialogs build columns from, loaded on open.
      importDialog: { open: false },
      exportDialog: { open: false },
      imexFields: {},
    });
    this.calendarRange = null;
    this.ganttRange = null;
    this.ganttGroupByField = "";
    this.onRemoteValueChange = null;
    this.root = useRef("modelViewRoot");
    this.confirmResolver = null;

    onWillStart(() => {
      void this.checkDeleteAccess();
      return this.load();
    });
    onMounted(() => {
      this.mountBaseUI();
      void loadModelDialogs(this);
    });
    // Stop any module-reinit poll/bus subscription left running when the page unmounts.
    this.reinitCancels = new Set();
    onWillUnmount(() => this.reinitCancels.forEach((cancel) => cancel()));
    onPatched(() => this.mountBaseUI());
    onWillUpdateProps((nextProps) => {
      const nextRoute = resolveRoute(nextProps.params);
      const sameModel = nextProps.params.actionModel === this.state.actionModel;
      const sameRoute = nextRoute.mode === this.state.route.mode && nextRoute.recordId === this.state.route.recordId && nextRoute.isNew === this.state.route.isNew;
      if (sameModel && sameRoute) return;
      this.state.actionModel = nextProps.params.actionModel;
      this.state.route = nextRoute;
      if (!sameModel) {
        this.state.offset = 0;
        this.state.domain = [];
        this.state.showInactive = false;
        this.state.hasActiveField = false;
        this.state.search = { facets: [], activeField: undefined };
        void this.checkDeleteAccess();
      }
      return this.load();
    });
  }

  // checkDeleteAccess mirrors base-view.tsx's RemoteModelForm effect: probe
  // check_access_rights("unlink") against the model (id 0, no specific
  // record) so the form's Delete button can be hidden client-side for users
  // without unlink rights, instead of only surfacing a server error on click.
  async checkDeleteAccess() {
    const model = this.state.actionModel;
    try {
      const response = await createModelRpc(model).callById("check_access_rights", 0, ["unlink"], { raise_exception: false });
      if (model !== this.state.actionModel) return;
      this.state.canDelete = response.result === true;
    } catch {
      if (model !== this.state.actionModel) return;
      this.state.canDelete = false;
    }
  }

  // mountBaseUI enhances dialog/toast nodes created after the initial route
  // mount; action results are rendered by Owl, so BaseUI needs a post-patch pass.
  mountBaseUI() {
    if (!this.root.el) return;
    window.thebase?.mountAll(this.root.el);
  }

  // load resolves the window action, then the SDUI metadata + data for the
  // active view mode. The arch (or a fields_get fallback) decides what renders
  // — no per-model frontend code.
  async load() {
    const model = this.state.actionModel;
    const route = this.state.route;
    this.state.loading = true;
    this.state.error = "";
    try {
      const action = await fetchWindowAction(model);
      const modes = viewModesFromWindowAction(action);
      this.state.title = action.name;
      this.state.viewModes = modes;
      const fallbackMode = modes.includes("list") ? "list" : (modes[0] ?? "list");
      // A hand-typed or stale /web/<model>/<view> for a view the model lacks falls back to the default view
      // (and fixes the address bar) instead of showing a raw error. Record routes (id/new) keep their strictness.
      const unavailableViewSegment = route.forced && !route.recordId && !route.isNew && modes.length > 0 && !modes.includes(route.mode);
      const mode = route.forced && !unavailableViewSegment ? route.mode : fallbackMode;
      if (unavailableViewSegment) window.history.replaceState(window.history.state, "", `/web/${model}`);
      this.state.route = { ...route, mode, ...(unavailableViewSegment ? { forced: false } : {}) };
      if (modes.length > 0 && !modes.includes(mode)) {
        throw new Error(t("Model \"{model}\" has no {mode} view mode ({viewMode})", { model, mode, viewMode: action.view_mode || "none" }));
      }
      // Resolved from VIEW_MOUNT_REGISTRY instead of a per-mode if/else chain
      // — an unregistered mode falls back to the list loader, matching the
      // previous chain's catch-all `else` branch.
      const mountEntry = VIEW_MOUNT_REGISTRY.get(mode);
      // Toolbar chrome is per mode: only loadList refills toolbarActions (list
      // arch header buttons), so clear both slots first — otherwise a list's
      // buttons and the previous mode's bound actions leak into kanban,
      // calendar, gantt, … after a mode switch.
      this.state.toolbarActions = [];
      this.state.createWidget = null;
      this.state.menuActions = [];
      // Bound actions load beside the view data for every mode, filtered by
      // the mode's view type on the server.
      await Promise.all([
        mountEntry ? mountEntry.load(this, model) : this.loadList(model),
        registerViewComponent(mode),
        this.loadMenuActions(model, mode),
      ]);
    } catch (error) {
      this.state.error = error instanceof Error ? error.message : t("Failed to load view");
    } finally {
      this.state.loading = false;
    }
  }

  async loadList(model) {
    // Records only need fields_get (shared with the view lookup through a short cache), so they start
    // at once instead of waiting for the arch; the dashboard is the only part that needs the arch first.
    const viewPromise = fetchViewMetadataForType("list", model);
    // Arch <dashboard> stat counts load alongside the page of records and
    // follow the same search domain, so tiles always match the filtered list.
    // The server hides active=false rows unless active_test is off, so the
    // inactive view lifts that default and filters active=false itself.
    const [viewData, read, dashboard] = await Promise.all([
      viewPromise,
      fetchModelListRecords({ model, limit: this.state.limit, offset: this.state.offset, domain: this.listDomain, context: this.state.showInactive ? { active_test: false } : undefined }),
      viewPromise.then((viewData) => fetchListDashboardStats(model, listDashboardFromView(viewData), this.state.domain)),
    ]);
    this.state.hasActiveField = Boolean(read.fields && read.fields.active);
    this.state.listDashboard = dashboard;
    this.state.columns = listFieldDefsFromView(viewData);
    this.state.rowActions = listRowActionsFromView(viewData);
    this.state.rowActionsColumn = listRowActionsColumnFromView(viewData);
    this.state.toolbarActions = listToolbarActionsFromView(viewData);
    this.state.createWidget = listCreateWidgetFromView(viewData);
    this.state.records = read.records;
    this.state.total = read.length;
  }

  // loadMenuActions loads the sys.model.action entries for `mode`. They are
  // optional chrome: a failed lookup (e.g. no read access to
  // sys.model.action) leaves only the mode's fixed items (list:
  // Import/Export) instead of failing the whole view.
  async loadMenuActions(model, mode) {
    const boundActions = await fetchBoundModelActions(model, mode).catch(() => []);
    // Drop a stale response: a fast mode/model switch starts a newer load, and
    // this older fetch must not overwrite the menu of the view now on screen.
    if (model !== this.state.actionModel || mode !== this.state.route.mode) return;
    this.state.menuActions = boundActions.map((bound) => ({
      name: `sys_model_action_${bound.id}`,
      label: bound.name,
      icon: bound.icon,
      confirm: bound.confirm,
      boundAction: bound,
    }));
  }

  async loadForm(model, recordId) {
    const [viewData, read] = await Promise.all([fetchViewMetadataForType("form", model), fetchModelFormRecord(model, recordId)]);
    const fields = fieldDefsFromFields(read.fields);
    this.state.formView = viewData;
    this.state.formFields = fields;
    // URL-derived "default_<field>=<value>" query params (see resolveRoute)
    // override the plain default_get values for a new record — e.g. gantt's
    // click-empty-cell-to-create navigates to "new" with the clicked
    // date_start/date_stop baked into the URL.
    const routeDefaults = !recordId ? this.state.route.defaults : null;
    this.state.formRecord = routeDefaults ? { ...read.record, ...routeDefaults } : read.record;
    this.onRemoteValueChange = createRemoteOnchangeHandler(model, Object.fromEntries(fields.map((field) => [field.name, field])));
  }

  // loadKanban resolves the board axis + card layout from the kanban arch (or
  // infers them from fields_get when no arch exists), then loads one page of
  // records plus the read_group totals that drive the per-column counts.
  async loadKanban(model) {
    const viewData = await fetchViewMetadataForType("kanban", model);
    const groupBy = resolveKanbanGroupByField(viewData) ?? "";
    const read = await fetchModelKanbanRecords({ model, groupBy: groupBy || "id", limit: this.state.limit, offset: 0, domain: this.state.domain });
    const fields = fieldDefsFromFields(read.fields);
    const groupTotals = Object.fromEntries((read.groups ?? []).map((group) => [group.value, group.count]));
    this.state.kanbanFields = fields;
    this.state.kanbanRecords = read.records;
    this.state.kanbanLayout = resolveKanbanCardLayout(viewData);
    this.state.kanbanGroupBy = groupBy;
    const unordered = (read.groups ?? []).map((group) => ({ id: group.value, label: group.label ?? group.value }));
    // Left-to-right by the stage's sequence, then id (stage 1 left-most).
    const relation = read.fields?.[groupBy]?.relation;
    const order = await fetchKanbanColumnOrder(relation, unordered.map((column) => column.id));
    const rank = new Map(order.ids.map((id, index) => [String(id), index]));
    this.state.kanbanColumns = unordered.sort((a, b) => rank.get(String(a.id)) - rank.get(String(b.id)));
    this.state.kanbanColumnModel = order.sequenceModel;
    this.state.kanbanRemaining = remainingCountsByColumn(groupTotals, countRecordsByGroup(read.records, groupBy));
    this.state.total = read.length;
  }

  // loadMoreKanbanColumn pages one column only, scoped by a group-value domain.
  async loadMoreKanbanColumn(column) {
    const model = this.state.actionModel;
    const groupBy = this.state.kanbanGroupBy;
    const loaded = this.state.kanbanRecords.filter((record) => groupKey(record[groupBy]) === String(column.id)).length;
    const read = await fetchModelKanbanRecords({ model, groupBy, groupValue: String(column.id), limit: this.state.limit, offset: loaded, domain: this.state.domain });
    this.state.kanbanRecords = appendUniqueRecords(this.state.kanbanRecords, read.records);
    const groupTotals = Object.fromEntries((read.groups ?? []).map((group) => [group.value, group.count]));
    this.state.kanbanRemaining = remainingCountsByColumn(groupTotals, countRecordsByGroup(this.state.kanbanRecords, groupBy));
  }

  // moveKanbanRecord applies the optimistic snapshot first, then persists the
  // grouped field write; a failed write reloads the board rather than leaving
  // the optimistic state in place.
  async moveKanbanRecord(record, column) {
    const groupBy = this.state.kanbanGroupBy;
    if (!groupBy) return;
    const nextValues = { [groupBy]: column.id };
    this.state.kanbanRecords = moveKanbanRecordSnapshot(this.state.kanbanRecords, record, nextValues, { targetColumn: column }, groupBy);
    try {
      await writeModelRecord(this.state.actionModel, Number(record.id), nextValues);
    } catch (error) {
      // A failed move must not replace the whole board with a page-level
      // error (state.error is checked ahead of every view-mode branch in
      // ModelView.xml) — surface it as a toast instead, matching React's
      // toast.error("Delete failed"/... ) pattern for post-load mutations.
      this.notify(error instanceof Error ? error.message : t("Failed to move record"), "danger");
      await this.load();
    }
  }

  // moveKanbanColumn reorders the board optimistically, then renumbers the
  // stages' `sequence` (10, 20, ...) so the order survives a reload; a failed
  // write reloads the board and toasts instead of leaving a phantom order.
  async moveKanbanColumn(column, target) {
    const model = this.state.kanbanColumnModel;
    if (!model) return;
    const columns = this.state.kanbanColumns;
    const from = columns.findIndex((item) => String(item.id) === String(column.id));
    const to = columns.findIndex((item) => String(item.id) === String(target.id));
    if (from < 0 || to < 0 || from === to) return;
    // Dropping takes the target's slot: moving right lands after it, moving
    // left lands before it, so both directions actually change the order.
    const next = columns.filter((item) => String(item.id) !== String(column.id));
    next.splice(to, 0, column);
    const previous = this.state.kanbanColumns;
    this.state.kanbanColumns = next;
    try {
      await Promise.all(next.map((item, index) => writeModelRecord(model, Number(item.id), { sequence: (index + 1) * 10 })));
    } catch (error) {
      this.state.kanbanColumns = previous;
      this.notify(error instanceof Error ? error.message : t("Failed to reorder columns"), "danger");
      await this.load();
    }
  }

  async deleteRecordFromBoard(record) {
    try {
      await deleteModelRecords(this.state.actionModel, [Number(record.id)]);
      await this.load();
    } catch (error) {
      this.notify(error instanceof Error ? error.message : t("Failed to delete record"), "danger");
    }
  }

  // loadCalendar resolves the anchor field from the <calendar> arch (or the
  // schedule_date -> transaction_date -> created_at fallback chain) and leaves
  // the occurrence fetch to CalendarView's first onRangeChange emit.
  async loadCalendar(model) {
    const { isCalendarExcludedModel, resolveCalendarViewData } = await loadViewUtils("calendar");
    if (isCalendarExcludedModel(model)) throw new Error(t("Model \"{model}\" has no calendar view (sys.* models are excluded)", { model }));
    const viewData = await fetchViewMetadataForType("calendar", model);
    const { anchorField, amountField } = resolveCalendarViewData(viewData);
    if (!anchorField) throw new Error(t("Model \"{model}\" has no date field the calendar can anchor on", { model }));
    this.state.calendarFields = viewData.fields;
    this.state.calendarAnchor = anchorField;
    this.state.calendarAmountField = amountField ?? "";
    this.state.calendarOccurrences = [];
  }

  async loadCalendarRange(from, to, mode) {
    this.calendarRange = { from, to, mode };
    try {
      const result = await fetchCalendarOccurrences({
        model: this.state.actionModel,
        dateFrom: from.toISOString(),
        dateTo: to.toISOString(),
      });
      this.state.calendarOccurrences = result.records ?? [];
    } catch (error) {
      // Same reasoning as moveKanbanRecord above: a range-refresh failure
      // after the calendar is already showing must not blank the whole view.
      this.notify(error instanceof Error ? error.message : t("Failed to load calendar events"), "danger");
    }
  }

  refreshCalendarRange() {
    if (!this.calendarRange) return Promise.resolve();
    return this.loadCalendarRange(this.calendarRange.from, this.calendarRange.to, this.calendarRange.mode);
  }

  // rescheduleOccurrence: a recurring row routes through the recurrence
  // engine with the chosen this/following/all scope; a plain row is a direct
  // write of the anchor field.
  async rescheduleOccurrence(occurrence, newDate, scope) {
    try {
      if (occurrence.is_recurring && occurrence.recurrence_id) {
        await rescheduleCalendarOccurrence({
          recurrenceID: Number(occurrence.recurrence_id),
          originalOccurrenceDate: String(occurrence.occurrence_date ?? ""),
          newDatetime: newDate.toISOString(),
          scope: scope ?? "this",
        });
      } else {
        const anchor = this.state.calendarAnchor;
        const value = anchor.kind === "date" ? newDate.toISOString().slice(0, 10) : newDate.toISOString();
        await writeModelRecord(this.state.actionModel, Number(occurrence.id), { [anchor.name]: value });
      }
      await this.refreshCalendarRange();
    } catch (error) {
      this.notify(error instanceof Error ? error.message : t("Failed to reschedule event"), "danger");
    }
  }

  // loadHierarchy fetches the plain flat record list (no new backend RPC per
  // the plan) and builds the parent/child forest client-side from the arch's
  // parent_field — mirrors loadKanban's arch-then-fetch shape but with no
  // grouping/pagination since v1 loads the whole model at once.
  async loadHierarchy(model) {
    const [viewData, read, utils] = await Promise.all([
      fetchViewMetadataForType("hierarchy", model),
      fetchModelListRecords({ model, limit: this.state.limit, offset: 0, domain: this.state.domain }),
      loadViewUtils("hierarchy"),
    ]);
    const { buildHierarchyTree, resolveHierarchyCardLayout, resolveHierarchyParentField } = utils;
    const parentField = resolveHierarchyParentField(viewData);
    this.state.hierarchyLayout = resolveHierarchyCardLayout(viewData);
    this.state.hierarchyRoots = buildHierarchyTree(read.records, parentField);
    this.state.total = read.length;
  }

  // loadGantt resolves the <gantt> arch's date_start/date_stop (required) and
  // default_group_by/color/scale (optional), then leaves the record fetch to
  // GanttView's first onRangeChange emit — same split as loadCalendar.
  async loadGantt(model) {
    const [viewData, utils] = await Promise.all([fetchViewMetadataForType("gantt", model), loadViewUtils("gantt")]);
    const ganttData = utils.resolveGanttViewData(viewData);
    if (!ganttData) throw new Error(t("Model \"{model}\" has no gantt view (date_start/date_stop fields not found)", { model }));
    this.state.ganttFields = viewData.fields;
    this.state.ganttDateStartField = ganttData.dateStartField;
    this.state.ganttDateStopField = ganttData.dateStopField;
    this.state.ganttDateKind = ganttData.dateKind;
    this.state.ganttColorField = ganttData.colorField ?? "";
    this.state.ganttDefaultScale = ganttData.scale;
    this.state.ganttTitle = ganttData.string ?? this.state.title;
    this.state.ganttRows = [];
    this.ganttGroupByField = ganttData.groupByField ?? "";
    this.ganttRange = null;
  }

  // loadGanttRange fetches only the records overlapping [from, to) — a plain
  // domain-filtered search_read, no new backend RPC — then groups them
  // client-side by default_group_by (buildGanttRows), mirroring loadCalendarRange's
  // range-driven refresh.
  async loadGanttRange(from, to, scale) {
    this.ganttRange = { from, to, scale };
    const model = this.state.actionModel;
    const dateStartField = this.state.ganttDateStartField;
    const dateStopField = this.state.ganttDateStopField;
    try {
      const fromIso = from.toISOString();
      const toIso = to.toISOString();
      // Overlap test that tolerates a half-filled record: with both dates it
      // is the usual start < to AND stop >= from; with only one date set
      // (SQL NULL never satisfies a comparison) that single date must fall
      // inside the window, and GanttRow draws it as a one-day pill.
      const overlap = [
        "|", "|",
        "&", [dateStartField, "<", toIso], [dateStopField, ">=", fromIso],
        "&", [dateStartField, "=", false], "&", [dateStopField, ">=", fromIso], [dateStopField, "<", toIso],
        "&", [dateStopField, "=", false], "&", [dateStartField, ">=", fromIso], [dateStartField, "<", toIso],
      ];
      const domain = [...this.state.domain, ...overlap];
      const read = await fetchModelListRecords({ model, limit: this.state.limit, offset: 0, domain });
      this.state.ganttRows = viewUtils.gantt.buildGanttRows(read.records, this.ganttGroupByField, this.state.ganttFields);
    } catch (error) {
      // Same reasoning as loadCalendarRange: a range-refresh failure must not
      // blank the whole view once it's already showing.
      this.notify(error instanceof Error ? error.message : t("Failed to load gantt records"), "danger");
    }
  }

  refreshGanttRange() {
    if (!this.ganttRange) return Promise.resolve();
    return this.loadGanttRange(this.ganttRange.from, this.ganttRange.to, this.ganttRange.scale);
  }

  // formatGanttDate matches CalendarView's anchor.kind handling: a date-only
  // field gets the plain YYYY-MM-DD slice, a datetime field the full ISO string.
  formatGanttDate(date) {
    return this.state.ganttDateKind === "date" ? date.toISOString().slice(0, 10) : date.toISOString();
  }

  // rescheduleGanttRecord is the plain write() path the plan calls for (no
  // recurrence engine like calendar's — gantt records aren't recurring).
  async rescheduleGanttRecord(record, newStart, newStop) {
    try {
      await writeModelRecord(this.state.actionModel, Number(record.id), {
        [this.state.ganttDateStartField]: this.formatGanttDate(newStart),
        [this.state.ganttDateStopField]: this.formatGanttDate(newStop),
      });
      await this.refreshGanttRange();
    } catch (error) {
      this.notify(error instanceof Error ? error.message : t("Failed to reschedule record"), "danger");
    }
  }

  // createGanttRecord encodes the clicked date range (and, when grouped, the
  // row's group value) as "default_<field>=<value>" query params and navigates
  // to "new" with them — v1 has no popover-in-dialog, so this opens the plain
  // FormView per the plan, pre-filled via the URL so it also works on a cold
  // page load (bookmark/new tab/back-forward), unlike an instance-field stash.
  createGanttRecord(row, dateStart, dateStop) {
    const defaults = {
      [this.state.ganttDateStartField]: this.formatGanttDate(dateStart),
      [this.state.ganttDateStopField]: this.formatGanttDate(dateStop),
    };
    if (this.ganttGroupByField && row.records[0]) defaults[this.ganttGroupByField] = row.records[0][this.ganttGroupByField];
    const query = Object.entries(defaults)
      .map(([field, value]) => `default_${field}=${encodeURIComponent(value)}`)
      .join("&");
    navigate(`/web/${this.state.actionModel}/new?${query}`);
  }

  // loadMap resolves the <map> arch's latitude_field/longitude_field (required)
  // and popup config, then fetches only records with both coordinates set (no
  // geocoding fallback per the plan) and builds the marker list client-side —
  // mirrors loadHierarchy's "plain search_read, no new backend RPC" shape.
  async loadMap(model) {
    const [viewData, utils] = await Promise.all([fetchViewMetadataForType("map", model), loadViewUtils("map")]);
    const mapData = utils.resolveMapViewData(viewData);
    if (!mapData) throw new Error(t("Model \"{model}\" has no map view (latitude_field/longitude_field not found)", { model }));
    const domain = [...this.state.domain, [mapData.latitudeField, "!=", false], [mapData.longitudeField, "!=", false]];
    const read = await fetchModelListRecords({ model, limit: this.state.limit, offset: 0, domain });
    this.state.mapMarkers = utils.buildMapMarkers(read.records, mapData.latitudeField, mapData.longitudeField);
    this.state.mapPanelTitle = mapData.panelTitle ?? this.state.title;
    this.state.mapHideName = mapData.hideName;
    this.state.mapHideAddress = mapData.hideAddress;
    this.state.mapPopupFields = mapData.popupFields;
    this.state.total = read.length;
  }

  // loadGrid resolves the <grid> arch's row/section/col/measure fields
  // (required) then leaves the pivot fetch to loadGridRange (first called
  // with the arch's default range) — same split as loadGantt/loadCalendar.
  async loadGrid(model) {
    const [viewData, utils] = await Promise.all([fetchViewMetadataForType("grid", model), loadViewUtils("grid")]);
    const gridData = utils.resolveGridViewData(viewData);
    if (!gridData) throw new Error(t("Model \"{model}\" has no grid view (row/col/measure fields not found)", { model }));
    this.state.gridRowFields = gridData.rowFields;
    this.state.gridColField = gridData.colField;
    this.state.gridMeasureField = gridData.measureField;
    this.state.gridMeasureOperator = gridData.measureOperator;
    this.state.gridRanges = gridData.ranges;
    this.state.gridActiveRange = gridData.defaultRange;
    this.state.gridMeasureLabel = viewData.fields?.[gridData.measureField]?.label ?? gridData.measureField;
    this.state.gridTitle = this.state.title;
    this.state.gridColumns = [];
    this.state.gridRows = [];
    this.state.gridMatrix = {};
    await this.loadGridRange(gridData.defaultRange);
  }

  // loadGridRange fetches the pivot for one named range (this-week/this-month)
  // via read_group's N-dimensional groupby ([...rowFields, colField]) — no new
  // backend RPC per backend-context.md's read_group contract — then pivots
  // the flat response into the row×column matrix the table renders.
  async loadGridRange(rangeName) {
    this.state.gridActiveRange = rangeName;
    const model = this.state.actionModel;
    const { from, to, columns } = viewUtils.grid.gridRangeFor(this.state.gridRanges, rangeName, new Date());
    this.state.gridColumns = columns;
    const groupby = [...this.state.gridRowFields, this.state.gridColField];
    const fields = [...groupby, `${this.state.gridMeasureField}:${this.state.gridMeasureOperator}`];
    const domain = [
      ...this.state.domain,
      [this.state.gridColField, ">=", from.toISOString().slice(0, 10)],
      [this.state.gridColField, "<", to.toISOString().slice(0, 10)],
    ];
    try {
      const response = await createModelRpc(model).read_group(domain, fields, groupby);
      const { matrix, rows } = viewUtils.grid.buildGridMatrix(response.result ?? [], this.state.gridRowFields, this.state.gridColField, this.state.gridMeasureField);
      this.state.gridMatrix = matrix;
      this.state.gridRows = rows;
    } catch (error) {
      // Same reasoning as loadGanttRange: a range-refresh failure must not
      // blank the whole view once it's already showing.
      this.notify(error instanceof Error ? error.message : t("Failed to load grid records"), "danger");
    }
  }

  // writeGridCell uses the atomic find_or_create RPC (see backend-context.md):
  // one call, keyed on the row's dimensions + this exact column date, writes
  // the measure onto the match or creates it from the domain's equality
  // values — closes the old search-then-create/write race, then reloads the
  // active range.
  async writeGridCell(row, columnKey, nextValue) {
    const model = this.state.actionModel;
    const rowDomain = this.state.gridRowFields.map((field, index) => [field, "=", row.ids[index]]);
    const domain = [...this.state.domain, ...rowDomain, [this.state.gridColField, "=", columnKey]];
    try {
      await createModelRpc(model).call("find_or_create", [], { domain, values: { [this.state.gridMeasureField]: nextValue } });
      await this.loadGridRange(this.state.gridActiveRange);
    } catch (error) {
      this.notify(error instanceof Error ? error.message : t("Failed to save grid cell"), "danger");
    }
  }

  // loadCohort resolves the <cohort> arch's date_start/date_stop (required)
  // and mode/interval/timeline/measure defaults, then fetches the matrix via
  // loadCohortData — same "arch then fetch" split as loadGrid/loadGantt.
  async loadCohort(model) {
    const [viewData, utils] = await Promise.all([fetchViewMetadataForType("cohort", model), loadViewUtils("cohort")]);
    const cohortData = utils.resolveCohortViewData(viewData);
    if (!cohortData) throw new Error(t("Model \"{model}\" has no cohort view (date_start/date_stop fields not found)", { model }));
    this.state.cohortTitle = cohortData.string ?? this.state.title;
    this.state.cohortDateStartField = cohortData.dateStartField;
    this.state.cohortDateStopField = cohortData.dateStopField;
    this.state.cohortInterval = cohortData.interval;
    this.state.cohortTimeline = cohortData.timeline;
    this.state.cohortMode = cohortData.mode;
    this.state.cohortMeasure = cohortData.measure;
    this.state.cohortMeasureOptions = utils.cohortMeasureOptions(viewData.fields);
    this.state.cohortDisableLinking = cohortData.disableLinking;
    this.state.cohortColumnCount = 0;
    this.state.cohortRows = [];
    await this.loadCohortData();
  }

  // loadCohortData calls the cohort_data RPC with the current selector state
  // (interval/timeline/measure + the active search domain) — the one RPC
  // call this view needs, unlike every other view type's search_read/
  // read_group fetch (see backend-context.md's cohort_data contract).
  async loadCohortData() {
    const model = this.state.actionModel;
    try {
      const response = await createModelRpc(model).call("cohort_data", [], {
        domain: this.state.domain,
        date_start: this.state.cohortDateStartField,
        date_stop: this.state.cohortDateStopField,
        interval: this.state.cohortInterval,
        timeline: this.state.cohortTimeline,
        measure: this.state.cohortMeasure,
      });
      const result = response.result ?? {};
      this.state.cohortColumnCount = result.column_count ?? 0;
      this.state.cohortRows = result.rows ?? [];
    } catch (error) {
      // Same reasoning as loadGridRange: a refresh failure must not blank
      // the whole view once it's already showing.
      this.notify(error instanceof Error ? error.message : t("Failed to load cohort data"), "danger");
    }
  }

  setCohortInterval(interval) {
    this.state.cohortInterval = interval;
    void this.loadCohortData();
  }

  setCohortTimeline(timeline) {
    this.state.cohortTimeline = timeline;
    void this.loadCohortData();
  }

  setCohortMeasure(measure) {
    this.state.cohortMeasure = measure;
    void this.loadCohortData();
  }

  // setCohortMode is display-only (retention vs. its churn complement): the
  // RPC has no "mode" kwarg, so this never re-fetches.
  setCohortMode(mode) {
    this.state.cohortMode = mode;
  }

  // openCohortBucket reproduces the clicked bucket's date range client-side
  // (the RPC returns no per-record ids) and reuses switchMode's same-model
  // route change, which keeps state.domain intact (see onWillUpdateProps's
  // sameModel check) so loadList picks up the filter on arrival.
  openCohortBucket(bucket) {
    const domain = viewUtils.cohort.cohortBucketDomain(
      {
        dateStartField: this.state.cohortDateStartField,
        dateStopField: this.state.cohortDateStopField,
        interval: this.state.cohortInterval,
        timeline: this.state.cohortTimeline,
        disableLinking: this.state.cohortDisableLinking,
      },
      bucket
    );
    if (!domain) return;
    this.state.domain = domain;
    this.state.offset = 0;
    this.switchMode("list");
  }

  get isCalendarMode() {
    return this.state.route.mode === "calendar" && Boolean(this.state.calendarAnchor);
  }

  get isFormMode() {
    return this.state.route.mode === "form";
  }

  get isNewRecord() {
    return this.isFormMode && !this.state.route.recordId;
  }

  get modelViewClassName() {
    return `basehub-modelview-shell${this.isFormMode ? " is-form-mode" : ""}`;
  }

  reload() {
    void this.load();
  }

  refreshActiveRecord() {
    return this.load();
  }

  switchMode(mode) {
    navigate(mode === "list" ? `/web/${this.state.actionModel}` : `/web/${this.state.actionModel}/${mode}`);
  }

  // openRecord routes base.workflow rows to the Phase 7a canvas instead of
  // the generic form — matches frontend-admin's canvas view-type plugin
  // being the record-open target for that one model.
  // An optional `tab` (a form <page name>) rides along as ?tab=, which the
  // form's notebook reads to pick its initially active page. An optional
  // `dialog` rides along as ?dialog= for a form widget to auto-open.
  openRecord(record, tab, dialog) {
    if (this.state.actionModel === "base.workflow") {
      navigate(`/web/base.workflow/canvas/${record.id}`);
      return;
    }
    const params = new URLSearchParams();
    if (tab) params.set("tab", tab);
    if (dialog) params.set("dialog", dialog);
    const query = params.size ? `?${params}` : "";
    navigate(`/web/${this.state.actionModel}/${record.id}${query}`);
  }

  // openRecordDialog loads the form view + full record and shows it in a
  // popup; the gantt keeps its place and refreshes after a save/delete.
  async openRecordDialog(record) {
    const model = this.state.actionModel;
    const recordId = Number(record.id);
    try {
      const [viewData, read] = await Promise.all([fetchViewMetadataForType("form", model), fetchModelFormRecord(model, recordId)]);
      const fields = fieldDefsFromFields(read.fields);
      this.recordDialogRemoteChange = createRemoteOnchangeHandler(model, Object.fromEntries(fields.map((field) => [field.name, field])));
      this.state.recordDialog = { open: true, recordId, viewData, fields, record: read.record };
    } catch (error) {
      this.notify(error instanceof Error ? error.message : t("Failed to open record"), "danger");
    }
  }

  closeRecordDialog() {
    this.state.recordDialog = { ...CLOSED_RECORD_DIALOG };
  }

  async saveRecordDialog(values) {
    const model = this.state.actionModel;
    const recordId = this.state.recordDialog.recordId;
    await writeModelRecord(model, recordId, values);
    const { record } = await fetchModelFormRecord(model, recordId);
    this.state.recordDialog.record = record;
    await this.refreshGanttRange();
  }

  async deleteRecordDialog() {
    await deleteModelRecords(this.state.actionModel, [this.state.recordDialog.recordId]);
    this.closeRecordDialog();
    await this.refreshGanttRange();
  }

  // listRowActions hides type="delete" row actions for users without unlink
  // rights, same client-side gate as the form's Delete button.
  get listRowActions() {
    // The inactive view offers only the icon-only Activate button; the usual
    // row actions (edit, delete, ...) don't apply to a deactivated record.
    if (this.state.showInactive) {
      return [{ buttonType: "activate", label: t("Activate"), icon: "power", className: "text-success" }];
    }
    return this.state.canDelete ? this.state.rowActions : this.state.rowActions.filter((action) => action.buttonType !== "delete");
  }

  // listDomain is the search domain plus, in the inactive view, active = false.
  get listDomain() {
    return this.state.showInactive ? [...this.state.domain, ["active", "=", false]] : this.state.domain;
  }

  // viewInactiveHandler is undefined for models without an `active` field, which
  // hides the Actions > View inactive entry.
  get viewInactiveHandler() {
    return this.state.hasActiveField ? () => this.toggleInactive() : undefined;
  }

  // toggleInactive flips the list between all records and inactive-only.
  toggleInactive() {
    this.state.showInactive = !this.state.showInactive;
    this.state.offset = 0;
    void this.refreshData();
  }

  createRecord() {
    // A list arch with create_widget routes New through that widget (it navigates to the form itself).
    const createWidget = this.state.createWidget;
    if (createWidget) {
      this.openToolbarWidget({ widget: createWidget.widget, label: createWidget.label, dialogTitle: createWidget.label, description: createWidget.description, size: createWidget.size }).catch((error) => {
        this.notify(error instanceof Error ? error.message : t("Could not open the create dialog"), "danger");
      });
      return;
    }
    navigate(`/web/${this.state.actionModel}/new`);
  }

  backToList() {
    navigate(`/web/${this.state.actionModel}`);
  }

  // updatePager is ControlPanel's Pager sub-component onUpdate callback —
  // it computes the next offset/limit, this just applies and reloads.
  updatePager(next) {
    this.state.offset = next.offset;
    this.state.limit = next.limit;
    void this.refreshData();
  }

  // controlPanelBreadcrumbs/controlPanelPager/controlPanelSearchFields feed
  // the <ControlPanel> instance in ModelView.xml's header.
  get controlPanelBreadcrumbs() {
    return [];
  }

  get controlPanelPager() {
    if (this.state.route.mode !== "list") return null;
    if (this.state.total <= this.state.limit) return null;
    return { offset: this.state.offset, limit: this.state.limit, total: this.state.total };
  }

  get controlPanelSearchFields() {
    const mode = this.state.route.mode;
    const columns = mode === "kanban" ? this.state.kanbanFields : mode === "list" ? this.state.columns : [];
    return columns.map((column) => ({ name: column.name, label: column.label }));
  }

  setSearchField(field) {
    this.state.search.activeField = field;
  }

  // commitSearch/removeSearchFacet/clearSearch keep state.search.facets in
  // sync, translate it into a domain (a flat list of triples is
  // an implicit AND), reset to the first page, and reload the active mode.
  commitSearch(value, field) {
    const trimmed = typeof value === "string" ? value.trim() : value;
    if (!trimmed) return;
    const facet = { id: `${field}:${Date.now()}:${Math.random().toString(36).slice(2)}`, label: `${field}: ${trimmed}`, field, value: trimmed };
    this.state.search.facets = [...this.state.search.facets, facet];
    this.applySearch();
  }

  removeSearchFacet(id) {
    this.state.search.facets = this.state.search.facets.filter((facet) => facet.id !== id);
    this.applySearch();
  }

  clearSearch() {
    this.state.search.facets = [];
    this.applySearch();
  }

  applySearch() {
    this.state.domain = this.state.search.facets.map((facet) => [facet.field, "ilike", facet.value]);
    this.state.offset = 0;
    void this.refreshData();
  }

  // refreshData re-runs only the active mode's data fetch (not the full
  // load(), which would also re-resolve the window action) — used by search
  // and pager updates where the view mode itself hasn't changed.
  async refreshData() {
    const model = this.state.actionModel;
    const mode = this.state.route.mode;
    const mountEntry = VIEW_MOUNT_REGISTRY.get(mode);
    if (mountEntry?.refresh) return mountEntry.refresh(this, model);
    if (mode === "calendar" || mode === "form" || mode === "gantt") return;
    return this.loadList(model);
  }

  // saveRecord is the single write path for the form view: create routes to
  // the freshly created record, update refreshes in place.
  async saveRecord(values, isCreateMode) {
    const model = this.state.actionModel;
    if (isCreateMode) {
      const created = await createModelRecord(model, values);
      const createdID = Number(created.id ?? 0);
      if (createdID > 0) {
        navigate(`/web/${model}/${createdID}`);
        return;
      }
      await this.refreshActiveRecord();
      return;
    }
    await writeModelRecord(model, this.state.route.recordId, values);
    await this.refreshActiveRecord();
  }

  async deleteRecords(ids) {
    await deleteModelRecords(this.state.actionModel, ids);
    this.backToList();
  }

  async duplicateRecord() {
    const copied = await duplicateModelRecord(this.state.actionModel, this.state.route.recordId);
    const copiedID = Number(copied?.id ?? copied ?? 0);
    if (copiedID > 0) navigate(`/web/${this.state.actionModel}/${copiedID}`);
  }

  // setRecordActive backs the form Actions menu's Activate/Deactivate entry.
  // `read` by id ignores active_test, so the form still shows the record
  // after it is deactivated.
  async setRecordActive(active) {
    await writeModelRecord(this.state.actionModel, this.state.route.recordId, { active });
    await this.refreshActiveRecord();
    this.notify(active ? t("Record activated") : t("Record deactivated"), "success");
  }

  // runArchButton dispatches a form-arch <button>. type="object" runs a
  // record-bound RPC; type="action" opens another model's window (list) by
  // XMLID; type="client" resolves a ui.actions.client row and, for the
  // navigate_route tag, client-side navigates (the mechanism website.page's
  // "Open in Studio" button uses — Phase 5's known gap). Any other client
  // tag (e.g. genActionFlow's per-model dialogs) stays unsupported, same as
  // before, since those are app-specific business dialogs out of this
  // generic engine's scope.
  async runArchButton(node) {
    try {
      if (node.buttonType === "action") {
        await this.runActionArchButton(node);
        return;
      }
      if (node.buttonType === "client") {
        await this.runClientArchButton(node);
        return;
      }
      if (node.buttonType !== "object" || !node.name) {
        throw new Error(t("Button \"{label}\" uses unsupported type \"{type}\"", { label: node.label, type: node.buttonType ?? "unknown" }));
      }
      const recordID = Number(this.state.route.recordId);
      if (!Number.isFinite(recordID) || recordID <= 0) throw new Error(t("Button \"{label}\" requires a saved record", { label: node.label }));

      // Phase 6 dialogs/x2many/PWA pass: four app-specific object-button
      // dispatches, ported 1:1 from base-view.tsx's runButton (base.database
      // duplicate/reinit, base.users password reset, sys.oauth_client secret
      // reveal) — these open a BaseUI dialog instead of running the RPC
      // (or, for the OAuth case, run the RPC then show its one-time result)
      // rather than falling through to the generic executeObjectButtonMethod
      // + refresh path every other object button uses.
      const buttonName = (node.name ?? "").trim();
      if (this.state.actionModel === "base.database" && buttonName === "action_duplicate") {
        this.openDatabaseDuplicateDialog(recordID, String(this.state.formRecord?.name ?? recordID));
        return;
      }
      if (this.state.actionModel === "base.database" && buttonName === "action_reinit") {
        const enabledModules = Array.isArray(this.state.formRecord?.enabled_modules)
          ? this.state.formRecord.enabled_modules.map((name) => String(name))
          : [];
        this.openDatabaseModuleSelectionDialog(recordID, String(this.state.formRecord?.name ?? recordID), enabledModules);
        return;
      }
      if (this.state.actionModel === "base.users" && buttonName === "action_set_password") {
        this.state.userSecurityPasswordDialog = {
          open: true,
          recordID,
          userName: String(this.state.formRecord?.name ?? this.state.formRecord?.login ?? recordID),
        };
        return;
      }
      if (this.state.actionModel === "base.users" && buttonName === "action_generate_api_key") {
        // One-time reveal, like the oauth client secret below: only the digest is stored.
        const keyResult = await executeObjectButtonMethod(this.state.actionModel, recordID, buttonName);
        this.state.userApiKeyDialog = {
          open: true,
          subjectName: String(keyResult?.user_login || this.state.formRecord?.name || recordID),
          apiKey: typeof keyResult?.user_api_key === "string" ? keyResult.user_api_key : "",
        };
        await this.refreshActiveRecord();
        return;
      }
      if (this.state.actionModel === "sys.oauth_client" && buttonName === "generate_client_secret") {
        // Unlike every other branch here, this response carries a one-time
        // plaintext client_secret that must be shown now — only the hash is
        // stored after rotation, so there is no later read to recover it from.
        const oauthResult = await executeObjectButtonMethod(this.state.actionModel, recordID, buttonName);
        this.state.oauthClientSecretDialog = {
          open: true,
          subjectName: String(this.state.formRecord?.service_name ?? this.state.formRecord?.client_id ?? recordID),
          result: (await loadViewUtils("oauth")).parseOauthClientRevealResult(oauthResult),
        };
        await this.refreshActiveRecord();
        return;
      }

      await executeObjectButtonMethod(this.state.actionModel, recordID, node.name);

      // `reload` (explicit <button reload="true"> XML attr) means the button's
      // effect can touch app-wide state (menus, views, config) beyond this
      // form, so force a full reload instead of the soft record refresh below.
      if (node.reload) {
        this.notify(node.label);
        window.location.reload();
        return;
      }

      await this.refreshActiveRecord();

      // sys.model's publish/unpublish buttons only start a background job
      // (see backend-context.md's sys.model async publish section) — the RPC
      // returning success here just means the job was accepted, not that it
      // finished. base.database's stop/reconnect completion toast is
      // delivered separately by its servicebus listener, so skip the local
      // toast there to avoid a misleading "completed successfully" message.
      const isAsyncSysModelJob = this.state.actionModel === "sys.model" && (buttonName === "publish" || buttonName === "unpublish");
      const isBaseDatabaseLifecycleAction = this.state.actionModel === "base.database" && (buttonName === "action_stop" || buttonName === "action_reconnect");
      if (isBaseDatabaseLifecycleAction) return;
      this.notify(isAsyncSysModelJob ? t("{label}: started in the background — you'll be notified when it finishes.", { label: node.label }) : node.label);
    } catch (error) {
      // A failed arch button must not replace the whole form with a
      // page-level error (state.error is exclusive with isFormMode in
      // ModelView.xml) — surface it as a toast, matching runRowAction/
      // runToolbarAction's error handling below.
      this.notify(error instanceof Error ? error.message : t("Button failed"), "danger");
    }
  }

  // setDialogOpen is the shared onOpenChange callback every base-view dialog
  // component's props wire back to — it also covers BaseUI's own
  // Escape/backdrop-click close paths via each dialog's "nodexui:close"
  // listener, keeping this state the single source of truth for visibility.
  setDialogOpen(key, open) {
    this.state[key] = { ...this.state[key], open };
  }

  // openImexDialog loads fresh field metadata before showing the Import or
  // Export dialog, so columns always match the live model definition.
  async openImexDialog(key) {
    try {
      this.state.imexFields = await fetchModelFields(this.state.actionModel);
      this.setDialogOpen(key, true);
    } catch (error) {
      this.notify(error instanceof Error ? error.message : t("Failed to load model fields"), "danger");
    }
  }

  // onImported reports the server's upsert counts and reloads the list.
  onImported(result) {
    this.notify(t("Imported {total} records ({created} created, {updated} updated)", { total: result.total, created: result.created, updated: result.updated }), "success");
    void this.load();
  }

  onExported(count) {
    this.notify(t("Exported {count} records", { count }), "success");
  }

  openDatabaseDuplicateDialog(recordID, sourceName) {
    this.state.databaseDuplicateDialog = {
      open: true,
      sourceID: recordID,
      sourceName,
      suggestedName: buildDuplicateDatabaseNameSuggestion(sourceName),
    };
  }

  // confirmDatabaseDuplicate submits base.database.action_duplicate with the
  // dialog-entered target name and opens the returned registry row when the
  // backend reports a valid duplicate record id.
  async confirmDatabaseDuplicate(newName) {
    const sourceID = Number(this.state.databaseDuplicateDialog.sourceID);
    if (!Number.isFinite(sourceID) || sourceID <= 0) throw new Error(t("Duplicate database requires a saved source record"));
    const created = await executeObjectMethodWithArgs(this.state.actionModel, sourceID, "action_duplicate", [], { new_name: newName });
    const createdID = Number(created?.id ?? 0);
    if (Number.isFinite(createdID) && createdID > 0) {
      navigate(`/web/${this.state.actionModel}/${createdID}`);
    } else {
      await this.refreshActiveRecord();
    }
    this.notify(t("Database \"{newName}\" is queued for duplication.", { newName }));
  }

  // openDatabaseModuleSelectionDialog records the current database record and
  // its enabled module list, then fetches the scanned module catalog so the
  // dialog can offer every installable module name (not free text).
  openDatabaseModuleSelectionDialog(recordID, databaseName, enabledModules) {
    this.state.databaseModuleSelectionDialog = {
      open: true,
      recordID,
      databaseName,
      enabledModules,
      availableModules: [],
      loadingModules: true,
    };
    void executeModelMethod(this.state.actionModel, "action_available_modules")
      .then((result) => {
        const modules = Array.isArray(result?.modules) ? result.modules.map((name) => String(name)) : [];
        this.state.databaseModuleSelectionDialog = { ...this.state.databaseModuleSelectionDialog, availableModules: modules, loadingModules: false };
      })
      .catch((error) => {
        this.state.databaseModuleSelectionDialog = { ...this.state.databaseModuleSelectionDialog, loadingModules: false };
        this.notify(error instanceof Error ? error.message : t("Failed to load the module catalog"), "danger");
      });
  }

  // confirmDatabaseModuleSelection submits base.database.action_reinit with
  // the dialog-checked module selection: modules dropped from the selection
  // are uninstalled on the target database, modules added are installed, and
  // every selected module's master data is replayed. No toast here for the
  // success case — base.database's reinit_succeeded/failed servicebus event
  // (not ported in this pass, same as the rest of the servicebus listener
  // set) would normally deliver that; refreshActiveRecord() alone still
  // reflects the accepted-job state.
  async confirmDatabaseModuleSelection(modules) {
    const recordID = Number(this.state.databaseModuleSelectionDialog.recordID);
    if (!Number.isFinite(recordID) || recordID <= 0) throw new Error(t("This action requires a saved database record"));
    await executeObjectMethodWithArgs(this.state.actionModel, recordID, "action_reinit", [], { modules });
    await this.refreshActiveRecord();
  }

  // confirmUserSecurityPassword submits base.users.action_set_password with a
  // positional arg (not a kwarg) — matches the backend method's signature,
  // confirmed against frontend-admin's identical call shape.
  async confirmUserSecurityPassword(newPassword) {
    const recordID = Number(this.state.userSecurityPasswordDialog.recordID);
    if (!Number.isFinite(recordID) || recordID <= 0) throw new Error(t("This action requires a saved user record"));
    await executeObjectMethodWithArgs(this.state.actionModel, recordID, "action_set_password", [newPassword]);
    this.notify(t("Password updated."));
  }

  async runActionArchButton(node) {
    const xmlID = extractArchButtonXMLID(node.name);
    if (!xmlID) throw new Error(t("Action button \"{label}\" is missing a valid XMLID", { label: node.label }));
    const action = await fetchWindowActionByXMLID(xmlID);
    navigate(`/web/${action.res_model}`);
  }

  async runClientArchButton(node, contextRecordID) {
    const xmlID = extractArchButtonXMLID(node.name);
    if (!xmlID) throw new Error(t("Client button \"{label}\" is missing a valid XMLID", { label: node.label }));
    const action = await fetchClientActionByXMLID(xmlID);
    const recordID = contextRecordID !== undefined ? Number(contextRecordID) : Number(this.state.route.recordId);
    const activeID = Number.isFinite(recordID) && recordID > 0 ? recordID : null;
    await this.runClientAction(action, activeID);
  }

  // runClientAction executes a normalized ui.actions.client (see
  // fetchClientActionByXMLID) against an optional active record — shared by
  // arch type="client" buttons and sys.model.action client entries.
  async runClientAction(action, activeID) {
    const tag = action.tag.trim();
    // open_widget presents a registered form widget where the action's
    // `target` says (dialog/sheet/new tab/in place) — the view arch only
    // references the action, so the presentation is data, not UI code.
    if (tag === "open_widget") {
      await this.openClientWidget(action, activeID);
      return;
    }
    if (tag !== "navigate_route") throw new Error(t("Unsupported client action \"{action}\"", { action: tag || action.name }));

    const placeholders = { active_model: this.state.actionModel, active_id: activeID, active_ids: activeID ? [activeID] : [] };
    const resolvedContext = replaceActionContextPlaceholders(action.context, placeholders);
    const resolvedParams = replaceActionContextPlaceholders(action.params, placeholders);

    const ensureModel = String(resolvedParams.ensure_model ?? resolvedContext.ensure_model ?? "").trim();
    const ensureMethod = String(resolvedParams.ensure_method ?? resolvedContext.ensure_method ?? "").trim();
    if (ensureModel && ensureMethod) {
      const ensureID = Number(resolvedParams.ensure_id ?? resolvedContext.ensure_id ?? resolvedContext.active_id);
      if (!Number.isFinite(ensureID) || ensureID <= 0) throw new Error(t("Client action \"{name}\" is missing a valid ensure id", { name: action.name }));
      // Route actions can declare a server-side precondition (e.g. website
      // Studio needs a draft page to exist before it opens).
      await executeObjectButtonMethod(ensureModel, ensureID, ensureMethod);
    }

    const routeContextID = Number(resolvedContext.active_id);
    const route = String(resolvedParams.route ?? resolvedContext.route ?? "").trim();
    const routeWithActiveID =
      this.state.actionModel === "tbl.website_page" && Number.isFinite(routeContextID) && routeContextID > 0
        ? route.replace(/^\/web\/tbl\.website_page(?:\/\d+)?\/editor\/?$/, `/web/tbl.website_page/${routeContextID}/editor`)
        : route;
    if (!routeWithActiveID.startsWith("/")) throw new Error(t("Client action \"{name}\" is missing a valid route", { name: action.name }));
    this.openClientRoute(routeWithActiveID, action.target);
  }

  // openClientRoute navigates in place, or opens a new browser tab when the
  // client action's target is "window".
  openClientRoute(route, target) {
    if (clientActionPresentation(target) === "window") {
      window.open(toFullPath(route), "_blank", "noopener");
      return;
    }
    navigate(route);
  }

  // openClientWidget runs a ui.actions.client `open_widget` action for one
  // record. params: widget (form-widget registry name, required), title,
  // title_field (optional record field shown before the title), description,
  // size (dialog: sm|md|lg|xl), side (sheet: left|right), and
  // tab/dialog — the form ?tab=/?dialog= hints used when the target
  // navigates (current/main/fullscreen) or opens a new window instead.
  async openClientWidget(action, activeID) {
    const params = action.params ?? {};
    const widget = String(params.widget ?? "").trim();
    if (!widget) throw new Error(t("Client action \"{name}\" is missing params.widget", { name: action.name }));
    if (!activeID) throw new Error(t("Client action \"{name}\" requires a saved record", { name: action.name }));
    const model = action.resModel || this.state.actionModel;
    const mode = clientActionPresentation(action.target);

    // Navigation targets open the record's form with the widget hints, so
    // one action definition can switch between popup and page via `target`.
    if (mode === "navigate" || mode === "window") {
      const query = new URLSearchParams();
      if (params.tab) query.set("tab", String(params.tab));
      if (params.dialog) query.set("dialog", String(params.dialog));
      this.openClientRoute(`/web/${model}/${activeID}${query.size ? `?${query}` : ""}`, action.target);
      return;
    }

    const component = await resolveFormWidget(widget);
    if (!component) throw new Error(t("Client action \"{name}\" has an unknown widget \"{widget}\"", { name: action.name, widget }));
    // A list row only carries its visible columns; load the full record so
    // the widget sees the same values it would on the form.
    const { record } = await fetchModelFormRecord(model, activeID);
    // Without title_field the header is just the title (+ description),
    // e.g. "Set Device Config"; with it, "<record[title_field]> — <title>".
    const heading = params.title_field ? String(record[params.title_field] ?? activeID) : "";
    this.state.widgetDialog = {
      open: true,
      mode,
      size: String(params.size ?? ""),
      side: String(params.side ?? "right"),
      heading,
      title: String(params.title || action.name),
      description: String(params.description ?? ""),
      component,
      record,
      node: { name: widget, attrs: { ...params } },
      // close lets a widget with its own Cancel/Done buttons hide the host;
      // closeAndNavigate defers a route change until the host has hidden.
      ctx: {
        model,
        values: record,
        isReadonly: true,
        isCreateMode: false,
        close: () => (this.state.widgetDialog.open = false),
        closeAndNavigate: (route) => this.closeWidgetHostAndNavigate(route),
      },
    };
  }

  async runRowAction(action, record) {
    try {
      // type="open" is a pure navigation button (no RPC, so no `name` needed):
      // it opens the row's form, optionally on the notebook page in `tab`
      // and with a widget `dialog` hint.
      if (action.buttonType === "open") {
        this.openRecord(record, action.tab, action.dialog);
        return;
      }
      // type="widget" shows a registered form widget in a dialog over the list.
      if (action.buttonType === "widget") {
        await this.openWidgetDialog(action, record);
        return;
      }
      // type="activate" (synthetic, inactive view only) flips active false -> true.
      if (action.buttonType === "activate") {
        await writeModelRecord(this.state.actionModel, Number(record.id), { active: true });
        await this.load();
        this.notify(action.label);
        return;
      }
      // type="delete" unlinks the row after a confirm (arch `confirm`, or a
      // default prompt — deletion is never silent) and refreshes the list.
      if (action.buttonType === "delete") {
        if (!(await this.confirmAction(action.confirm || t("Delete this record?")))) return;
        await deleteModelRecords(this.state.actionModel, [Number(record.id)]);
        await this.load();
        this.notify(action.label);
        return;
      }
      if (!action.name) throw new Error(t("Row action \"{label}\" is missing a valid name", { label: action.label }));
      if (!(await this.confirmAction(action.confirm))) return;
      if (action.buttonType === "action") {
        await this.runActionArchButton(action);
        return;
      }
      if (action.buttonType === "client") {
        await this.runClientArchButton(action, record.id);
        return;
      }
      if (action.buttonType !== "object") {
        throw new Error(t("Row action \"{label}\" uses unsupported type \"{type}\"", { label: action.label, type: action.buttonType ?? "unknown" }));
      }
      const recordID = Number(record.id);
      if (!Number.isFinite(recordID) || recordID <= 0) {
        throw new Error(t("Row action \"{label}\" requires a saved record", { label: action.label }));
      }
      // Module reinit replays data/views and can take a while: block the UI
      // until the RPC settles (finally below), success or failure.
      const isReinit = action.name?.trim() === "action_reinit";
      // Install/uninstall of sys.module also replay data/views: same blocking modal.
      const lifecycleMessages = {
        action_install: t("Installing module, please wait…"),
        action_uninstall: t("Uninstalling module, please wait…"),
      };
      const lifecycleMessage = this.state.actionModel === "sys.module.module" ? lifecycleMessages[action.name?.trim()] : "";
      if (isReinit) this.state.busyOverlay = { open: true, message: t("Reinitializing module, please wait…") };
      else if (lifecycleMessage) this.state.busyOverlay = { open: true, message: lifecycleMessage };
      try {
        if (isReinit) {
          const requestID = `reinit-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
          try {
            await executeLongRunningObjectMethod(this.state.actionModel, recordID, action.name, { request_id: requestID });
          } catch (error) {
            // Reinit of a big module (e.g. base) can outlive the connection
            // (proxy idle timeout, server restart). The server keeps
            // replaying, so keep the modal up until it reports the outcome
            // (bus event or status poll); real server errors still propagate.
            if (!error?.transport) throw error;
            await this.waitForReinitOutcome(requestID);
          }
        } else {
          await executeObjectButtonMethod(this.state.actionModel, recordID, action.name);
        }
      } catch (error) {
        this.state.busyOverlay = { open: false, message: "" };
        throw error;
      }
      // On success with reload="true" keep the overlay up until the page
      // reloads (no flash of the stale list); otherwise close it now.
      if (!action.reload) this.state.busyOverlay = { open: false, message: "" };

      // `reload` (explicit <button reload="true"> XML attr) means the action's
      // effect can touch app-wide state beyond this list, so force a full
      // reload instead of re-fetching just this list's rows.
      if (action.reload) {
        this.notify(action.label);
        window.location.reload();
        return;
      }

      await this.load();

      // sys.model's publish/unpublish buttons only start a background job —
      // say so explicitly since the RPC returning here just means it was
      // accepted, not that it finished (mirrors runArchButton's same note).
      const isAsyncSysModelJob = this.state.actionModel === "sys.model" && (action.name?.trim() === "publish" || action.name?.trim() === "unpublish");
      this.notify(isAsyncSysModelJob ? t("{label}: started in the background — you'll be notified when it finishes.", { label: action.label }) : action.label);
    } catch (error) {
      this.notify(error instanceof Error ? error.message : t("Row action failed"), "danger");
    }
  }

  // waitForReinitOutcome resolves when sys.module.module reports the reinit
  // finished, after the original RPC's connection dropped. Two signals race:
  // the sys.module.reinit_succeeded/failed bus event (instant) and an
  // action_reinit_status poll (survives a missed event while the bus socket
  // reconnects). Status "unknown" on a reachable server means the process
  // restarted and lost the tracker, so treat it as done (caller reloads).
  // Rejects on reinit_failed or after 30 minutes.
  waitForReinitOutcome(requestID) {
    return new Promise((resolve, reject) => {
      let settled = false;
      const deadline = Date.now() + 30 * 60_000;
      const finish = (error) => {
        if (settled) return;
        settled = true;
        this.reinitCancels.delete(cancel);
        clearInterval(timer);
        busService.unsubscribe("sys.module.reinit_succeeded", onOK);
        busService.unsubscribe("sys.module.reinit_failed", onFail);
        if (error) reject(error);
        else resolve();
      };
      const onOK = (payload) => {
        if (payload?.request_id === requestID) finish();
      };
      const onFail = (payload) => {
        if (payload?.request_id === requestID) finish(new Error(payload.error || t("Module reinit failed")));
      };
      busService.subscribe("sys.module.reinit_succeeded", onOK);
      busService.subscribe("sys.module.reinit_failed", onFail);
      const poll = async () => {
        if (Date.now() > deadline) {
          finish(new Error(t("Timed out waiting for module reinit; reload the page to check its state.")));
          return;
        }
        try {
          const res = await executeModelMethod("sys.module.module", "action_reinit_status", { request_id: requestID });
          if (res?.status === "failed") finish(new Error(res.error || t("Module reinit failed")));
          else if (res?.status === "succeeded" || res?.status === "unknown") finish();
        } catch {
          // Server unreachable (or restarting): keep waiting.
        }
      };
      const timer = setInterval(poll, 3000);
      // Unmount: drop the poll and subscriptions and leave the promise pending (nothing left to update).
      const cancel = () => {
        settled = true;
        clearInterval(timer);
        busService.unsubscribe("sys.module.reinit_succeeded", onOK);
        busService.unsubscribe("sys.module.reinit_failed", onFail);
      };
      this.reinitCancels.add(cancel);
    });
  }

  async runToolbarAction(action) {
    try {
      // type="widget" (list <toolbar>) is pure UI: it opens a registered form
      // widget in the widget host, with no RPC and no record.
      if (action.buttonType === "widget") {
        await this.openToolbarWidget(action);
        return;
      }
      // A model action whose method declares params opens the input popup
      // instead of running: the popup is the confirmation step (its confirm
      // text becomes the description), and only Submit runs the method.
      if (action.boundAction?.actionType === "model" && action.boundAction.params?.length > 0) {
        this.openModelActionParamsDialog(action.boundAction);
        return;
      }
      if (!(await this.confirmAction(action.confirm))) return;
      if (action.boundAction) {
        await this.runBoundModelAction(action.boundAction);
        return;
      }
      if (action.selection) {
        // Needs executeModelMethodOnRecords(model, ids, name) once the list
        // table grows row-selection checkboxes — not built yet.
        this.notify(t("Toolbar action \"{label}\" needs row selection, which the list table doesn't support yet.", { label: action.label }), "warning");
        return;
      }
      const result = await executeModelMethod(this.state.actionModel, action.name);

      // `reload` (explicit <button reload="true"> XML attr) means the action's
      // effect can touch app-wide state beyond this list, so force a full
      // reload instead of re-fetching just this list's rows.
      if (action.reload) {
        this.notify(action.label);
        window.location.reload();
        return;
      }

      await this.load();
      this.notify(result && typeof result === "object" ? t("{label}: {result}", { label: action.label, result: JSON.stringify(result) }) : action.label);
    } catch (error) {
      this.notify(error instanceof Error ? error.message : t("Action failed"), "danger");
    }
  }

  openModelActionParamsDialog(bound) {
    this.state.modelActionParamsDialog = {
      open: true,
      title: bound.name,
      description: bound.confirm,
      params: bound.params,
      bound,
    };
  }

  // submitModelActionParams runs the popup's model action with the collected
  // kwargs. Errors are rethrown so the dialog keeps itself open and shows
  // them (the user can correct the input); success closes it and notifies.
  async submitModelActionParams(kwargs) {
    const bound = this.state.modelActionParamsDialog.bound;
    if (!bound) throw new Error(t("No action selected"));
    await this.runBoundModelAction(bound, kwargs);
  }

  // runBoundModelAction dispatches a sys.model.action Actions-menu entry by
  // its action_type. The backend already resolved the target; permissions
  // are still enforced by the method/model the action points to.
  // `kwargs` holds the params popup's inputs for a model action (empty when
  // the method declares none).
  async runBoundModelAction(bound, kwargs = {}) {
    if (bound.actionType === "model") {
      const result = await executeModelMethod(bound.methodModel, bound.methodName, kwargs);
      await this.load();
      this.notify(result && typeof result === "object" ? t("{label}: {result}", { label: bound.name, result: JSON.stringify(result) }) : bound.name);
      return;
    }
    if (bound.actionType === "window") {
      // Same navigation as an arch type="action" button: open the window
      // action's model in its default views.
      const resModel = String(bound.windowAction?.sys_model ?? "").trim();
      if (!resModel) throw new Error(t("Action \"{name}\" has no target model", { name: bound.name }));
      navigate(`/web/${resModel}`);
      return;
    }
    if (bound.actionType === "client" && bound.clientAction) {
      // A saved form record is the active record; other modes have no row
      // selection, so client actions run without one (open_widget will
      // report that it needs one).
      const activeID = this.isFormMode && !this.isNewRecord ? Number(this.state.route.recordId) : null;
      await this.runClientAction(bound.clientAction, activeID);
      return;
    }
    throw new Error(t("Action \"{name}\" has unsupported type \"{type}\"", { name: bound.name, type: bound.actionType }));
  }

  // openWidgetDialog resolves the row action's form widget (lazy import) and
  // mounts it read-only on the list row, which already carries every model
  // field (fetchModelListRecords reads them all). Only display widgets fit
  // here: there is no form ctx to write back to.
  async openWidgetDialog(action, record) {
    const component = await resolveFormWidget(action.widget);
    if (!component) throw new Error(t("Row action \"{label}\" has an unknown widget \"{widget}\"", { label: action.label, widget: action.widget ?? "" }));
    const heading = record[action.titleField || "display_name"] ?? record.id;
    this.state.widgetDialog = {
      ...CLOSED_WIDGET_HOST,
      open: true,
      heading: String(heading),
      title: action.dialogTitle || action.label,
      component,
      record,
      node: { name: action.widget, attrs: {} },
      ctx: { model: this.state.actionModel, values: record, isReadonly: true, isCreateMode: false },
    };
  }

  // openToolbarWidget mounts a list <toolbar> type="widget" button's form
  // widget with no record: ctx.isToolbar tells the widget it owns the whole
  // interaction (e.g. a create-style builder), and close/closeAndNavigate
  // let it hide the host itself.
  async openToolbarWidget(action) {
    const component = await resolveFormWidget(action.widget);
    if (!component) throw new Error(t("Toolbar action \"{label}\" has an unknown widget \"{widget}\"", { label: action.label, widget: action.widget ?? "" }));
    this.state.widgetDialog = {
      ...CLOSED_WIDGET_HOST,
      open: true,
      size: action.size ?? "",
      title: action.dialogTitle || action.label,
      description: action.description ?? "",
      component,
      record: {},
      node: { name: action.widget, attrs: {} },
      ctx: {
        model: this.state.actionModel,
        values: {},
        isReadonly: false,
        isCreateMode: false,
        isToolbar: true,
        close: () => (this.state.widgetDialog.open = false),
        closeAndNavigate: (route) => this.closeWidgetHostAndNavigate(route),
        // closeAndReload: the widget saved rows server-side (e.g. a published
        // command) — hide the host, then re-fetch the list so they show up.
        closeAndReload: () => this.closeWidgetHostAndReload(),
      },
    };
  }

  // closeWidgetDialog is the host's onOpenChange(false), fired by BaseUI
  // after the modal/offcanvas has fully hidden — the first safe point to run
  // a route change a hosted widget asked for (unmounting mid-fade breaks
  // Bootstrap's teardown, see DeviceActions.confirmDeactivate).
  closeWidgetDialog() {
    this.state.widgetDialog = { ...CLOSED_WIDGET_HOST };
    const route = this.pendingWidgetRoute;
    const reload = this.pendingWidgetReload;
    this.pendingWidgetRoute = "";
    this.pendingWidgetReload = false;
    if (route) navigate(route);
    else if (reload) this.reload();
  }

  // closeWidgetHostAndNavigate is handed to hosted widgets as
  // ctx.closeAndNavigate: start hiding now, navigate once hidden.
  closeWidgetHostAndNavigate(route) {
    this.pendingWidgetRoute = route;
    this.state.widgetDialog.open = false;
  }

  // closeWidgetHostAndReload is ctx.closeAndReload: start hiding now, reload
  // the current view once hidden (same deferral as closeAndNavigate).
  closeWidgetHostAndReload() {
    this.pendingWidgetReload = true;
    this.state.widgetDialog.open = false;
  }

  get widgetHostPanelClass() {
    return widgetHostPanelClass(this.state.widgetDialog.size);
  }

  get widgetHostSheetStyle() {
    const width = SHEET_WIDTHS[this.state.widgetDialog.size];
    return width ? `--bs-offcanvas-width: min(${width}, 100vw);` : "";
  }

  // confirmAction wraps the controlled Dialog's open/onOpenChange state in a
  // Promise so row and toolbar actions can stay linear without falling back
  // to native dialogs. state.confirmMessage doubles as the dialog's open
  // flag (see the Dialog's `open="!!state.confirmMessage"` in ModelView.xml).
  confirmAction(message) {
    if (!message) return Promise.resolve(true);
    this.state.confirmMessage = message;
    return new Promise((resolve) => {
      this.confirmResolver = resolve;
    });
  }

  resolveConfirm(accepted) {
    this.state.confirmMessage = "";
    const resolve = this.confirmResolver;
    this.confirmResolver = null;
    resolve?.(accepted);
  }

  // Delegates to the app's single global toast (Shell.js's fixed top-right
  // overlay) instead of keeping page-local notification state — see
  // base-bus/bus-root.js's showToast() doc comment.
  notify(message, variant = "info") {
    showToast({ message, variant });
  }
}

// View components are added by registerViewComponent and the dialogs by loadModelDialogs (both lazy).
ModelView.components = {
  ControlPanel,
  Alert,
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
  Spinner,
};