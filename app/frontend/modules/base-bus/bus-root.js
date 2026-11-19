// bus-root.js — this tree's replacement for frontend-admin's
// components/bus-provider.tsx ("BusProvider"), a React context component
// mounted once at the app root and kept alive for the lifetime of the SPA.
//
// ARCHITECTURAL DECISION (why this is a plain singleton module, not a
// mounted Owl component): main.js mounts one persistent `AppRoot` (see
// router/AppRoot.js) that is never destroyed/recreated across navigation —
// only the routed page component underneath it (and Shell, when the route
// isn't `shell:true`) swaps in and out. Even so, Shell.js itself is NOT
// guaranteed mounted at all times (e.g. the bare Login route has no Shell),
// so a BusProvider-style component ported onto Shell's mount/unmount
// lifecycle would still stop/start the WebSocket connection on every
// authenticated<->unauthenticated transition. The bus connection must
// survive navigation exactly like authStore does (a true module-level
// singleton, constructed once at import time, never re-created).
//
// So this file is NOT a component: it's a plain singleton module, same shape
// as pwa-service.js/i18n-service.js (see those files for the established
// convention in this tree). `initBusRoot()` is called exactly once, at
// main.js's top level (module load, unconditionally), and is idempotent
// (guarded by `initialized`) in case it is ever accidentally called twice.
//
// It cannot render a toast directly (no render tree, no component instance),
// so instead of `toast.success(...)` (React's sonner call), it exposes a
// second, tiny pub/sub of its own: `subscribeBusToast(listener)`. Shell.js —
// which DOES persist for the duration of one route (it's the shell wrapper
// every `shell:true` route mounts inside, see main.js/Shell.js) — subscribes
// to it in onMounted and unsubscribes in willUnmount, storing the latest
// payload in its own Owl `state.busToast` and rendering it via
// `<Shell toast="state.busToast" .../>` — the app's single Toast render
// surface (see showToast() below: every other module, including page-local
// notify() calls, routes through this same pipe instead of keeping its own
// `state.notification` + local `<Toast>`). This means a
// bus event that fires while no `shell:true` route is mounted (e.g. on the
// bare Login page) has nowhere to render — same "notification pipe exists,
// but no listener is currently mounted" gap the React version doesn't have
// either in practice (BusProvider only ever wraps authenticated routes there
// too, since the whole app tree sits under one root).
//
// Toast payload contract (Shell.js must match this exactly):
//   { message: string, variant: "info"|"success"|"warning"|"danger",
//     title?: string, persistent?: boolean, actionLabel?: string,
//     onAction?: () => void }
// `title`, when present, renders as a bold line above `message` (shadcn/ui-
// style toast: bold title + muted description); omit it and `message` alone
// renders, same as before this field existed.
// `persistent: true` means Shell.js should render the toast with `delay="0"`
// (no auto-dismiss, same convention ModelView.xml's <Toast> already uses)
// and, when `actionLabel`/`onAction` are present, an inline action button
// inside the toast's slot that calls `onAction()` (used by BUS:OUTDATED's
// "Refresh" button, which calls `window.location.reload()`).
//
// BUS:NOTIFICATION -> saveNotification(...) note: `@/modules/base/services/
// pouchdb.js` is being ported in a parallel, independent task. Its
// `saveNotification(notification)` export already exists in this tree as of
// this writing (confirmed by reading the file before writing this import),
// but even if it hadn't, the import below would still be correct per the
// task instructions — `node --check` only validates JS syntax, not ESM
// module resolution, so a missing sibling file would not have failed that
// check (verified: see this task's validation section).

import { authStore } from "@/stores/auth-service.js";
import { busService } from "./bus-service.js";
import { dispatchActionClient } from "./action-client.js";
import { saveNotification } from "@/modules/base/services/pouchdb.js";
import { applyServerVersion } from "@/services/pwa/version-service.js";
import { emitUIMenuUpdated } from "@/modules/base/api/menu.js";

let initialized = false;
let unsubscribeAuth;
let busWasActiveForAuth = false;

