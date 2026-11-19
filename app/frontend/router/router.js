import { Router } from "@/router/match.js"; 
// Flat route table: pattern -> { name, module, css }. Patterns support
// ":param" and ":param?" (optional, trailing) segments, matched by ./router/match.js
// so "/web/:actionModel" and "/web/:actionModel/:recordId" both resolve to
// the same ModelView page module with different params.
const ROUTES = {
  // Default page for "/" when no website module site owns the request
  // domain (see runtime_route.go's website resolution) — the Go static
  // fallback serves this bundle's index.html straight from disk, so the
  // client router still needs its own match for "/" or AppRoot.js reports
  // "No frontend route matched" instead of rendering anything.
  "/": {
    name: "Login",
    module: "@/modules/base/pages/Login/Login.js",
    css: "./modules/base/pages/Login/Login.css",
  },
  "/login": {
    name: "Login",
    module: "@/modules/base/pages/Login/Login.js",
    css: "./modules/base/pages/Login/Login.css",
  },
  // Bare "/web" always forwards to the canonical "/web/dashboard" route below
  // (AppRoot.loadRoute() replaces history + re-resolves on an entry.redirect)
  // so the address bar and any bookmark/back-forward navigation converge on
  // one URL for the dashboard instead of two routes rendering the same page.
  "/web": {
    redirect: "/web/dashboard",
  },
  // Master dashboard for the authenticated shell — must stay above
  // "/web/:actionModel" for the same declaration-order reason as the
  // markdown/apidoc routes below ("dashboard" would otherwise be swallowed as
  // a model name; no "dashboard" model exists).
  "/web/dashboard": {
    name: "Dashboard",
    module: "@/modules/base/pages/Dashboard/Dashboard.js",
    css: "./modules/base/pages/Dashboard/Dashboard.css",
    shell: true,
  },
  // Root "/" cutover: /web/login must stay above "/web/:actionModel" so the
  // Router's declaration-order matching doesn't swallow "login" as a model
  // name (same reasoning as the markdown/apidoc routes below).
  "/web/login": {
    name: "Login",
    module: "@/modules/base/pages/Login/Login.js",
    css: "./modules/base/pages/Login/Login.css",
  },
  // Knowledge module (Phase 4). Must stay above "/web/:actionModel" — Router
  // tries patterns in declaration order, and ":actionModel" would otherwise
  // swallow "markdown" as a model name before this literal route is tried.
  "/web/markdown": {
    name: "MarkdownPage",
    module: "@/modules/knowledge/pages/MarkdownPage/MarkdownPage.js",
    css: "./modules/knowledge/pages/MarkdownPage/MarkdownPage.css",
    shell: true,
  },
  // API documentation browser — standalone /doc/*.json backend contract, not
  // a registered ORM model. Must stay above "/web/:actionModel" for the same
  // declaration-order reason as the markdown route above (ui.menu's
  // menu.apidoc entry links to this literal route_path).
  "/web/apidoc": {
    name: "ApiDocPage",
    module: "@/modules/base/pages/ApiDocPage/ApiDocPage.js",
    css: "./modules/base/pages/ApiDocPage/ApiDocPage.css",
    shell: true,
  },
  // Instruction documents (kernel + installed modules' documents/helper); menu.instruction links here.
  "/web/instruction": {
    name: "InstructionPage",
    module: "@/modules/base/pages/InstructionPage/InstructionPage.js",
    shell: true,
  },
  // Backend bus notifications retained in IndexedDB (modules/base/services/
  // pouchdb.js) and the service health dashboard. Neither is backed by a
  // registered ORM model, and frontend-admin's own router (modules/base/
  // routes.ts) has no sidebar/menu entry for either — system-health is
  // unreachable by menu there too; notifications is only reachable via the
  // topbar user-menu bell icon (nav-user.tsx), mirrored here as Shell.xml's
  // header bell link. Must stay above "/web/:actionModel" for the same
  // declaration-order reason as the apidoc/markdown routes above.
  "/web/notifications": {
    name: "NotificationsPage",
    module: "@/modules/base/pages/NotificationsPage/NotificationsPage.js",
    shell: true,
  },
  "/web/system-health": {
    name: "HealthCheckPage",
    module: "@/modules/base/pages/HealthCheckPage/HealthCheckPage.js",
    shell: true,
  },
  // Base website builder. Full-bleed (no Shell): it brings its own top bar and sidebar. Must stay above
  // "/web/:actionModel[/:recordId]" for the same declaration-order reason as the markdown route above.
  "/web/tbl.website_page/:pageId/builder": {
    name: "BuilderPage",
    module: "@/modules/website/pages/BuilderPage/BuilderPage.js",
    css: "./modules/website/pages/BuilderPage/BuilderPage.css",
  },
  // Workflow canvas (Phase 7a). Canonical route mirrors frontend-admin's
  // /web/base.workflow/canvas/$workflowId — must stay above
  // "/web/:actionModel[/:recordId]" for the same declaration-order reason as
  // the markdown/website routes above. workflowId is optional so a bare
  // /web/base.workflow/canvas (no id) still matches here instead of falling
  // through to ModelView's generic form fallback; WorkflowCanvasPage resolves
  // a default workflow itself when no id is present.
  "/web/base.workflow/canvas/:workflowId?": {
    name: "WorkflowCanvasPage",
    module: "@/modules/workflow/pages/WorkflowCanvasPage/WorkflowCanvasPage.js",
    css: "./modules/workflow/pages/WorkflowCanvasPage/WorkflowCanvasPage.css",
    shell: true,
  },
  "/web/:actionModel": {
    name: "ModelView",
    module: "@/modules/base/pages/ModelView/ModelView.js",
    css: "./modules/base/pages/ModelView/ModelView.css",
    shell: true,
  },
  // Second segment is either a forced view mode ("list"/"form"/"kanban"/
  // "calendar", frontend-admin's /web/<model>/<view> convention), the literal
  // "new", or a record id — ModelView disambiguates.
  "/web/:actionModel/:recordId": {
    name: "ModelView",
    module: "@/modules/base/pages/ModelView/ModelView.js",
    css: "./modules/base/pages/ModelView/ModelView.css",
    shell: true,
  },
  // Public, unauthenticated document viewer — no `shell`, matches
  // frontend-admin's "/view-md/$" wildcard route.
  "/view-md/*path": {
    name: "MarkdownPublicPage",
    module: "@/modules/knowledge/pages/MarkdownPublicPage/MarkdownPublicPage.js",
    css: "./modules/knowledge/pages/MarkdownPublicPage/MarkdownPublicPage.css",
  },
};

const HOME_ROUTE = "/";
export const router = new Router(ROUTES);
export const home = router.match(HOME_ROUTE)