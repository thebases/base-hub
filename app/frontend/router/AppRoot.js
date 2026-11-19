// Persistent Owl root, mounted once by main.js and never destroyed on
// navigation. Replaces the old per-navigation `new App(...).mount()` /
// `app.destroy()` cycle in main.js: that approach tore down and rebuilt the
// whole DOM (including Shell's sidebar/header) on every route change, which
// discarded Shell's menu-fetch/sidebar-collapsed state and repainted the
// full page instead of just the routed content.
//
// Here, navigation only updates this component's reactive `state` (path/
// entry/params/PageComponent). Shell.xml's `<t t-component="props.page">`
// and this component's own `<t t-component="state.PageComponent">` branch
// mean Owl keeps the *same* Shell instance alive across shell:true ->
// shell:true navigations (same tag, same class, just new props) and only
// destroys/remounts the inner page component that changed — i.e. only
// `<main class="basehub-shell-content">`'s content swaps, async, instead of
// the whole page reloading. While the target route's module is being
// dynamically imported, `state.navigating`/`PageComponent === null` makes
// Shell.xml (and this file's own no-shell branch) render a <Skeleton>
// placeholder instead of leaving the previous page's content on screen.
import {
  Component,
  loadTemplate,
  onError,
  onMounted,
  onPatched,
  onWillStart,
  useState,
  xml,
} from "nodexui/owl";
import { router, home } from "@/router/router.js";
import { toRoutePath } from "@/router/navigation.js";
import { authStore } from "@/stores/auth-service.js";
import { Skeleton } from "nodexui/component";

const template = await loadTemplate(new URL("./AppRoot.xml", import.meta.url));

// MAX_CAUSE_DEPTH bounds the `cause` walk so a cyclic chain can't loop forever.
const MAX_CAUSE_DEPTH = 10;

// innermostCause follows Error.cause down to the original failure.
function innermostCause(error) {
  let current = error;
  for (let depth = 0; depth < MAX_CAUSE_DEPTH && current instanceof Error && current.cause; depth++) {
    current = current.cause;
  }
  return current;
}

// describeErrorChain renders every link of the cause chain (innermost first)
// so the error panel shows the real stack, not only Owl's wrapper stack.
function describeErrorChain(error) {
  const parts = [];
  let current = error;
  for (let depth = 0; depth < MAX_CAUSE_DEPTH && current; depth++) {
    parts.unshift(current instanceof Error ? current.stack || `${current.name}: ${current.message}` : String(current));
    current = current instanceof Error ? current.cause : null;
  }
  return parts.join("\n\nwrapped by:\n");
}

export class AppRoot extends Component {
  static template = xml({ raw: [template] });
  static components = { Skeleton };

  setup() {
    this.state = useState({
      path: "",
      entry: null,
      params: {},
      PageComponent: null,
      // Shell class, imported on the first shell:true route so /login (and other bare routes)
      // never download its sidebar/menu/knowledge/bus module graph.
      Shell: null,
      // True from the moment a navigation starts (route matched, entry/
      // params/currentPath already updated so Shell reacts immediately)
      // until its dynamic import(entry.module) resolves. Shell.xml reads
      // this via `props.loading` to swap `<main>`'s content for a
      // <Skeleton> pulse instead of leaving stale page content on screen.
      navigating: false,
      error: "",
      errorDetails: "",
    });
    this.cssLink = document.getElementById("page-css");
    // Bumped on every loadRoute() call so a slow import that resolves after
    // a newer navigation already started can't clobber state with stale
    // (out-of-order) results.
    this.navToken = 0;

    this.onPopState = () => void this.loadRoute(toRoutePath(location.pathname) + location.search);
    window.addEventListener("popstate", this.onPopState);

    // Contains render/lifecycle errors thrown by the routed page (or Shell)
    // subtree to this component instead of letting them bubble past the
    // App root, which would otherwise destroy the entire persistent app —
    // see Owl's handleError(): an unhandled error always destroys the root.
    onError((error) => {
      // Owl wraps every lifecycle/render failure in a generic "An error
      // occured in the owl lifecycle" Error and puts the real one in
      // `cause` — surface the innermost cause, and log the full chain so
      // production failures are diagnosable from the console.
      console.error("[AppRoot] render error", error);
      const root = innermostCause(error);
      this.state.error = root instanceof Error ? root.message : String(root);
      this.state.errorDetails = describeErrorChain(error);
      this.state.PageComponent = null;
      this.state.entry = null;
    });

    onWillStart(() => this.loadRoute(toRoutePath(location.pathname) + location.search));
    onMounted(() => window.thebase?.mountAll(this.el));
    onPatched(() => window.thebase?.mountAll(this.el));
  }

  willUnmount() {
    window.removeEventListener("popstate", this.onPopState);
  }

  async loadRoute(fullPath) {
    const token = ++this.navToken;
    // Query string carries route-agnostic extras (e.g. "?default_field=value"
    // for form pre-fill) — split it off before matching, since the path
    // matcher only knows about ":param" segments, not "?"-delimited params.
    const [path, search = ""] = fullPath.split("?");
    const match = router.match(path) ?? home;
    if (!match) {
      this.state.error = t("Route not found");
      this.state.errorDetails = t('No frontend route matched "{path}".', { path });
      return;
    }
    const { entry, params } = match;
    // A redirect-only entry (e.g. "/web" -> "/web/dashboard") carries no
    // module to mount — swap the visible URL via replaceState (no extra
    // history entry, so back/forward doesn't bounce between the two paths)
    // and re-resolve against the real target instead of rendering anything.
    if (entry.redirect) {
      const target = entry.redirect + (search ? `?${search}` : "");
      history.replaceState(null, "", target);
      return this.loadRoute(target);
    }
    // A shell route with no session at all (no access token and nothing to refresh one) can only
    // end in a 401 from the menu load and a forced redirect, so go straight to /login instead of
    // downloading Shell and issuing a doomed request.
    if (entry.shell) {
      const auth = authStore.getState();
      if (!auth.accessToken && !auth.refreshToken) {
        history.replaceState(null, "", "/login");
        return this.loadRoute("/login");
      }
    }
    params.query = new URLSearchParams(search);
    // Commit path/entry/params and drop PageComponent before the import
    // starts: Shell (or the shell/no-shell branch) reacts immediately, and
    // Shell.xml renders the <Skeleton> placeholder in `<main>` in place of
    // stale page content for the duration of the async import below.
    this.state.path = path;
    this.state.entry = entry;
    this.state.params = params;
    this.state.PageComponent = null;
    this.state.navigating = true;
    this.state.error = "";
    this.state.errorDetails = "";
    try {
      const [mod, shellMod] = await Promise.all([
        import(entry.module),
        entry.shell && !this.state.Shell
          ? import("@/modules/base/pages/Shell/Shell.js")
          : null,
      ]);
      if (token !== this.navToken) return;
      if (shellMod) this.state.Shell = shellMod.Shell;
      const PageComponent = mod[entry.name];
      if (!PageComponent) {
        throw new Error(
          t('Route module "{module}" did not export "{name}".', {
            module: entry.module,
            name: entry.name,
          }),
        );
      }
      if (this.cssLink) this.cssLink.href = entry.css || "";
      this.state.PageComponent = PageComponent;
    } catch (error) {
      if (token !== this.navToken) return;
      this.state.error =
        error instanceof Error ? error.message : String(error);
      this.state.errorDetails = error instanceof Error ? error.stack || "" : "";
    } finally {
      if (token === this.navToken) this.state.navigating = false;
    }
  }
}
