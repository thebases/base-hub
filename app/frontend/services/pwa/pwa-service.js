// Port of frontend-admin's public/pwa_service.js — the prior Phase 6 pass
// deferred the PWA install drawer because it believed this service had no
// equivalent anywhere in app/frontend; the source file does exist
// (app/frontend-admin/public/pwa_service.js), it just was never ported into
// this tree. This is a fairly direct port, minus frontend-admin's `browser`
// indirection wrapper (that layer only exists there for test-mocking; this
// tree accesses window/navigator/localStorage directly everywhere else, e.g.
// Shell.js's sidebar-collapse cache) and minus the scoped-app / iOS-app /
// Android-app webview detection branches this tree has no equivalent of
// (no scoped_app route, no native wrapper shell here).
//
// Captures the `beforeinstallprompt` event (browsers only fire it once and
// only if `preventDefault()`s it immediately), exposes a promise-returning
// `prompt()`/`show()` to replay it on a user gesture, tracks
// accepted/dismissed install state in localStorage per start-url scope (so a
// declined prompt doesn't nag again), and falls back to manual
// add-to-home-screen instructions on iOS/macOS Safari, which never fires
// `beforeinstallprompt` at all.


const INSTALLATION_STATE_STORAGE_KEY = "basehub.pwa.installation-state.v1";
const DEFAULT_START_URL = "/";
const NATIVE_PROMPT_AVAILABLE = "onbeforeinstallprompt" in window || "BeforeInstallPromptEvent" in window;

let capturedBeforeInstallPromptEvent = null;
const beforeInstallPromptListeners = new Set();

window.addEventListener("beforeinstallprompt", (event) => {
  event.preventDefault();
  capturedBeforeInstallPromptEvent = event;
  for (const listener of beforeInstallPromptListeners) listener(event);
});

window.addEventListener("appinstalled", () => {
  capturedBeforeInstallPromptEvent = null;
});

function isBrowserSafari() {
  return !/Chrome/i.test(navigator.userAgent) && Boolean(navigator.userAgent?.includes("Safari"));
}

function isIOS() {
  const isIOSPlatform = "platform" in navigator && navigator.platform === "MacIntel";
  return /(iPad|iPhone|iPod)/i.test(navigator.userAgent) || (isIOSPlatform && (navigator.maxTouchPoints || 1) > 1);
}

function isMacOS() {
  return /Mac/i.test(navigator.userAgent);
}

function isDisplayStandalone() {
  return window.matchMedia("(display-mode: standalone)").matches;
}

function getSafariMajorVersion() {
  if (!isBrowserSafari()) return 0;
  const match = navigator.userAgent.match(/Version\/(\d+)/i);
  return Number(match?.[1] || 0);
}

function detectPlatform() {
  if (isIOS()) return "ios";
  if (isMacOS()) return "macos";
  return "other";
}

function supportsManualSafariInstall() {
  if (!isBrowserSafari() || isDisplayStandalone()) return false;
  return isIOS() || (isMacOS() && getSafariMajorVersion() >= 17);
}

function isInstallPromptSupported() {
  return NATIVE_PROMPT_AVAILABLE || supportsManualSafariInstall();
}

function getManualInstallInstructions(platform = detectPlatform()) {
  if (platform === "ios") {
    return [
      t("Open the Share menu in Safari."),
      t('Choose "Add to Home Screen".'),
      t("Confirm the app name and save it."),
    ];
  }
  if (platform === "macos") {
    return [
      t("Open the File menu in Safari."),
      t('Choose "Add to Dock".'),
      t("Confirm the app name to finish installation."),
    ];
  }
  return [
    t("Use your browser install action to add this app."),
    t("If no install action is shown, make sure the manifest and service worker are available."),
  ];
}

function safeParseJSON(value, fallback) {
  if (!value) return fallback;
  try {
    return JSON.parse(value);
  } catch {
    return fallback;
  }
}

function getStorageState() {
  return safeParseJSON(window.localStorage.getItem(INSTALLATION_STATE_STORAGE_KEY), {});
}

function setStorageState(value) {
  window.localStorage.setItem(INSTALLATION_STATE_STORAGE_KEY, JSON.stringify(value));
}

