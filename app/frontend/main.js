import { App } from "nodexui/owl";
// Must be the first app module: defines the global t()/tHtml() and loads the .po catalogs before anything else evaluates.
import "./i18n.js";
// Must run before any BaseUI Dialog/Toast mounts — see its header comment.
import "./services/bootstrap-guard.js";
import { NodexUITheme } from "nodexui/theme";

import { router } from "@/router/router.js";
import { loadModuleFrontends } from "@/modules/module-registry.js";
import { navigate } from "@/router/navigation.js";
import { AppRoot } from "@/router/AppRoot.js";
import { registerServiceWorker, checkForNewVersion } from "./services/pwa/version-service.js";
import { AUTH_INVALID_EVENT } from "@/services/api/jsonv2.js";
import { authStore } from "@/stores/auth-service.js";
import { startDomTranslator } from "@/services/i18n/dom-translator.js";

// index.html's inline head script already set b-theme/data-bs-theme
// synchronously pre-paint (avoids a light->dark flash); this call wires up
// the rest of the controller — [b-theme-toggle] click handling, localStorage
// persistence, iframe sync, and the "nodexui:theme-change" event — using the
// same storage/query keys so it agrees with what the inline script read.
NodexUITheme.init();

// Serve b-icon/b-att-icon Lucide icons from nodexui's own pinned
// icons/lucide/ directory (same jsDelivr host/version as nodexui.min.css/js
// elsewhere in this app) instead of BaseUI's default lucide-static@<ver>
// CDN base — keeps the icon set in lockstep with whatever nodexui
// version this app is pinned to, rather than drifting against a separately
// versioned lucide-static package.
if (window.thebase?.icons?.lucide) {
  window.thebase.icons.lucide.cdnBaseUrl =
    "https://cdn.jsdelivr.net/npm/nodexui@1.3.5/dist/icons/lucide";
}

// Pure-Owl BaseUI components (imported via the "nodexui/component" import map
// entry) reference named templates ("nodexui.Button", ...) rather than an
// inline xml`` literal, so they only resolve once this file's `<templates>`
// XML has been loaded into the Owl App's `templates` config — per
// docs/owl-components.md, a component class alone does not carry its
// template. Started here so it downloads in parallel with the module-frontend
// manifest below (index.html preloads the same URL); the default HTTP cache
// is fine because the URL is pinned to an immutable nodexui version.
const baseuiTemplatesRequest = fetch(
  "https://cdn.jsdelivr.net/npm/nodexui@1.3.5/dist/nodexui.templates.xml",
).then((response) => response.text());

// Module-owned form widgets and routes must be registered before the first route match/render.
await loadModuleFrontends();

// Translates rendered text for the active UI language (see dom-translator.js).
startDomTranslator();

const baseuiTemplates = await baseuiTemplatesRequest;

function safeText(value) {
  return value == null ? "" : String(value);
}

function errorMessage(error) {
  if (error instanceof Error && error.message) return error.message;
  return safeText(error) || t("Unknown render error");
}

function errorDetails(error) {
  if (error instanceof Error && error.stack) return error.stack;
  return "";
}

// renderFatalError is the last-resort safety net for failures that happen
// outside of AppRoot's own error boundary (e.g. AppRoot itself failing to
// mount, or a truly uncaught window error/rejection). It uses inline styles
// so even missing page CSS cannot produce a silent blank screen.
// The first fatal error wins: replaceChildren() detaches the still-live Owl tree without destroying
// it, so a later render (e.g. a pending toast) fails on the detached DOM and would overwrite the
// root cause with a secondary, misleading error.
let fatalErrorShown = false;
function renderFatalError(root, title, message, details = "") {
  if (!root) return;
  if (fatalErrorShown) {
    console.error(`[fatal:suppressed] ${title}: ${message}`, details);
    return;
  }
  fatalErrorShown = true;
  root.replaceChildren();
  const shell = document.createElement("main");
  shell.setAttribute("role", "alert");
  shell.style.cssText =
    "min-height:100vh;padding:32px;background:#f8fafc;color:#0f172a;font-family:system-ui,-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;";

  const card = document.createElement("section");
  card.style.cssText =
    "max-width:760px;border:1px solid #fecaca;background:#fff1f2;border-radius:12px;padding:20px;box-shadow:0 12px 30px rgba(15,23,42,.08);";

  const heading = document.createElement("h1");
  heading.textContent = title;
  heading.style.cssText = "margin:0 0 8px;font-size:20px;line-height:1.3;";

  const body = document.createElement("p");
  body.textContent = message;
  body.style.cssText = "margin:0;color:#9f1239;font-size:14px;line-height:1.5;";

  card.append(heading, body);
  if (details) {
    const pre = document.createElement("pre");
    pre.textContent = details;
    pre.style.cssText =
      "margin:16px 0 0;max-height:280px;overflow:auto;white-space:pre-wrap;border-radius:8px;background:#111827;color:#f9fafb;padding:12px;font-size:12px;line-height:1.45;";
    card.append(pre);
  }
  shell.append(card);
  root.append(shell);
}

