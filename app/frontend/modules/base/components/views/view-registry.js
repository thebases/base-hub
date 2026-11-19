// view-registry.js — the single source of truth for which view types this
// frontend knows about, shared by services/api/jsonv2.js (window-action /
// arch-fallback logic) and pages/ModelView (route segments, load/refresh,
// component mounting).
//
// This file is intentionally metadata-only (no Owl component imports): it is
// imported by jsonv2.js, and several view components (e.g. CalendarView.js)
// import jsonv2.js themselves, so pulling component classes in here would
// create an import cycle. The Owl component classes + per-type load/refresh
// wiring for ModelView live in ./view-mount-registry.js instead, which is
// only ever imported by ModelView.js/.xml (a page, not a service), so it is
// free to import the view components directly.
//
// To add a new view type later: add its string to VIEW_TYPES (and to
// WINDOW_ACTION_VIEW_ORDER if it should be requested as part of the window
// action), add a VIEW_TYPE_CONFIG entry here, and add its component + load/
// refresh wiring to view-mount-registry.js. No other file needs a new
// hardcoded branch.

// VIEW_TYPES: every view type ModelView can render, used for the /web/:model/:segment
// route-segment whitelist (see ModelView.js's resolveRoute/VIEW_SEGMENTS) and
// to build the mount registry's key set.
export const VIEW_TYPES = ["list", "form", "kanban", "calendar", "hierarchy", "gantt", "map", "grid", "cohort"];

// WINDOW_ACTION_VIEW_ORDER: the order fetchWindowAction() requests view ids in
// (jsonv2.js's former hardcoded requestedTypes arrays). This is a separate,
// explicit order from VIEW_TYPES because the window-action payload has always
// listed kanban before calendar/form — kept as-is to preserve exact existing
// behavior for `action.views`/`action.view_mode`.
export const WINDOW_ACTION_VIEW_ORDER = ["list", "kanban", "calendar", "hierarchy", "gantt", "map", "grid", "cohort", "form"];

// VIEW_TYPE_CONFIG: per-type flags jsonv2.js needs.
//  - synthesizeArch: whether fetchViewMetadataForType/fetchModelSingleView
//    should fall back to a fields_get-derived arch when no ui.view row
//    exists for this type (former FALLBACK_VIEW_TYPES set).
//  - includeInWindowAction(model): whether fetchWindowAction should request
//    this type's view id for the given model. Calendar is excluded for
//    sys.* models — ported verbatim from the old requestedTypes branch.
export const VIEW_TYPE_CONFIG = new Map([
  ["list", { synthesizeArch: true, includeInWindowAction: () => true }],
  ["form", { synthesizeArch: true, includeInWindowAction: () => true }],
  ["kanban", { synthesizeArch: true, includeInWindowAction: () => true }],
  ["calendar", { synthesizeArch: true, includeInWindowAction: (model) => !model.startsWith("sys.") }],
  // hierarchy never gets a synthesized fallback arch (parent_field can't be
  // inferred from fields_get alone) — it only ever renders/appears in a
  // window action when an explicit ui.view row exists, same gating
  // fetchWindowAction already applies to every type via fetchPrimaryUIViewID.
  ["hierarchy", { synthesizeArch: false, includeInWindowAction: () => true }],
  // gantt always needs an explicit arch (date_start/date_stop can't be
  // inferred from fields_get alone, same reasoning as hierarchy's parent_field).
  ["gantt", { synthesizeArch: false, includeInWindowAction: () => true }],
  // map always needs an explicit arch (latitude_field/longitude_field can't
  // be inferred from fields_get alone, same reasoning as gantt/hierarchy).
  ["map", { synthesizeArch: false, includeInWindowAction: () => true }],
  // grid always needs an explicit arch (row/col/measure fields can't be
  // inferred from fields_get alone, same reasoning as gantt/hierarchy/map).
  ["grid", { synthesizeArch: false, includeInWindowAction: () => true }],
  // cohort always needs an explicit arch (date_start/date_stop can't be
  // inferred from fields_get alone, same reasoning as gantt/hierarchy/map/grid).
  ["cohort", { synthesizeArch: false, includeInWindowAction: () => true }],
]);

// EXTRA_WINDOW_ACTION_VIEW_MODES: window-action view modes that are valid but
// are not one of ModelView's registered view types — "canvas" is
// base.workflow's dedicated canvas view (see ModelView.js's openRecord
// special-case), never rendered by ModelView's own list/form/kanban/calendar
// mount chain. Kept separate so viewModesFromWindowAction's whitelist doesn't
// have to pretend canvas is a registered type.
export const EXTRA_WINDOW_ACTION_VIEW_MODES = ["canvas"];
