// Authenticated app shell: built on BaseUI's pure Owl `Shell` compound
// (`Shell`/`ShellSidebar`/`ShellSidebarHeader`/`ShellNav`/`ShellMain`/
// `ShellHeader`/`ShellContent`, plus the lower-level `SidebarTrigger`/
// `SidebarClose` it wraps internally for off-canvas/Escape chrome — a single
// `SidebarTrigger` toggles the mobile drawer or the desktop collapse rail
// depending on viewport, there is no separate collapse-trigger component)
// around a menu tree sourced entirely from the backend's ui.menu
// SDUI records (modules/base/api/menu.js), same mechanism as ui.view drives
// ModelView — the backend decides what's in the sidebar, not a hardcoded nav
// list. Wraps whichever routed page main.js mounts as `props.page`,
// forwarding `props.params` to it unchanged.

import { ResLangRpc } from "@/services/api/jsonv2.js";
import { DEFAULT_LANG, normalizeLang } from "@/services/i18n/lang.js";
import { Component, loadTemplate, onMounted, onWillUnmount, useState, xml } from "nodexui/owl";
import {
  clearCachedSidebarMenus,
  fetchSidebarMenus,
  readCachedSidebarMenus,
  UI_MENU_UPDATED_EVENT,
} from "@/modules/base/api/menu.js";
import { clearLastNotificationId } from "@/modules/base-bus/bus-service.js";
import { authStore } from "@/stores/auth-service.js";
import { layoutStore } from "@/stores/layout-store.js";
import { navigate } from "@/router/navigation.js";
import { MenuTree } from "@/modules/base/components/menu-tree/MenuTree.js";
import {
  fetchMarkdownMenu,
  MARKDOWN_MENU_UPDATED_EVENT,
} from "@/modules/knowledge/api.js";
import { buildDocumentTree } from "@/modules/knowledge/document-tree.js";
import { KnowledgeTree } from "@/modules/knowledge/components/KnowledgeTree.js";
import { BuildVersionLabel } from "@/modules/base/components/BuildVersionLabel.js";
import { PWAInstallDrawer } from "@/modules/base/components/pwa/PWAInstallDrawer.js";
import { subscribeBusToast } from "@/modules/base-bus/bus-root.js";
import {
  Alert,
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbList,
  BreadcrumbSeparator,
  Button,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  Icon,
  Input,
  Separator,
  Shell as BaseUIShell,
  ShellContent,
  ShellHeader,
  ShellMain,
  ShellNav,
  ShellSidebar,
  ShellSidebarHeader,
  SidebarClose,
  SidebarGroupCollapsible,
  SidebarMenu,
  SidebarTrigger,
  Skeleton,
  ThemeButton,
  Toast,
} from "nodexui/component";

// Shell.css is only needed once the shell mounts, so it is not a render-blocking <link> in index.html
// (it delayed first paint of /login). Inserted before #page-css to keep the original cascade order
// (BaseUI -> Shell -> page -> main.css) and awaited so the shell never paints unstyled.
function loadShellCss() {
  const href = new URL("./Shell.css", import.meta.url).href;
  if (document.querySelector(`link[data-shell-css]`)) return Promise.resolve();
  return new Promise((resolve) => {
    const link = document.createElement("link");
    link.rel = "stylesheet";
    link.href = href;
    link.dataset.shellCss = "";
    link.onload = link.onerror = () => resolve();
    const anchor = document.getElementById("page-css");
    if (anchor) anchor.before(link);
    else document.head.appendChild(link);
  });
}

const [template] = await Promise.all([loadTemplate(new URL("./Shell.xml", import.meta.url)), loadShellCss()]);

