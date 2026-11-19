// Port of frontend-admin's pwa/pwa-install-dialog.tsx, rebuilt on BaseUI's
// pure-Owl `Sheet` component (side="bottom") per the rebuild plan's explicit
// suggestion ("check `sheet` as a possible base for it, since a bottom-sheet
// install prompt is close to `sheet`'s shape") instead of React's Radix
// `Drawer`. Wired into Shell.js so it can appear on any authenticated route.
// Both sheets are fully controlled via state.open/state.notificationOpen +
// onOpenChange — no imperative BaseUISheet refs or "nodexui:close" listeners
// needed.
import { Component, loadTemplate, onWillUnmount, useState, xml } from "nodexui/owl";

import { pwaService } from "@/services/pwa/pwa-service.js";
import { Button, Icon, Sheet, SheetContent } from "nodexui/component";

const template = await loadTemplate(new URL("./PWAInstallDrawer.xml", import.meta.url));

// Session-scoped: only offer the notification-permission drawer once per tab
// session after the install drawer is dismissed, matching React's
// sessionStorage guard (NOTIFICATION_OPEN_KEY).
const NOTIFICATION_OPEN_SESSION_KEY = "basehub.pwa.notification-prompt-opened.v1";

const IOS_MANUAL_STEPS = [
  t("Tap the Share button in Safari."),
  t('Scroll down and choose "Add to Home Screen".'),
  t("Tap Add to finish installing Base Hub."),
];

export class PWAInstallDrawer extends Component {
  static template = xml({ raw: [template] });
  static components = { Button, Icon, Sheet, SheetContent };

  setup() {
    this.state = useState({
      pwa: pwaService.getState(),
      open: false,
      notificationOpen: false,
      loading: false,
      notificationLoading: false,
      manualInstructions: [],
      notificationPermission: "Notification" in window ? window.Notification.permission : "unsupported",
    });

    this.unsubscribe = pwaService.subscribe((next) => {
      this.state.pwa = next;
      if (next.needsManualInstructions) this.state.manualInstructions = pwaService.getManualInstallInstructions();
    });
    if (this.state.pwa.needsManualInstructions) this.state.manualInstructions = pwaService.getManualInstallInstructions();

    onWillUnmount(() => {
      this.unsubscribe?.();
    });
  }

  get shouldRender() {
    return !this.state.pwa.isStandalone && this.state.pwa.isSupportedOnBrowser;
  }

  get shouldShowLauncher() {
    return this.state.pwa.canPromptToInstall && !this.state.open;
  }

  get shouldShowNotificationLauncher() {
    return (
      !this.state.open &&
      !this.state.notificationOpen &&
      this.state.notificationPermission === "default" &&
      !this.shouldShowLauncher
    );
  }

  get title() {
    return this.state.pwa.needsManualInstructions ? t("Add Base Hub to your Home Screen") : t("Install Base Hub");
  }

  get description() {
    return this.state.pwa.needsManualInstructions
      ? t("On iPhone or iPad, Safari installs this app through the Home Screen flow.")
      : t("Install Base Hub from your browser prompt so you can launch it like an app.");
  }

  get installButtonLabel() {
    return this.state.pwa.needsManualInstructions ? t("Continue") : t("Open install prompt");
  }

  get manualInstallSteps() {
    return this.state.pwa.platform === "ios" ? IOS_MANUAL_STEPS : this.state.manualInstructions;
  }

  openInstallDrawer() {
    this.state.open = true;
  }

  openNotificationDrawer() {
    this.state.notificationOpen = true;
  }

  maybeOpenNotificationPrompt() {
    if (!("Notification" in window)) return;
    if (window.Notification.permission !== "default") {
      this.state.notificationPermission = window.Notification.permission;
      return;
    }
    if (window.sessionStorage.getItem(NOTIFICATION_OPEN_SESSION_KEY) === "true") return;
    window.sessionStorage.setItem(NOTIFICATION_OPEN_SESSION_KEY, "true");
    this.state.notificationPermission = "default";
    this.state.notificationOpen = true;
  }

  async handleInstall() {
    if (this.state.pwa.needsManualInstructions) {
      this.state.open = false;
      this.maybeOpenNotificationPrompt();
      return;
    }
    this.state.loading = true;
    try {
      const result = await pwaService.show({
        onManualInstall: (manualResult) => { this.state.manualInstructions = manualResult.instructions; },
      });
      if (result.outcome !== "manual") {
        this.state.open = false;
        this.maybeOpenNotificationPrompt();
      }
    } finally {
      this.state.loading = false;
    }
  }

  handleLater() {
    this.state.open = false;
    this.maybeOpenNotificationPrompt();
  }

  handleHidePrompt() {
    pwaService.decline();
    this.state.open = false;
    this.maybeOpenNotificationPrompt();
  }

  async handleAllowNotifications() {
    if (!("Notification" in window)) {
      this.state.notificationPermission = "unsupported";
      this.state.notificationOpen = false;
      return;
    }
    this.state.notificationLoading = true;
    try {
      this.state.notificationPermission = await window.Notification.requestPermission();
      this.state.notificationOpen = false;
    } finally {
      this.state.notificationLoading = false;
    }
  }

  handleLaterNotifications() {
    this.state.notificationOpen = false;
  }
}
