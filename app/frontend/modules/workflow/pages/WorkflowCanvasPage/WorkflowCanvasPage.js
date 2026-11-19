// Workflow canvas (Phases 7a-7e). 7a landed the layout view: load the
// persisted action/trigger graph, pan/zoom/reposition nodes, save `layout`.
// 7b added full node CRUD (palette -> replace_action_sequence), the editable
// node/trigger param dialog, and Linear/DAG mode with real base.workflow.edge
// CRUD gated by the server-side validate-graph cycle check. 7c adds test-run
// ("Run Now") + a live run-status overlay (bus subscription + 30s poll),
// export/import, and create/duplicate/delete/rename workflow lifecycle
// actions (ported inline from remote-canvas.tsx's host callbacks — this app
// has no separate host component layer, so the RPCs + navigate() calls live
// directly on this page). 7d adds the "Generate from markdown" dialog. 7e
// adds an inline workflow-switcher selector (WorkflowRecordSelector's Owl
// equivalent — no ported Combobox, so an <input list>+<datalist> pair).
//
// The canvas is hand-built — React Flow has no Owl port — so pan/zoom/drag and
// DAG connect are raw pointer events and edges are SVG <path>s, extending the
// same mechanics 7a established.
import { Component, loadTemplate, onMounted, onPatched, onWillStart, onWillUnmount, onWillUpdateProps, useRef, useState, xml } from "nodexui/owl";

import {
  Alert,
  Badge,
  Button,
  Card,
  Combobox,
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Field,
  Icon,
  Input,
  Sheet,
  SheetContent,
  Switch,
  Tooltip,
} from "nodexui/component";

import { createModelRpc, fetchModelFormRecord, fetchRelationChoices, searchRelationChoices } from "@/services/api/jsonv2.js";
import { navigate } from "@/router/navigation.js";
import { busService } from "@/modules/base-bus/index.js";

import {
  RPC_CONFLICT,
  RPC_INVALID_PARAMS,
  createWorkflowEdge,
  deleteWorkflowEdge,
  fetchTriggerHealth,
  fetchWorkflowActionCatalog,
  fetchWorkflowActions,
  fetchWorkflowEdges,
  fetchWorkflowExport,
  fetchLatestWorkflowID,
  fetchWorkflowLatestRun,
  fetchWorkflowNodeSpecs,
  fetchWorkflowTriggers,
  fetchWorkflowVersionState,
  importWorkflowExport,
  publishWorkflow,
  replaceWorkflowActionSequence,
  unlinkWorkflowAction,
  updateWorkflowEdge,
  validateWorkflow,
  validateWorkflowGraph,
  workflowErrorInfo,
} from "@/modules/workflow/api.js";
import {
  DAG_EDGE_TYPE_LABELS,
  DEFAULT_DAG_FAILURE_POLICY,
  DEFAULT_MAX_PARALLEL_STEPS,
  TRIGGER_NODE_ID,
  buildActionNodeData,
  buildCanvasEdges,
  buildCanvasNodes,
  buildLayoutPayload,
  buildReplaceActionSequenceItems,
  buildTriggerNodeData,
  createDraftAction,
  isActionConfigured,
  isTriggerNodeID,
  isWorkflowRevisionConflict,
  nodeIDForStepKey,
  normalizeLayout,
  resolveTriggerDraft,
  stepKeyForNodeID,
  syncWorkflowTriggerState,
  triggerDraftChanged,
  upsertWorkflowTriggerRow,
} from "@/modules/workflow/canvas-graph.js";
import { DagSettingsDialog } from "@/modules/workflow/components/DagSettingsDialog.js";
import { EdgeTypeDialog } from "@/modules/workflow/components/EdgeTypeDialog.js";
import { GenActionFlowDialog } from "@/modules/workflow/components/GenActionFlowDialog.js";
import { NodeParamDialog } from "@/modules/workflow/components/NodeParamDialog.js";
import { notifySuccess, reportWorkflowError } from "@/modules/workflow/components/notify.js";
import { healthByTrigger, healthText, healthTone } from "@/modules/workflow/components/trigger-health.js";
import { ValidationPanel, issueText } from "@/modules/workflow/components/ValidationPanel.js";
import {
  describeActionConfig,
  describeTriggerConfig,
} from "@/modules/workflow/node-params.js";

const template = await loadTemplate(new URL("./WorkflowCanvasPage.xml", import.meta.url));

const NODE_WIDTH = 220;
const NODE_HEIGHT = 92;
const ZOOM_MIN = 0.4;
const ZOOM_MAX = 2;
const DRAG_THRESHOLD = 2;
// Delay before opening a panel after another one closes: a NodexUI Dialog/Sheet must finish its
// hide transition before the next one shows (or the page navigates).
const PANEL_SWAP_MS = 400;

// loadCanvasPanels downloads the rarely used panels (run history, test run, templates, trigger
// list) once and registers them on the page; their template block renders under state.panelsReady.
let canvasPanelsPromise = null;
function loadCanvasPanels() {
  canvasPanelsPromise ??= import("@/modules/workflow/components/canvas-panels.js").then((panels) => {
    Object.assign(WorkflowCanvasPage.components, panels);
  });
  return canvasPanelsPromise;
}

export class WorkflowCanvasPage extends Component {
  static template = xml({ raw: [template] });
  static components = {
    Alert,
    Button,
    Card,
    Combobox,
    Dialog,
    DialogContent,
    DialogFooter,
    DialogHeader,
    DialogTitle,
    Field,
    Icon,
    Input,
    Sheet,
    SheetContent,
    Switch,
    Tooltip,
    Badge,
    NodeParamDialog,
    DagSettingsDialog,
    EdgeTypeDialog,
    GenActionFlowDialog,
    ValidationPanel,
  };