// Desktop sidebar collapse is a separate concern from BaseUI `Shell`'s own
// mobile off-canvas open/close state (that one only drives
// `state.mobileSidebarOpen`/`SidebarTrigger`/`SidebarClose`). Collapsed state
// is passed to `<Shell collapsed=.../>` as a controlled prop, which drives
// its own `[b-att-collapsed="true"]` attribute (see
// nodexui's nodexui.min.css's `.b-shell[b-att-collapsed=true] ...`
// rules), and persists across reloads the same way the sidebar menu cache
// does.
// The sidebar is an off-canvas drawer below 992px. BaseUI hardcodes its own
// drawer cut-over at 767.98px (CSS + JS), so the 768-991px band is handled
// here (Shell.css ports the drawer rules; onSidebarClickCapture replaces the
// BaseUI trigger/auto-close behavior that only exists below 768px).
const DRAWER_TABLET_QUERY = "(min-width: 768px) and (max-width: 991.98px)";

const SIDEBAR_COLLAPSED_STORAGE_KEY = "basehub.shell.sidebar-collapsed.v1";

function readCachedSidebarCollapsed() {
  if (typeof window === "undefined") return false;
  return window.localStorage.getItem(SIDEBAR_COLLAPSED_STORAGE_KEY) === "1";
}

function writeCachedSidebarCollapsed(collapsed) {
  if (typeof window === "undefined") return;
  window.localStorage.setItem(
    SIDEBAR_COLLAPSED_STORAGE_KEY,
    collapsed ? "1" : "0",
  );
}

// Sidebar search box (sidebar-05 template) filters the ui.menu tree
// client-side: an item survives if its own name matches or any descendant
// does, and matching branches keep their full subtree pruned to only the
// matching descendants so a hit stays reachable through its ancestor groups.
function filterMenuTree(items, query) {
  if (!query) return items;
  const needle = query.toLowerCase();
  const result = [];
  for (const item of items) {
    const children = item.children?.length
      ? filterMenuTree(item.children, query)
      : [];
    const selfMatches = item.name?.toLowerCase().includes(needle);
    if (selfMatches || children.length > 0) {
      result.push({ ...item, children: selfMatches ? item.children ?? [] : children });
    }
  }
  return result;
}

// Same pruning idea as filterMenuTree, over the knowledge doc tree's
// {kind: "leaf"|"folder", label, children} shape (buildDocumentTree output).
function filterKnowledgeTree(items, query) {
  if (!query) return items;
  const needle = query.toLowerCase();
  const result = [];
  for (const item of items) {
    if (item.kind === "leaf") {
      if (item.label?.toLowerCase().includes(needle)) result.push(item);
      continue;
    }
    const children = filterKnowledgeTree(item.children ?? [], query);
    if (children.length > 0) result.push({ ...item, children });
  }
  return result;
}

export class Shell extends Component {
  static template = xml({ raw: [template] });
  static components = {
    MenuTree,
    KnowledgeTree,
    BuildVersionLabel,
    Alert,
    Breadcrumb,
    BreadcrumbItem,
    BreadcrumbList,
    BreadcrumbSeparator,
    Button,
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuLabel,
    DropdownMenuSeparator,
    DropdownMenuTrigger,
    Icon,
    Input,
    Separator,
    Shell: BaseUIShell,
    ShellContent,
    ShellHeader,
    ShellMain,
    ShellNav,
    ShellSidebar,
    ShellSidebarHeader,
    SidebarClose,
    SidebarGroupCollapsible,
    SidebarMenu,
    SidebarTrigger,
    Skeleton,
    ThemeButton,
    Toast,
  };