document.body.addEventListener("click", (ev) => {
  const link = ev.target.closest ? ev.target.closest("a[href]") : null;
  if (!link) return;
  const href = link.getAttribute("href");
  if (
    !href ||
    !href.startsWith("/") ||
    !router.match(href) ||
    link.target === "_blank"
  )
    return;
  ev.preventDefault();
  navigate(href);
});

// jsonv2.js's BaseJsonV2Rpc.post()/get() emit this once an RPC call comes
// back 401/403 with no recoverable silent-refresh path (see its
// shouldInvalidateSession()/emitInvalidAuthEvent()) — e.g. the backend's
// "rpc authentication is required" 401. Mirrors frontend-admin's
// root-route-shell.tsx listener: clear the persisted session then force a
// hard redirect to the login route so a stale/expired token never leaves the
// user stuck on an authenticated page issuing more doomed requests.
window.addEventListener(AUTH_INVALID_EVENT, async () => {
  // Imported on demand: these modules are only loaded once a session exists, so a static import
  // would put them on the critical path of the signed-out /login load.
  try {
    const [{ clearCachedSidebarMenus }, { clearLastNotificationId }] = await Promise.all([
      import("@/modules/base/api/menu.js"),
      import("@/modules/base-bus/bus-service.js"),
    ]);
    clearCachedSidebarMenus();
    clearLastNotificationId();
  } catch (error) {
    console.error("Failed to clear session caches", error);
  }
  authStore.clearAuth();
  navigate("/login");
});

window.addEventListener("error", (event) => {
  renderFatalError(
    document.body,
    t("Frontend runtime error"),
    errorMessage(event.error ?? event.message),
    errorDetails(event.error),
  );
});

// A stray rejected promise must not wipe the running app: log it and show a
// toast. The full-page error stays only for boot failures (see mount below).
window.addEventListener("unhandledrejection", (event) => {
  console.error("Unhandled promise rejection", event.reason);
  event.preventDefault();
  import("./modules/base-bus/bus-root.js")
    .then(({ showToast }) => showToast({ message: errorMessage(event.reason) || t("Frontend async error"), variant: "danger" }))
    .catch((error) => console.error("Failed to show error toast", error));
});

registerServiceWorker();
void checkForNewVersion();


// AppRoot is mounted once, here, and never destroyed/recreated — navigation
// (link clicks -> navigate() -> popstate, and browser back/forward) is
// handled entirely inside AppRoot by updating its own reactive state, so
// only the routed content (and Shell's <main>, specifically) re-renders
// instead of the whole page. See router/AppRoot.js's header comment.
try {
  const app = new App(AppRoot, {
    props: {},
    templates: baseuiTemplates,
  });
  await app.mount(document.body);
} catch (error) {
  console.error("Failed to mount frontend application", error);
  renderFatalError(
    document.body,
    t("Could not start the app"),
    errorMessage(error),
    errorDetails(error),
  );
}

// Starts the live WebSocket "bus" notification service (modules/base-bus/).
// Called once, after the first paint, and loaded dynamically so its module graph (bus-root,
// multi-tab, action-client) stays off the critical path of /login and /web. bus-root.js's
// applyAuthState() reads the current authStore state on init, so a session that already holds a
// token connects right away and a login performed later connects via the auth subscription. It
// must stay a top-level call here rather than something a route/component triggers, because the
// connection has to survive AppRoot swapping the routed page component in and out on navigation
// (see bus-root.js's header comment).
import("./modules/base-bus/bus-root.js")
  .then(({ initBusRoot }) => initBusRoot())
  .catch((error) => console.error("Failed to start bus service", error));