function createEmitter() {
  const target = new EventTarget();
  return {
    emit(detail) {
      target.dispatchEvent(new CustomEvent("change", { detail }));
    },
    subscribe(listener) {
      const wrapped = (event) => listener(event.detail);
      target.addEventListener("change", wrapped);
      return () => target.removeEventListener("change", wrapped);
    },
  };
}

function createPWAService() {
  const emitter = createEmitter();
  let destroyed = false;

  const state = {
    canPromptToInstall: false,
    isAvailable: false,
    isSupportedOnBrowser: false,
    isStandalone: isDisplayStandalone(),
    startUrl: DEFAULT_START_URL,
    needsManualInstructions: supportsManualSafariInstall(),
    platform: detectPlatform(),
  };

  function getInstallationState(scope = state.startUrl) {
    return getStorageState()[scope] || "";
  }

  function setInstallationState(value) {
    const next = getStorageState();
    next[state.startUrl] = value;
    setStorageState(next);
  }

  function removeInstallationState() {
    const next = getStorageState();
    delete next[state.startUrl];
    setStorageState(next);
  }

  function emitChange() {
    if (destroyed) return;
    state.isStandalone = isDisplayStandalone();
    emitter.emit(getState());
  }

  function setState(patch) {
    let changed = false;
    for (const [key, value] of Object.entries(patch)) {
      if (state[key] !== value) {
        state[key] = value;
        changed = true;
      }
    }
    if (changed) emitChange();
  }

  function getState() {
    return { ...state };
  }

  function decline() {
    setInstallationState("dismissed");
    setState({ canPromptToInstall: false });
  }

  function handleBeforeInstallPrompt(event) {
    capturedBeforeInstallPromptEvent = event;
    const installationState = getInstallationState();
    if (installationState === "accepted" && !isDisplayStandalone()) {
      // The app was uninstalled since the last "accepted" marker — let the
      // next prompt behave like a fresh install instead of staying stuck.
      removeInstallationState();
    }
    setState({ canPromptToInstall: getInstallationState() !== "dismissed", isAvailable: true });
  }

  async function prompt({ onDone, onManualInstall } = {}) {
    if (!state.isAvailable) {
      const result = { outcome: "unavailable" };
      onDone?.(result);
      return result;
    }
    if (capturedBeforeInstallPromptEvent) {
      const promptEvent = capturedBeforeInstallPromptEvent;
      capturedBeforeInstallPromptEvent = null;
      await promptEvent.prompt();
      const result = (await promptEvent.userChoice) || {};
      if (result.outcome) setInstallationState(result.outcome);
      setState({ canPromptToInstall: false, isAvailable: result.outcome !== "accepted" });
      onDone?.(result);
      return result;
    }
    if (state.needsManualInstructions) {
      const result = { outcome: "manual", instructions: getManualInstallInstructions(state.platform) };
      onManualInstall?.(result);
      onDone?.(result);
      return result;
    }
    const result = { outcome: "unsupported" };
    onDone?.(result);
    return result;
  }

  function onBeforeInstallPrompt(event) {
    handleBeforeInstallPrompt(event);
  }
  beforeInstallPromptListeners.add(onBeforeInstallPrompt);

  function onAppInstalled() {
    setInstallationState("accepted");
    setState({ canPromptToInstall: false, isAvailable: false, isStandalone: true });
  }
  window.addEventListener("appinstalled", onAppInstalled);

  state.isSupportedOnBrowser = isInstallPromptSupported();
  if (state.isSupportedOnBrowser) {
    if (capturedBeforeInstallPromptEvent) {
      handleBeforeInstallPrompt(capturedBeforeInstallPromptEvent);
    } else if (state.needsManualInstructions) {
      setState({ canPromptToInstall: getInstallationState() !== "dismissed", isAvailable: true });
    }
  }

  return {
    subscribe: emitter.subscribe,
    getState,
    getManualInstallInstructions: () => getManualInstallInstructions(state.platform),
    decline,
    prompt,
    show: prompt,
    destroy() {
      destroyed = true;
      beforeInstallPromptListeners.delete(onBeforeInstallPrompt);
      window.removeEventListener("appinstalled", onAppInstalled);
    },
  };
}

export const pwaService = createPWAService();

export default pwaService;