const toastTarget = new EventTarget();

// subscribeBusToast lets Shell.js (the only long-lived-per-route component
// in this tree that can host a Toast) react to bus-originated toast payloads
// while it's mounted. Returns an unsubscribe function, same shape as
// authStore.subscribe()/multi-tab's bus.
export function subscribeBusToast(listener) {
  const wrapped = (event) => listener(event.detail);
  toastTarget.addEventListener("toast", wrapped);
  return () => toastTarget.removeEventListener("toast", wrapped);
}

// Every toast in the app funnels through here, so forcing `delay: 1` here
// (explicit user request) applies it everywhere in one place instead of at
// each of the many call sites below/in ModelView.js/MarkdownPage.js. Has no
// effect on `persistent: true` payloads — Shell.xml's
// `props.toast.persistent ? 0 : (props.toast.delay or 4000)` ignores `delay`
// entirely once `persistent` is set.
function pushBusToast(payload) {
  toastTarget.dispatchEvent(new CustomEvent("toast", { detail: { ...payload, delay: 3000 } }));
}

// showToast is the single public entry point for triggering the app's one
// global toast (rendered by Shell.js as a fixed top-right overlay — see the
// payload contract above). Any component that previously kept its own
// `state.notification` + local `<Toast>` (ModelView.js, MarkdownPage.js) now
// calls this instead, so there is exactly one Toast surface in the app
// rather than one per page. Same "no listener mounted" caveat as the
// bus-originated calls above applies: nothing renders if no `shell:true`
// route is currently mounted.
export function showToast(payload) {
  pushBusToast(payload);
}

// mapNotificationVariant collapses the wider SimpleNotificationPayload/
// BUS:ACTION_TOAST `type` vocabulary ("info"|"success"|"warning"|"danger"|
// "error") onto the Toast component's actual variant set — "error" and
// "danger" both mean the same visual/role treatment there (see
// nodexui.components.esm.js's Toast.role getter: only "danger" is checked).
function mapNotificationVariant(type) {
  if (type === "error") return "danger";
  if (type === "success" || type === "warning" || type === "danger") return type;
  return "info";
}

// showSimpleNotification maps the legacy simple_notification payload onto a
// bus-toast payload — port of bus-provider.tsx's showSimpleNotification,
// minus the sonner call (see the toast-payload-contract comment above).
function showSimpleNotification(payload) {
  const description = payload.message || payload.title || t("Notification");
  pushBusToast({
    message: payload.title && payload.message ? payload.message : description,
    variant: mapNotificationVariant(payload.type),
    persistent: Boolean(payload.sticky),
  });
}

// handleActionToast reacts to action-client.js's "toast" handler, which
// re-broadcasts on busService's own event target instead of rendering
// directly (see action-client.js's header comment) — same payload shape as
// showSimpleNotification above, just a different origin event.
function handleActionToast(payload) {
  const description = payload.message || payload.title || t("Notification");
  pushBusToast({
    message: description,
    variant: mapNotificationVariant(payload.variant),
    persistent: Boolean(payload.sticky),
  });
}

// storeNotification persists every BUS:NOTIFICATION-tagged event via the
// pouchdb-backed notification log — port of bus-provider.tsx's
// storeNotification, minus the dev-only debug toast: the React version also
// shows a `toast.info("[debug] bus event: ...")` for every notification when
// `import.meta.env.DEV` is true. This static/no-bundler tree has no
// DEV/isDev flag anywhere (confirmed: grepped the tree for `__ENV__`/env.js
// keys, see this task's Step 0 instructions), so that one sub-behavior is
// intentionally omitted rather than faked with a made-up flag — everything
// else (the actual persistence call) is ported unchanged.
function storeNotification(notification) {
  void saveNotification(notification).catch((error) => {
    console.warn("Failed to store bus notification", error);
  });
}

// handleActionClient forwards a backend-pushed action_client event to
// action-client.js's tag dispatcher — port of bus-provider.tsx's
// handleActionClient.
function handleActionClient(payload) {
  void dispatchActionClient(payload);
}

