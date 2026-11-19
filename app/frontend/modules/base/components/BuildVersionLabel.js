// Port of frontend-admin's build-version-label.tsx — reads the stamped
// build id from /frontend/version.json (already precached by sw.js) and
// renders nothing while unavailable/loading.
import { Component, loadTemplate, onMounted, useState, xml } from "nodexui/owl";

const template = await loadTemplate(new URL("./BuildVersionLabel.xml", import.meta.url));

export class BuildVersionLabel extends Component {
  static template = xml({ raw: [template] });
  static props = { className: { type: String, optional: true } };
  static defaultProps = { className: "" };

  setup() {
    this.state = useState({ version: "" });
    // Not onWillStart: the label is cosmetic, so the page (e.g. /login) must not wait a round trip for version.json.
    onMounted(() => void this.loadVersion());
  }

  async loadVersion() {
    try {
      const response = await fetch(new URL("../../../version.json", import.meta.url), { cache: "no-store" });
      if (!response.ok) return;
      const payload = await response.json();
      this.state.version = typeof payload.version === "string" ? payload.version : "";
    } catch {
      this.state.version = "";
    }
  }
}