  setup() {
    this.state = useState({
      loading: true,
      error: "",
      saving: false,
      dirty: false,
      // base.workflow.revision as loaded; sent as expected_revision so a save over someone
      // else's newer save is refused instead of silently overwriting it (plan T59).
      revision: null,
      workflowName: "",
      workflowTopic: "",
      workflowGroupID: null,
      workflowRecord: null,
      isActive: false,
      togglingActive: false,
      executionMode: "linear",
      maxParallelSteps: DEFAULT_MAX_PARALLEL_STEPS,
      dagFailurePolicy: DEFAULT_DAG_FAILURE_POLICY,
      togglingMode: false,
      specs: { triggers: [], actions: [] },
      catalog: [],
      triggerRecords: [],
      nodes: [],
      actionOrder: [],
      dagEdges: [],
      cyclicStepKeys: [],
      viewport: { x: 0, y: 0, zoom: 1 },
      selectedNodeID: null,
      connect: null,
      paramDialog: { open: false, node: null },
      dagSettings: { open: false, saving: false, maxParallelSteps: DEFAULT_MAX_PARALLEL_STEPS, dagFailurePolicy: DEFAULT_DAG_FAILURE_POLICY },
      edgeDialog: { edge: null, saving: false },
      // 7c/7d/7e additions below.
      triggerSampleJSON: "",
      latestRunStatus: null,
      exportingWorkflow: false,
      importingWorkflow: false,
      creatingWorkflow: false,
      duplicatingWorkflow: false,
      deletingWorkflow: false,
      deleteWorkflowConfirmOpen: false,
      renameOpen: false,
      renameValue: "",
      renaming: false,
      genFlowOpen: false,
      workflowSelectorOptions: [],
      // Controls the quick-view Sheet opened by a node's "View" icon button
      // (see openNodeDetailSheet) — the node detail/actions view on every
      // screen size.
      mobileDetailOpen: false,
      // Controls the Actions-palette Sheet opened by the header button next
      // to Save — the inline palette aside is hidden on mobile, so this is
      // the only way to add an action node there.
      actionsSheetOpen: false,
      // Phase 6: publication badge (T97), validation (T98), trigger health (T99), lazy panels.
      versionState: null,
      publishing: false,
      validation: { issues: [], panelOpen: false, running: false },
      triggerHealth: {},
      panelsReady: false,
      latestRunID: 0,
      runInspector: { open: false, runID: 0 },
      testRunOpen: false,
      templatesOpen: false,
      triggerPanelOpen: false,
    });
    // Pending "open the next panel" timer (see PANEL_SWAP_MS), cleared on unmount.
    this.panelSwapTimer = null;
    this.viewportRef = useRef("canvasViewport");
    this.importFileInput = useRef("importFileInput");
    this.drag = null; // { kind: "pan"|"node"|"connect", ... }
    // Synchronous re-entrancy guard: the `saving` state flag updates
    // asynchronously, so a second delete arriving before the first render
    // commits would re-run unlink against an already-removed row.
    this.isDeletingAction = false;
    // Run-status polling/bus-subscription handles — torn down and rebuilt
    // whenever the workflow topic changes (see setupRunStatusPolling), and
    // always torn down on unmount. This module was already bitten once by a
    // leaked pointer-listener bug (see onWillUnmount below), so the same
    // explicit-cleanup discipline applies here.
    this.runPollTimer = null;
    this.runPollChannel = null;
    this.runStepEventHandler = null;
    this.runPollCancelled = true;

    this.onPointerMove = this.onPointerMove.bind(this);
    this.onPointerUp = this.onPointerUp.bind(this);
    this.onBeforeUnload = this.onBeforeUnload.bind(this);
    this.onDocumentLinkClick = this.onDocumentLinkClick.bind(this);
    this.onPointerDown = this.onPointerDown.bind(this);
    this.onWheel = this.onWheel.bind(this);
    // The viewport element the pointerdown/wheel listeners are bound to. The body (and so the
    // viewport) is re-created after every load, so onPatched re-binds when the element changed.
    this.boundViewportEl = null;

    // The workflow this page shows and every action targets. It is the route param, but kept in
    // a field (not read from this.props) because AppRoot reuses this page instance when only the
    // :workflowId param changes (New workflow, Duplicate, Use template, switcher): the field is
    // switched in onWillUpdateProps before the reload, so no action can hit the old id.
    this.routeWorkflowId = this.props.params.workflowId;
    // Incremented by every load(); an older load that resolves late drops its results.
    this.loadSeq = 0;

    onWillStart(() => this.load());
    onWillUpdateProps((nextProps) => {
      const nextWorkflowId = nextProps.params.workflowId;
      if (nextWorkflowId === this.routeWorkflowId) return;
      this.routeWorkflowId = nextWorkflowId;
      this.resetWorkflowState();
      // Not awaited: the "Loading workflow…" state must render now (it disables the toolbar)
      // instead of the previous workflow staying on screen until the new one has loaded.
      void this.load();
    });
    onPatched(() => this.attachPointerHandlers());
    onMounted(() => {
      this.attachPointerHandlers();
      // Unsaved-changes guard (plan T60): a tab close/reload asks through the browser prompt; an
      // in-app link click is caught in the capture phase, before main.js's body listener routes it.
      window.addEventListener("beforeunload", this.onBeforeUnload);
      document.addEventListener("click", this.onDocumentLinkClick, true);
      void this.loadWorkflowSelectorOptions("");
      const whenIdle = window.requestIdleCallback ?? ((callback) => setTimeout(callback, 200));
      whenIdle(() => void this.ensurePanels());
    });
    // pointermove/pointerup live on window so a gesture can continue past the
    // viewport edges — Owl only tears down DOM it owns, so these must be
    // removed explicitly or they leak into the destroyed component's state.
    onWillUnmount(() => {
      window.removeEventListener("pointermove", this.onPointerMove);
      window.removeEventListener("pointerup", this.onPointerUp);
      window.removeEventListener("beforeunload", this.onBeforeUnload);
      document.removeEventListener("click", this.onDocumentLinkClick, true);
      this.teardownRunStatusPolling();
      clearTimeout(this.panelSwapTimer);
    });
  }

  // ------------------------------------------------------- unsaved changes

  onBeforeUnload(ev) {
    if (!this.state.dirty) return;
    ev.preventDefault();
    ev.returnValue = "";
  }

  // confirmDiscardChanges asks before leaving a dirty canvas; true = go ahead.
  confirmDiscardChanges() {
    if (!this.state.dirty) return true;
    if (!window.confirm(t("You have unsaved changes on this canvas. Leave without saving?"))) return false;
    this.state.dirty = false;
    return true;
  }

  // onDocumentLinkClick stops an in-app link (sidebar, breadcrumbs...) from unmounting a dirty
  // canvas unless the operator confirms. Browser back/forward cannot be cancelled here.
  onDocumentLinkClick(ev) {
    if (!this.state.dirty || ev.defaultPrevented || ev.button !== 0 || ev.metaKey || ev.ctrlKey || ev.shiftKey) return;
    const link = ev.target.closest ? ev.target.closest("a[href]") : null;
    if (!link || link.target === "_blank") return;
    const href = link.getAttribute("href") ?? "";
    if (!href.startsWith("/") || href === window.location.pathname) return;
    if (this.confirmDiscardChanges()) return;
    ev.preventDefault();
    ev.stopPropagation();
  }

  // leaveTo navigates away from the canvas (workflow switch, duplicate, import...) after the
  // unsaved-changes check.
  leaveTo(route) {
    if (!this.confirmDiscardChanges()) return false;
    navigate(route);
    return true;
  }

  get workflowID() {
    return Number(this.routeWorkflowId);
  }

  // resetWorkflowState drops everything that belongs to the workflow being left (open dialogs,
  // selection, run overlay, a gesture in progress) before another workflow loads into this page.
  resetWorkflowState() {
    this.teardownRunStatusPolling();
    clearTimeout(this.panelSwapTimer);
    this.panelSwapTimer = null;
    this.drag = null;
    Object.assign(this.state, {
      dirty: false,
      error: "",
      selectedNodeID: null,
      connect: null,
      paramDialog: { open: false, node: null },
      dagSettings: { ...this.state.dagSettings, open: false, saving: false },
      edgeDialog: { edge: null, saving: false },
      triggerSampleJSON: "",
      latestRunStatus: null,
      deleteWorkflowConfirmOpen: false,
      renameOpen: false,
      genFlowOpen: false,
      mobileDetailOpen: false,
      actionsSheetOpen: false,
      runInspector: { open: false, runID: 0 },
      testRunOpen: false,
      templatesOpen: false,
      triggerPanelOpen: false,
    });
  }

  // resolveWorkflowID returns the id to load: the URL param if present, otherwise the most
  // recently created workflow the user can read (id 1 used to be hard-coded: it 404s once that
  // workflow is deleted or belongs to another company). No workflow at all opens the list.
  async resolveWorkflowID() {
    if (this.routeWorkflowId) return this.workflowID;
    const id = await fetchLatestWorkflowID();
    navigate(id > 0 ? `/web/base.workflow/canvas/${id}` : "/web/base.workflow");
    return id;
  }

  get selectedNode() {
    return this.state.nodes.find((node) => node.id === this.state.selectedNodeID) ?? null;
  }

  // selectedActionDetail feeds the "View details" sheet's "What it does"
  // section: the spec description (fallback when the row has none), the
  // configured params (a lua_script@v1 node lists its custom action and data).
  get selectedActionDetail() {
    const node = this.selectedNode;
    if (!node || node.kind !== "action") return null;
    const action = node.action;
    const spec = this.state.specs.actions.find((item) => item.code === action.action_code);
    return {
      description: action.description || spec?.description || "",
      configRows: describeActionConfig(action, spec?.params),
    };
  }

  get worldTransform() {
    const { x, y, zoom } = this.state.viewport;
    return `translate(${x}px, ${y}px) scale(${zoom})`;
  }

  get isDag() {
    return this.state.executionMode === "dag";
  }

  // orderedStepKeys are the action step keys in run order (targets for "On error, continue at").
  get orderedStepKeys() {
    const nodeMap = new Map(this.state.nodes.map((node) => [node.id, node]));
    return this.state.actionOrder.map((id) => nodeMap.get(id)?.action?.step_key).filter(Boolean);
  }

  get actionSpecs() {
    // parse_payload is an implicit backend step, never a palette entry.
    return this.state.specs.actions.filter((spec) => spec.code !== "parse_payload");
  }

  // hasWebhookRow: the workflow has an explicit webhook trigger row (managed in the trigger list).
  get hasWebhookRow() {
    return this.state.triggerRecords.some((row) => row.type === "webhook");
  }