  setup() {
    const cached = readCachedSidebarMenus();
    const authState = authStore.getState();
    this.state = useState({
      menu: cached?.records ?? [],
      loading: !cached,
      error: "",
      username: authState.username,
      // Language switcher: codes come from backend app.language (GET /languages);
      // currentLang is the explicit choice, else the user's own lang (both canonical ll_cc).
      languages: [],
      currentLang: normalizeLang(authState.uiLang || authState.lang) || DEFAULT_LANG,
      // Company switcher (Odoo-style): companies = authorized {id, name} options; the active
      // set (context.allowed_company_ids) and current company (context.company_id) live in
      // authStore. selectedCompanyIds is the checkbox selection staged until Confirm.
      companies: authState.companies ?? [],
      authorizedCompanyIds: authStore.authorizedCompanyIds(),
      currentCompanyId: authState.currentCompanyId ?? null,
      selectedCompanyIds: [...(authState.allowedCompanyIds ?? [])],
      knowledgeTree: [],
      // Sidebar search box (sidebar-05 template) — client-side filter over
      // the already-loaded menu/knowledge trees, not a backend query.
      menuSearch: "",
      sidebarCollapsed: readCachedSidebarCollapsed(),
      // Mobile off-canvas open/close, owned locally and handed to BaseUI
      // <Shell> as a controlled `open` prop so MenuTree/KnowledgeTree
      // (mounted as its descendants) can close it on navigation via
      // `this.env.setSidebarOpen(false)` instead of the old DOM-lookup
      // `document.getElementById(...).BaseUISidebar.close()` static API.
      mobileSidebarOpen: false,
      // The app's single global toast, passed straight through to
      // <Shell toast=...> (bus-root.js's subscribeBusToast payload shape —
      // {message, variant, persistent?, actionLabel?, onAction?} — matches
      // <Shell>'s own `toast` prop contract exactly). Every toast in the
      // app — backend bus events and page-local notify() calls alike (see
      // bus-root.js's showToast()) — flows through this one state slot;
      // Shell is the only place a toast can render since it's the one
      // component that persists for the lifetime of a shell:true route
      // (bus-root.js itself has no render tree, see its header comment for
      // why).
      busToast: null,
    });

    // layoutStore is the one piece of this state a component outside the
    // Shell tree (FormView's docked ChatterPanel) needs to read, so it's
    // pushed there in addition to the local/localStorage-backed state above.
    layoutStore.setSidebarCollapsed(this.state.sidebarCollapsed);

    // this.runBusToastAction() below just invokes `busToast.onAction()`
    // without clearing the toast — wrap the payload's onAction here so
    // clicking the action still dismisses the toast first, matching this
    // app's previous behavior.
    this.unsubscribeBusToast = subscribeBusToast((payload) => {
      this.state.busToast = payload
        ? {
            ...payload,
            onAction: payload.onAction
              ? () => {
                  this.state.busToast = null;
                  payload.onAction();
                }
              : undefined,
          }
        : null;
    });

    // Mounted once at the <Shell pwa=...> root (sibling of the internal
    // <Sidebar>, not a descendant) — see shell.component.js's header
    // comment for why this is a bare component-class prop rather than a
    // slotted child.
    this.PWAInstallDrawer = PWAInstallDrawer;

    this.unsubscribe = authStore.subscribe((next) => {
      this.state.username = next.username;
      this.state.companies = next.companies ?? [];
      this.state.authorizedCompanyIds = authStore.authorizedCompanyIds();
    });

    this.onKnowledgeMenuUpdated = () => void this.loadKnowledgeMenu();
    this.onUIMenuUpdated = () => void this.loadMenu();
    // Capture phase so the 768-991px trigger click is handled before BaseUI's
    // SidebarTrigger (which would toggle desktop collapse there).
    this.onSidebarClickCapture = (event) => {
      if (!window.matchMedia?.(DRAWER_TABLET_QUERY).matches) return;
      const target = event.target instanceof Element ? event.target : null;
      if (!target) return;
      if (target.closest(".b-shell-header .b-sidebar-trigger")) {
        event.stopPropagation();
        this.state.mobileSidebarOpen = !this.state.mobileSidebarOpen;
        return;
      }
      // Navigating from the drawer closes it (BaseUI only does this < 768px).
      if (this.state.mobileSidebarOpen && target.closest(".b-sidebar a[href]")) {
        this.state.mobileSidebarOpen = false;
      }
    };

    // The sidebar paints from the cached menu (or its loading state), so the menu RPCs must not hold back the
    // routed page: awaiting them here delayed ModelView's own requests by a full round trip.
    // Their results are written to state only after mount (whenMounted): a Shell re-render while the page
    // is still loading would make Owl rebuild the page component, repeating every request it started.
    this.whenMounted = new Promise((resolve) => (this.markMounted = resolve));
    void this.loadMenu();
    void this.loadKnowledgeMenu();
    onMounted(() => {
      this.markMounted();
      void this.loadLanguages();
      // Sessions persisted before login stored company names fetch them once.
      if (this.state.companies.length === 0 && this.state.authorizedCompanyIds.length > 0) {
        void authStore.loadCompanies();
      }
      window.addEventListener(
        MARKDOWN_MENU_UPDATED_EVENT,
        this.onKnowledgeMenuUpdated,
      );
      window.addEventListener(UI_MENU_UPDATED_EVENT, this.onUIMenuUpdated);
      document.addEventListener("click", this.onSidebarClickCapture, true);
    });
    // Owl 2 never calls a class `willUnmount()` method — only the hook runs —
    // so without this every Shell remount leaked its toast/auth/menu listeners.
    onWillUnmount(() => this.willUnmount());
  }

