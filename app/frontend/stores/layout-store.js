// Tiny hand-rolled observable store (same shape as stores/auth-service.js's
// AuthStore) for the one piece of shell layout state that a page component
// outside the Shell tree needs to read: whether the left sidebar is
// collapsed. Shell.js owns the actual collapsed/localStorage state and pushes
// updates here; this store also tracks the viewport width itself via
// matchMedia since that's a pure window concern no component owns.

// > 1024px, i.e. min-width: 1025px.
const WIDE_VIEWPORT_QUERY = "(min-width: 1025px)";

function isWideViewportNow() {
  if (typeof window === "undefined" || typeof window.matchMedia !== "function") return true;
  return window.matchMedia(WIDE_VIEWPORT_QUERY).matches;
}

class LayoutStore {
  constructor() {
    this.state = { sidebarCollapsed: false, isWideViewport: isWideViewportNow() };
    this.listeners = new Set();
    if (typeof window !== "undefined" && typeof window.matchMedia === "function") {
      this.mediaQuery = window.matchMedia(WIDE_VIEWPORT_QUERY);
      this.onMediaChange = (event) => this.#set({ isWideViewport: event.matches });
      // addEventListener is the standard MediaQueryList API; no legacy
      // addListener fallback needed for this app's supported browsers.
      this.mediaQuery.addEventListener("change", this.onMediaChange);
    }
  }

  getState() {
    return this.state;
  }

  subscribe(listener) {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  setSidebarCollapsed(collapsed) {
    this.#set({ sidebarCollapsed: collapsed });
  }

  #set(partial) {
    this.state = { ...this.state, ...partial };
    this.listeners.forEach((listener) => listener(this.state));
  }
}

export const layoutStore = new LayoutStore();

// chatterFitsAsSidebar: the docked chatter panel only has room to sit beside
// the form when the left sidebar is collapsed (freeing width) or the
// viewport is wide enough (>1024px) to fit an expanded left sidebar and the
// chatter panel side by side. Narrow viewport + expanded sidebar is the only
// case with no room for it.
export function chatterFitsAsSidebar(state = layoutStore.getState()) {
  return state.sidebarCollapsed || state.isWideViewport;
}