// handleFrontendVersionUpdated reacts to a module install/uninstall: the
// backend bumps app/frontend/version.json and publishes this event
// (see internal/modules/frontend_version.go's bumpFrontendVersion) so a tab
// that's already open doesn't have to wait for its next full page load to
// notice — version-service.js's applyServerVersion() purges the SW's offline
// cache and reloads once, exactly like the on-load version.json check does.
function handleFrontendVersionUpdated(payload) {
  const version = typeof payload?.version === "string" ? payload.version : "";
  if (!version) return;
  void applyServerVersion(version);
}

// handleUIMenuUpdated reacts to internal/models/ui_menu.go's
// publishUIMenuUpdated — fired on ui.menu create/write and on module
// install/uninstall/reinit (sys_module.go) — by re-dispatching as a window
// CustomEvent Shell.js can listen for, mirroring the
// MARKDOWN_MENU_UPDATED_EVENT decoupling pattern (knowledge/api.js). This is
// a soft sidebar refresh, independent of (and faster than)
// handleFrontendVersionUpdated's heavier purge+reload path above.
function handleUIMenuUpdated() {
  emitUIMenuUpdated();
}

// showOutdatedPageWarning pushes a persistent bus-toast with a "Refresh"
// action — port of bus-provider.tsx's showOutdatedPageWarning, replacing the
// sonner `action: {label, onClick}` option with this module's own
// actionLabel/onAction toast-payload fields (see the contract comment above).
function showOutdatedPageWarning() {
  pushBusToast({
    message:
      t("The page is out of date. Save your work and refresh to get the latest updates and avoid potential issues."),
    variant: "warning",
    persistent: true,
    actionLabel: t("Refresh"),
    onAction: () => window.location.reload(),
  });
}

// applyAuthState mirrors bus-provider.tsx's second effect
// (`useEffect(..., [accessToken, db])`): start the bus once an access token
// is present, stop it the moment it disappears (logout). Also runs
// synchronously once from initBusRoot() below (matching the effect's
// on-mount behavior, not just its on-change behavior) and again on every
// authStore change (matching the effect's dependency-array re-run — this
// includes a token *refresh*, since authStore.applyTokenPair() triggers a
// state change with a new accessToken value; busService.start() itself is
// idempotent when already active, so this only actually reconnects when the
// token was previously absent).
function applyAuthState(state) {
  if (!state.accessToken) {
    if (busWasActiveForAuth) {
      busService.stop();
      busWasActiveForAuth = false;
    }
    busService.setAuth(undefined);
    return;
  }
  busService.setAuth({ accessToken: state.accessToken, db: state.db });
  void busService.start();
  busWasActiveForAuth = true;
}

// initBusRoot wires the bus service into authStore and the toast bridge
// described above. Idempotent — safe to call more than once (main.js calls
// it exactly once, unconditionally, at module load; the guard exists only as
// a defensive safety net, matching pwa-service.js/i18n-service.js's
// module-singleton convention).
export function initBusRoot() {
  if (initialized) return;
  initialized = true;

  busService.subscribe("simple_notification", showSimpleNotification);
  busService.subscribe("action_client", handleActionClient);
  busService.subscribe("app.frontend_version_updated", handleFrontendVersionUpdated);
  busService.subscribe("ui.menu.updated", handleUIMenuUpdated);
  busService.addEventListener("BUS:ACTION_TOAST", (event) => handleActionToast(event.detail));
  busService.addEventListener("BUS:NOTIFICATION", (event) => storeNotification(event.detail));
  busService.addEventListener("BUS:OUTDATED", showOutdatedPageWarning);

  unsubscribeAuth = authStore.subscribe(applyAuthState);
  // Matches the React effect's on-mount run, not just its on-change run —
  // BusProvider's second `useEffect` fires once immediately with whatever
  // accessToken/db authStore already holds at mount time, in addition to
  // every later change.
  applyAuthState(authStore.getState());
}
