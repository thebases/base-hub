// Port of frontend-admin's modules/base-bus/action-client.ts — the
// ui.actions.client / "action_client" bus-event dispatch registry, types
// dropped. Retargeted import: "@/modules/base-bus/bus-service" -> the
// sibling "./bus-service.js" (this tree's own import convention, see the
// task's Step 1 instructions and bus-service.js's own header comment).
//
// Deliberate deviation from the React source (documented, not silent): the
// React handleToastAction calls `toast.*(...)` from the "sonner" package
// directly — this static/no-bundler Owl tree has no toast library at this
// layer (only ModelView.js/Shell.js own an Owl <Toast> component instance,
// and this module must stay UI-framework-agnostic, same reasoning the source
// itself already applies to handleRefreshDataAction/handleOpenPopupAction
// below). So the built-in "toast" handler follows that same
// re-broadcast-on-the-bus pattern instead of rendering anything itself:
// it triggers "BUS:ACTION_TOAST" on busService's own event target.
// bus-root.js (modules/base-bus/bus-root.js) subscribes to that event and
// forwards it through its own subscribeBusToast(...) pub/sub, which
// Shell.js's Owl <Toast> instance renders. See bus-root.js's header comment
// for the full toast-payload contract.

import { busService } from "./bus-service.js";

const handlers = new Map();

// registerActionClientHandler lets any module extend what a backend-pushed
// action_client event can do — the three tags below (toast, refresh_data,
// open_popup) ship built in; a feature module can register additional tags
// or override a built-in one (e.g. to route "open_popup" through its own
// dialog manager instead of the generic BUS:OPEN_POPUP broadcast).
export function registerActionClientHandler(tag, handler) {
  handlers.set(tag, handler);
}

export function unregisterActionClientHandler(tag) {
  handlers.delete(tag);
}

// dispatchActionClient is the base-bus module's single entry point for
// backend-pushed action_client events — bus-root.js wires it to the
// "action_client" bus notification type; nothing else should call
// busService.subscribe("action_client", ...) directly.
export async function dispatchActionClient(action) {
  const tag = action?.tag?.trim();
  if (!tag) return;
  const handler = handlers.get(tag);
  if (!handler) {
    console.warn(`[base-bus] no action_client handler registered for tag "${tag}"`);
    return;
  }
  await handler(action);
}

// handleToastAction re-broadcasts a toast-shaped payload on the bus's own
// event target instead of rendering one directly — see the header comment
// above for why (no toast library at this layer in this tree).
function handleToastAction(action) {
  const params = action.params ?? {};
  const description = params.message || params.title || t("Notification");
  busService.trigger("BUS:ACTION_TOAST", {
    message: params.title && params.message ? params.message : description,
    title: params.title,
    variant: params.type,
    sticky: Boolean(params.sticky),
  });
}

// handleRefreshDataAction re-broadcasts on the bus's own event target instead
// of touching any query cache directly — base-bus has no knowledge of any
// app-specific data layer. Feature modules that own cached data subscribe to
// "BUS:REFRESH_DATA" (via busService.addEventListener) and decide what
// "refresh" means for them.
function handleRefreshDataAction(action) {
  busService.trigger("BUS:REFRESH_DATA", action.params ?? {});
}

// handleOpenPopupAction re-broadcasts the same way: base-bus has no dialog
// manager of its own, so a module that owns one (or wants to) should call
// registerActionClientHandler("open_popup", ...) to take over this tag, or
// subscribe to "BUS:OPEN_POPUP" to react to the generic broadcast.
function handleOpenPopupAction(action) {
  busService.trigger("BUS:OPEN_POPUP", action.params ?? {});
}

registerActionClientHandler("toast", handleToastAction);
registerActionClientHandler("refresh_data", handleRefreshDataAction);
registerActionClientHandler("open_popup", handleOpenPopupAction);