  get currentTrigger() {
    const node = this.state.nodes.find((item) => item.id === TRIGGER_NODE_ID);
    return node?.trigger ?? null;
  }

  // edges is the render model: linear mode derives the chain from actionOrder
  // (no backend rows involved), DAG mode maps the persisted base.workflow.edge
  // rows onto node ids.
  get edges() {
    if (!this.isDag) {
      return buildCanvasEdges(this.state.actionOrder).map((edge) => ({ ...edge, kind: "linear" }));
    }
    const cyclic = new Set(this.state.cyclicStepKeys);
    return this.state.dagEdges.map((dagEdge) => ({
      id: `edge:${dagEdge.id}`,
      edgeID: dagEdge.id,
      kind: "dag",
      source: nodeIDForStepKey(dagEdge.from_step_key),
      target: nodeIDForStepKey(dagEdge.to_step_key),
      edgeType: dagEdge.edge_type,
      label: dagEdge.edge_type === "success" ? "" : (DAG_EDGE_TYPE_LABELS[dagEdge.edge_type] ?? dagEdge.edge_type),
      isCyclic: cyclic.has(dagEdge.from_step_key) && cyclic.has(dagEdge.to_step_key),
    }));
  }

  isNodeCyclic(node) {
    if (node.kind !== "action") return false;
    return this.state.cyclicStepKeys.includes(node.action.step_key);
  }

  async load() {
    // A newer load (another workflow opened meanwhile) owns the page; this one stops writing.
    const seq = ++this.loadSeq;
    const isStale = () => seq !== this.loadSeq;
    this.state.loading = true;
    this.state.error = "";
    // Phase 6 per-workflow state, re-read below for the workflow being opened.
    this.state.versionState = null;
    this.state.validation = { issues: [], panelOpen: false, running: false };
    this.state.triggerHealth = {};
    this.state.latestRunID = 0;
    try {
      const workflowID = await this.resolveWorkflowID();
      if (!this.routeWorkflowId || isStale()) return;
      const formRecord = await fetchModelFormRecord("base.workflow", workflowID);
      if (isStale()) return;
      const record = formRecord.record ?? {};
      this.state.workflowRecord = record;
      this.state.workflowName = String(record.name ?? t("Workflow #{id}", { id: workflowID }));
      this.state.workflowTopic = typeof record.topic === "string" && record.topic.trim() ? record.topic : "workflow.topic";
      this.state.workflowGroupID = record.group_id ?? null;
      // base.workflow's run switch is is_active (`active` is the archive flag).
      this.state.isActive = Boolean(record.is_active);
      this.state.revision = Number.isInteger(record.revision) ? record.revision : null;
      this.state.executionMode = record.execution_mode === "dag" ? "dag" : "linear";
      this.state.maxParallelSteps = Number(record.max_parallel_steps) > 0 ? Number(record.max_parallel_steps) : DEFAULT_MAX_PARALLEL_STEPS;
      this.state.dagFailurePolicy = record.dag_failure_policy === "fail_workflow" ? "fail_workflow" : DEFAULT_DAG_FAILURE_POLICY;

      await this.loadCanvas(normalizeLayout(record.layout), isStale);
      if (isStale()) return;
      this.setupRunStatusPolling();
      void this.refreshVersionState();
      void this.refreshTriggerHealth();
    } catch (error) {
      if (!isStale()) this.state.error = error instanceof Error ? error.message : t("Failed to load workflow canvas");
    } finally {
      if (!isStale()) this.state.loading = false;
    }
  }

  // loadCanvas rebuilds the node/edge graph from the backend. Switching
  // linear->dag never auto-creates edges: dagEdges only ever populates from an
  // explicit fetchWorkflowEdges, so a workflow with actions but no edge rows
  // simply renders disconnected until the operator draws connections.
  // isStale (from load) drops the result when another workflow was opened meanwhile.
  async loadCanvas(layout, isStale = () => false) {
    const workflowID = this.workflowID;
    const [specs, actions, triggers, catalog, dagEdges] = await Promise.all([
      fetchWorkflowNodeSpecs(),
      fetchWorkflowActions(workflowID),
      fetchWorkflowTriggers(workflowID),
      fetchWorkflowActionCatalog(),
      this.isDag ? fetchWorkflowEdges(workflowID) : Promise.resolve([]),
    ]);
    if (isStale() || workflowID !== this.workflowID) return;
    const nodes = buildCanvasNodes({
      topic: this.state.workflowTopic,
      groupID: this.state.workflowGroupID,
      actions,
      triggers,
      specs,
      layout,
    });
    this.state.specs = specs;
    this.state.catalog = catalog;
    this.state.triggerRecords = triggers;
    this.state.dagEdges = dagEdges;
    this.state.cyclicStepKeys = [];
    this.state.nodes = nodes;
    this.state.actionOrder = nodes.filter((node) => node.kind === "action").map((node) => node.id);
    this.state.viewport = layout.viewport;
    if (!nodes.some((node) => node.id === this.state.selectedNodeID)) this.state.selectedNodeID = null;
    this.state.dirty = false;
  }

  async refresh() {
    if (!this.confirmDiscardChanges()) return;
    await this.load();
  }

  // ---------------------------------------------------------------- pointer

  // attachPointerHandlers binds pan/zoom/drag to the current viewport element. It runs after
  // mount and after every patch: a reload ("Loading workflow…") replaces the viewport element,
  // and the new one has no listeners until re-bound here (same function refs, so the window
  // listeners are never added twice).
  attachPointerHandlers() {
    const el = this.viewportRef.el;
    if (!el || el === this.boundViewportEl) return;
    this.boundViewportEl = el;
    el.addEventListener("pointerdown", this.onPointerDown);
    window.addEventListener("pointermove", this.onPointerMove);
    window.addEventListener("pointerup", this.onPointerUp);
    el.addEventListener("wheel", this.onWheel, { passive: false });
  }

  // toWorld converts a client-space point into unscaled canvas coordinates —
  // needed for palette drops and for the in-flight DAG connection line.
  toWorld(clientX, clientY) {
    const rect = this.viewportRef.el?.getBoundingClientRect();
    if (!rect) return { x: 0, y: 0 };
    const { x, y, zoom } = this.state.viewport;
    return { x: (clientX - rect.left - x) / (zoom || 1), y: (clientY - rect.top - y) / (zoom || 1) };
  }

  onPointerDown(ev) {
    const handleEl = ev.target.closest?.("[data-workflow-handle]");
    if (handleEl && this.isDag) {
      const nodeID = handleEl.getAttribute("data-workflow-node-id");
      const point = this.toWorld(ev.clientX, ev.clientY);
      this.drag = { kind: "connect", nodeID };
      this.state.connect = { sourceID: nodeID, x: point.x, y: point.y };
      return;
    }
    const nodeEl = ev.target.closest?.("[data-workflow-node-id]");
    if (nodeEl) {
      const nodeID = nodeEl.getAttribute("data-workflow-node-id");
      const node = this.state.nodes.find((item) => item.id === nodeID);
      if (!node) return;
      this.state.selectedNodeID = nodeID;
      this.drag = {
        kind: "node",
        nodeID,
        startClientX: ev.clientX,
        startClientY: ev.clientY,
        startX: node.x,
        startY: node.y,
        moved: false,
      };
      return;
    }
    // Clicking empty canvas deselects, matching React Flow's onPaneClick.
    this.state.selectedNodeID = null;
    this.drag = {
      kind: "pan",
      startClientX: ev.clientX,
      startClientY: ev.clientY,
      startX: this.state.viewport.x,
      startY: this.state.viewport.y,
      moved: false,
    };
  }

  onPointerMove(ev) {
    if (!this.drag) return;
    if (this.drag.kind === "connect") {
      const point = this.toWorld(ev.clientX, ev.clientY);
      this.state.connect = { ...this.state.connect, x: point.x, y: point.y };
      return;
    }
    const dx = ev.clientX - this.drag.startClientX;
    const dy = ev.clientY - this.drag.startClientY;
    if (Math.abs(dx) > DRAG_THRESHOLD || Math.abs(dy) > DRAG_THRESHOLD) this.drag.moved = true;
    if (this.drag.kind === "pan") {
      this.state.viewport = { ...this.state.viewport, x: this.drag.startX + dx, y: this.drag.startY + dy };
      return;
    }
    // node drag: screen-space delta / zoom, since node x/y are world coords.
    const zoom = this.state.viewport.zoom || 1;
    const node = this.state.nodes.find((item) => item.id === this.drag.nodeID);
    if (!node) return;
    node.x = this.drag.startX + dx / zoom;
    node.y = this.drag.startY + dy / zoom;
  }