  willUnmount() {
    this.unsubscribe?.();
    this.unsubscribeBusToast?.();
    window.removeEventListener(
      MARKDOWN_MENU_UPDATED_EVENT,
      this.onKnowledgeMenuUpdated,
    );
    window.removeEventListener(UI_MENU_UPDATED_EVENT, this.onUIMenuUpdated);
    document.removeEventListener("click", this.onSidebarClickCapture, true);
  }

  dismissBusToast() {
    this.state.busToast = null;
  }

  runBusToastAction() {
    this.state.busToast?.onAction?.();
  }

  // loadKnowledgeMenu populates the sidebar's "KnowLedge" doc tree; a fetch
  // failure here just leaves the section empty rather than surfacing an error
  // — the main ui.menu sidebar staying usable matters more than this section.
  async loadKnowledgeMenu() {
    let tree = [];
    try {
      tree = buildDocumentTree(await fetchMarkdownMenu());
    } catch {
      // Leave the section empty.
    }
    await this.whenMounted;
    this.state.knowledgeTree = tree;
  }

  // loadMenu always refreshes from the backend even when a cached tree was
  // used for the initial render (cache-first paint, network-fresh follow-up);
  // a fetch failure only surfaces as an error when there's no cached tree to
  // fall back on, so a flaky reload doesn't blank out a working sidebar.
  async loadMenu() {
    let records = null;
    let failure = null;
    try {
      records = await fetchSidebarMenus();
    } catch (error) {
      failure = error;
    }
    await this.whenMounted;
    if (records) {
      this.state.menu = records;
      this.state.error = "";
    } else if (this.state.menu.length === 0) {
      this.state.error = failure instanceof Error ? failure.message : t("Failed to load menu");
    }
    this.state.loading = false;
  }

  setMenuSearch(value) {
    this.state.menuSearch = value;
  }

  get filteredMenu() {
    return filterMenuTree(this.state.menu, this.state.menuSearch.trim());
  }

  get filteredKnowledgeTree() {
    return filterKnowledgeTree(this.state.knowledgeTree, this.state.menuSearch.trim());
  }

  // Minimal breadcrumb label — the trailing route segment (e.g. "tbl.partner"
  // from "/web/tbl.partner"). No page-title/backend breadcrumb metadata
  // exists in this tree, so this stays a plain path-derived string.
  get currentLabel() {
    return this.props.currentPath?.split("/").filter(Boolean).pop() ?? "";
  }

  // loadLanguages builds the switcher options from tbl.lang (code + name):
  // /languages (app.language) is only the allow-list, tbl.lang.get_installed
  // is the source of truth for which codes exist. On failure the switcher
  // stays hidden rather than blocking the shell.
  async loadLanguages() {
    try {
      const response = await fetch("/languages");
      if (!response.ok) return;
      const body = await response.json();
      const allowed = Array.isArray(body?.languages) ? body.languages.map(normalizeLang) : [];
      const installed = await ResLangRpc.call("get_installed");
      const names = new Map((installed.result ?? []).map(([code, name]) => [normalizeLang(code), name]));
      this.state.languages = allowed
        .filter((code) => names.has(code))
        .map((code) => ({ code, name: names.get(code) }));
    } catch {
      this.state.languages = [];
    }
  }

