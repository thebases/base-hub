// view-mount-registry.js — ModelView-only registry: for each registered view
// type (see ./view-registry.js's VIEW_TYPES), the Owl component ModelView
// mounts plus the load/refresh functions that populate ModelView's state for
// that mode. This is what ModelView.js's load()/refreshData()/components map
// resolve from instead of a hardcoded if/else chain.
//
// Kept separate from view-registry.js because view-registry.js is also
// imported by services/api/jsonv2.js, and CalendarView.js (like other view
// components) imports jsonv2.js — importing the component classes into
// view-registry.js would create an import cycle. This file has no such
// constraint since only ModelView.js/.xml (a page) imports it.
//
// To register a new view type: add an entry here keyed by the same string
// used in view-registry.js's VIEW_TYPES/VIEW_TYPE_CONFIG.
//  - componentName: the tag name used in ModelView.xml and the key under
//    which the component class is exposed via ModelView.components.
//  - loadComponent(): lazily imports the Owl component class to mount, so a /web page only downloads
//    the view types it renders (ModelView awaits it before first render via resolveViewComponent).
//  - load(modelView, model): populates `modelView.state` for this mode.
//    Called from ModelView#load(). May return a Promise.
//  - refresh(modelView, model): re-runs this mode's data fetch without
//    re-resolving the window action (ModelView#refreshData()). Pass `null`
//    when the mode manages its own refresh path (form/calendar do) instead
//    of supporting a generic re-fetch.

export const VIEW_MOUNT_REGISTRY = new Map([
  [
    "list",
    {
      componentName: "ListView",
      loadComponent: () => import("@/modules/base/components/views/list/ListView.js").then((mod) => mod.ListView),
      load: (modelView, model) => modelView.loadList(model),
      refresh: (modelView, model) => modelView.loadList(model),
    },
  ],
  [
    "form",
    {
      componentName: "FormView",
      loadComponent: () => import("@/modules/base/components/views/form/FormView.js").then((mod) => mod.FormView),
      load: (modelView, model) => modelView.loadForm(model, modelView.state.route.recordId),
      refresh: null,
    },
  ],
  [
    "kanban",
    {
      componentName: "KanbanView",
      loadComponent: () => import("@/modules/base/components/views/kanban/KanbanView.js").then((mod) => mod.KanbanView),
      load: (modelView, model) => modelView.loadKanban(model),
      refresh: (modelView, model) => modelView.loadKanban(model),
    },
  ],
  [
    "calendar",
    {
      componentName: "CalendarView",
      loadComponent: () => import("@/modules/base/components/views/calendar/CalendarView.js").then((mod) => mod.CalendarView),
      load: (modelView, model) => modelView.loadCalendar(model),
      refresh: null,
    },
  ],
  [
    "hierarchy",
    {
      componentName: "HierarchyView",
      loadComponent: () => import("@/modules/base/components/views/hierarchy/HierarchyView.js").then((mod) => mod.HierarchyView),
      load: (modelView, model) => modelView.loadHierarchy(model),
      refresh: (modelView, model) => modelView.loadHierarchy(model),
    },
  ],
  [
    "gantt",
    {
      componentName: "GanttView",
      loadComponent: () => import("@/modules/base/components/views/gantt/GanttView.js").then((mod) => mod.GanttView),
      load: (modelView, model) => modelView.loadGantt(model),
      // Range/scale changes are re-fetched via GanttView's onRangeChange
      // (see loadGanttRange), same as calendar's own range-driven refresh.
      refresh: null,
    },
  ],
  [
    "map",
    {
      componentName: "MapView",
      loadComponent: () => import("@/modules/base/components/views/map/MapView.js").then((mod) => mod.MapView),
      load: (modelView, model) => modelView.loadMap(model),
      refresh: (modelView, model) => modelView.loadMap(model),
    },
  ],
  [
    "grid",
    {
      componentName: "GridView",
      loadComponent: () => import("@/modules/base/components/views/grid/GridView.js").then((mod) => mod.GridView),
      load: (modelView, model) => modelView.loadGrid(model),
      // Range switches are re-fetched via GridView's onRangeChange
      // (see loadGridRange), same as gantt's own range-driven refresh.
      refresh: null,
    },
  ],
  [
    "cohort",
    {
      componentName: "CohortView",
      loadComponent: () => import("@/modules/base/components/views/cohort/CohortView.js").then((mod) => mod.CohortView),
      load: (modelView, model) => modelView.loadCohort(model),
      // Search/facet changes re-fetch the same selector state via
      // loadCohortData (interval/timeline/measure/mode toggles already call
      // it directly, same split as loadGridRange's range-driven refresh).
      refresh: (modelView) => modelView.loadCohortData(),
    },
  ],
]);

const loadedViews = new Map();

// resolveViewComponent imports (once) the Owl class for a view mode, or returns null for an unregistered mode.
export function resolveViewComponent(mode) {
  const entry = VIEW_MOUNT_REGISTRY.get(mode);
  if (!entry) return Promise.resolve(null);
  if (!loadedViews.has(mode)) loadedViews.set(mode, entry.loadComponent());
  return loadedViews.get(mode);
}