  onPointerUp(ev) {
    const drag = this.drag;
    this.drag = null;
    if (!drag) return;
    if (drag.kind === "connect") {
      this.state.connect = null;
      const targetEl = document.elementFromPoint(ev.clientX, ev.clientY)?.closest?.("[data-workflow-node-id]");
      const targetID = targetEl?.getAttribute("data-workflow-node-id");
      if (targetID && targetID !== drag.nodeID) void this.connectDagEdge(drag.nodeID, targetID);
      return;
    }
    // Both node reposition and pan are persisted inside `layout`, so either
    // gesture marks the canvas dirty.
    if (drag.moved) this.state.dirty = true;
  }

  onWheel(ev) {
    ev.preventDefault();
    const nextZoom = Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, this.state.viewport.zoom - ev.deltaY * 0.001));
    this.state.viewport = { ...this.state.viewport, zoom: nextZoom };
    this.state.dirty = true;
  }
  // selectedTriggerDetail feeds the same sheet for the trigger node: the
  // trigger type's configured params (QoS, consumer group, model event…)
  // resolved against its spec so defaults show their effective value.
  get selectedTriggerDetail() {
    const node = this.selectedNode;
    if (!node || node.kind !== "trigger") return null;
    const spec = this.state.specs.triggers.find((item) => item.type === node.trigger.type);
    return { configRows: describeTriggerConfig(node.trigger, spec?.params) };
  }

  selectNode(nodeID) {
    this.state.selectedNodeID = nodeID;
  }

  // openNodeDetailSheet/setMobileDetailOpen control the quick-view Sheet
  // opened by a node's "View" icon button — selects the node (so the sheet's
  // body, which reads `this.selectedNode`, shows the right one) then opens.
  openNodeDetailSheet(nodeID) {
    this.state.selectedNodeID = nodeID;
    this.state.mobileDetailOpen = true;
  }

  setMobileDetailOpen(open) {
    this.state.mobileDetailOpen = open;
  }

  // openActionsSheet/setActionsSheetOpen control the Actions-palette Sheet
  // (header button next to Save) — the substitute for the inline palette
  // aside, which .workflow-canvas-palette hides on mobile.
  openActionsSheet() {
    this.state.actionsSheetOpen = true;
  }

  setActionsSheetOpen(open) {
    this.state.actionsSheetOpen = open;
  }

  // ----------------------------------------------------------------- geometry

  nodeAnchor(nodeID, side) {
    const node = this.state.nodes.find((item) => item.id === nodeID);
    if (!node) return null;
    return { x: side === "source" ? node.x + NODE_WIDTH : node.x, y: node.y + NODE_HEIGHT / 2 };
  }

  edgePath(edge) {
    const source = this.nodeAnchor(edge.source, "source");
    const target = this.nodeAnchor(edge.target, "target");
    if (!source || !target) return "";
    return bezierPath(source, target);
  }

  edgeMidpoint(edge) {
    const source = this.nodeAnchor(edge.source, "source");
    const target = this.nodeAnchor(edge.target, "target");
    if (!source || !target) return null;
    return { x: (source.x + target.x) / 2, y: (source.y + target.y) / 2 };
  }

  edgeMidpointStyle(edge) {
    const mid = this.edgeMidpoint(edge);
    if (!mid) return "display: none;";
    return `left: ${mid.x}px; top: ${mid.y}px;`;
  }

  connectPath() {
    const connect = this.state.connect;
    if (!connect) return "";
    const source = this.nodeAnchor(connect.sourceID, "source");
    if (!source) return "";
    return bezierPath(source, { x: connect.x, y: connect.y });
  }

  // ------------------------------------------------------------- node CRUD

  onPaletteDragStart(spec, ev) {
    ev.dataTransfer.effectAllowed = "move";
    ev.dataTransfer.setData("application/basehub-workflow-action", JSON.stringify(spec));
  }

  onCanvasDragOver(ev) {
    ev.preventDefault();
    ev.dataTransfer.dropEffect = "move";
  }

  onCanvasDrop(ev) {
    ev.preventDefault();
    const raw = ev.dataTransfer.getData("application/basehub-workflow-action");
    if (!raw) return;
    let spec;
    try {
      spec = JSON.parse(raw);
    } catch {
      return;
    }
    spec = { ...spec, params: Array.isArray(spec.params) ? spec.params : [] };
    this.appendActionNode(spec, this.toWorld(ev.clientX, ev.clientY));
  }

  // appendActionNode seeds a new node entirely from its catalog/node spec.
  // The node stays local (dirty) until the next save — same as the React
  // canvas, which only persists on param save/delete/explicit save.
  appendActionNode(spec, position) {
    const draft = createDraftAction(spec);
    const node = {
      id: draft.nodeId,
      ...buildActionNodeData(draft, spec, isActionConfigured(draft, spec)),
      x: position.x,
      y: position.y,
    };
    this.state.nodes = [...this.state.nodes, node];
    this.state.actionOrder = [...this.state.actionOrder, node.id];
    this.state.selectedNodeID = node.id;
    this.state.dirty = true;
  }

  addActionFromPalette(spec) {
    const actionCount = this.state.nodes.filter((node) => node.kind === "action").length;
    this.appendActionNode(spec, { x: 340 + actionCount * 260, y: 180 });
  }

  // Same as addActionFromPalette, but also closes the Actions Sheet — used
  // by that Sheet's "Add" button so picking an action returns to the canvas.
  addActionFromPaletteAndCloseSheet(spec) {
    this.addActionFromPalette(spec);
    this.state.actionsSheetOpen = false;
  }

  openNodeDialog(nodeID) {
    const node = this.state.nodes.find((item) => item.id === nodeID);
    if (!node) return;
    this.state.selectedNodeID = nodeID;
    this.state.paramDialog = { open: true, node, rowEdit: false };
  }

  // setParamDialogOpen closes the parameter dialog; one opened from the trigger list goes back to it.
  setParamDialogOpen(open) {
    const rowEdit = Boolean(this.state.paramDialog.rowEdit);
    this.state.paramDialog = { open, node: open ? this.state.paramDialog.node : null, rowEdit: open && rowEdit };
    if (!open && rowEdit) this.afterPanelSwap(() => (this.state.triggerPanelOpen = true));
  }

  // onParamDialogSaveTrigger routes a trigger save: a row from the trigger list is saved as that
  // row, the canvas trigger node through the canvas save path.
  async onParamDialogSaveTrigger(trigger) {
    if (this.state.paramDialog.rowEdit) {
      await this.saveTriggerRow(trigger);
      return;
    }
    await this.saveTrigger(trigger);
  }

  // saveNode applies the dialog's edited draft to the local node, then routes
  // through the shared persist path so the sequence + layout land together.
  async saveNode(nextAction) {
    const spec = this.state.specs.actions.find((item) => item.code === nextAction.action_code);
    this.state.nodes = this.state.nodes.map((node) => {
      if (node.id !== nextAction.nodeId || node.kind !== "action") return node;
      return {
        ...node,
        ...buildActionNodeData(nextAction, spec, spec ? isActionConfigured(nextAction, spec) : true),
        x: node.x,
        y: node.y,
      };
    });
    await this.persistCanvasState({ syncTrigger: false });
  }

  // deleteActionNode unlinks the persisted row immediately (not deferred to
  // save), then re-runs the shared persist path so the remaining sequence
  // reloads from the backend rather than trusting filtered local state.
  async deleteActionNode(action) {
    if (this.isDeletingAction) return;
    this.isDeletingAction = true;
    try {
      this.state.error = "";
      if ((action.id ?? 0) > 0) {
        const ok = await unlinkWorkflowAction(action.id);
        if (!ok) throw new Error(t("Failed to delete workflow action."));
      }
      this.state.nodes = this.state.nodes.filter((node) => node.id !== action.nodeId);
      this.state.actionOrder = this.state.actionOrder.filter((nodeID) => nodeID !== action.nodeId);
      if (this.state.selectedNodeID === action.nodeId) this.state.selectedNodeID = null;
      await this.persistCanvasState({ syncTrigger: false });
    } catch (error) {
      this.state.error = error instanceof Error ? error.message : t("Failed to delete workflow action.");
    } finally {
      this.isDeletingAction = false;
    }
  }

  // ---------------------------------------------------------- trigger edits

  async saveTrigger(nextTrigger) {
    const workflowID = this.workflowID;
    const rpc = createModelRpc("base.workflow");
    const nextTopic = nextTrigger.type === "webhook" ? nextTrigger.topic : this.state.workflowTopic;
    const values = {};
    if (nextTrigger.type === "webhook") values.topic = nextTrigger.topic;

    if (Object.keys(values).length > 0) {
      const written = await rpc.callById("write", workflowID, [values]);
      if (!written.result) throw new Error(t("Failed to save workflow trigger settings."));
    }

    await syncWorkflowTriggerState(workflowID, nextTrigger, this.state.triggerRecords);
    // Always re-read: the webhook row (kept for its signature config) is part of the state too.
    const refreshedTriggers = await fetchWorkflowTriggers(workflowID);
    const refreshedDraft = resolveTriggerDraft(
      nextTopic,
      this.state.workflowGroupID,
      refreshedTriggers,
      this.state.specs.triggers,
    );

    this.state.workflowTopic = nextTopic;
    this.state.triggerRecords = refreshedTriggers;
    this.state.nodes = this.state.nodes.map((node) => {
      if (node.id !== TRIGGER_NODE_ID) return node;
      return { ...node, ...buildTriggerNodeData(refreshedDraft, nextTopic, this.state.specs.triggers), x: node.x, y: node.y };
    });
    // Trigger rows were just written, so the follow-up canvas save must skip
    // the trigger sync — replaying stale triggerRecords would unlink the row
    // that was just created.
    await this.persistCanvasState({ syncTrigger: false });
  }

  // --------------------------------------------------------------- DAG mode

  async toggleExecutionMode(nextMode) {
    if (nextMode === this.state.executionMode || this.state.togglingMode) return;
    this.state.togglingMode = true;
    this.state.error = "";
    try {
      // execution_mode is a plain 2-value select — a direct write, not a
      // toggle-only method like base.workflow.active.
      const response = await createModelRpc("base.workflow").callById("write", this.workflowID, [{ execution_mode: nextMode }]);
      if (!response.result) throw new Error(t("Failed to update workflow execution mode."));
      this.state.executionMode = nextMode;
      this.state.cyclicStepKeys = [];
      this.state.dagEdges = nextMode === "dag" ? await fetchWorkflowEdges(this.workflowID) : [];
      void this.refreshVersionState();
    } catch (error) {
      this.state.error = error instanceof Error ? error.message : t("Failed to update workflow execution mode.");
    } finally {
      this.state.togglingMode = false;
    }
  }

  async toggleActive() {
    if (this.state.togglingActive) return;
    this.state.togglingActive = true;
    this.state.error = "";
    try {
      // The switch drives is_active (whether the workflow runs). toggle_active would flip the
      // archive flag `active` instead and hide the workflow from every list. set_active is the
      // operator-level method (a generic write needs the author role).
      const nextActive = !this.state.isActive;
      const response = await createModelRpc("base.workflow").callById("set_active", this.workflowID, [], { active: nextActive });
      if (!response.result) throw new Error(t("Failed to update workflow active state."));
      this.state.isActive = nextActive;
    } catch (error) {
      this.state.error = reportWorkflowError(error, t("Failed to update workflow active state."));
    } finally {
      this.state.togglingActive = false;
    }
  }

  openDagSettings() {
    this.state.dagSettings = {
      open: true,
      saving: false,
      maxParallelSteps: this.state.maxParallelSteps,
      dagFailurePolicy: this.state.dagFailurePolicy,
    };
  }

  setDagSettingsOpen(open) {
    this.state.dagSettings = { ...this.state.dagSettings, open };
  }

  async saveDagSettings(maxParallelSteps, failurePolicy) {
    this.state.dagSettings = { ...this.state.dagSettings, saving: true };
    this.state.error = "";
    try {
      const response = await createModelRpc("base.workflow").callById("write", this.workflowID, [
        { max_parallel_steps: maxParallelSteps, dag_failure_policy: failurePolicy },
      ]);
      if (!response.result) throw new Error(t("Failed to update DAG settings."));
      this.state.maxParallelSteps = maxParallelSteps;
      this.state.dagFailurePolicy = failurePolicy;
      this.state.dagSettings = { ...this.state.dagSettings, open: false, saving: false };
      void this.refreshVersionState();
    } catch (error) {
      this.state.error = error instanceof Error ? error.message : t("Failed to update DAG settings.");
      this.state.dagSettings = { ...this.state.dagSettings, saving: false };
    }
  }

  // connectDagEdge creates a real base.workflow.edge row immediately —
  // decoupled from actionOrder, unlike a linear-mode connector.
  async connectDagEdge(sourceNodeID, targetNodeID) {
    // The trigger node is a canvas-only placeholder, not a base.workflow.action
    // row: an edge referencing it has no step to execute. Action steps with no
    // incoming edge are already implicit DAG roots.
    if (isTriggerNodeID(sourceNodeID) || isTriggerNodeID(targetNodeID)) {
      this.state.error = t("The trigger node cannot be connected as a DAG step.");
      return;
    }
    const fromStepKey = stepKeyForNodeID(sourceNodeID);
    const toStepKey = stepKeyForNodeID(targetNodeID);
    if (this.state.dagEdges.some((edge) => edge.from_step_key === fromStepKey && edge.to_step_key === toStepKey)) return;
    this.state.error = "";
    try {
      // An edge may only name saved steps (the server refuses the rest): save new nodes first.
      const unsaved = [sourceNodeID, targetNodeID].some((nodeID) => {
        const node = this.state.nodes.find((item) => item.id === nodeID);
        return node?.kind === "action" && !(Number(node.action.id) > 0);
      });
      if (unsaved) await this.persistCanvasState({ syncTrigger: false });
      const id = await createWorkflowEdge(this.workflowID, fromStepKey, toStepKey);
      this.state.dagEdges = [
        ...this.state.dagEdges,
        {
          id,
          workflow_id: this.workflowID,
          from_step_key: fromStepKey,
          to_step_key: toStepKey,
          edge_type: "success",
          condition_expr: "",
        },
      ];
      void this.refreshVersionState();
    } catch (error) {
      this.state.error = error instanceof Error ? error.message : t("Failed to create edge.");
    }
  }

  async deleteDagEdge(edgeID) {
    this.state.error = "";
    try {
      await deleteWorkflowEdge(edgeID);
      this.state.dagEdges = this.state.dagEdges.filter((edge) => edge.id !== edgeID);
      void this.refreshVersionState();
    } catch (error) {
      this.state.error = error instanceof Error ? error.message : t("Failed to delete edge.");
    }
  }

  openEdgeDialog(edgeID) {
    const edge = this.state.dagEdges.find((item) => item.id === edgeID);
    if (!edge) return;
    this.state.edgeDialog = { edge, saving: false };
  }

  setEdgeDialogOpen(open) {
    if (!open) this.state.edgeDialog = { edge: null, saving: false };
  }

  async saveEdgeType(edgeID, edgeType, conditionExpr) {
    this.state.edgeDialog = { ...this.state.edgeDialog, saving: true };
    this.state.error = "";
    try {
      await updateWorkflowEdge(edgeID, { edge_type: edgeType, condition_expr: conditionExpr });
      this.state.dagEdges = this.state.dagEdges.map((edge) =>
        edge.id === edgeID ? { ...edge, edge_type: edgeType, condition_expr: conditionExpr } : edge,
      );
      this.state.edgeDialog = { edge: null, saving: false };
      void this.refreshVersionState();
    } catch (error) {
      this.state.error = error instanceof Error ? error.message : t("Failed to update edge type.");
      this.state.edgeDialog = { ...this.state.edgeDialog, saving: false };
    }
  }

  // ------------------------------------------------------------------- save

  // persistCanvasState is the single save path for node edits, trigger edits,
  // deletes, and the toolbar Save button — so all of them converge on the same
  // ordering: trigger sync -> DAG cycle gate -> replace_action_sequence ->
  // write(layout) -> reload.
  async persistCanvasState({ syncTrigger }) {
    const workflowID = this.workflowID;
    if (!workflowID) return;
    this.state.saving = true;
    this.state.error = "";
    try {
      const triggerNode = this.state.nodes.find((node) => node.id === TRIGGER_NODE_ID);
      const triggerDraft = triggerNode?.trigger;
      if (!triggerDraft) throw new Error(t("Failed to resolve workflow trigger."));
      if (syncTrigger && triggerDraftChanged(triggerDraft, this.state.triggerRecords)) {
        await syncWorkflowTriggerState(workflowID, triggerDraft, this.state.triggerRecords);
      }

      // DAG mode: re-validate the persisted edge graph before writing
      // anything. Rejection stays inline (banner + node highlight) — the
      // offending step keys are the whole point, a generic toast loses them.
      if (this.isDag) {
        const validation = await validateWorkflowGraph(workflowID);
        if (!validation.acyclic) {
          const cycle = validation.cycle ?? [];
          this.state.cyclicStepKeys = cycle;
          this.state.error = t("Workflow graph has a cycle involving: {nodes}. Fix the connections before saving.", { nodes: cycle.join(", ") });
          return;
        }
        this.state.cyclicStepKeys = [];
      }

      // Steps and layout are saved in one server transaction, checked against the revision this
      // canvas loaded (plan T59).
      const items = buildReplaceActionSequenceItems(this.state.nodes, this.state.actionOrder);
      const layout = buildLayoutPayload(this.state.nodes, this.edges, this.state.viewport);
      const result = await replaceWorkflowActionSequence(workflowID, items, {
        expectedRevision: this.state.revision ?? undefined,
        layout,
      });
      if (!result) throw new Error(t("Failed to save workflow layout."));
      if (Number.isInteger(result.revision)) this.state.revision = result.revision;
      await this.loadCanvas(normalizeLayout(layout));
      // A save turns the workflow back into a draft (T97) and is re-checked (T98).
      void this.refreshVersionState();
      void this.runValidation({ openPanel: false });
    } catch (error) {
      this.state.error = isWorkflowRevisionConflict(error)
        ? t("This workflow was saved by someone else after you opened it. Refresh to load the latest version (your unsaved changes here will be lost), then apply them again.")
        : error instanceof Error
          ? error.message
          : t("Failed to save workflow canvas");
      throw error;
    } finally {
      this.state.saving = false;
    }
  }

  async saveCanvas() {
    try {
      await this.persistCanvasState({ syncTrigger: true });
    } catch {
      // Error already surfaced inline by persistCanvasState.
    }
  }

  // ------------------------------------------------------------- run status

  // setupRunStatusPolling mirrors WorkflowCanvas.tsx's effect: live-updates
  // the run overlay from the workflow's service-bus channel
  // (workflow:run:<topic> / "workflow.run.step"), with a 30s poll as a
  // fallback in case a bus event is ever dropped. Both paths reconcile from a
  // full fetchWorkflowLatestRun rather than trusting the event payload as
  // complete history. Always tears down any previous subscription first so
  // calling this again (e.g. from refresh()/load()) never leaks a duplicate
  // interval/channel — the same discipline this module already enforces for
  // the pointermove/pointerup window listeners.
  setupRunStatusPolling() {
    this.teardownRunStatusPolling();
    const workflowID = this.workflowID;
    const topic = this.state.workflowTopic;
    if (!workflowID || !topic || topic === "workflow.topic") return;

    this.runPollCancelled = false;
    const poll = async () => {
      try {
        const run = await fetchWorkflowLatestRun(topic);
        if (this.runPollCancelled || !run) return;
        this.state.latestRunStatus = run.status;
        this.state.latestRunID = Number(run.id) || 0;
        const nextStatus = {};
        for (const stepRun of run.step_runs ?? []) {
          if (stepRun.step_key) nextStatus[stepRun.step_key] = stepRun.status;
        }
        this.applyRunStepStatus(nextStatus);
      } catch {
        // Silent — overlay is best-effort, do not surface polling errors.
      }
    };

    void poll();
    this.runPollTimer = setInterval(() => void poll(), 30000);
    this.runPollChannel = `workflow:run:${topic}`;
    this.runStepEventHandler = () => void poll();
    busService.addChannel(this.runPollChannel);
    busService.subscribe("workflow.run.step", this.runStepEventHandler);
  }

  teardownRunStatusPolling() {
    this.runPollCancelled = true;
    if (this.runPollTimer) {
      clearInterval(this.runPollTimer);
      this.runPollTimer = null;
    }
    if (this.runStepEventHandler) {
      busService.unsubscribe("workflow.run.step", this.runStepEventHandler);
      this.runStepEventHandler = null;
    }
    if (this.runPollChannel) {
      busService.deleteChannel(this.runPollChannel);
      this.runPollChannel = null;
    }
  }

  // applyRunStepStatus overlays the latest run's per-step status onto action
  // nodes — mirrors the React effect keyed on runStepStatus.
  applyRunStepStatus(nextStatus) {
    this.state.nodes = this.state.nodes.map((node) => {
      if (node.kind !== "action") return node;
      const status = nextStatus[node.action.step_key];
      if (status === node.lastRunStatus) return node;
      return { ...node, lastRunStatus: status };
    });
  }

  // nodeDotClass picks the node-head status dot: a live run status wins over
  // the plain configured/unconfigured coloring, matching WorkflowCanvas.tsx's
  // ternary chain on data.lastRunStatus / data.configured.
  nodeDotClass(node) {
    if (node.kind === "action" && node.lastRunStatus) return this.runStatusDotClass(node.lastRunStatus);
    if (node.kind === "action" && !node.configured) return "is-warning";
    return "is-ok";
  }

  runStatusDotClass(status) {
    switch (status) {
      case "succeeded":
        return "is-run-succeeded";
      case "failed":
      case "dead_letter":
        return "is-run-failed";
      case "running":
        return "is-run-running";
      case "waiting_retry":
      case "waiting_approval":
        return "is-run-waiting-retry";
      case "skipped":
        return "is-run-skipped";
      default:
        return "";
    }
  }

  latestRunBadgeClass() {
    switch (this.state.latestRunStatus) {
      case "succeeded":
        return "is-succeeded";
      case "running":
        return "is-running";
      case "failed":
      case "dead_letter":
        return "is-failed";
      case "waiting_retry":
      case "waiting_approval":
        return "is-waiting-retry";
      default:
        return "";
    }
  }

  // ---------------------------------------------------------- run/export/import

  setTriggerSampleJSON(value) {
    this.state.triggerSampleJSON = value;
  }

  // handleExportWorkflow downloads a version-tagged JSON snapshot of the
  // current workflow — a client-side Blob download, no host navigation.
  async handleExportWorkflow() {
    const workflowID = this.workflowID;
    if (!workflowID) return;
    this.state.exportingWorkflow = true;
    this.state.error = "";
    try {
      const document = await fetchWorkflowExport(workflowID);
      const blob = new Blob([JSON.stringify(document, null, 2)], { type: "application/json" });
      const url = URL.createObjectURL(blob);
      const anchor = window.document.createElement("a");
      const exportName = this.state.workflowTopic && this.state.workflowTopic !== "workflow.topic" ? this.state.workflowTopic : `workflow-${workflowID}`;
      anchor.href = url;
      anchor.download = `${exportName}.export.json`;
      anchor.click();
      URL.revokeObjectURL(url);
    } catch (error) {
      this.state.error = error instanceof Error ? error.message : t("Failed to export workflow.");
    } finally {
      this.state.exportingWorkflow = false;
    }
  }

  triggerImportFile() {
    this.importFileInput.el?.click();
  }

  // handleImportWorkflowFile imports an export document (picked from disk) as
  // a brand-new workflow. The backend always mints a fresh name/topic, so
  // this can never collide with a live webhook.
  async onImportFileChange(ev) {
    const file = ev.target.files?.[0];
    ev.target.value = "";
    if (file) await this.handleImportWorkflowFile(file);
  }

  async handleImportWorkflowFile(file) {
    this.state.importingWorkflow = true;
    this.state.error = "";
    try {
      const text = await file.text();
      let document;
      try {
        document = JSON.parse(text);
      } catch {
        throw new Error(t("Selected file is not valid JSON."));
      }
      const result = await importWorkflowExport(document);
      if (result?.workflow_id) this.leaveTo(`/web/base.workflow/canvas/${result.workflow_id}`);
    } catch (error) {
      this.state.error = error instanceof Error ? error.message : t("Failed to import workflow.");
    } finally {
      this.state.importingWorkflow = false;
    }
  }

  // ---------------------------------------------- create/duplicate/delete/rename
  // Ported inline from remote-canvas.tsx's onCreateWorkflow/onDuplicateWorkflow/
  // onDeleteWorkflow/onRenameWorkflow host callbacks — this app has no
  // separate host-view layer above the canvas page, so the RPCs + navigation
  // that lived in the React host now live directly here.

  // handleCreateWorkflow clones the current group with a fresh topic, same
  // slug shape as remote-canvas.tsx's buildWorkflowSlug.
  async handleCreateWorkflow() {
    this.state.creatingWorkflow = true;
    this.state.error = "";
    try {
      const stamp = new Date().toISOString().replace(/[-:.TZ]/g, "").slice(0, 14);
      const random = Math.random().toString(36).slice(2, 8);
      const slug = `${stamp}-${random}`;
      const response = await createModelRpc("base.workflow").create({
        name: `Workflow ${slug}`,
        topic: `workflow.${slug}`,
        group_id: this.state.workflowGroupID ?? false,
      });
      const createdID = Number(extractWorkflowCreateID(response.result ?? response));
      if (!Number.isFinite(createdID) || createdID <= 0) throw new Error(t("Failed to create workflow"));
      this.leaveTo(`/web/base.workflow/canvas/${createdID}`);
    } catch (error) {
      this.state.error = error instanceof Error ? error.message : t("Failed to create workflow.");
    } finally {
      this.state.creatingWorkflow = false;
    }
  }

  // handleDuplicateWorkflow uses base.workflow.copy, then opens the
  // duplicate in the same canvas view.
  async handleDuplicateWorkflow() {
    const workflowID = this.workflowID;
    if (!workflowID) return;
    this.state.duplicatingWorkflow = true;
    this.state.error = "";
    try {
      const response = await createModelRpc("base.workflow").callById("copy", workflowID, []);
      const duplicatedID = Number(extractWorkflowCreateID(response.result ?? response));
      if (!Number.isFinite(duplicatedID) || duplicatedID <= 0) throw new Error(t("Duplicate workflow did not return a valid ID."));
      this.leaveTo(`/web/base.workflow/canvas/${duplicatedID}`);
    } catch (error) {
      this.state.error = error instanceof Error ? error.message : t("Failed to duplicate workflow.");
    } finally {
      this.state.duplicatingWorkflow = false;
    }
  }

  openDeleteWorkflowConfirm() {
    this.state.deleteWorkflowConfirmOpen = true;
  }

  setDeleteWorkflowConfirmOpen(open) {
    if (this.state.deletingWorkflow) return;
    this.state.deleteWorkflowConfirmOpen = open;
  }

  // handleDeleteWorkflow deletes the active workflow, then opens the bare canvas route, which
  // resolves the latest remaining workflow (or the list when none is left).
  async handleDeleteWorkflow() {
    const workflowID = this.workflowID;
    if (!workflowID) return;
    this.state.deletingWorkflow = true;
    this.state.error = "";
    try {
      const response = await createModelRpc("base.workflow").callById("unlink", workflowID);
      if (!response.result) throw new Error(t("Failed to delete workflow."));
      this.state.deleteWorkflowConfirmOpen = false;
      this.state.dirty = false;
      navigate("/web/base.workflow/canvas");
    } catch (error) {
      this.state.error = error instanceof Error ? error.message : t("Failed to delete workflow.");
    } finally {
      this.state.deletingWorkflow = false;
    }
  }

  openRenameDialog() {
    this.state.renameValue = this.state.workflowName;
    this.state.renameOpen = true;
  }

  setRenameOpen(open) {
    if (this.state.renaming) return;
    this.state.renameOpen = open;
    if (open) this.state.renameValue = this.state.workflowName;
  }

  setRenameValue(value) {
    this.state.renameValue = value;
  }

  get canSaveRenameWorkflow() {
    return this.state.renameValue.trim().length > 0;
  }

  async handleRenameWorkflow() {
    const workflowID = this.workflowID;
    const nextName = this.state.renameValue.trim();
    if (!workflowID || !nextName) return;
    this.state.renaming = true;
    try {
      const response = await createModelRpc("base.workflow").callById("write", workflowID, [{ name: nextName }]);
      if (!response.result) throw new Error(t("Failed to rename workflow."));
      this.state.workflowName = nextName;
      this.state.renameOpen = false;
    } catch (error) {
      this.state.error = error instanceof Error ? error.message : t("Failed to rename workflow.");
    } finally {
      this.state.renaming = false;
    }
  }

  // ------------------------------------------------------- generate-from-markdown

  openGenFlowDialog() {
    this.state.genFlowOpen = true;
  }

  setGenFlowOpen(open) {
    this.state.genFlowOpen = open;
  }

  // The dialog already replaced the steps on the server: reload without the unsaved-changes prompt.
  async onWorkflowGenerated() {
    await this.load();
  }

  // -------------------------------------------------------- workflow selector
  // Owl equivalent of WorkflowRecordSelector.tsx, now on the pure-Owl
  // Combobox (same relationCombo pattern as FormField's many2one picker):
  // the base.workflow list is fetched once up front and the Combobox filters
  // it client-side per keystroke — it has no onInput/onFocus hook to trigger
  // a remote name_search per keystroke like the old raw <input> did.

  async loadWorkflowSelectorOptions(query) {
    try {
      const options = query.trim() ? await searchRelationChoices("base.workflow", query) : await fetchRelationChoices("base.workflow");
      this.state.workflowSelectorOptions = options;
    } catch {
      this.state.workflowSelectorOptions = [];
    }
  }

  get workflowComboOptions() {
    return this.state.workflowSelectorOptions.map((option) => ({ value: String(option.id), label: option.name }));
  }

  onWorkflowSelectorChange(value) {
    if (value === undefined || value === null) return;
    const matched = this.state.workflowSelectorOptions.find((option) => String(option.id) === String(value));
    if (matched && matched.id !== this.workflowID) this.leaveTo(`/web/base.workflow/canvas/${matched.id}`);
  }

  // ------------------------------------------------------- Phase 6 panels

  // ensurePanels loads the lazy panels (idle after mount, or on first use) and shows their block.
  async ensurePanels() {
    await loadCanvasPanels();
    this.state.panelsReady = true;
  }

  // afterPanelSwap runs `open` once the panel that just closed has finished hiding.
  afterPanelSwap(open) {
    clearTimeout(this.panelSwapTimer);
    this.panelSwapTimer = setTimeout(() => {
      this.panelSwapTimer = null;
      open();
    }, PANEL_SWAP_MS);
  }

  // ------------------------------------------------- publish / draft (T97)

  // refreshVersionState re-reads draft/published + version for the toolbar badge. Best-effort:
  // the badge just keeps its last value on failure. The canvas revision is NOT taken from here,
  // so a save by someone else is still caught as a conflict.
  async refreshVersionState() {
    const workflowID = this.workflowID;
    if (!workflowID) return;
    try {
      const versionState = await fetchWorkflowVersionState(workflowID);
      if (versionState && workflowID === this.workflowID) this.state.versionState = versionState;
    } catch {
      // Silent: a missing badge is not worth an error banner.
    }
  }

  get versionBadge() {
    const version = this.state.versionState;
    if (!version) return null;
    if (version.state === "published") {
      return { variant: "success", label: t("Published v{version}", { version: version.published_version }) };
    }
    if (version.published_version > 0) {
      return { variant: "warning", label: t("Draft (v{version} is live)", { version: version.published_version }) };
    }
    return { variant: "secondary", label: t("Draft, never published") };
  }

  // publish freezes the saved definition as the next version triggers run. Validation errors show
  // as badges (validate is re-run for the details); a revision conflict shows the reload hint.
  async publish() {
    const workflowID = this.workflowID;
    if (!workflowID || this.state.publishing) return;
    if (this.state.dirty) {
      this.state.error = t("Save the canvas before publishing: only the saved definition is published.");
      return;
    }
    this.state.publishing = true;
    this.state.error = "";
    try {
      const result = await publishWorkflow(workflowID, this.state.revision ?? undefined);
      // Another workflow was opened meanwhile: its revision/badge must not take this result.
      if (workflowID !== this.workflowID) return;
      if (Number.isInteger(result.revision)) this.state.revision = result.revision;
      this.state.versionState = {
        ...(this.state.versionState ?? {}),
        state: result.state === "draft" ? "draft" : "published",
        published_version: Number(result.published_version ?? 0) || 0,
        published_at: result.published_at || 0,
      };
      notifySuccess(t("Published version {version}.", { version: result.published_version }));
      const warnings = Array.isArray(result.warnings) ? result.warnings : [];
      this.state.validation = { issues: warnings, panelOpen: warnings.length > 0, running: false };
    } catch (error) {
      const info = workflowErrorInfo(error);
      if (info.permission) {
        reportWorkflowError(error);
      } else if (info.code === RPC_CONFLICT) {
        this.state.error = t("This workflow was saved by someone else after you opened it. Refresh to load the latest version (your unsaved changes here will be lost), then apply them again.");
      } else if (info.code === RPC_INVALID_PARAMS) {
        this.state.error = t("The workflow cannot be published until the errors shown are fixed.");
        await this.runValidation({ openPanel: true });
      } else {
        this.state.error = info.message || t("Failed to publish the workflow.");
      }
    } finally {
      this.state.publishing = false;
    }
  }

  // ------------------------------------------------------ validation (T98)

  // runValidation asks the server to check the saved definition. openPanel shows the summary even
  // when everything is fine (the toolbar action); after a save it only opens when there are issues.
  async runValidation({ openPanel }) {
    const workflowID = this.workflowID;
    if (!workflowID) return;
    this.state.validation = { ...this.state.validation, running: true };
    try {
      const result = await validateWorkflow(workflowID);
      if (workflowID !== this.workflowID) return;
      this.state.validation = { issues: result.issues, panelOpen: openPanel || result.issues.length > 0, running: false };
    } catch (error) {
      this.state.validation = { ...this.state.validation, running: false };
      if (openPanel) {
        const message = reportWorkflowError(error, t("Failed to validate the workflow."));
        if (message) this.state.error = message;
      }
    }
  }

  closeValidationPanel() {
    this.state.validation = { ...this.state.validation, panelOpen: false };
  }

  // nodeIssues lists the validation issues a node shows as its badge: a step's by step key, the
  // trigger node's by its trigger row id.
  nodeIssues(node) {
    const issues = this.state.validation.issues;
    if (!issues.length) return [];
    if (node.kind === "action") return node.action.step_key ? issues.filter((issue) => issue.step_key === node.action.step_key) : [];
    const triggerID = Number(node.trigger?.id ?? 0);
    return triggerID > 0 ? issues.filter((issue) => !issue.step_key && Number(issue.trigger_id) === triggerID) : [];
  }

  nodeIssueCount(node) {
    return String(this.nodeIssues(node).length);
  }

  nodeIssueVariant(node) {
    return this.nodeIssues(node).some((issue) => issue.severity === "error") ? "danger" : "warning";
  }

  nodeIssueText(node) {
    return this.nodeIssues(node).map(issueText).join(" · ");
  }

  // --------------------------------------------------- trigger health (T99)

  // refreshTriggerHealth reads every trigger's listener state once (on load and on demand, never
  // polled). Best-effort: no dot is shown when it fails.
  async refreshTriggerHealth() {
    const workflowID = this.workflowID;
    if (!workflowID) return;
    try {
      const rows = await fetchTriggerHealth(workflowID);
      if (workflowID === this.workflowID) this.state.triggerHealth = healthByTrigger(rows);
    } catch {
      this.state.triggerHealth = {};
    }
  }

  // triggerHealthTone is the trigger node's dot colour ("" = no dot, e.g. the implicit webhook).
  triggerHealthTone(node) {
    if (node.kind !== "trigger" || !(Number(node.trigger?.id) > 0)) return "";
    return healthTone(this.state.triggerHealth[node.trigger.id]);
  }

  triggerHealthText(node) {
    return healthText(this.state.triggerHealth[node.trigger.id]);
  }

  // ------------------------------------------------ trigger list (T104)

  async openTriggerPanel() {
    await this.ensurePanels();
    this.state.triggerPanelOpen = true;
  }

  setTriggerPanelOpen(open) {
    this.state.triggerPanelOpen = open;
  }

  // editTriggerRow closes the trigger list and opens the shared parameter dialog on one trigger
  // row (or a new one); its type is locked, a row never changes transport.
  editTriggerRow(trigger) {
    this.state.triggerPanelOpen = false;
    this.afterPanelSwap(() => {
      this.state.paramDialog = { open: true, node: { id: "trigger:row", kind: "trigger", trigger }, rowEdit: true };
    });
  }

  // saveTriggerRow writes (or creates) one trigger row from the list, then re-reads the rows.
  async saveTriggerRow(trigger) {
    try {
      await upsertWorkflowTriggerRow(this.workflowID, trigger);
    } catch (error) {
      reportWorkflowError(error);
      throw error;
    }
    await this.reloadTriggers();
  }

  // reloadTriggers re-reads the trigger rows after the list changed them and redraws the trigger
  // node (it stands for the first active non-webhook row, else the implicit webhook).
  async reloadTriggers() {
    let triggers;
    try {
      triggers = await fetchWorkflowTriggers(this.workflowID);
    } catch (error) {
      this.state.error = reportWorkflowError(error, t("Failed to reload the workflow triggers."));
      return;
    }
    const draft = resolveTriggerDraft(this.state.workflowTopic, this.state.workflowGroupID, triggers, this.state.specs.triggers);
    this.state.triggerRecords = triggers;
    this.state.nodes = this.state.nodes.map((node) => {
      if (node.id !== TRIGGER_NODE_ID) return node;
      return { ...node, ...buildTriggerNodeData(draft, this.state.workflowTopic, this.state.specs.triggers), x: node.x, y: node.y };
    });
    void this.refreshTriggerHealth();
  }

  // -------------------------------------------- runs / test run (T95, T100, T101)

  async openRunInspector(runID = 0) {
    await this.ensurePanels();
    this.state.runInspector = { open: true, runID: Number(runID) || 0 };
  }

  setRunInspectorOpen(open) {
    this.state.runInspector = { ...this.state.runInspector, open };
  }

  async openTestRun() {
    await this.ensurePanels();
    this.state.testRunOpen = true;
  }

  setTestRunOpen(open) {
    this.state.testRunOpen = open;
  }

  // openRunFromTest closes the test-run dialog, then shows that run in the run history.
  openRunFromTest(runID) {
    this.state.testRunOpen = false;
    this.afterPanelSwap(() => void this.openRunInspector(runID));
  }

  // ------------------------------------------------------- templates (T101)

  async openTemplates() {
    await this.ensurePanels();
    this.state.templatesOpen = true;
  }

  setTemplatesOpen(open) {
    this.state.templatesOpen = open;
  }

  // onTemplateCreated opens the new workflow once the templates dialog has finished closing.
  onTemplateCreated(workflowID) {
    this.state.templatesOpen = false;
    this.afterPanelSwap(() => this.leaveTo(`/web/base.workflow/canvas/${workflowID}`));
  }
}

// extractWorkflowCreateID normalizes create/copy RPC payloads that may return
// either a scalar id or a record object — same helper as remote-canvas.tsx.
function extractWorkflowCreateID(value) {
  if (typeof value === "number" || typeof value === "string") return value;
  if (!value || typeof value !== "object") return null;
  if ("id" in value) return value.id ?? null;
  return null;
}

// bezierPath draws the same left-to-right cubic connector 7a introduced —
// React Flow's getSmoothStepPath has no vanilla equivalent here.
function bezierPath(source, target) {
  const midX = (source.x + target.x) / 2;
  return `M ${source.x} ${source.y} C ${midX} ${source.y}, ${midX} ${target.y}, ${target.x} ${target.y}`;
}