  // selectLanguage stores the choice in the auth store (so it rides on every
  // RPC as context.lang) and reloads so already-rendered views re-fetch
  // translated data.
  selectLanguage(lang) {
    if (!lang || normalizeLang(lang) === this.state.currentLang) return;
    authStore.setUiLanguage(lang);
    window.location.reload();
  }

  // companyOptions feeds the navbar switcher (always shown for a signed-in user with a company):
  // the {id, name} list from login, else bare ids while names are unavailable (session from
  // before allowed_companies existed, or a backend without it).
  get companyOptions() {
    if (this.state.companies.length > 0) return this.state.companies;
    return this.state.authorizedCompanyIds.map((id) => ({ id, name: `#${id}` }));
  }

  get currentCompanyName() {
    return this.companyOptions.find((company) => company.id === this.state.currentCompanyId)?.name ?? "";
  }

  // companySelectionDirty is true while checkbox toggles differ from the applied active set.
  get companySelectionDirty() {
    const applied = authStore.getState().allowedCompanyIds ?? [];
    const staged = this.state.selectedCompanyIds;
    return staged.length !== applied.length || staged.some((id) => !applied.includes(id));
  }

  // toggleCompany stages a checkbox change (applied by Confirm). The last selected company
  // cannot be unticked: an empty active set would fall back server-side to every company.
  toggleCompany(companyId) {
    const selected = this.state.selectedCompanyIds;
    if (selected.includes(companyId)) {
      if (selected.length > 1) this.state.selectedCompanyIds = selected.filter((id) => id !== companyId);
    } else {
      this.state.selectedCompanyIds = [...selected, companyId];
    }
  }

  resetCompanySelection() {
    this.state.selectedCompanyIds = [...(authStore.getState().allowedCompanyIds ?? [])];
  }

  // confirmCompanySelection applies the staged set; the current company stays current when
  // still ticked, else the first ticked company takes over.
  confirmCompanySelection() {
    const selected = this.state.selectedCompanyIds;
    const current = selected.includes(this.state.currentCompanyId) ? this.state.currentCompanyId : selected[0];
    this.applyCompanyContext(current, selected);
  }

  // switchCompany (click on a company name) makes it the current company, i.e. the
  // company_id stamped on new records. Like Odoo's "log into": with a single active company
  // it replaces it; with several it joins them as the current one. Staged ticks are dropped.
  switchCompany(companyId) {
    const applied = authStore.getState().allowedCompanyIds ?? [];
    const selected = applied.length <= 1 ? [companyId] : [companyId, ...applied.filter((id) => id !== companyId)];
    this.applyCompanyContext(companyId, selected);
  }

  // applyCompanyContext stores the scope (it rides on every RPC as context.company_id /
  // allowed_company_ids) and reloads so open views, the sidebar (its cache key includes the
  // company scope) and defaults re-fetch under the new companies.
  applyCompanyContext(currentCompanyId, allowedCompanyIds) {
    authStore.setCompanyContext(currentCompanyId, { allowedCompanyIds });
    window.location.reload();
  }

  // logout clears every persisted auth artifact — the sessionStorage-backed
  // auth store plus the localStorage sidebar menu cache and bus notification
  // cursor left behind under the old identity — before returning to /login.
  logout() {
    clearCachedSidebarMenus();
    clearLastNotificationId();
    authStore.clearAuth();
    navigate("/login");
  }

  // Passed to <Shell> as `onCollapsedChange` — fires from the desktop
  // <SidebarTrigger>'s own click handler (env.setShellCollapsed), separate
  // from the mobile hamburger trigger below.
  setSidebarCollapsed(collapsed) {
    this.state.sidebarCollapsed = collapsed;
    writeCachedSidebarCollapsed(collapsed);
    layoutStore.setSidebarCollapsed(collapsed);
  }

  // Passed to <Shell> as `onOpenChange` — fires on mobile trigger/close
  // clicks and on Escape (the internal Sidebar's own onKeyDown).
  setMobileSidebarOpen(open) {
    this.state.mobileSidebarOpen = open;
  }
}
