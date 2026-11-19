// Port of frontend-admin's modules/base/pages/notifications-page.tsx. Renders
// the retained backend bus notifications stored in IndexedDB through PouchDB
// (modules/base/services/pouchdb.js). No Shell/sidebar wiring here — this file
// is only the page body component; routing is handled centrally by main.js.

import { Component, loadTemplate, onWillStart, useState, xml } from "nodexui/owl";

import { listNotifications } from "@/modules/base/services/pouchdb.js";
import { Badge } from "nodexui/component";

const template = await loadTemplate(new URL("./NotificationsPage.xml", import.meta.url));

export class NotificationsPage extends Component {
  static template = xml({ raw: [template] });
  static components = { Badge };

  setup() {
    this.state = useState({
      status: "loading",
      message: "",
      notifications: [],
    });

    onWillStart(() => this.loadNotifications());
  }

  async loadNotifications() {
    this.state.status = "loading";
    try {
      const notifications = await listNotifications();
      this.state.notifications = notifications;
      this.state.status = "ready";
    } catch (error) {
      this.state.status = "error";
      this.state.message = error instanceof Error ? error.message : t("Failed to load notifications");
    }
  }

  // formatPayload renders a notification's payload as pretty-printed JSON for
  // the <pre> block. JSON.stringify is not a global available inside Owl's
  // hand-rolled template expression evaluator, so this must live here.
  formatPayload(notification) {
    return JSON.stringify(notification.payload, null, 2);
  }

  // formatReceivedAt renders a notification's receivedAt ISO timestamp using
  // the browser's locale-aware formatting, same as the React source.
  formatReceivedAt(notification) {
    return new Date(notification.receivedAt).toLocaleString();
  }
}
